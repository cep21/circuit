package responsetimeslo

import (
	"time"

	"sync"

	"expvar"

	"github.com/cep21/circuit"
	"github.com/cep21/circuit/faststats"
)

// Tracker sets up a response time SLO that has a reasonable meaning for hystrix.  Use it for an SLO like
// "99% of requests should respond correctly within 300 ms".
//
// Define a maximum time that a healthy request is allowed to take.  This should be less than the maximum "break" point
// of the circuit.  Only Successful requests <= that time are counted as healthy.
//
// Requests that are interrupted, or have bad input, are not considered healthy or unhealthy.  It's like they don't
// happen.  All other types of errors are blamed on the down stream service, or the Run method's request time.  They
// will count as failing the SLA.
type Tracker struct {
	MaximumHealthyTime faststats.AtomicInt64
	MeetsSLOCount      faststats.AtomicInt64
	FailsSLOCount      faststats.AtomicInt64
	Collectors         []Collector

	mu     sync.Mutex
	config Config
}

// Config controls how SLO is tracked by default for a Tracker
type Config struct {
	// MaximumHealthyTime is the maximum amount of time a request can take and still be considered healthy
	MaximumHealthyTime time.Duration
}

var defaultConfig = Config{
	MaximumHealthyTime: time.Millisecond * 250,
}

// Merge this configuration with another, changing any values that are non zero into other's value
func (c *Config) Merge(other Config) {
	if c.MaximumHealthyTime == 0 {
		c.MaximumHealthyTime = other.MaximumHealthyTime
	}
}

// Factory creates SLO monitors for a circuit
type Factory struct {
	Config                Config
	ConfigConstructor     []func(circuitName string) Config
	CollectorConstructors []func(circuitName string) Collector
}

var _ circuit.RunMetrics = &Tracker{}

func (r *Factory) getConfig(circuitName string) Config {
	finalConfig := Config{}
	// Merge in reverse order so the most recently appending constructor is more important
	for i := len(r.ConfigConstructor) - 1; i >= 0; i-- {
		finalConfig.Merge(r.ConfigConstructor[i](circuitName))
	}
	finalConfig.Merge(r.Config)
	finalConfig.Merge(defaultConfig)
	return finalConfig
}

// CommandProperties appends SLO tracking to a circuit
func (r *Factory) CommandProperties(circuitName string) circuit.Config {
	collectors := make([]Collector, 0, len(r.CollectorConstructors))
	for _, constructor := range r.CollectorConstructors {
		collectors = append(collectors, constructor(circuitName))
	}
	tracker := &Tracker{
		Collectors: collectors,
	}

	cfg := r.getConfig(circuitName)
	tracker.SetConfigThreadSafe(cfg)
	return circuit.Config{
		Metrics: circuit.MetricsCollectors{
			Run: []circuit.RunMetrics{tracker},
		},
	}
}

// Var returns something to pass to expvar
func (r *Tracker) Var() expvar.Var {
	return expvar.Func(func() interface{} {
		return map[string]interface{}{
			"config": r.Config(),
			"pass":   r.MeetsSLOCount.Get(),
			"fail":   r.FailsSLOCount.Get(),
		}
	})
}

// Success adds a healthy check if duration <= maximum healthy time
func (r *Tracker) Success(now time.Time, duration time.Duration) {
	if duration.Nanoseconds() <= r.MaximumHealthyTime.Get() {
		r.healthy()
		return
	}
	r.failure()
}

func (r *Tracker) failure() {
	r.FailsSLOCount.Add(1)
	for _, c := range r.Collectors {
		c.Failed()
	}
}

func (r *Tracker) healthy() {
	r.MeetsSLOCount.Add(1)
	for _, c := range r.Collectors {
		c.Passed()
	}
}

// ErrFailure is always a failure
func (r *Tracker) ErrFailure(now time.Time, duration time.Duration) {
	r.failure()
}

// ErrTimeout is always a failure
func (r *Tracker) ErrTimeout(now time.Time, duration time.Duration) {
	r.failure()
}

// ErrConcurrencyLimitReject is always a failure
func (r *Tracker) ErrConcurrencyLimitReject(now time.Time) {
	// Your endpoint could be healthy, but because we can't process commands fast enough, you're considered unhealthy.
	// This one could honestly go either way, but generally if a service cannot process commands fast enough, it's not
	// doing what you want.
	r.failure()
}

// ErrShortCircuit is always a failure
func (r *Tracker) ErrShortCircuit(now time.Time) {
	// We had to end the request early.  It's possible the endpoint we want is healthy, but because we had to trip
	// our circuit, due to past misbehavior, it is still end endpoint's fault we cannot satisfy this request, so it
	// fails the SLO.
	r.failure()
}

// ErrBadRequest is ignored
func (r *Tracker) ErrBadRequest(now time.Time, duration time.Duration) {}

// SetConfigThreadSafe updates the configuration stored in the tracker
func (r *Tracker) SetConfigThreadSafe(config Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = config
	r.MaximumHealthyTime.Set(config.MaximumHealthyTime.Nanoseconds())
}

// Config returns the tracker's config
func (r *Tracker) Config() Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config
}

// ErrInterrupt is only a failure if healthy time has passed
func (r *Tracker) ErrInterrupt(now time.Time, duration time.Duration) {
	// If it is interrupted, but past the healthy time.  Then it is as good as unhealthy
	if duration.Nanoseconds() > r.MaximumHealthyTime.Get() {
		r.failure()
	}
	// Cannot consider this value healthy, since it didn't return
}

// Collector can collect metrics about the happy SLO of a request.
type Collector interface {
	// Failed the SLO
	Failed()
	// Passed the SLO (responded correctly fast enough)
	Passed()
}
