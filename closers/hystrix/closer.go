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

// Closer is hystrix's default half-open logic: try again every X ms.
//
// A Closer tracks the open episode of the single circuit it belongs to (via Opened/Closed), so construct one per
// circuit through CloserFactory / Factory rather than sharing an instance.  If the circuit nevertheless asks Allow
// while the Closer has not been told about the current open event (a hand-assigned Circuit.OpenToClose, one
// instance mistakenly shared by two circuits, ...), the Closer denies and, unless it was told Closed less than a
// SleepWindow ago, arms a sleep window on the spot as if Opened had just been called: that costs at most two extra
// SleepWindows instead of leaving the circuit open forever.
type Closer struct {
	// Tracks when we should try to close an open circuit again
	reopenCircuitCheck faststats.TimedCheck

	concurrentSuccessfulAttempts faststats.AtomicInt64
	closeOnCurrentCount          faststats.AtomicInt64

	// openedAt is the start of the current open episode: the `now` given to the most recent Opened() (or to the
	// Allow() that had to self-arm), and nil while closed / not armed.  It is the single source of truth for "are
	// we armed": half-open probes are only granted while it is set (the sleep window is always started before it is
	// published), and only the results of requests that *started* after it count towards closing: a request that
	// began while the circuit was still closed and happens to finish after it opened says nothing about whether the
	// backend has recovered.  Kept as a time.Time (rather than UnixNano) so comparisons use the monotonic clock when
	// available and survive wall-clock steps.
	openedAt atomic.Pointer[time.Time]
	// closedAt is the `now` given to the most recent Closed(), or nil if Closed was never called.  Allow uses it to
	// tell a request that merely raced a close (and must not re-arm us) from a Closer that genuinely was never told
	// about the current open episode.
	closedAt atomic.Pointer[time.Time]
	// sleepWindow mirrors config.SleepWindow (nanoseconds) for lock-free reads in Allow
	sleepWindow faststats.AtomicInt64

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
	s.arm(now, true)
}

// arm starts a new open episode at now: it starts the sleep window, then publishes openedAt, and resets the
// success streak last.  Starting the window before publishing means anyone who observes openedAt != nil is
// guaranteed to Check() a TimedCheck that has already been armed for this episode (never an expired window left
// over from a previous one).
//
// Opened passes force=true and always wins.  The self-arming fallback in Allow passes force=false so that it only
// fills in a missing openedAt and never overwrites the one a concurrent, real Opened() just published.  Several
// goroutines may race to self-arm and each call SleepStart; that is fine: TimedCheck is versioned (last writer wins)
// and they all start the window at ~now.
func (s *Closer) arm(now time.Time, force bool) {
	s.reopenCircuitCheck.SleepStart(now)
	// Publish only once the sleep window above is armed: Allow() must not be able to Check() before that
	if force {
		s.openedAt.Store(&now)
	} else {
		s.openedAt.CompareAndSwap(nil, &now)
	}
	// Reset the streak last.  An Allow that slipped in between the circuit flipping to open and the real Opened()
	// may have self-armed a slightly earlier openedAt, and the success of a straggler that started while the circuit
	// was still closed (but after that transient openedAt) may already have been counted against it.  A real Opened()
	// has just superseded that openedAt above, so wipe whatever was counted under it.  Nothing legitimate is lost: no
	// probe of the window armed above can have been granted yet, let alone completed.
	s.concurrentSuccessfulAttempts.Set(0)
}

// Closed circuit.  It can turn off now.
func (s *Closer) Closed(_ context.Context, now time.Time) {
	// Publish closedAt first: anyone who observes the nil openedAt stored below must also see this close's time.
	s.closedAt.Store(&now)
	s.openedAt.Store(nil)
	s.concurrentSuccessfulAttempts.Set(0)
}

// Allow checks for half open state.
// The circuit is currently open.  Check and return true if this request should be allowed.  This will signal
// the circuit in a "half-open" state, allowing that one request.
// If any requests are allowed, the circuit moves into a half open state.
//
// While the Closer is not armed (it was never told Opened for this open event, or was just told Closed) Allow always
// denies.  If it was told Closed less than a SleepWindow before now it does nothing else: the caller is a request
// that observed the open state just before the circuit closed, or the circuit re-opened and Opened() is about to
// arrive and arm us properly.  Otherwise nobody is going to tell us (see the type documentation), so this Allow arms
// a sleep window starting at now itself; probes are then handed out on the normal schedule after that window.
func (s *Closer) Allow(_ context.Context, now time.Time) (shouldAllow bool) {
	if s.openedAt.Load() == nil {
		if closedAt := s.closedAt.Load(); closedAt != nil && now.Sub(*closedAt) < s.sleepWindow.Duration() {
			// We were told Closed a moment ago.  This request raced that close (it read the open state, then the
			// circuit closed, then it asked us), or the circuit has already re-opened and its Opened() will arm us
			// any moment now.  Either way do not arm here: a self-armed openedAt that outlives the close would count
			// closed-circuit successes as probe results and hand the next open episode a long-expired sleep window.
			return false
		}
		// The circuit only asks while it is open, yet nobody told us it opened and nobody closed us recently: a
		// Closer shared between circuits and Closed by the other one a while ago, or a Closer assigned to
		// Circuit.OpenToClose by hand.  Arm a sleep window starting now (as if Opened had just been called) and deny
		// this request; the worst case is an extra SleepWindow or two rather than never probing again.
		s.arm(now, false)
		return false
	}
	return s.reopenCircuitCheck.Check(now)
}

// startedWhileOpen reports whether a request that finished at now after running for duration (i.e. started at
// now-duration) began strictly after the most recent Opened() (or self-arm).  Only those requests -- half-open
// probes -- say anything about whether we should close.
func (s *Closer) startedWhileOpen(now time.Time, duration time.Duration) bool {
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

// SetConfigThreadSafe updates SleepWindow, HalfOpenAttempts and RequiredConcurrentSuccessful on a live Closer.
// config.AfterFunc is recorded (and visible via Config()) but not applied: AfterFunc cannot be changed on a live
// Closer; use SetConfigNotThreadSafe for that.
func (s *Closer) SetConfigThreadSafe(config ConfigureCloser) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = config
	s.reopenCircuitCheck.SetSleepDuration(config.SleepWindow)
	s.sleepWindow.Set(config.SleepWindow.Nanoseconds())
	s.reopenCircuitCheck.SetEventCountToAllow(config.HalfOpenAttempts)
	s.closeOnCurrentCount.Set(config.RequiredConcurrentSuccessful)
}

// SetConfigNotThreadSafe (re)configures everything, including AfterFunc. It is not safe to call while the circuit
// is active.
func (s *Closer) SetConfigNotThreadSafe(config ConfigureCloser) {
	s.reopenCircuitCheck.TimeAfterFunc = config.AfterFunc
	s.SetConfigThreadSafe(config)
}
