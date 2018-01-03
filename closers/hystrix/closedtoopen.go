package circuit

import (
	"sync"
	"time"

	"github.com/cep21/circuit"
	"github.com/cep21/circuit/faststats"
)

// OpenOnErrPercentage is ClosedToOpen that opens a circuit after a threshold and % error has been
// reached.  It is the default hystrix implementation.
type OpenOnErrPercentage struct {
	errorsCount             faststats.RollingCounter
	legitimateAttemptsCount faststats.RollingCounter

	errorPercentage        faststats.AtomicInt64
	requestVolumeThreshold faststats.AtomicInt64

	mu     sync.Mutex
	config ConfigureOpenOnErrPercentage
}

var _ circuit.ClosedToOpen = &OpenOnErrPercentage{}

// OpenOnErrPercentageFactory creates a err % opener
func OpenOnErrPercentageFactory(config ConfigureOpenOnErrPercentage) func() circuit.ClosedToOpen {
	return func() circuit.ClosedToOpen {
		s := OpenOnErrPercentage{}
		config.Merge(defaultConfigureOpenOnErrPercentage)
		s.SetConfigNotThreadSafe(config)
		return &s
	}
}

// ConfigureOpenOnErrPercentage configures OpenOnErrPercentage
type ConfigureOpenOnErrPercentage struct {
	// ErrorThresholdPercentage is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakererrorthresholdpercentage
	ErrorThresholdPercentage int64
	// RequestVolumeThreshold is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerrequestvolumethreshold
	RequestVolumeThreshold int64
	// Now should simulate time.Now
	Now func() time.Time
	// RollingDuration is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatstimeinmilliseconds
	RollingDuration time.Duration
	// NumBuckets is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatsnumbuckets
	NumBuckets int
}

// Merge this configuration with another
func (c *ConfigureOpenOnErrPercentage) Merge(other ConfigureOpenOnErrPercentage) {
	if c.ErrorThresholdPercentage == 0 {
		c.ErrorThresholdPercentage = other.ErrorThresholdPercentage
	}
	if c.RequestVolumeThreshold == 0 {
		c.RequestVolumeThreshold = other.RequestVolumeThreshold
	}
	if c.Now == nil {
		c.Now = other.Now
	}
	if c.RollingDuration == 0 {
		c.RollingDuration = other.RollingDuration
	}
	if c.NumBuckets == 0 {
		c.NumBuckets = other.NumBuckets
	}
}

var defaultConfigureOpenOnErrPercentage = ConfigureOpenOnErrPercentage{
	RequestVolumeThreshold:   20,
	ErrorThresholdPercentage: 50,
	Now:             time.Now,
	NumBuckets:      10,
	RollingDuration: 10 * time.Second,
}

// Closed resets the error and attempt count
func (e *OpenOnErrPercentage) Closed(now time.Time) {
	e.errorsCount.Reset(now)
	e.legitimateAttemptsCount.Reset(now)
}

// Opened resets the error and attempt count
func (e *OpenOnErrPercentage) Opened(now time.Time) {
	e.errorsCount.Reset(now)
	e.legitimateAttemptsCount.Reset(now)
}

// Success increases the number of correct attempts
func (e *OpenOnErrPercentage) Success(now time.Time, duration time.Duration) {
	e.legitimateAttemptsCount.Inc(now)
}

// Prevent never returns true
func (e *OpenOnErrPercentage) Prevent(now time.Time) (shouldAllow bool) {
	return false
}

// ErrBadRequest is ignored
func (e *OpenOnErrPercentage) ErrBadRequest(now time.Time, duration time.Duration) {}

// ErrInterrupt is ignored
func (e *OpenOnErrPercentage) ErrInterrupt(now time.Time, duration time.Duration) {}

// ErrFailure increases error count for the circuit
func (e *OpenOnErrPercentage) ErrFailure(now time.Time, duration time.Duration) {
	e.legitimateAttemptsCount.Inc(now)
	e.errorsCount.Inc(now)
}

// ErrTimeout increases error count for the circuit
func (e *OpenOnErrPercentage) ErrTimeout(now time.Time, duration time.Duration) {
	e.legitimateAttemptsCount.Inc(now)
	e.errorsCount.Inc(now)
}

// ErrConcurrencyLimitReject is ignored
func (e *OpenOnErrPercentage) ErrConcurrencyLimitReject(now time.Time) {}

// ErrShortCircuit is ignored
func (e *OpenOnErrPercentage) ErrShortCircuit(now time.Time) {}

// ShouldOpen returns true if rolling count >= threshold and
// error % is high enough.
func (e *OpenOnErrPercentage) ShouldOpen(now time.Time) bool {
	attemptCount := e.legitimateAttemptsCount.RollingSumAt(now)
	if attemptCount == 0 || attemptCount < e.requestVolumeThreshold.Get() {
		// not enough requests. Will not open circuit
		return false
	}

	errCount := e.errorsCount.RollingSumAt(now)
	errPercentage := int64(float64(errCount) / float64(attemptCount) * 100)
	return errPercentage >= e.errorPercentage.Get()
}

// SetConfigThreadSafe modifies error % and request volume threshold
func (e *OpenOnErrPercentage) SetConfigThreadSafe(props ConfigureOpenOnErrPercentage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.config = props
	e.errorPercentage.Set(props.ErrorThresholdPercentage)
	e.requestVolumeThreshold.Set(props.RequestVolumeThreshold)
}

// SetConfigNotThreadSafe recreates the buckets
func (e *OpenOnErrPercentage) SetConfigNotThreadSafe(props ConfigureOpenOnErrPercentage) {
	e.SetConfigThreadSafe(props)
	now := props.Now()
	rollingCounterBucketWidth := time.Duration(props.RollingDuration.Nanoseconds() / int64(props.NumBuckets))
	e.errorsCount = faststats.NewRollingCounter(rollingCounterBucketWidth, props.NumBuckets, now)
	e.legitimateAttemptsCount = faststats.NewRollingCounter(rollingCounterBucketWidth, props.NumBuckets, now)
}
