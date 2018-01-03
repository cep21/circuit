package statsdmetrics

import (
	"strings"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
	"github.com/cep21/circuit"
)

// CommandFactory allows ingesting statsd metrics
type CommandFactory struct {
	SubStatter statsd.SubStatter
	SampleRate float32
}

func (c *CommandFactory) sampleRate() float32 {
	if c.SampleRate == 0 {
		return 1
	}
	return c.SampleRate
}

// We can do better than this.  Some kind of whitelist thing
func sanitizeStatsd(name string) string {
	if len(name) > 32 {
		name = name[0:32]
	}
	name = strings.Replace(name, "/", "-", -1)
	name = strings.Replace(name, ":", "-", -1)
	name = strings.Replace(name, ".", "-", -1)
	return strings.Replace(name, ".", "_", -1)
}

func appendStatsdParts(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		part = sanitizeStatsd(part)
		part = strings.Trim(part, ".")
		if len(part) <= 0 {
			continue
		}
		nonEmpty = append(nonEmpty, part)
	}
	return strings.Join(nonEmpty, ".")
}

// CommandProperties creates statsd metrics for a circuit
func (c *CommandFactory) CommandProperties(circuitName string) circuit.Config {
	return circuit.Config{
		Metrics: circuit.MetricsCollectors{
			Run: []circuit.RunMetrics{
				&RunMetricsCollector{
					SendTo:     c.SubStatter.NewSubStatter(appendStatsdParts(circuitName, "cmd")),
					SampleRate: c.sampleRate(),
				},
			},
			Fallback: []circuit.FallbackMetrics{
				&FallbackMetricsCollector{
					SendTo:     c.SubStatter.NewSubStatter(appendStatsdParts(circuitName, "fallback")),
					SampleRate: c.sampleRate(),
				},
			},
		},
	}
}

// RunMetricsCollector collects command metrics
type RunMetricsCollector struct {
	SendTo       statsd.StatSender
	SampleRate   float32
	StatsdErrors func(err error)
}

func (c *RunMetricsCollector) check(err error) {
	if err != nil && c.StatsdErrors != nil {
		c.StatsdErrors(err)
	}
}

// Success sends a success to statsd
func (c *RunMetricsCollector) Success(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("success", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrFailure sends a failure to statsd
func (c *RunMetricsCollector) ErrFailure(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_failure", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrTimeout sends a timeout to statsd
func (c *RunMetricsCollector) ErrTimeout(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_timeout", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrBadRequest sends a bad request error to statsd
func (c *RunMetricsCollector) ErrBadRequest(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_bad_request", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrInterrupt sends an interrupt error to statsd
func (c *RunMetricsCollector) ErrInterrupt(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_interrupt", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrShortCircuit sends a short circuit to statsd
func (c *RunMetricsCollector) ErrShortCircuit(now time.Time) {
	c.check(c.SendTo.Inc("err_short_circuit", 1, c.SampleRate))
}

// ErrConcurrencyLimitReject sends a concurrency limit error to statsd
func (c *RunMetricsCollector) ErrConcurrencyLimitReject(now time.Time) {
	c.check(c.SendTo.Inc("err_concurrency_limit_reject", 1, c.SampleRate))
}

var _ circuit.RunMetrics = &RunMetricsCollector{}

// FallbackMetricsCollector collects fallback metrics
type FallbackMetricsCollector struct {
	SendTo       statsd.StatSender
	SampleRate   float32
	StatsdErrors func(err error)
}

func (c *FallbackMetricsCollector) check(err error) {
	if err != nil && c.StatsdErrors != nil {
		c.StatsdErrors(err)
	}
}

// Success sends a success to statsd
func (c *FallbackMetricsCollector) Success(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("success", 1, c.SampleRate))
}

// ErrConcurrencyLimitReject sends a concurrency-limit to statsd
func (c *FallbackMetricsCollector) ErrConcurrencyLimitReject(now time.Time) {
	c.check(c.SendTo.Inc("err_concurrency_limit_reject", 1, c.SampleRate))
}

// ErrFailure sends a failure to statsd
func (c *FallbackMetricsCollector) ErrFailure(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_failure", 1, c.SampleRate))
}

var _ circuit.FallbackMetrics = &FallbackMetricsCollector{}
