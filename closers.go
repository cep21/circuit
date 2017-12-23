package hystrix

import (
	"time"

	"github.com/cep21/hystrix/fastmath"
)

// ClosedToOpen receives events and controls if the circuit should open or close as a result of those events.
// Return true if the circuit should open, false if the circuit should close.
//
// OpenLogic can make a closed circuit Open.
type ClosedToOpen interface {
	// Closed called when the circuit transitions from Open to Closed
	Closed(now time.Time)
	// Even though the circuit is closed, and we want to allow the circuit to remain closed, we still prevent this
	// command from happening.  The error will return as a short circuit to the caller, as well as trigger fallback
	// logic.
	Prevent(now time.Time) bool
	// SuccessfulAttempt any time runFunc was called and appeared healthy
	SuccessfulAttempt(now time.Time, duration time.Duration)
	// Any time run was called, but we backed out of the attempt or the return value doesn't appear like a legitimate run
	BackedOutAttempt(now time.Time)
	// Any time the run function failed for a real reason
	ErrorAttempt(now time.Time)
	// AttemptToOpen a circuit that is currently closed, after a bad request comes in
	AttemptToOpen(now time.Time) bool
	Configurable
}

// OpenToClosed controls logic that tries to close an open circuit
type OpenToClosed interface {
	// Opened circuit. It should now check to see if it should ever allow various requests in an attempt to become closed
	Opened(now time.Time)
	// The circuit is currently closed.  Check and return true if this request should be allowed.  This will signal
	// the circuit in a "half-open" state, allowing that one request.
	// If any requests are allowed, the circuit moves into a half open state.
	Allow(now time.Time) (shouldAllow bool)
	// SuccessfulAttempt any time runFunc was called and appeared healthy
	SuccessfulAttempt(now time.Time, duration time.Duration)
	// Any time run was called, but we backed out of the attempt or the return value doesn't appear like a legitimate run
	BackedOutAttempt(now time.Time)
	// Any time the run function failed for a real reason
	ErrorAttempt(now time.Time)
	// AttemptToOpen a circuit that is currently closed, after a bad request comes in
	AttemptToClose(now time.Time) bool
	Configurable
}

// ErrorPercentageCheck is ClosedToOpen that opens a circuit after a threshold and % error has been
// reached.  It is the default hystrix implementation.
type ErrorPercentageCheck struct {
	errorsCount             fastmath.RollingCounter
	legitimateAttemptsCount fastmath.RollingCounter

	errorPercentage        fastmath.AtomicInt64
	requestVolumeThreshold fastmath.AtomicInt64
}

var _ ClosedToOpen = &ErrorPercentageCheck{}

// Closed resets the error and attempt count
func (e *ErrorPercentageCheck) Closed(now time.Time) {
	e.errorsCount.Reset(now)
	e.legitimateAttemptsCount.Reset(now)
}

// SuccessfulAttempt increases the number of correct attempts
func (e *ErrorPercentageCheck) SuccessfulAttempt(now time.Time, duration time.Duration) {
	e.legitimateAttemptsCount.Inc(now)
}

// Prevent never returns true
func (e *ErrorPercentageCheck) Prevent(now time.Time) (shouldAllow bool) {
	return false
}

// BackedOutAttempt is ignored
func (e *ErrorPercentageCheck) BackedOutAttempt(now time.Time) {
}

// ErrorAttempt increases error count for the circuit
func (e *ErrorPercentageCheck) ErrorAttempt(now time.Time) {
	e.legitimateAttemptsCount.Inc(now)
	e.errorsCount.Inc(now)
}

// AttemptToOpen returns true if rolling count >= threshold and
// error % is high enough.
func (e *ErrorPercentageCheck) AttemptToOpen(now time.Time) bool {
	attemptCount := e.legitimateAttemptsCount.RollingSum(now)
	if attemptCount == 0 || attemptCount < e.requestVolumeThreshold.Get() {
		// not enough requests. Will not open circuit
		return false
	}

	errCount := e.errorsCount.RollingSum(now)
	errPercentage := int64(float64(errCount) / float64(attemptCount) * 100)
	return errPercentage >= e.errorPercentage.Get()
}

// SetConfigThreadSafe modifies error % and request volume threshold
func (e *ErrorPercentageCheck) SetConfigThreadSafe(props CommandProperties) {
	e.errorPercentage.Set(props.CircuitBreaker.ErrorThresholdPercentage)
	e.requestVolumeThreshold.Set(props.CircuitBreaker.RequestVolumeThreshold)
}

// SetConfigNotThreadSafe recreates the buckets
func (e *ErrorPercentageCheck) SetConfigNotThreadSafe(props CommandProperties) {
	e.SetConfigThreadSafe(props)
	rollingCounterBucketWidth := time.Duration(props.Metrics.RollingStatsDuration.Nanoseconds() / int64(props.Metrics.RollingStatsNumBuckets))
	e.errorsCount = fastmath.NewRollingCounter(rollingCounterBucketWidth, props.Metrics.RollingStatsNumBuckets)
	e.legitimateAttemptsCount = fastmath.NewRollingCounter(rollingCounterBucketWidth, props.Metrics.RollingStatsNumBuckets)
}

func newErrorPercentageCheck() ClosedToOpen {
	return &ErrorPercentageCheck{}
}

// Configurable is anything that can receive configuration changes while live
type Configurable interface {
	SetConfigThreadSafe(props CommandProperties)
	SetConfigNotThreadSafe(props CommandProperties)
}

// SleepyOpenToClose is hystrix's default half-open logic: try again ever X ms
type SleepyOpenToClose struct {
	// Tracks when we should try to close an open circuit again
	reopenCircuitCheck fastmath.TimedCheck

	concurrentSuccessfulAttempts fastmath.AtomicInt64
	closeOnCurrentCount          fastmath.AtomicInt64
}

var _ OpenToClosed = &SleepyOpenToClose{}

func newSleepyOpenToClose() OpenToClosed {
	return &SleepyOpenToClose{}
}

// Opened circuit. It should now check to see if it should ever allow various requests in an attempt to become closed
func (s *SleepyOpenToClose) Opened(now time.Time) {
	s.concurrentSuccessfulAttempts.Set(0)
	s.reopenCircuitCheck.SleepStart(now)
}

// Allow checks for half open state.
// The circuit is currently closed.  Check and return true if this request should be allowed.  This will signal
// the circuit in a "half-open" state, allowing that one request.
// If any requests are allowed, the circuit moves into a half open state.
func (s *SleepyOpenToClose) Allow(now time.Time) (shouldAllow bool) {
	return s.reopenCircuitCheck.Check(now)
}

// SuccessfulAttempt any time runFunc was called and appeared healthy
func (s *SleepyOpenToClose) SuccessfulAttempt(now time.Time, duration time.Duration) {
	s.concurrentSuccessfulAttempts.Add(1)
}

// BackedOutAttempt is ignored
func (s *SleepyOpenToClose) BackedOutAttempt(now time.Time) {
	// Ignored
}

// ErrorAttempt resets the consecutive Successful count
func (s *SleepyOpenToClose) ErrorAttempt(now time.Time) {
	s.concurrentSuccessfulAttempts.Set(0)
}

// AttemptToClose is true if we hav enough successful attempts in a row.
func (s *SleepyOpenToClose) AttemptToClose(now time.Time) bool {
	return s.concurrentSuccessfulAttempts.Get() > s.closeOnCurrentCount.Get()
}

// SetConfigThreadSafe resets the sleep duration during reopen attempts
func (s *SleepyOpenToClose) SetConfigThreadSafe(props CommandProperties) {
	s.reopenCircuitCheck.SetSleepDuration(props.CircuitBreaker.SleepWindow)
}

// SetConfigNotThreadSafe just calls SetConfigThreadSafe
func (s *SleepyOpenToClose) SetConfigNotThreadSafe(props CommandProperties) {
	s.SetConfigThreadSafe(props)
}
