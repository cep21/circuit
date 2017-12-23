package internal

import (
	"sync"
	"time"

	"github.com/cep21/hystrix"
)

type AsyncCmdMetricCollector struct {
	SuccessChan                   chan time.Duration
	ErrConcurrencyLimitRejectChan chan struct{}
	ErrFailureChan                chan time.Duration
	ErrShortCircuitChan           chan struct{}
	ErrTimeoutChan                chan time.Duration
	ErrBadRequestChan             chan time.Duration
	ForwardTo                     hystrix.RunMetrics
	OnFull                        func(metricType string, duration time.Duration)

	once      sync.Once
	closeChan chan struct{}
}

func NewAsyncCmdMetricCollector(bufferSize int, forwardTo hystrix.RunMetrics) AsyncCmdMetricCollector {
	return AsyncCmdMetricCollector{
		SuccessChan:                   make(chan time.Duration, bufferSize),
		ErrFailureChan:                make(chan time.Duration, bufferSize),
		ErrTimeoutChan:                make(chan time.Duration, bufferSize),
		ErrBadRequestChan:             make(chan time.Duration, bufferSize),
		ErrConcurrencyLimitRejectChan: make(chan struct{}, bufferSize),
		ErrShortCircuitChan:           make(chan struct{}, bufferSize),
		ForwardTo:                     forwardTo,
	}
}

func (c *AsyncCmdMetricCollector) doOnce() {
	c.once.Do(func() {
		c.closeChan = make(chan struct{})
	})
}

func (c *AsyncCmdMetricCollector) onFull(metricType string, duration time.Duration) {
	if c.OnFull != nil {
		c.OnFull(metricType, duration)
	}
}

func (c *AsyncCmdMetricCollector) Start() {
	c.doOnce()
	for {
		select {
		case <-c.closeChan:
			return
		case dur := <-c.SuccessChan:
			c.ForwardTo.Success(dur)
		case <-c.ErrConcurrencyLimitRejectChan:
			c.ForwardTo.ErrConcurrencyLimitReject()
		case dur := <-c.ErrFailureChan:
			c.ForwardTo.ErrFailure(dur)
		case <-c.ErrShortCircuitChan:
			c.ForwardTo.ErrShortCircuit()
		case dur := <-c.ErrTimeoutChan:
			c.ForwardTo.ErrTimeout(dur)
		case dur := <-c.ErrBadRequestChan:
			c.ForwardTo.ErrBadRequest(dur)
		}
	}
}

func (c *AsyncCmdMetricCollector) Close() error {
	c.doOnce()
	close(c.closeChan)
	return nil
}

func (c *AsyncCmdMetricCollector) Success(duration time.Duration) {
	select {
	case c.SuccessChan <- duration:
	default:
		c.onFull("Success", duration)
	}
}

func (c *AsyncCmdMetricCollector) ErrConcurrencyLimitReject() {
	select {
	case c.ErrConcurrencyLimitRejectChan <- struct{}{}:
	default:
		c.onFull("ErrConcurrencyLimitReject", 0)
	}
}

func (c *AsyncCmdMetricCollector) ErrFailure(duration time.Duration) {
	select {
	case c.ErrFailureChan <- duration:
	default:
		c.onFull("ErrFailure", duration)
	}
}

func (c *AsyncCmdMetricCollector) ErrShortCircuit() {
	select {
	case c.ErrShortCircuitChan <- struct{}{}:
	default:
		c.onFull("ErrShortCircuit", 0)
	}
}

func (c *AsyncCmdMetricCollector) ErrTimeout(duration time.Duration) {
	select {
	case c.ErrTimeoutChan <- duration:
	default:
		c.onFull("ErrTimeout", duration)
	}
}

func (c *AsyncCmdMetricCollector) ErrBadRequest(duration time.Duration) {
	select {
	case c.ErrBadRequestChan <- duration:
	default:
		c.onFull("ErrBadRequest", duration)
	}
}

type AsyncFallbackMetricCollector struct {
	SuccessChan                   chan time.Duration
	ErrConcurrencyLimitRejectChan chan struct{}
	ErrFailureChan                chan time.Duration
	ForwardTo                     hystrix.FallbackMetric
	OnFull                        func(metricType string, duration time.Duration)

	once      sync.Once
	closeChan chan struct{}
}

func NewAsyncFallbackMetricCollector(bufferSize int, forwardTo hystrix.FallbackMetric) AsyncFallbackMetricCollector {
	return AsyncFallbackMetricCollector{
		SuccessChan:                   make(chan time.Duration, bufferSize),
		ErrConcurrencyLimitRejectChan: make(chan struct{}, bufferSize),
		ErrFailureChan:                make(chan time.Duration, bufferSize),
		ForwardTo:                     forwardTo,
	}
}

func (c *AsyncFallbackMetricCollector) doOnce() {
	c.once.Do(func() {
		c.closeChan = make(chan struct{})
	})
}

func (c *AsyncFallbackMetricCollector) onFull(metricType string, duration time.Duration) {
	if c.OnFull != nil {
		c.OnFull(metricType, duration)
	}
}

func (c *AsyncFallbackMetricCollector) Start() {
	c.doOnce()
	for {
		select {
		case <-c.closeChan:
			return
		case dur := <-c.SuccessChan:
			c.ForwardTo.Success(dur)
		case <-c.ErrConcurrencyLimitRejectChan:
			c.ForwardTo.ErrConcurrencyLimitReject()
		case dur := <-c.ErrFailureChan:
			c.ForwardTo.ErrFailure(dur)
		}
	}
}

func (c *AsyncFallbackMetricCollector) Close() error {
	c.doOnce()
	close(c.closeChan)
	return nil
}

func (c *AsyncFallbackMetricCollector) Success(duration time.Duration) {
	select {
	case c.SuccessChan <- duration:
	default:
		c.onFull("Success", duration)
	}
}
func (c *AsyncFallbackMetricCollector) ErrConcurrencyLimitReject() {
	select {
	case c.ErrConcurrencyLimitRejectChan <- struct{}{}:
	default:
		c.onFull("ErrConcurrencyLimitReject", 0)
	}
}
func (c *AsyncFallbackMetricCollector) ErrFailure(duration time.Duration) {
	select {
	case c.ErrFailureChan <- duration:
	default:
		c.onFull("ErrFailure", duration)
	}
}
