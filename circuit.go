package circuit

import (
	"context"
	"expvar"
	"sync"
	"time"

	"github.com/cep21/circuit/faststats"
)

// Circuit is a circuit breaker pattern implementation that can accept commands and open/close on failures
type Circuit struct {
	//circuitStats
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

	// Tracks how many commands are currently running
	concurrentCommands faststats.AtomicInt64
	// Tracks how many fallbacks are currently running
	concurrentFallbacks faststats.AtomicInt64

	// ClosedToOpen controls when to open a closed circuit
	ClosedToOpen ClosedToOpen
	// openToClosed controls when to close an open circuit
	OpenToClose OpenToClosed

	timeNow func() time.Time
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
	//c.circuitStats.SetConfigThreadSafe(config)
	c.threadSafeConfig.reset(c.notThreadSafeConfig)
	c.notThreadSafeConfig = config
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
}

func (c *Circuit) now() time.Time {
	return c.timeNow()
}

// Var exports that help diagnose the circuit
func (c *Circuit) Var() expvar.Var {
	return expvar.Func(func() interface{} {
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
	return c.name
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
	c.close(c.now(), true)
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
	c.CircuitMetricsCollector.Opened(now)
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

// Execute the circuit.  Prefer this over Go.  Similar to http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/HystrixCommand.html#execute--
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

// --------- only private functions below here

func (c *Circuit) throttleConcurrentCommands(currentCommandCount int64) error {
	if c.threadSafeConfig.Execution.MaxConcurrentRequests.Get() >= 0 && currentCommandCount > c.threadSafeConfig.Execution.MaxConcurrentRequests.Get() {
		return errThrottledConcucrrentCommands
	}
	return nil
}

// run is the equivalent of Java Manager's http://netflix.github.io/Hystrix/javadoc/com/netflix/hystrix/HystrixCommand.html#run()
func (c *Circuit) run(ctx context.Context, runFunc func(context.Context) error) (retErr error) {
	if runFunc == nil {
		return nil
	}
	var expectedDoneBy time.Time
	startTime := c.now()
	originalContext := ctx

	if !c.allowNewRun(startTime) {
		// Rather than make this inline, return a global reference (for memory optimization sake).
		c.CmdMetricCollector.ErrShortCircuit(startTime)
		return errCircuitOpen
	}

	if c.ClosedToOpen.Prevent(startTime) {
		return errCircuitOpen
	}

	currentCommandCount := c.concurrentCommands.Add(1)
	defer c.concurrentCommands.Add(-1)
	if err := c.throttleConcurrentCommands(currentCommandCount); err != nil {
		c.CmdMetricCollector.ErrConcurrencyLimitReject(startTime)
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
		// Note: ret could possibly be nil.  We will still return nil, but the circuit will consider it a failure.
		return ret
	}

	// The runFunc failed, but someone asked the original context to end.  This probably isn't a failure of the
	// circuit: someone just wanted `Execute` to end early, so don't track it as a failure.
	if c.checkErrInterrupt(originalContext, ret, runFuncDoneTime, totalCmdTime) {
		return ret
	}

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
	c.CmdMetricCollector.Success(runFuncDoneTime, totalCmdTime)
	if c.IsOpen() {
		c.close(runFuncDoneTime, false)
	}
}

func (c *Circuit) checkErrInterrupt(originalContext context.Context, ret error, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	if !c.threadSafeConfig.GoSpecific.IgnoreInterrputs.Get() && ret != nil && originalContext.Err() != nil {
		c.CmdMetricCollector.ErrInterrupt(runFuncDoneTime, totalCmdTime)
		return true
	}
	return false
}

func (c *Circuit) checkErrBadRequest(ret error, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	if IsBadRequest(ret) {
		c.CmdMetricCollector.ErrBadRequest(runFuncDoneTime, totalCmdTime)
		return true
	}
	return false
}

func (c *Circuit) checkErrFailure(ret error, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	if ret != nil {
		c.CmdMetricCollector.ErrFailure(runFuncDoneTime, totalCmdTime)
		if !c.IsOpen() {
			c.attemptToOpen(runFuncDoneTime)
		}
		return true
	}
	return false
}

func (c *Circuit) checkErrTimeout(expectedDoneBy time.Time, runFuncDoneTime time.Time, totalCmdTime time.Duration) bool {
	// I don't use the deadline from the context because it could be a smaller timeout from the parent context
	if !expectedDoneBy.IsZero() && expectedDoneBy.Before(runFuncDoneTime) {
		c.CmdMetricCollector.ErrTimeout(runFuncDoneTime, totalCmdTime)
		if !c.IsOpen() {
			c.attemptToOpen(runFuncDoneTime)
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
	if c.threadSafeConfig.Fallback.MaxConcurrentRequests.Get() >= 0 && currentFallbackCount > c.threadSafeConfig.Fallback.MaxConcurrentRequests.Get() {
		c.FallbackMetricCollector.ErrConcurrencyLimitReject(c.now())
		return &circuitError{concurrencyLimitReached: true, msg: "throttling concurrency to fallbacks"}
	}

	startTime := c.now()
	retErr := fallbackFunc(ctx, err)
	totalCmdTime := c.now().Sub(startTime)
	if retErr != nil {
		c.FallbackMetricCollector.ErrFailure(startTime, totalCmdTime)
		return retErr
	}
	c.FallbackMetricCollector.Success(startTime, totalCmdTime)
	return nil
}

// allowNewRun checks if the circuit is allowing new run commands. This happens if the circuit is closed, or
// if it is open, but we want to explore to see if we should close it again.
func (c *Circuit) allowNewRun(now time.Time) bool {
	if !c.IsOpen() {
		return true
	}
	if c.OpenToClose.Allow(now) {
		return true
	}
	return false
}

// close closes an open circuit.  Usually because we think it's healthy again.
func (c *Circuit) close(now time.Time, forceClosed bool) {
	if !c.IsOpen() {
		// Not open.  Don't need to close it
		return
	}
	if c.threadSafeConfig.CircuitBreaker.ForceOpen.Get() {
		return
	}
	if forceClosed || c.OpenToClose.ShouldClose(now) {
		c.CircuitMetricsCollector.Closed(now)
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

	if c.ClosedToOpen.ShouldOpen(now) {
		c.openCircuit(now)
	}
}
