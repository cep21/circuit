package circuit

import (
	"context"
	"expvar"
	"sync"
	"time"

	"github.com/cep21/circuit/v4/faststats"
	"github.com/cep21/circuit/v4/internal/evar"
)

// Circuit is a circuit breaker pattern implementation that can accept commands and open/close on failures
type Circuit struct {
	// circuitStats
	CmdMetricCollector      RunMetricsCollection
	FallbackMetricCollector FallbackMetricsCollection
	CircuitMetricsCollector MetricsCollection
	// This is used to help run `Go` calls in the background
	goroutineWrapper goroutineWrapper
	name             string
	// The passed in config is not atomic and thread safe.  We reference thread safe values during circuit operations
	// with atomicCircuitConfig.  Those are, also, the only values that can actually be changed while a circuit is
	// running.
	notThreadSafeConfig Config
	// The mutex supports setting and reading the command properties, but is not locked when we reference the config
	// while live: we use the threadSafeConfig below
	notThreadSafeConfigMu sync.Mutex
	threadSafeConfig      atomicCircuitConfig

	// Tracks if the circuit has been shut open or closed
	isOpen faststats.AtomicBoolean

	// ClosedToOpen controls when to open a closed circuit
	ClosedToOpen ClosedToOpen
	// openToClosed controls when to close an open circuit
	OpenToClose OpenToClosed

	timeNow func() time.Time

	// transitionMu guards open<->close transitions and the two fields below.  It is never taken on the
	// per-request fast path: only once a transition is actually being attempted (commitTransition).  It is held
	// while the circuit's own OpenToClose/ClosedToOpen logic is told about a transition, but never while user
	// Metrics listeners run.
	transitionMu sync.Mutex
	// pendingTransitions are state changes whose Opened()/Closed() notifications have not been delivered to user
	// Metrics listeners yet.  They are delivered FIFO by a single goroutine at a time (deliveringTransitions) so
	// listeners observe them strictly alternating and in order, while a listener that re-enters
	// OpenCircuit/CloseCircuit just enqueues.  The backing array is kept across transitions.
	pendingTransitions    []transition
	deliveringTransitions bool

	// The two counters below are written (atomic add) on every Execute.  Everything above is read-mostly and also
	// consulted on every Execute, so keep the counters on their own cache line: otherwise each request on one core
	// invalidates the config/state line every other core is reading (false sharing).
	_ [cacheLineSize]byte
	// Tracks how many commands are currently running
	concurrentCommands faststats.AtomicInt64
	// Tracks how many fallbacks are currently running
	concurrentFallbacks faststats.AtomicInt64
	// A full trailing line (rather than just topping the counters up to one line) keeps the isolation independent
	// of where the struct happens to land, e.g. a Circuit embedded by value or in a []Circuit.
	_ [cacheLineSize]byte
}

// cacheLineSize: 64 bytes covers amd64 and most arm64 parts (some arm64 use 128, where this merely halves the
// benefit).  Being wrong only costs a little padding.
const cacheLineSize = 64

// transition is a queued Opened (open=true) or Closed notification for user Metrics listeners
type transition struct {
	open bool
	ctx  context.Context
	now  time.Time
}

// NewCircuitFromConfig creates an inline circuit.  If you want to group all your circuits together, you should probably
// just use Manager struct instead.
func NewCircuitFromConfig(name string, config Config) *Circuit {
	config.Merge(defaultCommandProperties)
	ret := &Circuit{
		name:                name,
		notThreadSafeConfig: config,
	}
	ret.SetConfigNotThreadSafe(config)
	return ret
}

// ConcurrentCommands returns how many commands are currently running
func (c *Circuit) ConcurrentCommands() int64 {
	return c.concurrentCommands.Get()
}

// ConcurrentFallbacks returns how many fallbacks are currently running
func (c *Circuit) ConcurrentFallbacks() int64 {
	return c.concurrentFallbacks.Get()
}

// SetConfigThreadSafe changes the current configuration of this circuit. Note that many config parameters, specifically those
// around creating stat tracking buckets, are not modifiable during runtime for efficiency reasons.  Those buckets
// will stay the same.
func (c *Circuit) SetConfigThreadSafe(config Config) {
	c.notThreadSafeConfigMu.Lock()
	defer c.notThreadSafeConfigMu.Unlock()
	c.notThreadSafeConfig = config
	c.threadSafeConfig.reset(c.notThreadSafeConfig)
	if cfg, ok := c.OpenToClose.(Configurable); ok {
		cfg.SetConfigThreadSafe(config)
	}
	if cfg, ok := c.ClosedToOpen.(Configurable); ok {
		cfg.SetConfigThreadSafe(config)
	}
}

// Config returns the circuit's configuration.  Modifications to this configuration are not reflected by the circuit.
// In other words, this creates a copy.
func (c *Circuit) Config() Config {
	c.notThreadSafeConfigMu.Lock()
	defer c.notThreadSafeConfigMu.Unlock()
	return c.notThreadSafeConfig
}

// SetConfigNotThreadSafe rebuilds the circuit's open/close logic and metric collectors from config.  It must not
// run concurrently with Execute or other Set* calls, but may be used to reconfigure an idle circuit: if the circuit
// is currently open, the new open/close logic is told so it can still recover.  It does *NOT* merge in the default
// configuration parameters.
func (c *Circuit) SetConfigNotThreadSafe(config Config) {
	c.notThreadSafeConfigMu.Lock()
	// Set, but do not reference this config inside this function, since that would not be thread safe (no mu protection)
	c.notThreadSafeConfig = config
	c.notThreadSafeConfigMu.Unlock()

	c.goroutineWrapper.lostErrors = config.General.GoLostErrors
	c.timeNow = config.General.TimeKeeper.Now

	c.OpenToClose = config.General.OpenToClosedFactory()
	c.ClosedToOpen = config.General.ClosedToOpenFactory()
	if cfg, ok := c.OpenToClose.(Configurable); ok {
		cfg.SetConfigNotThreadSafe(config)
	}
	if cfg, ok := c.ClosedToOpen.(Configurable); ok {
		cfg.SetConfigNotThreadSafe(config)
	}
	c.CmdMetricCollector = append(
		make([]RunMetrics, 0, len(config.Metrics.Run)+2),
		c.OpenToClose,
		c.ClosedToOpen)
	c.CmdMetricCollector = append(c.CmdMetricCollector, config.Metrics.Run...)

	c.FallbackMetricCollector = append(
		make([]FallbackMetrics, 0, len(config.Metrics.Fallback)+2),
		config.Metrics.Fallback...)

	// Only the user's listeners: the circuit's own OpenToClose/ClosedToOpen logic is told about transitions
	// directly (notifyStateMachines), ahead of and separately from these.
	c.CircuitMetricsCollector = append(
		make([]Metrics, 0, len(config.Metrics.Circuit)),
		config.Metrics.Circuit...)

	c.SetConfigThreadSafe(config)

	if c.isOpen.Get() {
		// The open/close logic was just recreated from the factories and has never seen this circuit's state.  If we
		// are (re)configured while open, tell the new logic so it can arm its half-open behavior; otherwise it may
		// never allow a probe and the circuit could never close on its own.  User listeners already heard about
		// this open event, so only the state machines are told.
		c.notifyStateMachines(context.Background(), c.now(), true)
	}
}

func (c *Circuit) now() time.Time {
	return c.timeNow()
}

// Var exports that help diagnose the circuit
func (c *Circuit) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		if c == nil {
			return nil
		}
		ret := map[string]interface{}{
			"config":               c.Config(),
			"is_open":              c.IsOpen(),
			"name":                 c.Name(),
			"run_metrics":          evar.ExpvarToVal(c.CmdMetricCollector.Var()),
			"concurrent_commands":  c.ConcurrentCommands(),
			"concurrent_fallbacks": c.ConcurrentFallbacks(),
			"closer":               c.OpenToClose,
			"opener":               c.ClosedToOpen,
			"fallback_metrics":     evar.ExpvarToVal(c.FallbackMetricCollector.Var()),
		}
		return ret
	})
}

// Name of this circuit
func (c *Circuit) Name() string {
	if c == nil {
		return ""
	}
	return c.name
}

// IsOpen returns true if the circuit should be considered 'open' (ie not allowing runFunc calls)
func (c *Circuit) IsOpen() bool {
	if c == nil {
		return false
	}
	if c.threadSafeConfig.CircuitBreaker.ForceOpen.Get() {
		return true
	}
	if c.threadSafeConfig.CircuitBreaker.ForcedClosed.Get() {
		return false
	}
	return c.isOpen.Get()
}

// CloseCircuit closes an open circuit.  Usually because we think it's healthy again.  Be aware, if the circuit isn't actually
// healthy, it will just open back up again.  It changes the underlying state (and notifies Metrics listeners) even
// while ForceOpen/ForcedClosed is set; IsOpen continues to reflect the override until it is cleared.
func (c *Circuit) CloseCircuit(ctx context.Context) {
	c.transitionTo(ctx, c.now(), false, nil)
}

// OpenCircuit will open a closed circuit.  The circuit will then try to repair itself.  It changes the underlying
// state (and notifies Metrics listeners) even while ForceOpen/ForcedClosed is set; IsOpen continues to reflect the
// override until it is cleared.
func (c *Circuit) OpenCircuit(ctx context.Context) {
	c.transitionTo(ctx, c.now(), true, nil)
}

// transitionTo moves the circuit's underlying state to open (open=true) or closed and delivers the resulting
// Opened()/Closed() notifications.  It is the single entry point for every state change: OpenCircuit/CloseCircuit
// (recheck == nil: unconditional) and the automatic paths in attemptToOpen/checkSuccess (recheck is the pluggable
// ShouldOpen/ShouldClose predicate, re-asked under the transition lock because the caller's unlocked answer may have
// gone stale if the circuit flapped in between).
//
// This operates on the underlying open/closed state, independent of the ForceOpen/ForcedClosed config overrides
// (which only change how that state is interpreted), so that for example a manual CloseCircuit() while ForcedClosed
// is set does not silently spring back open once the override is removed.  Callers that want an override to
// suppress an *automatic* transition check it themselves before calling.
func (c *Circuit) transitionTo(ctx context.Context, now time.Time, open bool, recheck func(context.Context, time.Time) bool) {
	if c.isOpen.Get() == open {
		// Cheap pre-check without the lock: already in the target state
		return
	}
	if c.commitTransition(ctx, now, open, recheck) {
		c.deliverPendingTransitions()
	}
}

// commitTransition is the critical section of transitionTo and the only place isOpen is flipped.  Under
// transitionMu it re-tests the target state (so concurrent callers are idempotent and Opened/Closed are emitted
// exactly once per real transition), re-asks recheck if one was given, flips the state, stamps the transition
// time, synchronously tells the circuit's own OpenToClose/ClosedToOpen logic, and queues the notification for
// user Metrics listeners.  It returns true if a transition happened.  The lock is released even if recheck or a
// state machine panics.
func (c *Circuit) commitTransition(ctx context.Context, now time.Time, open bool, recheck func(context.Context, time.Time) bool) bool {
	c.transitionMu.Lock()
	defer c.transitionMu.Unlock()
	if c.isOpen.Get() == open {
		// Another goroutine got here first; don't double-emit Opened()/Closed()
		return false
	}
	if recheck != nil && !recheck(ctx, now) {
		// The caller's earlier ShouldOpen/ShouldClose answer went stale: the circuit transitioned there and back
		// between that unlocked check and us taking the lock.
		return false
	}
	c.isOpen.Set(open)
	// Timestamp the transition *after* the flag flipped, as the later of the caller's now and the clock: any request
	// that starts after this instant is guaranteed to have seen the new state.  OpenToClosed logic (e.g. hystrix
	// Closer's openedAt) relies on Opened's now being >= the flip instant to tell genuine half-open probes from
	// requests that were already in flight.
	if flipped := c.now(); flipped.After(now) {
		now = flipped
	}
	// The state machines hear about the transition right here, inside the critical section and before any user
	// listener, so a slow or panicking user listener can never delay (or lose) arming the half-open logic.
	c.notifyStateMachines(ctx, now, open)
	c.pendingTransitions = append(c.pendingTransitions, transition{open: open, ctx: ctx, now: now})
	return true
}

// notifyStateMachines synchronously delivers Opened (open=true) or Closed to the circuit's own OpenToClose and
// ClosedToOpen logic, in that order.
func (c *Circuit) notifyStateMachines(ctx context.Context, now time.Time, open bool) {
	if open {
		c.OpenToClose.Opened(ctx, now)
		c.ClosedToOpen.Opened(ctx, now)
	} else {
		c.OpenToClose.Closed(ctx, now)
		c.ClosedToOpen.Closed(ctx, now)
	}
}

// deliverPendingTransitions delivers queued transitions to user Metrics listeners.  transitionMu must NOT be held.
// Whichever goroutine finds no delivery in progress becomes the single deliverer and drains the queue in FIFO
// order, calling listeners without holding the lock (they are user code that may be slow or re-enter this
// circuit); everyone else just returns and their queued transition is delivered by that goroutine after the ones
// before it.  Normally that means listeners run on the goroutine that caused the transition, before it returns.
//
// If a listener panics (and something up the stack recovers, as net/http does) or calls runtime.Goexit, the
// deliverer role is still released so later transitions are not queued forever behind a dead loop, and anything
// already stranded in the queue is delivered right away from the deferred cleanup: still on this goroutine (while
// it unwinds) and still in order.  The library never runs listeners on a goroutine of its own.
func (c *Circuit) deliverPendingTransitions() {
	c.transitionMu.Lock()
	if c.deliveringTransitions {
		c.transitionMu.Unlock()
		return
	}
	c.deliveringTransitions = true
	finished := false
	defer func() {
		if finished {
			return
		}
		// Abnormal exit: a listener panicked or called runtime.Goexit (the lock is not held while listeners run).
		// Do not recover: just give up the deliverer role and drain again from here so whatever was queued behind us
		// keeps flowing (on an empty queue that re-entry takes the role, loops zero times and releases it).  Deferred
		// calls run during panic unwinding and on Goexit alike; should another listener panic in there, that panic
		// nests on this same goroutine and propagates to the caller exactly like the first one (to whatever
		// recover is up the stack, if any).
		c.transitionMu.Lock()
		c.deliveringTransitions = false
		c.transitionMu.Unlock()
		c.deliverPendingTransitions()
	}()
	for len(c.pendingTransitions) > 0 {
		next := c.pendingTransitions[0]
		// Pop from the front by shifting down rather than re-slicing, so the backing array is reused forever (the
		// queue is almost always 0 or 1 long) and the vacated slot does not pin a stale ctx.
		n := copy(c.pendingTransitions, c.pendingTransitions[1:])
		c.pendingTransitions[n] = transition{}
		c.pendingTransitions = c.pendingTransitions[:n]
		c.transitionMu.Unlock()
		c.notifyListeners(next)
		c.transitionMu.Lock()
	}
	c.deliveringTransitions = false
	finished = true
	c.transitionMu.Unlock()
}

// notifyListeners invokes the user Metrics listeners for one transition (the circuit's own OpenToClose and
// ClosedToOpen logic was already told synchronously in commitTransition).  transitionMu must NOT be held.
func (c *Circuit) notifyListeners(t transition) {
	if t.open {
		c.CircuitMetricsCollector.Opened(t.ctx, t.now)
	} else {
		c.CircuitMetricsCollector.Closed(t.ctx, t.now)
	}
}

// Go executes `Execute`, but uses spawned goroutines to end early if the context is canceled.  Use this if you don't trust
// the runFunc to end correctly if context fails.  This is a design mirroed in the go-hystrix library, but be warned it
// is very dangerous and could leave orphaned goroutines hanging around forever doing who knows what.
func (c *Circuit) Go(ctx context.Context, runFunc func(context.Context) error, fallbackFunc func(context.Context, error) error) error {
	if c == nil {
		var wrapper goroutineWrapper
		return c.Execute(ctx, wrapper.run(runFunc), wrapper.fallback(fallbackFunc))
	}
	return c.Execute(ctx, c.goroutineWrapper.run(runFunc), c.goroutineWrapper.fallback(fallbackFunc))
}

// Run will execute the circuit without a fallback.  It is the equivalent of calling Execute with a nil fallback function
func (c *Circuit) Run(ctx context.Context, runFunc func(context.Context) error) error {
	return c.Execute(ctx, runFunc, nil)
}

// Execute the circuit.  Prefer this over Go.  Similar to http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/HystrixCommand.html#execute--
// The returned error will either be the result of runFunc, the result of fallbackFunc, or an internal library error.
// Internal library errors will match the interface Error and you can use type casting to check this.
// A nil runFunc is a no-op: Execute returns nil and records no metrics.
func (c *Circuit) Execute(ctx context.Context, runFunc func(context.Context) error, fallbackFunc func(context.Context, error) error) error {
	if runFunc == nil {
		return nil
	}
	if c.isEmptyOrNil() || c.threadSafeConfig.CircuitBreaker.Disabled.Get() {
		return runFunc(ctx)
	}

	// Try to run the command in the context of the circuit
	err := c.run(ctx, runFunc)
	if err == nil {
		return nil
	}
	// A bad request should not trigger fallback logic.  The user just gave bad input.
	// The list of conditions that trigger fallbacks is documented at
	// https://github.com/Netflix/Hystrix/wiki/Metrics-and-Monitoring#command-execution-event-types-comnetflixhystrixhystrixeventtype
	if IsBadRequest(err) {
		return err
	}
	return c.fallback(ctx, err, fallbackFunc)
}

// --------- only private functions below here

// isEmptyOrNil returns true if the circuit is nil or if the circuit was created from an empty circuit.  The empty
// circuit setup is mostly a guess (checking OpenToClose).  This allows us to give circuits reasonable behavior
// in the nil/empty case.
func (c *Circuit) isEmptyOrNil() bool {
	return c == nil || c.OpenToClose == nil
}

// run is the equivalent of Java Manager's http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/HystrixCommand.html#run()
func (c *Circuit) run(ctx context.Context, runFunc func(context.Context) error) (retErr error) {
	var expectedDoneBy time.Time
	startTime := c.now()
	originalContext := ctx

	maxConcurrent := c.threadSafeConfig.Execution.MaxConcurrentRequests.Get()
	if !c.allowNewRun(ctx, startTime, maxConcurrent) {
		// Rather than make this inline, return a global reference (for memory optimization sake).
		c.CmdMetricCollector.ErrShortCircuit(ctx, startTime)
		return errCircuitOpen
	}

	if c.ClosedToOpen.Prevent(ctx, startTime) {
		return errCircuitOpen
	}

	currentCommandCount := c.concurrentCommands.Add(1)
	defer c.concurrentCommands.Add(-1)
	if maxConcurrent >= 0 && currentCommandCount > maxConcurrent {
		c.CmdMetricCollector.ErrConcurrencyLimitReject(ctx, startTime)
		return errThrottledConcurrentCommands
	}

	// Set timeout on the command if we have one.  Read the atomic exactly once: a concurrent SetConfigThreadSafe
	// between two reads could otherwise produce a deadline in the past and a spurious ErrTimeout.
	if timeout := c.threadSafeConfig.Execution.ExecutionTimeout.Duration(); timeout > 0 {
		var timeoutCancel func()
		expectedDoneBy = startTime.Add(timeout)
		ctx, timeoutCancel = context.WithDeadline(ctx, expectedDoneBy)
		defer timeoutCancel()
	}

	ret := runFunc(ctx)
	runFuncDoneTime := c.now()
	totalCmdTime := runFuncDoneTime.Sub(startTime)
	// See bad request documentation at https://github.com/Netflix/Hystrix/wiki/How-To-Use#error-propagation
	// This request had invalid input, but shouldn't be marked as an 'error' for the circuit
	// From documentation
	// -------
	// The HystrixBadRequestException is intended for use cases such as reporting illegal arguments or non-system
	// failures that should not count against the failure metrics and should not trigger fallback logic.
	if c.checkErrBadRequest(ctx, ret, runFuncDoneTime, totalCmdTime) {
		return ret
	}

	// Even if there is no error (or if there is an error), if the request took too long it is always an error for the
	// circuit.  Note that ret *MAY* actually be nil.  In that case, we still want to return nil.
	if c.checkErrTimeout(ctx, expectedDoneBy, runFuncDoneTime, totalCmdTime) {
		// Note: ret could possibly be nil.  We will still return nil, but the circuit will consider it a failure.
		return ret
	}

	// The runFunc failed, but someone asked the original context to end.  This probably isn't a failure of the
	// circuit: someone just wanted `Execute` to end early, so don't track it as a failure.
	if c.checkErrInterrupt(ctx, originalContext, ret, runFuncDoneTime, totalCmdTime) {
		return ret
	}

	if c.checkErrFailure(ctx, ret, runFuncDoneTime, totalCmdTime) {
		return ret
	}

	// The circuit works.  Close it!
	// Note: Execute this *after* you check for timeouts so we can still track circuit time outs that happen to also return a
	//       valid value later.
	c.checkSuccess(ctx, runFuncDoneTime, totalCmdTime)
	return nil
}

// checkSuccess records a success and, if the circuit is open, tries to close it automatically.
func (c *Circuit) checkSuccess(ctx context.Context, runFuncDoneTime time.Time, totalCmdTime time.Duration) {
	c.CmdMetricCollector.Success(ctx, runFuncDoneTime, totalCmdTime)
	if !c.isOpen.Get() || c.threadSafeConfig.CircuitBreaker.ForceOpen.Get() {
		// Not open (nothing to close), or forced open: while forced open only an explicit CloseCircuit() may change
		// the underlying state.
		return
	}
	// Unlocked pre-check so a success while open does not take transitionMu unless a close is actually likely;
	// transitionTo re-asks ShouldClose under the lock.
	if c.OpenToClose.ShouldClose(ctx, runFuncDoneTime) {
		c.transitionTo(ctx, runFuncDoneTime, false, c.OpenToClose.ShouldClose)
	}
}

// checkErrInterrupt returns true if this is considered an interrupt error: interrupt errors do not open the circuit.
// Normally if the parent context is canceled before a timeout is reached, we don't consider the circuit
// unhealthy: unless ExecutionConfig.IgnoreInterrupts is set to true, we classify originalContext.Err()
// with the help of ExecutionConfig.IsErrInterrupt (default: every context error is an interrupt). When that
// function returns true we do not count the failure against the circuit.
func (c *Circuit) checkErrInterrupt(ctx context.Context, originalContext context.Context, ret error, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	// We need to see an error in both the original context and the return value to consider this an "interrupt" caused
	// error.
	if ret == nil || originalContext.Err() == nil {
		return false
	}

	isErrInterrupt := c.notThreadSafeConfig.Execution.IsErrInterrupt
	if isErrInterrupt == nil {
		isErrInterrupt = func(_ error) bool {
			// By default, we consider any error from the original context an interrupt causing error
			return true
		}
	}

	if !c.threadSafeConfig.GoSpecific.IgnoreInterrupts.Get() && isErrInterrupt(originalContext.Err()) {
		c.CmdMetricCollector.ErrInterrupt(ctx, runFuncDoneTime, totalCmdTime)
		return true
	}

	return false
}

func (c *Circuit) checkErrBadRequest(ctx context.Context, ret error, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	if IsBadRequest(ret) {
		c.CmdMetricCollector.ErrBadRequest(ctx, runFuncDoneTime, totalCmdTime)
		return true
	}
	return false
}

func (c *Circuit) checkErrFailure(ctx context.Context, ret error, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	if ret != nil {
		c.CmdMetricCollector.ErrFailure(ctx, runFuncDoneTime, totalCmdTime)
		if !c.isOpen.Get() {
			c.attemptToOpen(ctx, runFuncDoneTime)
		}
		return true
	}
	return false
}

func (c *Circuit) checkErrTimeout(ctx context.Context, expectedDoneBy time.Time, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	// I don't use the deadline from the context because it could be a smaller timeout from the parent context
	if !expectedDoneBy.IsZero() && expectedDoneBy.Before(runFuncDoneTime) {
		c.CmdMetricCollector.ErrTimeout(ctx, runFuncDoneTime, totalCmdTime)
		if !c.isOpen.Get() {
			c.attemptToOpen(ctx, runFuncDoneTime)
		}
		return true
	}
	return false
}

// Does fallback logic.  Equivalent of
// http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/HystrixCommand.html#getFallback
func (c *Circuit) fallback(ctx context.Context, err error, fallbackFunc func(context.Context, error) error) error {
	// Use the fallback command if available
	if fallbackFunc == nil || c.threadSafeConfig.Fallback.Disabled.Get() {
		return err
	}

	// Throttle concurrent fallback calls
	currentFallbackCount := c.concurrentFallbacks.Add(1)
	defer c.concurrentFallbacks.Add(-1)
	maxFallback := c.threadSafeConfig.Fallback.MaxConcurrentRequests.Get()
	startTime := c.now()
	if maxFallback >= 0 && currentFallbackCount > maxFallback {
		c.FallbackMetricCollector.ErrConcurrencyLimitReject(ctx, startTime)
		return errThrottledConcurrentFallbacks
	}

	retErr := fallbackFunc(ctx, err)
	totalCmdTime := c.now().Sub(startTime)
	if retErr != nil {
		c.FallbackMetricCollector.ErrFailure(ctx, startTime, totalCmdTime)
		return retErr
	}
	c.FallbackMetricCollector.Success(ctx, startTime, totalCmdTime)
	return nil
}

// allowNewRun checks if the circuit is allowing new run commands. This happens if the circuit is closed, or
// if it is open, but we want to explore to see if we should close it again.
func (c *Circuit) allowNewRun(ctx context.Context, now time.Time, maxConcurrent int64) bool {
	if c.threadSafeConfig.CircuitBreaker.ForceOpen.Get() {
		// Forced open means reject everything: do not even let half-open probes through.
		return false
	}
	if c.threadSafeConfig.CircuitBreaker.ForcedClosed.Get() || !c.isOpen.Get() {
		return true
	}
	if maxConcurrent >= 0 && c.concurrentCommands.Get() >= maxConcurrent {
		// Open *and* already at the concurrency limit: this request would be throttled without ever running, so do
		// not spend the closer's (usually single, once-per-sleep-window) half-open permit on it.
		return false
	}
	return c.OpenToClose.Allow(ctx, now)
}

// attemptToOpen tries to open an unhealthy circuit.  Usually because we think run is having problems, and we want
// to give run a rest for a bit.
//
// It is called "attemptToOpen" because the circuit may not actually open (for example if there aren't enough
// requests, or the circuit is forced closed).
func (c *Circuit) attemptToOpen(ctx context.Context, now time.Time) {
	if c.threadSafeConfig.CircuitBreaker.ForcedClosed.Get() {
		// Don't open circuits that are forced closed
		return
	}
	if c.isOpen.Get() {
		// Don't bother opening a circuit that is already open
		// This check isn't needed (it is also checked inside transitionTo below), but is an optimization to avoid
		// the below logic when the circuit is in a bad state and would otherwise try to close itself repeatedly.
		return
	}
	// Unlocked pre-filter; transitionTo re-asks ShouldOpen under the transition lock in case this answer goes stale.
	if c.ClosedToOpen.ShouldOpen(ctx, now) {
		c.transitionTo(ctx, now, true, c.ClosedToOpen.ShouldOpen)
	}
}
