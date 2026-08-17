package hystrix

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cep21/circuit/v4"
	"github.com/cep21/circuit/v4/faststats"
)

// Closer is hystrix's default half-open logic: try again ever X ms.
//
// A Closer tracks the state of the single circuit it belongs to (via Opened/Closed), so one instance must never be
// shared between circuits: always construct through CloserFactory / Factory, which create one per circuit.
type Closer struct {
	// Tracks when we should try to close an open circuit again
	reopenCircuitCheck faststats.TimedCheck

	concurrentSuccessfulAttempts faststats.AtomicInt64
	closeOnCurrentCount          faststats.AtomicInt64

	// isOpen mirrors the circuit's state as reported to us via Opened()/Closed().  Half-open probes are only
	// granted while open (after Opened() has armed the sleep window), and only the results of requests that
	// *started* after openedAt count towards closing: a request that began while the circuit was still closed and
	// happens to finish after it opened says nothing about whether the backend has recovered.
	isOpen faststats.AtomicBoolean
	// openedAt is the `now` given to the most recent Opened().  Kept as a time.Time (rather than UnixNano) so
	// comparisons use the monotonic clock when available and survive wall-clock steps.
	openedAt atomic.Pointer[time.Time]

	mu     sync.Mutex
	config ConfigureCloser
}

// CloserFactory creates Closer closer
func CloserFactory(config ConfigureCloser) func() circuit.OpenToClosed {
	return func() circuit.OpenToClosed {
		s := Closer{}
		cfg := config
		cfg.Merge(defaultConfigureCloser)
		s.SetConfigNotThreadSafe(cfg)
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
func (s *Closer) Opened(_ context.Context, now time.Time) {
	s.openedAt.Store(&now)
	s.concurrentSuccessfulAttempts.Set(0)
	s.reopenCircuitCheck.SleepStart(now)
	// Set last: Allow() must not grant a probe until the sleep window above is armed
	s.isOpen.Set(true)
}

// Closed circuit.  It can turn off now.
func (s *Closer) Closed(_ context.Context, _ time.Time) {
	s.isOpen.Set(false)
	s.concurrentSuccessfulAttempts.Set(0)
}

// Allow checks for half open state.
// The circuit is currently open.  Check and return true if this request should be allowed.  This will signal
// the circuit in a "half-open" state, allowing that one request.
// If any requests are allowed, the circuit moves into a half open state.
func (s *Closer) Allow(_ context.Context, now time.Time) (shouldAllow bool) {
	if !s.isOpen.Get() {
		// Either genuinely closed (the circuit won't ask), forced open without ever having opened, or we are in the
		// tiny window between the circuit flipping to open and telling us via Opened().  In every case the sleep
		// window is not armed for this open event, so do not let a probe through.
		return false
	}
	return s.reopenCircuitCheck.Check(now)
}

// startedWhileOpen returns true if a request that finished at now after running for duration began after the most
// recent Opened().  Only those requests (half-open probes) say anything about whether we should close.
func (s *Closer) startedWhileOpen(now time.Time, duration time.Duration) bool {
	if !s.isOpen.Get() {
		return false
	}
	openedAt := s.openedAt.Load()
	return openedAt != nil && now.Add(-duration).After(*openedAt)
}

// Success any time runFunc was called and appeared healthy
func (s *Closer) Success(_ context.Context, now time.Time, duration time.Duration) {
	if s.startedWhileOpen(now, duration) {
		s.concurrentSuccessfulAttempts.Add(1)
	}
}

// ErrBadRequest is ignored
func (s *Closer) ErrBadRequest(_ context.Context, _ time.Time, _ time.Duration) {
}

// ErrInterrupt is ignored
func (s *Closer) ErrInterrupt(_ context.Context, _ time.Time, _ time.Duration) {
}

// ErrConcurrencyLimitReject is ignored
func (s *Closer) ErrConcurrencyLimitReject(_ context.Context, _ time.Time) {
}

// ErrShortCircuit is ignored
func (s *Closer) ErrShortCircuit(_ context.Context, _ time.Time) {
}

// ErrFailure resets the consecutive Successful count
func (s *Closer) ErrFailure(_ context.Context, now time.Time, duration time.Duration) {
	if s.startedWhileOpen(now, duration) {
		s.concurrentSuccessfulAttempts.Set(0)
	}
}

// ErrTimeout resets the consecutive Successful count
func (s *Closer) ErrTimeout(_ context.Context, now time.Time, duration time.Duration) {
	if s.startedWhileOpen(now, duration) {
		s.concurrentSuccessfulAttempts.Set(0)
	}
}

// ShouldClose is true if we have enough successful attempts in a row.
func (s *Closer) ShouldClose(_ context.Context, _ time.Time) bool {
	return s.concurrentSuccessfulAttempts.Get() >= s.closeOnCurrentCount.Get()
}

// Config returns the current configuration.  Use SetConfigThreadSafe to modify the current configuration.
func (s *Closer) Config() ConfigureCloser {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

// SetConfigThreadSafe resets the sleep duration during reopen attempts.  AfterFunc cannot be changed on a live
// Closer; use SetConfigNotThreadSafe for that.
func (s *Closer) SetConfigThreadSafe(config ConfigureCloser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = config
	s.reopenCircuitCheck.SetSleepDuration(config.SleepWindow)
	s.reopenCircuitCheck.SetEventCountToAllow(config.HalfOpenAttempts)
	s.closeOnCurrentCount.Set(config.RequiredConcurrentSuccessful)
}

// SetConfigNotThreadSafe (re)configures everything, including AfterFunc. It is not safe to call while the circuit
// is active.
func (s *Closer) SetConfigNotThreadSafe(config ConfigureCloser) {
	s.reopenCircuitCheck.TimeAfterFunc = config.AfterFunc
	s.SetConfigThreadSafe(config)
}
