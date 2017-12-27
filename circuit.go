package hystrix

import (
	"context"
	"expvar"
	"sync"
	"time"

	"github.com/cep21/hystrix/internal/fastmath"
)

// Circuit is a hystrix circuit that can accept commands and open/close on failures
type Circuit struct {
	circuitStats
	goroutineWrapper goroutineWrapper
	name             string
	// The passed in config is not atomic and thread safe.  We reference thread safe values during circuit operations
	// with atomicCircuitConfig.  Those are, also, the only values that can actually be changed while a circuit is
	// running.
	notThreadSafeConfig CommandProperties
	// The mutex supports setting and reading the command properties, but is not locked when we reference the config
	// while live: we use the threadSafeConfig below
	notThreadSafeConfigMu sync.Mutex
	threadSafeConfig      atomicCircuitConfig

	// Tracks if the circuit has been shut open or closed
	isOpen fastmath.AtomicBoolean

	// Tracks how many commands are currently running
	concurrentCommands fastmath.AtomicInt64
	// Tracks how many fallbacks are currently running
	concurrentFallbacks fastmath.AtomicInt64

	// closedToOpen controls when to open a closed circuit
	closedToOpen ClosedToOpen
	// openToClosed controls when to close an open circuit
	openToClose OpenToClosed

	timeNow func() time.Time
}

// NewCircuitFromConfig creates an inline circuit.  If you want to group all your circuits together, you should probably
// just use Hystrix struct instead.
func NewCircuitFromConfig(name string, config CommandProperties) *Circuit {
	config.merge(defaultCommandProperties)
	ret := &Circuit{
		name:                name,
		notThreadSafeConfig: config,
	}
	ret.SetConfigNotThreadSafe(config)
	return ret
}

// SetConfigThreadSafe changes the current configuration of this circuit. Note that many config parameters, specifically those
// around creating stat tracking buckets, are not modifiable during runtime for efficiency reasons.  Those buckets
// will stay the same.
func (c *Circuit) SetConfigThreadSafe(config CommandProperties) {
	c.notThreadSafeConfigMu.Lock()
	defer c.notThreadSafeConfigMu.Unlock()
	c.circuitStats.SetConfigThreadSafe(config)
	c.threadSafeConfig.reset(c.notThreadSafeConfig)
	c.notThreadSafeConfig = config
	c.openToClose.SetConfigThreadSafe(config)
	c.closedToOpen.SetConfigThreadSafe(config)
}

// Config returns the circuit's configuration.  Modifications to this configuration are not reflected by the circuit.
// In other words, this creates a copy.
func (c *Circuit) Config() CommandProperties {
	c.notThreadSafeConfigMu.Lock()
	defer c.notThreadSafeConfigMu.Unlock()
	return c.notThreadSafeConfig
}

// SetConfigNotThreadSafe is only useful during construction before a circuit is being used.  It is not thread safe,
// but will modify all the circuit's internal structs to match what the config wants.  It also doe *NOT* use the
// default configuration parameters.
func (c *Circuit) SetConfigNotThreadSafe(config CommandProperties) {
	c.notThreadSafeConfigMu.Lock()
	c.notThreadSafeConfig = config
	c.notThreadSafeConfigMu.Unlock()
	c.timeNow = config.GoSpecific.TimeKeeper.Now

	c.openToClose = config.GoSpecific.OpenToClosedFactory()
	c.closedToOpen = config.GoSpecific.ClosedToOpenFactory()
	c.openToClose.SetConfigNotThreadSafe(config)
	c.closedToOpen.SetConfigNotThreadSafe(config)

	c.SetConfigThreadSafe(config)
	c.circuitStats.SetConfigNotThreadSafe(config)
}

func (c *Circuit) now() time.Time {
	return c.timeNow()
}

// Var exports that help diagnose the circuit
func (c *Circuit) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		return c.DebugValues()
	})
}

// DebugValues is a random-ish map of interesting values to debug your circuit
func (c *Circuit) DebugValues() interface{} {
	return collectCommandMetrics(c)
}

// Name of this circuit
func (c *Circuit) Name() string {
	return c.name
}

// ConcurrentCommands is the number of executions happening right now
func (c *Circuit) ConcurrentCommands() int64 {
	return c.concurrentCommands.Get()
}

// ConcurrentFallbacks is the number of fallbacks happening right now
func (c *Circuit) ConcurrentFallbacks() int64 {
	return c.concurrentFallbacks.Get()
}

// IsOpen returns true if the circuit should be considered 'open' (ie not allowing runFunc calls)
func (c *Circuit) IsOpen() bool {
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
func (c *Circuit) CloseCircuit() {
	c.close(c.now())
}

// OpenCircuit will open a closed circuit.  The circuit will then try to repair itself
func (c *Circuit) OpenCircuit() {
	c.openCircuit(time.Now())
}

// OpenCircuit opens a circuit, without checking error thresholds or request volume thresholds.  The circuit will, after
// some delay, try to close again.
func (c *Circuit) openCircuit(now time.Time) {
	if c.threadSafeConfig.CircuitBreaker.ForcedClosed.Get() {
		// Don't open circuits that are forced closed
		return
	}
	if c.IsOpen() {
		// Don't bother opening a circuit that is already open
		return
	}
	c.openToClose.Opened(now)
	c.isOpen.Set(true)
}

// Go executes `Execute`, but uses spawned goroutines to end early if the context is canceled.  Use this if you don't trust
// the runFunc to end correctly if context fails.  This is a design mirroed in the go-hystrix library, but be warned it
// is very dangerous and could leave orphaned goroutines hanging around forever doing who knows what.
func (c *Circuit) Go(ctx context.Context, runFunc func(context.Context) error, fallbackFunc func(context.Context, error) error) error {
	return c.Execute(ctx, c.goroutineWrapper.run(runFunc), c.goroutineWrapper.fallback(fallbackFunc))
}

// Run will execute the circuit without a fallback.  It is the equivalent of calling Execute with a nil fallback function
func (c *Circuit) Run(ctx context.Context, runFunc func(context.Context) error) error {
	return c.Execute(ctx, runFunc, nil)
}

// Execute the hystrix circuit.  Prefer this over Go.  Similar to http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/HystrixCommand.html#execute--
func (c *Circuit) Execute(ctx context.Context, runFunc func(context.Context) error, fallbackFunc func(context.Context, error) error) error {
	if c.threadSafeConfig.CircuitBreaker.Disabled.Get() {
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

// ErrorPercentage returns [0.0 - 1.0] what % of request are considered failing in the rolling window.
func (c *Circuit) ErrorPercentage() float64 {
	return c.circuitStats.errorPercentage(time.Now())
}

// --------- only private functions below here

func (c *Circuit) throttleConcurrentCommands(currentCommandCount int64) error {
	if c.threadSafeConfig.Execution.MaxConcurrentRequests.Get() >= 0 && currentCommandCount > c.threadSafeConfig.Execution.MaxConcurrentRequests.Get() {
		return errThrottledConcucrrentCommands
	}
	return nil
}

// run is the equivalent of Java Hystrix's http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/HystrixCommand.html#run()
func (c *Circuit) run(ctx context.Context, runFunc func(context.Context) error) (retErr error) {
	if runFunc == nil {
		return nil
	}
	var expectedDoneBy time.Time
	startTime := c.now()
	originalContext := ctx

	if !c.allowNewRun(startTime) {
		// Rather than make this inline, return a global reference (for memory optimization sake).
		c.circuitStats.cmdMetricCollector.ErrShortCircuit()
		return errCircuitOpen
	}

	if c.closedToOpen.Prevent(startTime) {
		return errCircuitOpen
	}

	currentCommandCount := c.concurrentCommands.Add(1)
	defer c.concurrentCommands.Add(-1)
	if err := c.throttleConcurrentCommands(currentCommandCount); err != nil {
		c.circuitStats.cmdMetricCollector.ErrConcurrencyLimitReject()
		return err
	}

	// Set timeout on the command if we have one
	if c.threadSafeConfig.Execution.ExecutionTimeout.Get() > 0 {
		var timeoutCancel func()
		expectedDoneBy = startTime.Add(c.threadSafeConfig.Execution.ExecutionTimeout.Duration())
		ctx, timeoutCancel = context.WithDeadline(ctx, expectedDoneBy)
		defer timeoutCancel()
	}

	ret := runFunc(ctx)
	endTime := c.now()
	totalCmdTime := endTime.Sub(startTime)
	runFuncDoneTime := c.now()
	// See bad request documentation at https://github.com/Netflix/Hystrix/wiki/How-To-Use#error-propagation
	// This request had invalid input, but shouldn't be marked as an 'error' for the circuit
	// From documentation
	// -------
	// The HystrixBadRequestException is intended for use cases such as reporting illegal arguments or non-system
	// failures that should not count against the failure metrics and should not trigger fallback logic.
	if c.checkErrBadRequest(ret, runFuncDoneTime, totalCmdTime) {
		return ret
	}

	// Even if there is no error (or if there is an error), if the request took too long it is always an error for the
	// socket.  Note that ret *MAY* actually be nil.  In that case, we still want to return nil.
	if c.checkErrTimeout(expectedDoneBy, runFuncDoneTime, totalCmdTime) {
		// Timeouts are legit requests.  We must check this before interrupt.  Even if the parent context
		// was interrupted, if we failed the timeout check, it is still a failure.
		c.circuitStats.legitimateAttemptsCount.Inc(runFuncDoneTime)
		// Note: ret could possibly be nil.  We will still return nil, but the circuit will consider it a failure.
		return ret
	}

	// The runFunc failed, but someone asked the original context to end.  This probably isn't a failure of the hystrix
	// circuit: someone just wanted `Execute` to end early, so don't track it as a failure.
	if c.checkErrInterrupt(originalContext, ret, runFuncDoneTime, totalCmdTime) {
		return ret
	}

	// Both failure and success are legitimate attempts
	c.circuitStats.legitimateAttemptsCount.Inc(runFuncDoneTime)

	if c.checkErrFailure(ret, runFuncDoneTime, totalCmdTime) {
		return ret
	}

	// The circuit works.  Close it!
	// Note: Execute this *after* you check for timeouts so we can still track circuit time outs that happen to also return a
	//       valid value later.
	c.checkSuccess(runFuncDoneTime, totalCmdTime)
	return nil
}

func (c *Circuit) checkSuccess(runFuncDoneTime time.Time, totalCmdTime time.Duration) {
	if c.IsOpen() {
		c.openToClose.SuccessfulAttempt(runFuncDoneTime, totalCmdTime)
	} else {
		c.closedToOpen.SuccessfulAttempt(runFuncDoneTime, totalCmdTime)
	}
	c.circuitStats.cmdMetricCollector.Success(totalCmdTime)
	c.close(runFuncDoneTime)
}

func (c *Circuit) checkErrInterrupt(originalContext context.Context, ret error, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	if !c.threadSafeConfig.GoSpecific.IgnoreInterrputs.Get() && ret != nil && originalContext.Err() != nil {
		if c.IsOpen() {
			c.openToClose.BackedOutAttempt(runFuncDoneTime)
		} else {
			c.closedToOpen.BackedOutAttempt(runFuncDoneTime)
		}
		c.circuitStats.backedOutAttemptsCount.Inc(runFuncDoneTime)
		c.circuitStats.cmdMetricCollector.ErrInterrupt(totalCmdTime)
		return true
	}
	return false
}

func (c *Circuit) checkErrBadRequest(ret error, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	if IsBadRequest(ret) {
		if c.IsOpen() {
			c.openToClose.BackedOutAttempt(runFuncDoneTime)
		} else {
			c.closedToOpen.BackedOutAttempt(runFuncDoneTime)
		}
		c.circuitStats.backedOutAttemptsCount.Inc(runFuncDoneTime)
		c.circuitStats.cmdMetricCollector.ErrBadRequest(totalCmdTime)
		return true
	}
	return false
}

func (c *Circuit) checkErrFailure(ret error, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	if ret != nil {
		if c.IsOpen() {
			c.openToClose.ErrorAttempt(runFuncDoneTime)
		} else {
			c.closedToOpen.ErrorAttempt(runFuncDoneTime)
		}
		c.circuitStats.cmdMetricCollector.ErrFailure(totalCmdTime)
		c.circuitStats.errorsCount.Inc(runFuncDoneTime)
		c.attemptToOpen(runFuncDoneTime)
		return true
	}
	return false
}

func (c *Circuit) checkErrTimeout(expectedDoneBy time.Time, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	if !expectedDoneBy.IsZero() && expectedDoneBy.Before(runFuncDoneTime) {
		if c.IsOpen() {
			c.openToClose.ErrorAttempt(runFuncDoneTime)
		} else {
			c.closedToOpen.ErrorAttempt(runFuncDoneTime)
		}
		c.circuitStats.errorsCount.Inc(runFuncDoneTime)
		c.circuitStats.cmdMetricCollector.ErrTimeout(totalCmdTime)
		c.attemptToOpen(runFuncDoneTime)
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
	if c.threadSafeConfig.Fallback.MaxConcurrentRequests.Get() >= 0 && currentFallbackCount > c.threadSafeConfig.Fallback.MaxConcurrentRequests.Get() {
		c.circuitStats.fallbackMetricCollector.ErrConcurrencyLimitReject()
		return &hystrixError{concurrencyLimitReached: true, msg: "throttling concurrency to fallbacks"}
	}

	startTime := c.now()
	retErr := fallbackFunc(ctx, err)
	totalCmdTime := c.now().Sub(startTime)
	if retErr != nil {
		c.circuitStats.fallbackMetricCollector.ErrFailure(totalCmdTime)
		return retErr
	}
	c.circuitStats.fallbackMetricCollector.Success(totalCmdTime)
	return nil
}

// allowNewRun checks if the circuit is allowing new run commands. This happens if the circuit is closed, or
// if it is open, but we want to explore to see if we should close it again.
func (c *Circuit) allowNewRun(now time.Time) bool {
	if !c.IsOpen() {
		return true
	}
	if c.openToClose.Allow(now) {
		return true
	}
	return false
}

// Close closes an open circuit.  Usually because we think it's healthy again.
// For linguistic sake, it is called "Close" and not "CloseCircuit".  It is *NOT* the io.Closer Close() function
// you are used to.
func (c *Circuit) close(now time.Time) {
	if !c.IsOpen() {
		// Not open.  Don't need to close it
		return
	}
	if c.threadSafeConfig.CircuitBreaker.ForceOpen.Get() {
		return
	}
	if c.openToClose.AttemptToClose(now) {
		c.isOpen.Set(false)
	}
}

// attemptToOpen tries to open an unhealthy circuit.  Usually because we think run is having problems, and we want
// to give run a rest for a bit.
//
// It is called "attemptToOpen" because the circuit may not actually open (for example if there aren't enough requests)
func (c *Circuit) attemptToOpen(now time.Time) {
	if c.threadSafeConfig.CircuitBreaker.ForcedClosed.Get() {
		// Don't open circuits that are forced closed
		return
	}
	if c.IsOpen() {
		// Don't bother opening a circuit that is already open
		// This check isn't needed (it is also checked inside OpenCircuit below), but is an optimization to avoid
		// the below logic when the circuit is in a bad state and would otherwise try to close itself repeatedly.
		return
	}

	if c.closedToOpen.AttemptToOpen(now) {
		c.openCircuit(now)
	}
}
