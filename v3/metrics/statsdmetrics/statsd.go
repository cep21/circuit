package statsdmetrics

import (
	"strings"
	"time"

	"sync"

	"github.com/cep21/circuit/v3"
	"github.com/cep21/circuit/v3/faststats"
	"github.com/cep21/circuit/v3/metrics/responsetimeslo"
)

// Our interface should be satisfied by go-statsd-client
// We still assert this in our tests, but don't require the dependency for the library to allow greater flexibility.
//var _ statsdmetrics.StatSender = statsd.StatSender(nil)

// The StatSender interface wraps all the statsd metric methods we care about
type StatSender interface {
	// Inc increments a statsd metric
	Inc(stat string, val int64, sampleRate float32) error
	// Gauge sets a statsd metric
	Gauge(stat string, val int64, sampleRate float32) error
	// TimingDuration adds a statsd timing
	TimingDuration(stat string, val time.Duration, sampleRate float32) error
}

// CommandFactory allows ingesting statsd metrics
type CommandFactory struct {
	StatSender StatSender
	SampleRate float32
	// This function will sanitize the circuit name.  If you leave it empty, we will use a default implementation.
	// You are free to make this however you want to sanitize your circuit names for statsd.
	SanitizeStatsdFunction func(name string) string
}

func (c *CommandFactory) sampleRate() float32 {
	if c.SampleRate == 0 {
		return 1
	}
	return c.SampleRate
}

func (c *CommandFactory) sanitizeStatsdFunction() func(name string) string {
	if c.SanitizeStatsdFunction == nil {
		return sanitizeStatsd
	}
	return c.SanitizeStatsdFunction
}

// sanitizeStatsd borrowed from github.com/twitchtv/twirp
func sanitizeStatsd(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.Map(sanitizeRune, s)
}

func sanitizeRune(r rune) rune {
	switch {
	case 'a' <= r && r <= 'z':
		return r
	case '0' <= r && r <= '9':
		return r
	case 'A' <= r && r <= 'Z':
		return r
	default:
		return '_'
	}
}

func appendStatsdParts(sanitizeStatsdFunction func(name string) string, parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		part = sanitizeStatsdFunction(part)
		part = strings.Trim(part, ".")
		if len(part) == 0 {
			continue
		}
		nonEmpty = append(nonEmpty, part)
	}
	return strings.Join(nonEmpty, ".")
}

// ConcurrencyCollector runs as a background routine to collect concurrency stats
type ConcurrencyCollector struct {
	errorChecker
	StatSender             StatSender
	SanitizeStatsdFunction func(name string) string
	Delay                  faststats.AtomicInt64
	onClose                chan struct{}
	Manager                *circuit.Manager
	SampleRate             float32
	timeAfter              func(time.Duration) <-chan time.Time
	once                   sync.Once
}

func (c *ConcurrencyCollector) delay() time.Duration {
	ret := c.Delay.Duration()
	if ret == 0 {
		return time.Second * 10
	}
	return ret
}

func (c *ConcurrencyCollector) after(dur time.Duration) <-chan time.Time {
	if c.timeAfter == nil {
		return time.After(dur)
	}
	return c.timeAfter(dur)
}

func (c *ConcurrencyCollector) init() {
	c.once.Do(func() {
		c.onClose = make(chan struct{})
	})
}

// Collect reports concurrency information for each circuit
func (c *ConcurrencyCollector) Collect() {
	for _, circ := range c.Manager.AllCircuits() {
		name := circ.Name()
		concurrent := circ.ConcurrentCommands()
		maxConcurrent := circ.Config().Execution.MaxConcurrentRequests
		p := prefixedStatSender{
			prefix: appendStatsdParts(c.SanitizeStatsdFunction, name, "circuit"),
			sendTo: c.StatSender,
		}
		isOpen := int64(0)
		if circ.IsOpen() {
			isOpen = 1
		}
		c.check(p.Gauge("is_open", isOpen, c.SampleRate))
		c.check(p.Gauge("concurrent", concurrent, c.SampleRate))
		c.check(p.Gauge("max_concurrent", maxConcurrent, c.SampleRate))
	}
}

// Start blocks forever and runs circuit collection on a delay of variable `Delay`
func (c *ConcurrencyCollector) Start() {
	c.init()
	for {
		select {
		case <-c.onClose:
			return
		case <-c.after(c.delay()):
			c.Collect()
		}
	}
}

// Close stops the `Start` routine
func (c *ConcurrencyCollector) Close() error {
	c.init()
	close(c.onClose)
	return nil
}

// ConcurrencyCollector returns a ConcurrencyCollector, which can be used to collect stats on each circuit tracked
// by the manager.  You should call `Start` on the returned object and `Close` when you are done collecting metrics.
func (c *CommandFactory) ConcurrencyCollector(manager *circuit.Manager) *ConcurrencyCollector {
	return &ConcurrencyCollector{
		Manager:                manager,
		SampleRate:             c.sampleRate(),
		StatSender:             c.StatSender,
		SanitizeStatsdFunction: c.sanitizeStatsdFunction(),
	}
}

// SLOCollector tracks SLO stats for statsd
func (c *CommandFactory) SLOCollector(circuitName string) responsetimeslo.Collector {
	return &SLOCollector{
		prefixedStatSender: prefixedStatSender{
			prefix: appendStatsdParts(c.sanitizeStatsdFunction(), circuitName, "slo"),
			sendTo: c.StatSender,
		},
		SampleRate: c.sampleRate(),
	}
}

// CommandProperties creates statsd metrics for a circuit
func (c *CommandFactory) CommandProperties(circuitName string) circuit.Config {
	return circuit.Config{
		Metrics: circuit.MetricsCollectors{
			Run: []circuit.RunMetrics{
				&RunMetricsCollector{
					prefixedStatSender: prefixedStatSender{
						prefix: appendStatsdParts(c.sanitizeStatsdFunction(), circuitName, "run"),
						sendTo: c.StatSender,
					},
					SampleRate: c.sampleRate(),
				},
			},
			Fallback: []circuit.FallbackMetrics{
				&FallbackMetricsCollector{
					prefixedStatSender: prefixedStatSender{
						prefix: appendStatsdParts(c.sanitizeStatsdFunction(), circuitName, "fallback"),
						sendTo: c.StatSender,
					},
					SampleRate: c.sampleRate(),
				},
			},
			Circuit: []circuit.Metrics{
				&CircuitMetricsCollector{
					prefixedStatSender: prefixedStatSender{
						prefix: appendStatsdParts(c.sanitizeStatsdFunction(), circuitName, "circuit"),
						sendTo: c.StatSender,
					},
					SampleRate: c.sampleRate(),
				},
			},
		},
	}
}

type errorChecker struct {
	checkFunction func(err error)
}

func (e *errorChecker) check(err error) {
	if err != nil && e.checkFunction != nil {
		e.checkFunction(err)
	}
}

// By writing this simple part ourselves, we can remove the need to import go-statsd-client entirely
type prefixedStatSender struct {
	sendTo StatSender
	prefix string
}

var _ StatSender = &prefixedStatSender{}

func (p *prefixedStatSender) Inc(stat string, val int64, sampleRate float32) error {
	return p.sendTo.Inc(p.prefix+"."+stat, val, sampleRate)
}

func (p *prefixedStatSender) Gauge(stat string, val int64, sampleRate float32) error {
	return p.sendTo.Gauge(p.prefix+"."+stat, val, sampleRate)
}

func (p *prefixedStatSender) TimingDuration(stat string, val time.Duration, sampleRate float32) error {
	return p.sendTo.TimingDuration(p.prefix+"."+stat, val, sampleRate)
}

// CircuitMetricsCollector collects opened/closed metrics
type CircuitMetricsCollector struct {
	errorChecker
	prefixedStatSender
	SampleRate float32
}

// Closed sets a gauge as closed for the collector
func (c *CircuitMetricsCollector) Closed(now time.Time) {
	c.check(c.Gauge("is_open", 0, c.SampleRate))
}

// Opened sets a gauge as opened for the collector
func (c *CircuitMetricsCollector) Opened(now time.Time) {
	c.check(c.Gauge("is_open", 1, c.SampleRate))
}

var _ circuit.Metrics = &CircuitMetricsCollector{}

// SLOCollector collects SLO level metrics
type SLOCollector struct {
	errorChecker
	prefixedStatSender
	SampleRate float32
}

// Failed increments a failed metric
func (s *SLOCollector) Failed() {
	s.check(s.Inc("failed", 1, s.SampleRate))
}

// Passed increments a passed metric
func (s *SLOCollector) Passed() {
	s.check(s.Inc("passed", 1, s.SampleRate))
}

var _ responsetimeslo.Collector = &SLOCollector{}

// RunMetricsCollector collects command metrics
type RunMetricsCollector struct {
	errorChecker
	prefixedStatSender
	SampleRate float32
}

// Success sends a success to statsd
func (c *RunMetricsCollector) Success(now time.Time, duration time.Duration) {
	c.check(c.Inc("success", 1, c.SampleRate))
	c.check(c.TimingDuration("calls", duration, c.SampleRate))
}

// ErrFailure sends a failure to statsd
func (c *RunMetricsCollector) ErrFailure(now time.Time, duration time.Duration) {
	c.check(c.Inc("err_failure", 1, c.SampleRate))
	c.check(c.TimingDuration("calls", duration, c.SampleRate))
}

// ErrTimeout sends a timeout to statsd
func (c *RunMetricsCollector) ErrTimeout(now time.Time, duration time.Duration) {
	c.check(c.Inc("err_timeout", 1, c.SampleRate))
	c.check(c.TimingDuration("calls", duration, c.SampleRate))
}

// ErrBadRequest sends a bad request error to statsd
func (c *RunMetricsCollector) ErrBadRequest(now time.Time, duration time.Duration) {
	c.check(c.Inc("err_bad_request", 1, c.SampleRate))
	c.check(c.TimingDuration("calls", duration, c.SampleRate))
}

// ErrInterrupt sends an interrupt error to statsd
func (c *RunMetricsCollector) ErrInterrupt(now time.Time, duration time.Duration) {
	c.check(c.Inc("err_interrupt", 1, c.SampleRate))
	c.check(c.TimingDuration("calls", duration, c.SampleRate))
}

// ErrShortCircuit sends a short circuit to statsd
func (c *RunMetricsCollector) ErrShortCircuit(now time.Time) {
	c.check(c.Inc("err_short_circuit", 1, c.SampleRate))
}

// ErrConcurrencyLimitReject sends a concurrency limit error to statsd
func (c *RunMetricsCollector) ErrConcurrencyLimitReject(now time.Time) {
	c.check(c.Inc("err_concurrency_limit_reject", 1, c.SampleRate))
}

var _ circuit.RunMetrics = &RunMetricsCollector{}

// FallbackMetricsCollector collects fallback metrics
type FallbackMetricsCollector struct {
	errorChecker
	prefixedStatSender
	SampleRate float32
}

// Success sends a success to statsd
func (c *FallbackMetricsCollector) Success(now time.Time, duration time.Duration) {
	c.check(c.Inc("success", 1, c.SampleRate))
}

// ErrConcurrencyLimitReject sends a concurrency-limit to statsd
func (c *FallbackMetricsCollector) ErrConcurrencyLimitReject(now time.Time) {
	c.check(c.Inc("err_concurrency_limit_reject", 1, c.SampleRate))
}

// ErrFailure sends a failure to statsd
func (c *FallbackMetricsCollector) ErrFailure(now time.Time, duration time.Duration) {
	c.check(c.Inc("err_failure", 1, c.SampleRate))
}

var _ circuit.FallbackMetrics = &FallbackMetricsCollector{}
