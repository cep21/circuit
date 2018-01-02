package statsdmetrics

import (
	"strings"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
	"github.com/cep21/hystrix"
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
func (c *CommandFactory) CommandProperties(circuitName string) hystrix.CommandProperties {
	return hystrix.CommandProperties{
		MetricsCollectors: hystrix.MetricsCollectors{
			Run: []hystrix.RunMetrics{
				&CmdMetricCollector{
					SendTo:     c.SubStatter.NewSubStatter(appendStatsdParts(circuitName, "cmd")),
					SampleRate: c.sampleRate(),
				},
			},
			Fallback: []hystrix.FallbackMetric{
				&FallbackMetricCollector{
					SendTo:     c.SubStatter.NewSubStatter(appendStatsdParts(circuitName, "fallback")),
					SampleRate: c.sampleRate(),
				},
			},
		},
	}
}

// CmdMetricCollector collects command metrics
type CmdMetricCollector struct {
	SendTo       statsd.StatSender
	SampleRate   float32
	StatsdErrors func(err error)
}

func (c *CmdMetricCollector) check(err error) {
	if err != nil && c.StatsdErrors != nil {
		c.StatsdErrors(err)
	}
}

// Success sends a success to statsd
func (c *CmdMetricCollector) Success(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("success", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrFailure sends a failure to statsd
func (c *CmdMetricCollector) ErrFailure(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_failure", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrTimeout sends a timeout to statsd
func (c *CmdMetricCollector) ErrTimeout(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_timeout", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrBadRequest sends a bad request error to statsd
func (c *CmdMetricCollector) ErrBadRequest(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_bad_request", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrInterrupt sends an interrupt error to statsd
func (c *CmdMetricCollector) ErrInterrupt(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_interrupt", 1, c.SampleRate))
	c.check(c.SendTo.TimingDuration("calls", duration, c.SampleRate))
}

// ErrShortCircuit sends a short circuit to statsd
func (c *CmdMetricCollector) ErrShortCircuit(now time.Time) {
	c.check(c.SendTo.Inc("err_short_circuit", 1, c.SampleRate))
}

// ErrConcurrencyLimitReject sends a concurrency limit error to statsd
func (c *CmdMetricCollector) ErrConcurrencyLimitReject(now time.Time) {
	c.check(c.SendTo.Inc("err_concurrency_limit_reject", 1, c.SampleRate))
}

var _ hystrix.RunMetrics = &CmdMetricCollector{}

// FallbackMetricCollector collects fallback metrics
type FallbackMetricCollector struct {
	SendTo       statsd.StatSender
	SampleRate   float32
	StatsdErrors func(err error)
}

func (c *FallbackMetricCollector) check(err error) {
	if err != nil && c.StatsdErrors != nil {
		c.StatsdErrors(err)
	}
}

// Success sends a success to statsd
func (c *FallbackMetricCollector) Success(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("success", 1, c.SampleRate))
}

// ErrConcurrencyLimitReject sends a concurrency-limit to statsd
func (c *FallbackMetricCollector) ErrConcurrencyLimitReject(now time.Time) {
	c.check(c.SendTo.Inc("err_concurrency_limit_reject", 1, c.SampleRate))
}

// ErrFailure sends a failure to statsd
func (c *FallbackMetricCollector) ErrFailure(now time.Time, duration time.Duration) {
	c.check(c.SendTo.Inc("err_failure", 1, c.SampleRate))
}

var _ hystrix.FallbackMetric = &FallbackMetricCollector{}
