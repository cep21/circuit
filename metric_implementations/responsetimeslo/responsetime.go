package responsetimeslo

import (
	"time"

	"github.com/cep21/hystrix"
	"github.com/cep21/hystrix/internal/fastmath"
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
	MaximumHealthyTime fastmath.AtomicInt64
	MeetsSLOCount      fastmath.AtomicInt64
	FailsSLOCount      fastmath.AtomicInt64

	Collectors []Collector
}

var _ hystrix.RunMetrics = &Tracker{}

// Success adds a healthy check if duration <= maximum healthy time
func (r *Tracker) Success(duration time.Duration) {
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
func (r *Tracker) ErrFailure(duration time.Duration) {
	r.failure()
}

// ErrTimeout is always a failure
func (r *Tracker) ErrTimeout(duration time.Duration) {
	r.failure()
}

// ErrConcurrencyLimitReject is always a failure
func (r *Tracker) ErrConcurrencyLimitReject() {
	// Your endpoint could be healthy, but because we can't process commands fast enough, you're considered unhealthy.
	// This one could honestly go either way, but generally if a service cannot process commands fast enough, it's not
	// doing what you want.
	r.failure()
}

// ErrShortCircuit is always a failure
func (r *Tracker) ErrShortCircuit() {
	// We had to end the request early.  It's possible the endpoint we want is healthy, but because we had to trip
	// our circuit, due to past misbehavior, it is still end endpoint's fault we cannot satisfy this request, so it
	// fails the SLO.
	r.failure()
}

// ErrBadRequest is ignored
func (r *Tracker) ErrBadRequest(duration time.Duration) {}

// ErrInterrupt is only a failure if healthy time has passed
func (r *Tracker) ErrInterrupt(duration time.Duration) {
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
