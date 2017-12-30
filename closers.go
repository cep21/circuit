package hystrix

import (
	"time"

	"github.com/cep21/hystrix/internal/fastmath"
)

// ClosedToOpen receives events and controls if the circuit should open or close as a result of those events.
// Return true if the circuit should open, false if the circuit should close.
type ClosedToOpen interface {
	// Closed is called when the circuit transitions from Open to Closed.  Your logic should reinitialize itself
	// to prepare for when it should try to open back up again.
	Closed(now time.Time)
	// Even though the circuit is closed, and we want to allow the circuit to remain closed, we still prevent this
	// command from happening.  The error will return as a short circuit to the caller, as well as trigger fallback
	// logic.  This could be useful if your circuit is closed, but some external force wants you to pretend to be open.
	Prevent(now time.Time) bool
	// SuccessfulAttempt is called any time runFunc was called and appeared healthy
	SuccessfulAttempt(now time.Time, duration time.Duration)
	// Any time run was called, but we backed out of the attempt or the return value doesn't appear like a legitimate run
	// These are probably not failures that would make you open a circuit, but could be useful to know about
	BackedOutAttempt(now time.Time)
	// Any time the run function failed for a real reason.  You should expect an AttemptToOpen call later, to see if
	// the circuit should be opened because of thi serror
	ErrorAttempt(now time.Time)
	// AttemptToOpen a circuit that is currently closed, after a bad request comes in.  Only called after bad requests,
	// never called after a successful request
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

// errorPercentageCheck is ClosedToOpen that opens a circuit after a threshold and % error has been
// reached.  It is the default hystrix implementation.
type errorPercentageCheck struct {
	errorsCount             fastmath.RollingCounter
	legitimateAttemptsCount fastmath.RollingCounter

	errorPercentage        fastmath.AtomicInt64
	requestVolumeThreshold fastmath.AtomicInt64
}

var _ ClosedToOpen = &errorPercentageCheck{}

// Closed resets the error and attempt count
func (e *errorPercentageCheck) Closed(now time.Time) {
	e.errorsCount.Reset(now)
	e.legitimateAttemptsCount.Reset(now)
}

// SuccessfulAttempt increases the number of correct attempts
func (e *errorPercentageCheck) SuccessfulAttempt(now time.Time, duration time.Duration) {
	e.legitimateAttemptsCount.Inc(now)
}

// Prevent never returns true
func (e *errorPercentageCheck) Prevent(now time.Time) (shouldAllow bool) {
	return false
}

// BackedOutAttempt is ignored
func (e *errorPercentageCheck) BackedOutAttempt(now time.Time) {
}

// ErrorAttempt increases error count for the circuit
func (e *errorPercentageCheck) ErrorAttempt(now time.Time) {
	e.legitimateAttemptsCount.Inc(now)
	e.errorsCount.Inc(now)
}

// AttemptToOpen returns true if rolling count >= threshold and
// error % is high enough.
func (e *errorPercentageCheck) AttemptToOpen(now time.Time) bool {
	attemptCount := e.legitimateAttemptsCount.RollingSumAt(now)
	if attemptCount == 0 || attemptCount < e.requestVolumeThreshold.Get() {
		// not enough requests. Will not open circuit
		return false
	}

	errCount := e.errorsCount.RollingSumAt(now)
	errPercentage := int64(float64(errCount) / float64(attemptCount) * 100)
	return errPercentage >= e.errorPercentage.Get()
}

// SetConfigThreadSafe modifies error % and request volume threshold
func (e *errorPercentageCheck) SetConfigThreadSafe(props CommandProperties) {
	e.errorPercentage.Set(props.CircuitBreaker.ErrorThresholdPercentage)
	e.requestVolumeThreshold.Set(props.CircuitBreaker.RequestVolumeThreshold)
}

// SetConfigNotThreadSafe recreates the buckets
func (e *errorPercentageCheck) SetConfigNotThreadSafe(props CommandProperties) {
	e.SetConfigThreadSafe(props)
	now := props.GoSpecific.TimeKeeper.Now()
	rollingCounterBucketWidth := time.Duration(props.Metrics.RollingStatsDuration.Nanoseconds() / int64(props.Metrics.RollingStatsNumBuckets))
	e.errorsCount = fastmath.NewRollingCounter(rollingCounterBucketWidth, props.Metrics.RollingStatsNumBuckets, now)
	e.legitimateAttemptsCount = fastmath.NewRollingCounter(rollingCounterBucketWidth, props.Metrics.RollingStatsNumBuckets, now)
}

func newErrorPercentageCheck() ClosedToOpen {
	return &errorPercentageCheck{}
}

// Configurable is anything that can receive configuration changes while live
type Configurable interface {
	// SetConfigThreadSafe can be called while the circuit is currently being used and will modify things that are
	// safe to change live.
	SetConfigThreadSafe(props CommandProperties)
	// SetConfigNotThreadSafe should only be called when the circuit is not in use: otherwise it will fail -race
	// detection
	SetConfigNotThreadSafe(props CommandProperties)
}

// sleepyOpenToClose is hystrix's default half-open logic: try again ever X ms
type sleepyOpenToClose struct {
	// Tracks when we should try to close an open circuit again
	reopenCircuitCheck fastmath.TimedCheck

	concurrentSuccessfulAttempts fastmath.AtomicInt64
	closeOnCurrentCount          fastmath.AtomicInt64
}

var _ OpenToClosed = &sleepyOpenToClose{}

func newSleepyOpenToClose() OpenToClosed {
	return &sleepyOpenToClose{}
}

// Opened circuit. It should now check to see if it should ever allow various requests in an attempt to become closed
func (s *sleepyOpenToClose) Opened(now time.Time) {
	s.concurrentSuccessfulAttempts.Set(0)
	s.reopenCircuitCheck.SleepStart(now)
}

// Allow checks for half open state.
// The circuit is currently closed.  Check and return true if this request should be allowed.  This will signal
// the circuit in a "half-open" state, allowing that one request.
// If any requests are allowed, the circuit moves into a half open state.
func (s *sleepyOpenToClose) Allow(now time.Time) (shouldAllow bool) {
	return s.reopenCircuitCheck.Check(now)
}

// SuccessfulAttempt any time runFunc was called and appeared healthy
func (s *sleepyOpenToClose) SuccessfulAttempt(now time.Time, duration time.Duration) {
	s.concurrentSuccessfulAttempts.Add(1)
}

// BackedOutAttempt is ignored
func (s *sleepyOpenToClose) BackedOutAttempt(now time.Time) {
	// Ignored
}

// ErrorAttempt resets the consecutive Successful count
func (s *sleepyOpenToClose) ErrorAttempt(now time.Time) {
	s.concurrentSuccessfulAttempts.Set(0)
}

// AttemptToClose is true if we hav enough successful attempts in a row.
func (s *sleepyOpenToClose) AttemptToClose(now time.Time) bool {
	return s.concurrentSuccessfulAttempts.Get() > s.closeOnCurrentCount.Get()
}

// SetConfigThreadSafe resets the sleep duration during reopen attempts
func (s *sleepyOpenToClose) SetConfigThreadSafe(props CommandProperties) {
	s.reopenCircuitCheck.SetSleepDuration(props.CircuitBreaker.SleepWindow)
}

// SetConfigNotThreadSafe just calls SetConfigThreadSafe
func (s *sleepyOpenToClose) SetConfigNotThreadSafe(props CommandProperties) {
	s.SetConfigThreadSafe(props)
	s.reopenCircuitCheck.TimeAfterFunc = props.GoSpecific.TimeKeeper.AfterFunc
}
