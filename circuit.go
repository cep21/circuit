package circuit

import (
	"context"
	"expvar"
	"sync"
	"time"

	"github.com/cep21/circuit/v4/faststats"
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
	// per-request fast path: only once a transition is actually being attempted, and never held while
	// Opened()/Closed() listeners run.
	transitionMu sync.Mutex
	// pendingTransitions are state changes whose Opened()/Closed() notifications have not been delivered yet.
	// They are delivered FIFO by a single goroutine at a time (deliveringTransitions) so listeners observe them
	// strictly alternating and in order, while a listener that re-enters OpenCircuit/CloseCircuit just enqueues.
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
	_                   [cacheLineSize - 16]byte
}

// cacheLineSize is a conservative guess that covers amd64/arm64.  Being wrong only costs a little padding.
const cacheLineSize = 64

// transition is a queued Opened (open=true) or Closed notification
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

// SetConfigNotThreadSafe is only useful during construction before a circuit is being used.  It is not thread safe,
// but will modify all the circuit's internal structs to match what the config wants.  It also doe *NOT* use the
// default configuration parameters.
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

	c.CircuitMetricsCollector = append(
		make([]Metrics, 0, len(config.Metrics.Circuit)+2),
		c.OpenToClose,
		c.ClosedToOpen)
	c.CircuitMetricsCollector = append(c.CircuitMetricsCollector, config.Metrics.Circuit...)

	c.SetConfigThreadSafe(config)

	if c.isOpen.Get() {
		// The open/close logic was just recreated from the factories and has never seen this circuit's state.  If we
		// are (re)configured while open, tell the new logic so it can arm its half-open behavior; otherwise it may
		// never allow a probe and the circuit could never close on its own.
		now := c.now()
		c.OpenToClose.Opened(context.Background(), now)
		c.ClosedToOpen.Opened(context.Background(), now)
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
			"run_metrics":          expvarToVal(c.CmdMetricCollector.Var()),
			"concurrent_commands":  c.ConcurrentCommands(),
			"concurrent_fallbacks": c.ConcurrentFallbacks(),
			"closer":               c.OpenToClose,
			"opener":               c.ClosedToOpen,
			"fallback_metrics":     expvarToVal(c.FallbackMetricCollector.Var()),
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
// healthy, it will just open back up again.
func (c *Circuit) CloseCircuit(ctx context.Context) {
	c.close(ctx, c.now(), true)
}

// OpenCircuit will open a closed circuit.  The circuit will then try to repair itself
func (c *Circuit) OpenCircuit(ctx context.Context) {
	c.openCircuit(ctx, c.now(), false)
}

// openCircuit opens a circuit.  If recheck is true, ClosedToOpen.ShouldOpen is consulted again under the transition
// lock (the caller's earlier answer may be stale if the circuit opened and closed in between); otherwise the open is
// unconditional.  This operates on the underlying open/closed state, independent of the ForceOpen/ForcedClosed config
// overrides (which only change how that state is interpreted).
func (c *Circuit) openCircuit(ctx context.Context, now time.Time, recheck bool) {
	if c.isOpen.Get() {
		// Cheap pre-check: don't bother opening a circuit that is already open
		return
	}
	c.transitionMu.Lock()
	if c.isOpen.Get() || (recheck && !c.ClosedToOpen.ShouldOpen(ctx, now)) {
		// Another goroutine already opened it (don't double-emit Opened()), or the caller's earlier ShouldOpen
		// answer went stale because the circuit opened and closed in between.
		c.transitionMu.Unlock()
		return
	}
	c.isOpen.Set(true)
	// Timestamp the transition *after* the flag flipped (never earlier than the event that caused it): any request
	// that starts after this instant is guaranteed to have seen the open circuit, which lets OpenToClosed logic tell
	// genuine half-open probes from requests that were already in flight.
	if transitionTime := c.now(); transitionTime.After(now) {
		now = transitionTime
	}
	c.pendingTransitions = append(c.pendingTransitions, transition{open: true, ctx: ctx, now: now})
	c.deliverTransitionsAndUnlock()
}

// deliverTransitionsAndUnlock must be called with transitionMu held, right after queueing a transition, and returns
// with it released.  Notifications are delivered without holding the lock (listeners are user code that may be
// slow or re-enter this circuit), by whichever goroutine got here first, in FIFO order until the queue is empty.
func (c *Circuit) deliverTransitionsAndUnlock() {
	if c.deliveringTransitions {
		// A delivery loop further up some stack (possibly the very listener that re-entered us) will deliver the
		// transition we just queued, after the ones before it.
		c.transitionMu.Unlock()
		return
	}
	c.deliveringTransitions = true
	for len(c.pendingTransitions) > 0 {
		next := c.pendingTransitions[0]
		c.pendingTransitions = c.pendingTransitions[1:]
		if len(c.pendingTransitions) == 0 {
			c.pendingTransitions = nil
		}
		c.transitionMu.Unlock()
		c.notifyTransition(next)
		c.transitionMu.Lock()
	}
	c.deliveringTransitions = false
	c.transitionMu.Unlock()
}

// notifyTransition invokes the Opened/Closed listeners for one transition.  transitionMu must NOT be held.
func (c *Circuit) notifyTransition(t transition) {
	defer func() {
		if r := recover(); r != nil {
			// A listener panicked.  If something up the stack recovers (net/http does), make sure later
			// transitions can still be delivered rather than queueing forever behind a dead delivery loop.
			c.transitionMu.Lock()
			c.deliveringTransitions = false
			c.transitionMu.Unlock()
			panic(r)
		}
	}()
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
func (c *Circuit) Execute(ctx context.Context, runFunc func(context.Context) error, fallbackFunc func(context.Context, error) error) error {
	if runFunc == nil {
		return nil
	}
	if c.isEmptyOrNil() || c.threadSafeConfig.CircuitBreaker.Disabled.Get() {
		return runFunc(ctx)
	}

	// Try to run the command in the context of the circuit
	badRequest, err := c.run(ctx, runFunc)
	if err == nil {
		return nil
	}
	// A bad request should not trigger fallback logic.  The user just gave bad input.
	// The list of conditions that trigger fallbacks is documented at
	// https://github.com/Netflix/Hystrix/wiki/Metrics-and-Monitoring#command-execution-event-types-comnetflixhystrixhystrixeventtype
	if badRequest {
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
//
// badRequest is true if retErr was classified as a bad request (see IsBadRequest), so callers do not
// need to re-run that (comparatively expensive) classification.
func (c *Circuit) run(ctx context.Context, runFunc func(context.Context) error) (badRequest bool, retErr error) {
	var expectedDoneBy time.Time
	startTime := c.now()
	originalContext := ctx

	maxConcurrent := c.threadSafeConfig.Execution.MaxConcurrentRequests.Get()
	if !c.allowNewRun(ctx, startTime, maxConcurrent) {
		// Rather than make this inline, return a global reference (for memory optimization sake).
		c.CmdMetricCollector.ErrShortCircuit(ctx, startTime)
		return false, errCircuitOpen
	}

	if c.ClosedToOpen.Prevent(ctx, startTime) {
		return false, errCircuitOpen
	}

	currentCommandCount := c.concurrentCommands.Add(1)
	defer c.concurrentCommands.Add(-1)
	if maxConcurrent >= 0 && currentCommandCount > maxConcurrent {
		c.CmdMetricCollector.ErrConcurrencyLimitReject(ctx, startTime)
		return false, errThrottledConcurrentCommands
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
		return true, ret
	}

	// Even if there is no error (or if there is an error), if the request took too long it is always an error for the
	// circuit.  Note that ret *MAY* actually be nil.  In that case, we still want to return nil.
	if c.checkErrTimeout(ctx, expectedDoneBy, runFuncDoneTime, totalCmdTime) {
		// Note: ret could possibly be nil.  We will still return nil, but the circuit will consider it a failure.
		return false, ret
	}

	// The runFunc failed, but someone asked the original context to end.  This probably isn't a failure of the
	// circuit: someone just wanted `Execute` to end early, so don't track it as a failure.
	if c.checkErrInterrupt(ctx, originalContext, ret, runFuncDoneTime, totalCmdTime) {
		return false, ret
	}

	if c.checkErrFailure(ctx, ret, runFuncDoneTime, totalCmdTime) {
		return false, ret
	}

	// The circuit works.  Close it!
	// Note: Execute this *after* you check for timeouts so we can still track circuit time outs that happen to also return a
	//       valid value later.
	c.checkSuccess(ctx, runFuncDoneTime, totalCmdTime)
	return false, nil
}

func (c *Circuit) checkSuccess(ctx context.Context, runFuncDoneTime time.Time, totalCmdTime time.Duration) {
	c.CmdMetricCollector.Success(ctx, runFuncDoneTime, totalCmdTime)
	if c.isOpen.Get() {
		c.close(ctx, runFuncDoneTime, false)
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

// close closes an open circuit.  Usually because we think it's healthy again.  Like openCircuit, this operates on
// the underlying open/closed state independent of the ForceOpen/ForcedClosed overrides, so that (for example) a
// manual CloseCircuit() while ForcedClosed is set does not silently spring back open once the override is removed.
func (c *Circuit) close(ctx context.Context, now time.Time, forceClosed bool) {
	if !c.isOpen.Get() {
		// Not open.  Don't need to close it
		return
	}
	if !forceClosed {
		if c.threadSafeConfig.CircuitBreaker.ForceOpen.Get() {
			// While forced open, only an explicit CloseCircuit() may change the underlying state
			return
		}
		if !c.OpenToClose.ShouldClose(ctx, now) {
			return
		}
	}
	c.transitionMu.Lock()
	if !c.isOpen.Get() || (!forceClosed && !c.OpenToClose.ShouldClose(ctx, now)) {
		// Another goroutine already closed it (don't double-emit Closed()), or the circuit closed and re-opened
		// between our first check and taking the lock so our answer was stale.
		c.transitionMu.Unlock()
		return
	}
	c.isOpen.Set(false)
	c.pendingTransitions = append(c.pendingTransitions, transition{open: false, ctx: ctx, now: now})
	c.deliverTransitionsAndUnlock()
}

// attemptToOpen tries to open an unhealthy circuit.  Usually because we think run is having problems, and we want
// to give run a rest for a bit.
//
// It is called "attemptToOpen" because the circuit may not actually open (for example if there aren't enough requests)
func (c *Circuit) attemptToOpen(ctx context.Context, now time.Time) {
	if c.threadSafeConfig.CircuitBreaker.ForcedClosed.Get() {
		// Don't open circuits that are forced closed
		return
	}
	if c.isOpen.Get() {
		// Don't bother opening a circuit that is already open
		// This check isn't needed (it is also checked inside openCircuit below), but is an optimization to avoid
		// the below logic when the circuit is in a bad state and would otherwise try to close itself repeatedly.
		return
	}

	if c.ClosedToOpen.ShouldOpen(ctx, now) {
		c.openCircuit(ctx, now, true)
	}
}
