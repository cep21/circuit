package hystrix

import (
	"sync"
	"time"

	"github.com/cep21/circuit"
	"github.com/cep21/circuit/faststats"
)

// SleepyCloseCheck is hystrix's default half-open logic: try again ever X ms
type SleepyCloseCheck struct {
	// Tracks when we should try to close an open circuit again
	reopenCircuitCheck faststats.TimedCheck

	concurrentSuccessfulAttempts faststats.AtomicInt64
	closeOnCurrentCount          faststats.AtomicInt64

	mu     sync.Mutex
	config ConfigureSleepyCloseCheck
}

// SleepyCloseCheckFactory creates SleepyCloseCheck closer
func SleepyCloseCheckFactory(config ConfigureSleepyCloseCheck) func() circuit.OpenToClosed {
	return func() circuit.OpenToClosed {
		s := SleepyCloseCheck{}
		config.Merge(defaultConfigureSleepyCloseCheck)
		s.SetConfigNotThreadSafe(config)
		return &s
	}
}

var _ circuit.OpenToClosed = &SleepyCloseCheck{}

// ConfigureSleepyCloseCheck configures values for SleepyCloseCheck
type ConfigureSleepyCloseCheck struct {
	// SleepWindow is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakersleepwindowinmilliseconds
	SleepWindow time.Duration
	// HalfOpenAttempts is how many attempts to allow per SleepWindow
	HalfOpenAttempts int64
	// RequiredConcurrentSuccessful is how may consecutive passing requests are required before the circuit is closed
	RequiredConcurrentSuccessful int64
}

// Merge this configuration with another
func (c *ConfigureSleepyCloseCheck) Merge(other ConfigureSleepyCloseCheck) {
	if c.SleepWindow == 0 {
		c.SleepWindow = other.SleepWindow
	}
	if c.HalfOpenAttempts == 0 {
		c.HalfOpenAttempts = other.HalfOpenAttempts
	}
	if c.RequiredConcurrentSuccessful == 0 {
		c.RequiredConcurrentSuccessful = other.RequiredConcurrentSuccessful
	}
}

var defaultConfigureSleepyCloseCheck = ConfigureSleepyCloseCheck{
	SleepWindow:                  5 * time.Second,
	HalfOpenAttempts:             1,
	RequiredConcurrentSuccessful: 1,
}

// Opened circuit. It should now check to see if it should ever allow various requests in an attempt to become closed
func (s *SleepyCloseCheck) Opened(now time.Time) {
	s.concurrentSuccessfulAttempts.Set(0)
	s.reopenCircuitCheck.SleepStart(now)
}

// Closed circuit.  It can turn off now.
func (s *SleepyCloseCheck) Closed(now time.Time) {
	s.concurrentSuccessfulAttempts.Set(0)
	s.reopenCircuitCheck.SleepStart(now)
}

// Allow checks for half open state.
// The circuit is currently closed.  Check and return true if this request should be allowed.  This will signal
// the circuit in a "half-open" state, allowing that one request.
// If any requests are allowed, the circuit moves into a half open state.
func (s *SleepyCloseCheck) Allow(now time.Time) (shouldAllow bool) {
	return s.reopenCircuitCheck.Check(now)
}

// Success any time runFunc was called and appeared healthy
func (s *SleepyCloseCheck) Success(now time.Time, duration time.Duration) {
	s.concurrentSuccessfulAttempts.Add(1)
}

// ErrBadRequest is ignored
func (s *SleepyCloseCheck) ErrBadRequest(now time.Time, duration time.Duration) {
}

// ErrInterrupt is ignored
func (s *SleepyCloseCheck) ErrInterrupt(now time.Time, duration time.Duration) {
}

// ErrConcurrencyLimitReject is ignored
func (s *SleepyCloseCheck) ErrConcurrencyLimitReject(now time.Time) {
}

// ErrShortCircuit is ignored
func (s *SleepyCloseCheck) ErrShortCircuit(now time.Time) {
}

// ErrFailure resets the consecutive Successful count
func (s *SleepyCloseCheck) ErrFailure(now time.Time, duration time.Duration) {
	s.concurrentSuccessfulAttempts.Set(0)
}

// ErrTimeout resets the consecutive Successful count
func (s *SleepyCloseCheck) ErrTimeout(now time.Time, duration time.Duration) {
	s.concurrentSuccessfulAttempts.Set(0)
}

// ShouldClose is true if we hav enough successful attempts in a row.
func (s *SleepyCloseCheck) ShouldClose(now time.Time) bool {
	return s.concurrentSuccessfulAttempts.Get() > s.closeOnCurrentCount.Get()
}

// Config returns the current configuration.  Use SetConfigThreadSafe to modify the current configuration.
func (s *SleepyCloseCheck) Config() ConfigureSleepyCloseCheck {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

// SetConfigThreadSafe resets the sleep duration during reopen attempts
func (s *SleepyCloseCheck) SetConfigThreadSafe(config ConfigureSleepyCloseCheck) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = config
	s.reopenCircuitCheck.SetSleepDuration(config.SleepWindow)
	s.reopenCircuitCheck.SetEventCountToAllow(config.HalfOpenAttempts)
	s.closeOnCurrentCount.Set(config.RequiredConcurrentSuccessful)
}

// SetConfigNotThreadSafe just calls SetConfigThreadSafe. It is not safe to call while the circuit is active.
func (s *SleepyCloseCheck) SetConfigNotThreadSafe(config ConfigureSleepyCloseCheck) {
	s.SetConfigThreadSafe(config)
}
