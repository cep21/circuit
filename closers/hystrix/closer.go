package hystrix

import (
	"context"
	"sync"
	"time"

	"encoding/json"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/faststats"
)

// Closer is hystrix's default half-open logic: try again ever X ms
type Closer struct {
	// Tracks when we should try to close an open circuit again
	reopenCircuitCheck faststats.TimedCheck

	concurrentSuccessfulAttempts faststats.AtomicInt64
	closeOnCurrentCount          faststats.AtomicInt64

	mu     sync.Mutex
	config ConfigureCloser
}

// CloserFactory creates Closer closer
func CloserFactory(config ConfigureCloser) func() circuit.OpenToClosed {
	return func() circuit.OpenToClosed {
		s := Closer{}
		config.Merge(defaultConfigureCloser)
		s.SetConfigNotThreadSafe(config)
		return &s
	}
}

var _ circuit.OpenToClosed = &Closer{}

// ConfigureCloser configures values for Closer
type ConfigureCloser struct {
	// AfterFunc should simulate time.AfterFunc
	AfterFunc func(time.Duration, func()) *time.Timer `json:"-"`

	// SleepWindow is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakersleepwindowinmilliseconds
	SleepWindow time.Duration
	// HalfOpenAttempts is how many attempts to allow per SleepWindow
	HalfOpenAttempts int64
	// RequiredConcurrentSuccessful is how may consecutive passing requests are required before the circuit is closed
	RequiredConcurrentSuccessful int64
}

// Merge this configuration with another
func (c *ConfigureCloser) Merge(other ConfigureCloser) {
	if c.SleepWindow == 0 {
		c.SleepWindow = other.SleepWindow
	}
	if c.HalfOpenAttempts == 0 {
		c.HalfOpenAttempts = other.HalfOpenAttempts
	}
	if c.RequiredConcurrentSuccessful == 0 {
		c.RequiredConcurrentSuccessful = other.RequiredConcurrentSuccessful
	}
	if c.AfterFunc == nil {
		c.AfterFunc = other.AfterFunc
	}
}

var defaultConfigureCloser = ConfigureCloser{
	SleepWindow:                  5 * time.Second,
	HalfOpenAttempts:             1,
	RequiredConcurrentSuccessful: 1,
}

// MarshalJSON returns closer information in a JSON format
func (s *Closer) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"config":                       s.Config(),
		"concurrentSuccessfulAttempts": s.concurrentSuccessfulAttempts.Get(),
	})
}

var _ json.Marshaler = &Closer{}

// Opened circuit. It should now check to see if it should ever allow various requests in an attempt to become closed
func (s *Closer) Opened(ctx context.Context, now time.Time) {
	s.concurrentSuccessfulAttempts.Set(0)
	s.reopenCircuitCheck.SleepStart(now)
}

// Closed circuit.  It can turn off now.
func (s *Closer) Closed(ctx context.Context, now time.Time) {
	s.concurrentSuccessfulAttempts.Set(0)
	s.reopenCircuitCheck.SleepStart(now)
}

// Allow checks for half open state.
// The circuit is currently closed.  Check and return true if this request should be allowed.  This will signal
// the circuit in a "half-open" state, allowing that one request.
// If any requests are allowed, the circuit moves into a half open state.
func (s *Closer) Allow(ctx context.Context, now time.Time) (shouldAllow bool) {
	return s.reopenCircuitCheck.Check(now)
}

// Success any time runFunc was called and appeared healthy
func (s *Closer) Success(ctx context.Context, now time.Time, duration time.Duration) {
	s.concurrentSuccessfulAttempts.Add(1)
}

// ErrBadRequest is ignored
func (s *Closer) ErrBadRequest(ctx context.Context, now time.Time, duration time.Duration) {
}

// ErrInterrupt is ignored
func (s *Closer) ErrInterrupt(ctx context.Context, now time.Time, duration time.Duration) {
}

// ErrConcurrencyLimitReject is ignored
func (s *Closer) ErrConcurrencyLimitReject(ctx context.Context, now time.Time) {
}

// ErrShortCircuit is ignored
func (s *Closer) ErrShortCircuit(ctx context.Context, now time.Time) {
}

// ErrFailure resets the consecutive Successful count
func (s *Closer) ErrFailure(ctx context.Context, now time.Time, duration time.Duration) {
	s.concurrentSuccessfulAttempts.Set(0)
}

// ErrTimeout resets the consecutive Successful count
func (s *Closer) ErrTimeout(ctx context.Context, now time.Time, duration time.Duration) {
	s.concurrentSuccessfulAttempts.Set(0)
}

// ShouldClose is true if we have enough successful attempts in a row.
func (s *Closer) ShouldClose(ctx context.Context, now time.Time) bool {
	return s.concurrentSuccessfulAttempts.Get() >= s.closeOnCurrentCount.Get()
}

// Config returns the current configuration.  Use SetConfigThreadSafe to modify the current configuration.
func (s *Closer) Config() ConfigureCloser {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

// SetConfigThreadSafe resets the sleep duration during reopen attempts
func (s *Closer) SetConfigThreadSafe(config ConfigureCloser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = config
	s.reopenCircuitCheck.TimeAfterFunc = config.AfterFunc
	s.reopenCircuitCheck.SetSleepDuration(config.SleepWindow)
	s.reopenCircuitCheck.SetEventCountToAllow(config.HalfOpenAttempts)
	s.closeOnCurrentCount.Set(config.RequiredConcurrentSuccessful)
}

// SetConfigNotThreadSafe just calls SetConfigThreadSafe. It is not safe to call while the circuit is active.
func (s *Closer) SetConfigNotThreadSafe(config ConfigureCloser) {
	s.SetConfigThreadSafe(config)
}
