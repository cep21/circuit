package metriceventstream

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"io"

	"github.com/cep21/circuit/v3"
	"github.com/cep21/circuit/v3/closers/hystrix"
	"github.com/cep21/circuit/v3/faststats"
	"github.com/cep21/circuit/v3/metrics/rolling"
)

// MetricEventStream is a HTTP handler that supports hystrix's metric stream API
// See https://github.com/Netflix/Hystrix/wiki/Metrics-and-Monitoring#metrics-event-stream.  It requires that your
// metrics are monitored by rolling stats, because it uses them to get health information.
type MetricEventStream struct {
	Manager      *circuit.Manager
	TickDuration time.Duration

	eventStreams map[*http.Request]chan []byte
	closeChan    chan struct{}
	mu           sync.Mutex
	once         sync.Once
}

var _ http.Handler = &MetricEventStream{}

func (m *MetricEventStream) doOnce() {
	m.closeChan = make(chan struct{})
	m.eventStreams = make(map[*http.Request]chan []byte)
}

type writableFlusher interface {
	http.Flusher
	http.ResponseWriter
}

func (m *MetricEventStream) tickDuration() time.Duration {
	if m.TickDuration == 0 {
		return time.Second
	}
	return m.TickDuration
}

// ServeHTTP sends a never ending list of metric events
func (m *MetricEventStream) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	m.once.Do(m.doOnce)
	// Make sure that the writer supports flushing.
	flusher, ok := rw.(writableFlusher)
	if !ok {
		http.Error(rw, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}
	rw.Header().Add("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")

	myBytesToWrite := make(chan []byte, 1024)
	m.mu.Lock()
	m.eventStreams[req] = myBytesToWrite
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.eventStreams, req)
		m.mu.Unlock()
	}()

	for {
		select {
		case <-req.Context().Done():
			// client is gone
			return
		case <-m.closeChan:
			// The event stream was asked to close
			return
		case toWriteBytes := <-myBytesToWrite:
			_, err := flusher.Write(toWriteBytes)
			if err != nil {
				// This writer is bad.  Bye felicia
				return
			}
			flusher.Flush()
		}
	}
}

func (m *MetricEventStream) listenerCount() int {
	m.mu.Lock()
	ret := len(m.eventStreams)
	m.mu.Unlock()
	return ret
}

func (m *MetricEventStream) sendEvent(event []byte) {
	m.mu.Lock()
	placesToSend := make([]chan []byte, 0, len(m.eventStreams))
	for _, stream := range m.eventStreams {
		placesToSend = append(placesToSend, stream)
	}
	m.mu.Unlock()
	for _, stream := range placesToSend {
		select {
		case stream <- event:
		default:
			// chan full.  Maybe not flushing fast enough.  Move on.
		}
	}
}

func mustWrite(w io.Writer, s string) {
	_, err := io.WriteString(w, s)
	if err != nil {
		panic(err)
	}
}

// Start should be called once per MetricEventStream.  It runs forever, until Close is called.
func (m *MetricEventStream) Start() error {
	m.once.Do(m.doOnce)
	for {
		select {
		case <-time.After(m.tickDuration()):
			// Don't collect events if nobody is listening
			if m.listenerCount() == 0 {
				continue
			}
			for _, circuit := range m.Manager.AllCircuits() {
				buf := &bytes.Buffer{}
				mustWrite(buf, "data:")
				commandMetrics := collectCommandMetrics(circuit)
				encoder := json.NewEncoder(buf)
				err := encoder.Encode(commandMetrics)
				if err != nil {
					continue
				}

				mustWrite(buf, "\n")
				m.sendEvent(buf.Bytes())
			}
		case <-m.closeChan:
			return nil
		}
	}
}

// Close ends the Start function
func (m *MetricEventStream) Close() error {
	m.once.Do(m.doOnce)
	close(m.closeChan)
	return nil
}

func collectCommandMetrics(cb *circuit.Circuit) *streamCmdMetric {
	builtInRollingCmdMetricCollector := rolling.FindCommandMetrics(cb)
	if builtInRollingCmdMetricCollector == nil {
		// We still show the circuit, but everything shows up as zero
		builtInRollingCmdMetricCollector = &rolling.RunStats{}
	}
	builtInRollingFallbackMetricCollector := rolling.FindFallbackMetrics(cb)
	if builtInRollingFallbackMetricCollector == nil {
		// We still show the circuit, but everything shows up as zero
		builtInRollingFallbackMetricCollector = &rolling.FallbackStats{}
	}
	now := cb.Config().General.TimeKeeper.Now()
	snap := builtInRollingCmdMetricCollector.Latencies.SnapshotAt(now)
	circuitConfig := cb.Config()
	return attachHystrixProperties(cb, &streamCmdMetric{
		Type:           "HystrixCommand",
		Name:           cb.Name(),
		Group:          "",
		ReportingHosts: 1,
		Time:           now.UnixNano() / time.Millisecond.Nanoseconds(),

		RequestCount:       builtInRollingCmdMetricCollector.LegitimateAttemptsAt(now) + builtInRollingCmdMetricCollector.ErrInterrupts.RollingSumAt(now),
		ErrorCount:         builtInRollingCmdMetricCollector.ErrorsAt(now),
		ErrorPct:           int64(100 * builtInRollingCmdMetricCollector.ErrorPercentageAt(now)),
		CircuitBreakerOpen: cb.IsOpen(),

		RollingCountFallbackSuccess:   builtInRollingFallbackMetricCollector.Successes.RollingSumAt(now),
		RollingCountFallbackFailure:   builtInRollingFallbackMetricCollector.ErrFailures.RollingSumAt(now),
		RollingCountFallbackRejection: builtInRollingFallbackMetricCollector.ErrConcurrencyLimitRejects.RollingSumAt(now),

		RollingCountSuccess:           builtInRollingCmdMetricCollector.Successes.RollingSumAt(now),
		RollingCountSemaphoreRejected: builtInRollingCmdMetricCollector.ErrConcurrencyLimitRejects.RollingSumAt(now),
		RollingCountFailure:           builtInRollingCmdMetricCollector.ErrFailures.RollingSumAt(now),
		RollingCountShortCircuited:    builtInRollingCmdMetricCollector.ErrShortCircuits.RollingSumAt(now),
		RollingCountTimeout:           builtInRollingCmdMetricCollector.ErrTimeouts.RollingSumAt(now),
		// Note: There is no errInterrupt field inside the dashboard, but i still want to expose these metrics there,
		//       so I just roll them into BadRequests
		// nolint: lll
		RollingCountBadRequests: builtInRollingCmdMetricCollector.ErrBadRequests.RollingSumAt(now) + builtInRollingCmdMetricCollector.ErrInterrupts.RollingSumAt(now),

		TotalCountFallbackSuccess:   builtInRollingFallbackMetricCollector.Successes.TotalSum(),
		TotalCountFallbackFailure:   builtInRollingFallbackMetricCollector.ErrFailures.TotalSum(),
		TotalCountFallbackRejection: builtInRollingFallbackMetricCollector.ErrConcurrencyLimitRejects.TotalSum(),

		TotalCountSuccess:           builtInRollingCmdMetricCollector.Successes.TotalSum(),
		TotalCountSemaphoreRejected: builtInRollingCmdMetricCollector.ErrConcurrencyLimitRejects.TotalSum(),
		TotalCountFailure:           builtInRollingCmdMetricCollector.ErrFailures.TotalSum(),
		TotalCountShortCircuited:    builtInRollingCmdMetricCollector.ErrShortCircuits.TotalSum(),
		TotalCountTimeout:           builtInRollingCmdMetricCollector.ErrTimeouts.TotalSum(),
		TotalCountBadRequests:       builtInRollingCmdMetricCollector.ErrBadRequests.TotalSum() + builtInRollingCmdMetricCollector.ErrInterrupts.TotalSum(),

		LatencyTotal:       generateLatencyTimings(snap),
		LatencyTotalMean:   snap.Mean().Nanoseconds() / time.Millisecond.Nanoseconds(),
		LatencyExecute:     generateLatencyTimings(snap),
		LatencyExecuteMean: snap.Mean().Nanoseconds() / time.Millisecond.Nanoseconds(),

		CurrentConcurrentExecutionCount: cb.ConcurrentCommands(),

		ExecutionIsolationStrategy: "SEMAPHORE",

		// Circuit config
		CircuitBreakerEnabled:     !circuitConfig.General.Disabled,
		CircuitBreakerForceClosed: circuitConfig.General.ForcedClosed,
		CircuitBreakerForceOpen:   circuitConfig.General.ForceOpen,

		// Execution config
		ExecutionIsolationSemaphoreMaxConcurrentRequests: circuitConfig.Execution.MaxConcurrentRequests,
		ExecutionIsolationThreadTimeout:                  circuitConfig.Execution.Timeout.Nanoseconds() / time.Millisecond.Nanoseconds(),

		// Fallback config
		FallbackIsolationSemaphoreMaxConcurrentRequests: circuitConfig.Fallback.MaxConcurrentRequests,

		RollingStatsWindow: builtInRollingCmdMetricCollector.Config().RollingStatsDuration.Nanoseconds() / time.Millisecond.Nanoseconds(),
	})
}
func attachHystrixProperties(cb *circuit.Circuit, into *streamCmdMetric) *streamCmdMetric {
	if asHystrix, ok := cb.ClosedToOpen.(*hystrix.Opener); ok {
		into.CircuitBreakerErrorThresholdPercent = asHystrix.Config().ErrorThresholdPercentage
		into.CircuitBreakerRequestVolumeThreshold = asHystrix.Config().RequestVolumeThreshold
	}
	if asHystrix, ok := cb.OpenToClose.(*hystrix.Closer); ok {
		into.CircuitBreakerSleepWindow = asHystrix.Config().SleepWindow.Nanoseconds() / time.Millisecond.Nanoseconds()
	}
	return into
}

func generateLatencyTimings(snap faststats.SortedDurations) streamCmdLatency {
	return streamCmdLatency{
		Timing0:   snap.Percentile(0).Nanoseconds() / time.Millisecond.Nanoseconds(),
		Timing25:  snap.Percentile(25).Nanoseconds() / time.Millisecond.Nanoseconds(),
		Timing50:  snap.Percentile(50).Nanoseconds() / time.Millisecond.Nanoseconds(),
		Timing75:  snap.Percentile(75).Nanoseconds() / time.Millisecond.Nanoseconds(),
		Timing90:  snap.Percentile(90).Nanoseconds() / time.Millisecond.Nanoseconds(),
		Timing95:  snap.Percentile(95).Nanoseconds() / time.Millisecond.Nanoseconds(),
		Timing99:  snap.Percentile(99).Nanoseconds() / time.Millisecond.Nanoseconds(),
		Timing995: snap.Percentile(99.5).Nanoseconds() / time.Millisecond.Nanoseconds(),
		Timing100: snap.Percentile(100).Nanoseconds() / time.Millisecond.Nanoseconds(),
	}
}

type streamCmdMetric struct {
	Type           string `json:"type"`
	Name           string `json:"name"`
	Group          string `json:"group"`
	Time           int64  `json:"currentTime"`
	ReportingHosts int64  `json:"reportingHosts"`

	// Health
	RequestCount int64 `json:"requestCount"`
	ErrorCount   int64 `json:"errorCount"`
	ErrorPct     int64 `json:"errorPercentage"`

	// None of these are used
	RollingCountCollapsedRequests  int64 `json:"rollingCountCollapsedRequests"`
	RollingCountExceptionsThrown   int64 `json:"rollingCountExceptionsThrown"`
	RollingCountResponsesFromCache int64 `json:"rollingCountResponsesFromCache"`

	// All from FallbackMetrics
	RollingCountFallbackFailure   int64 `json:"rollingCountFallbackFailure"`
	RollingCountFallbackRejection int64 `json:"rollingCountFallbackRejection"`
	RollingCountFallbackSuccess   int64 `json:"rollingCountFallbackSuccess"`

	RollingCountFailure           int64 `json:"rollingCountFailure"`
	RollingCountSemaphoreRejected int64 `json:"rollingCountSemaphoreRejected"`
	RollingCountShortCircuited    int64 `json:"rollingCountShortCircuited"`
	RollingCountSuccess           int64 `json:"rollingCountSuccess"`
	// We don't use thread pool model
	RollingCountThreadPoolRejected int64 `json:"rollingCountThreadPoolRejected"`
	RollingCountTimeout            int64 `json:"rollingCountTimeout"`
	RollingCountBadRequests        int64 `json:"rollingCountBadRequests"`

	CurrentConcurrentExecutionCount int64 `json:"currentConcurrentExecutionCount"`

	LatencyExecuteMean int64            `json:"latencyExecute_mean"`
	LatencyTotalMean   int64            `json:"latencyTotal_mean"`
	LatencyExecute     streamCmdLatency `json:"latencyExecute"`
	LatencyTotal       streamCmdLatency `json:"latencyTotal"`

	// Properties
	CircuitBreakerRequestVolumeThreshold             int64  `json:"propertyValue_circuitBreakerRequestVolumeThreshold"`
	CircuitBreakerSleepWindow                        int64  `json:"propertyValue_circuitBreakerSleepWindowInMilliseconds"`
	CircuitBreakerErrorThresholdPercent              int64  `json:"propertyValue_circuitBreakerErrorThresholdPercentage"`
	ExecutionIsolationStrategy                       string `json:"propertyValue_executionIsolationStrategy"`
	ExecutionIsolationThreadPoolKeyOverride          string `json:"propertyValue_executionIsolationThreadPoolKeyOverride"`
	ExecutionIsolationThreadTimeout                  int64  `json:"propertyValue_executionIsolationThreadTimeoutInMilliseconds"`
	ExecutionIsolationSemaphoreMaxConcurrentRequests int64  `json:"propertyValue_executionIsolationSemaphoreMaxConcurrentRequests"`
	FallbackIsolationSemaphoreMaxConcurrentRequests  int64  `json:"propertyValue_fallbackIsolationSemaphoreMaxConcurrentRequests"`
	RollingStatsWindow                               int64  `json:"propertyValue_metricsRollingStatisticalWindowInMilliseconds"`
	CircuitBreakerForceOpen                          bool   `json:"propertyValue_circuitBreakerForceOpen"`
	CircuitBreakerForceClosed                        bool   `json:"propertyValue_circuitBreakerForceClosed"`
	CircuitBreakerEnabled                            bool   `json:"propertyValue_circuitBreakerEnabled"`
	ExecutionIsolationThreadInterruptOnTimeout       bool   `json:"propertyValue_executionIsolationThreadInterruptOnTimeout"`
	RequestCacheEnabled                              bool   `json:"propertyValue_requestCacheEnabled"`
	RequestLogEnabled                                bool   `json:"propertyValue_requestLogEnabled"`

	// Health
	CircuitBreakerOpen bool `json:"isCircuitBreakerOpen"`

	TotalCountFallbackSuccess   int64 `json:"countFallbackSuccess"`
	TotalCountFallbackFailure   int64 `json:"countFallbackFailure"`
	TotalCountFallbackRejection int64 `json:"countFallbackRejection"`
	TotalCountSuccess           int64 `json:"countSuccess"`
	TotalCountSemaphoreRejected int64 `json:"countSemaphoreRejected"`
	TotalCountFailure           int64 `json:"countFailure"`
	TotalCountShortCircuited    int64 `json:"countShortCircuited"`
	TotalCountTimeout           int64 `json:"countTimeout"`
	TotalCountBadRequests       int64 `json:"countBadRequests"`
}

type streamCmdLatency struct {
	Timing0   int64 `json:"0"`
	Timing25  int64 `json:"25"`
	Timing50  int64 `json:"50"`
	Timing75  int64 `json:"75"`
	Timing90  int64 `json:"90"`
	Timing95  int64 `json:"95"`
	Timing99  int64 `json:"99"`
	Timing995 int64 `json:"99.5"`
	Timing100 int64 `json:"100"`
}
