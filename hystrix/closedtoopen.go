package hystrix

import (
	"github.com/cep21/hystrix/internal/fastmath"
	"time"
	"github.com/cep21/hystrix"
	"sync"
)

// OpenOnErrPercentage is ClosedToOpen that opens a circuit after a threshold and % error has been
// reached.  It is the default hystrix implementation.
type OpenOnErrPercentage struct {
	errorsCount             fastmath.RollingCounter
	legitimateAttemptsCount fastmath.RollingCounter

	errorPercentage        fastmath.AtomicInt64
	requestVolumeThreshold fastmath.AtomicInt64

	mu sync.Mutex
	config ConfigureOpenOnErrPercentage
}

var _ hystrix.ClosedToOpen = &OpenOnErrPercentage{}

func OpenOnErrPercentageFactory(config ConfigureOpenOnErrPercentage) func() hystrix.ClosedToOpen {
	return func() hystrix.ClosedToOpen {
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
	Now func()time.Time
	// RollingDuration is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatstimeinmilliseconds
	RollingDuration time.Duration
	// NumBuckets is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatsnumbuckets
	NumBuckets int
}

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

var defaultConfigureOpenOnErrPercentage = ConfigureOpenOnErrPercentage {
	RequestVolumeThreshold:   20,
	ErrorThresholdPercentage: 50,
	Now: time.Now,
	NumBuckets:      10,
	RollingDuration:        10 * time.Second,
}

// Closed resets the error and attempt count
func (e *OpenOnErrPercentage) Closed(now time.Time) {
	e.errorsCount.Reset(now)
	e.legitimateAttemptsCount.Reset(now)
}

// SuccessfulAttempt increases the number of correct attempts
func (e *OpenOnErrPercentage) SuccessfulAttempt(now time.Time, duration time.Duration) {
	e.legitimateAttemptsCount.Inc(now)
}

// Prevent never returns true
func (e *OpenOnErrPercentage) Prevent(now time.Time) (shouldAllow bool) {
	return false
}

// BackedOutAttempt is ignored
func (e *OpenOnErrPercentage) BackedOutAttempt(now time.Time) {
}

// ErrorAttempt increases error count for the circuit
func (e *OpenOnErrPercentage) ErrorAttempt(now time.Time) {
	e.legitimateAttemptsCount.Inc(now)
	e.errorsCount.Inc(now)
}

// AttemptToOpen returns true if rolling count >= threshold and
// error % is high enough.
func (e *OpenOnErrPercentage) AttemptToOpen(now time.Time) bool {
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
	e.errorsCount = fastmath.NewRollingCounter(rollingCounterBucketWidth, props.NumBuckets, now)
	e.legitimateAttemptsCount = fastmath.NewRollingCounter(rollingCounterBucketWidth, props.NumBuckets, now)
}