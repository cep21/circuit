package hystrix

import (
	"sync"
	"time"

	"encoding/json"

	"github.com/cep21/circuit"
	"github.com/cep21/circuit/faststats"
)

// Opener is ClosedToOpen that opens a circuit after a threshold and % error has been
// reached.  It is the default hystrix implementation.
type Opener struct {
	errorsCount             faststats.RollingCounter
	legitimateAttemptsCount faststats.RollingCounter

	errorPercentage        faststats.AtomicInt64
	requestVolumeThreshold faststats.AtomicInt64

	mu     sync.Mutex
	config ConfigureOpener
}

var _ circuit.ClosedToOpen = &Opener{}

// OpenerFactory creates a err % opener
func OpenerFactory(config ConfigureOpener) func() circuit.ClosedToOpen {
	return func() circuit.ClosedToOpen {
		s := Opener{}
		config.Merge(defaultConfigureOpener)
		s.SetConfigNotThreadSafe(config)
		return &s
	}
}

// ConfigureOpener configures Opener
type ConfigureOpener struct {
	// ErrorThresholdPercentage is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakererrorthresholdpercentage
	ErrorThresholdPercentage int64
	// RequestVolumeThreshold is https://github.com/Netflix/Hystrix/wiki/Configuration#circuitbreakerrequestvolumethreshold
	RequestVolumeThreshold int64
	// Now should simulate time.Now
	Now func() time.Time `json:"-"`
	// RollingDuration is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatstimeinmilliseconds
	RollingDuration time.Duration
	// NumBuckets is https://github.com/Netflix/Hystrix/wiki/Configuration#metricsrollingstatsnumbuckets
	NumBuckets int
}

// Merge this configuration with another
func (c *ConfigureOpener) Merge(other ConfigureOpener) {
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

var defaultConfigureOpener = ConfigureOpener{
	RequestVolumeThreshold:   20,
	ErrorThresholdPercentage: 50,
	Now:             time.Now,
	NumBuckets:      10,
	RollingDuration: 10 * time.Second,
}

// MarshalJSON returns opener information in a JSON format
func (e *Opener) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"config": e.Config(),
	})
}

var _ json.Marshaler = &Opener{}

// Closed resets the error and attempt count
func (e *Opener) Closed(now time.Time) {
	e.errorsCount.Reset(now)
	e.legitimateAttemptsCount.Reset(now)
}

// Opened resets the error and attempt count
func (e *Opener) Opened(now time.Time) {
	e.errorsCount.Reset(now)
	e.legitimateAttemptsCount.Reset(now)
}

// Success increases the number of correct attempts
func (e *Opener) Success(now time.Time, duration time.Duration) {
	e.legitimateAttemptsCount.Inc(now)
}

// Prevent never returns true
func (e *Opener) Prevent(now time.Time) (shouldAllow bool) {
	return false
}

// ErrBadRequest is ignored
func (e *Opener) ErrBadRequest(now time.Time, duration time.Duration) {}

// ErrInterrupt is ignored
func (e *Opener) ErrInterrupt(now time.Time, duration time.Duration) {}

// ErrFailure increases error count for the circuit
func (e *Opener) ErrFailure(now time.Time, duration time.Duration) {
	e.legitimateAttemptsCount.Inc(now)
	e.errorsCount.Inc(now)
}

// ErrTimeout increases error count for the circuit
func (e *Opener) ErrTimeout(now time.Time, duration time.Duration) {
	e.legitimateAttemptsCount.Inc(now)
	e.errorsCount.Inc(now)
}

// ErrConcurrencyLimitReject is ignored
func (e *Opener) ErrConcurrencyLimitReject(now time.Time) {}

// ErrShortCircuit is ignored
func (e *Opener) ErrShortCircuit(now time.Time) {}

// ShouldOpen returns true if rolling count >= threshold and
// error % is high enough.
func (e *Opener) ShouldOpen(now time.Time) bool {
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
func (e *Opener) SetConfigThreadSafe(props ConfigureOpener) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.config = props
	e.errorPercentage.Set(props.ErrorThresholdPercentage)
	e.requestVolumeThreshold.Set(props.RequestVolumeThreshold)
}

// SetConfigNotThreadSafe recreates the buckets.  It is not safe to call while the circuit is active.
func (e *Opener) SetConfigNotThreadSafe(props ConfigureOpener) {
	e.SetConfigThreadSafe(props)
	now := props.Now()
	rollingCounterBucketWidth := time.Duration(props.RollingDuration.Nanoseconds() / int64(props.NumBuckets))
	e.errorsCount = faststats.NewRollingCounter(rollingCounterBucketWidth, props.NumBuckets, now)
	e.legitimateAttemptsCount = faststats.NewRollingCounter(rollingCounterBucketWidth, props.NumBuckets, now)
}

// Config returns the current configuration.  To update configuration, please call SetConfigThreadSafe or
// SetConfigNotThreadSafe
func (e *Opener) Config() ConfigureOpener {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.config
}
