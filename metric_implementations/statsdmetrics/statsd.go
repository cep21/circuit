package statsdmetrics

import (
	"strings"
	"time"

	"github.com/cactus/go-statsd-client/statsd"
	"github.com/cep21/hystrix"
)

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
			ResponseTimeSLO: []hystrix.ResponseTimeSLOCollector{
				&SLOMetricCollector{
					SendTo: c.SubStatter.NewSubStatter(appendStatsdParts(circuitName, "slo")),
				},
			},
		},
	}
}

type SLOMetricCollector struct {
	SendTo     statsd.StatSender
	SampleRate float32
}

func (s *SLOMetricCollector) Failed() {
	s.SendTo.Inc("fail", 1, s.SampleRate)
}
func (s *SLOMetricCollector) Passed() {
	s.SendTo.Inc("pass", 1, s.SampleRate)
}

var _ hystrix.ResponseTimeSLOCollector = &SLOMetricCollector{}

type CmdMetricCollector struct {
	SendTo     statsd.StatSender
	SampleRate float32
}

func (c *CmdMetricCollector) Success(duration time.Duration) {
	c.SendTo.Inc("success", 1, c.SampleRate)
	c.SendTo.TimingDuration("calls", duration, c.SampleRate)
}

func (c *CmdMetricCollector) ErrFailure(duration time.Duration) {
	c.SendTo.Inc("err_failure", 1, c.SampleRate)
	c.SendTo.TimingDuration("calls", duration, c.SampleRate)
}

func (c *CmdMetricCollector) ErrTimeout(duration time.Duration) {
	c.SendTo.Inc("err_timeout", 1, c.SampleRate)
	c.SendTo.TimingDuration("calls", duration, c.SampleRate)
}

func (c *CmdMetricCollector) ErrBadRequest(duration time.Duration) {
	c.SendTo.Inc("err_bad_request", 1, c.SampleRate)
	c.SendTo.TimingDuration("calls", duration, c.SampleRate)
}

func (c *CmdMetricCollector) ErrInterrupt(duration time.Duration) {
	c.SendTo.Inc("err_interrupt", 1, c.SampleRate)
	c.SendTo.TimingDuration("calls", duration, c.SampleRate)
}

func (c *CmdMetricCollector) ErrShortCircuit() {
	c.SendTo.Inc("err_short_circuit", 1, c.SampleRate)
}
func (c *CmdMetricCollector) ErrConcurrencyLimitReject() {
	c.SendTo.Inc("err_concurrency_limit_reject", 1, c.SampleRate)
}

var _ hystrix.RunMetrics = &CmdMetricCollector{}

type FallbackMetricCollector struct {
	SendTo     statsd.StatSender
	SampleRate float32
}

func (c *FallbackMetricCollector) Success(duration time.Duration) {
	c.SendTo.Inc("success", 1, c.SampleRate)
}

func (c *FallbackMetricCollector) ErrConcurrencyLimitReject() {
	c.SendTo.Inc("err_concurrency_limit_reject", 1, c.SampleRate)
}
func (c *FallbackMetricCollector) ErrFailure(duration time.Duration) {
	c.SendTo.Inc("err_failure", 1, c.SampleRate)
}

var _ hystrix.FallbackMetric = &FallbackMetricCollector{}
