package benchmarking

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	gohystrix "github.com/afex/hystrix-go/hystrix"
	"github.com/cep21/hystrix"
	"github.com/cep21/hystrix/metric_implementations/rolling"
	"github.com/cep21/hystrix/simplelogic"
	iandCircuit "github.com/iand/circuit"
	"github.com/rubyist/circuitbreaker"
	"github.com/sony/gobreaker"
	"github.com/streadway/handy/breaker"
)

type circuitConfigs struct {
	config interface{}
	name   string
}

type circuitImpls struct {
	name      string
	runner    func(b *testing.B, configIn interface{}, concurrent int, funcToRun interface{}, pass bool)
	configs   []circuitConfigs
	funcTypes []interface{}
}

func BenchmarkCiruits(b *testing.B) {
	rollingTimeoutStats := rolling.CollectRollingStats(rolling.RunStatsConfig{}, rolling.FallbackStatsConfig{})("")
	rollingTimeoutStats.Merge(hystrix.CommandProperties{
		Execution: hystrix.ExecutionConfig{
			Timeout: -1,
		},
	})
	concurrents := []int{1, 75}
	passesParam := []bool{true, false}
	impls := []circuitImpls{
		{
			name:   "Hystrix",
			runner: hystrixRunner,
			configs: []circuitConfigs{
				{
					name:   "Metrics",
					config: rollingTimeoutStats,
				}, {
					name: "Minimal",
					config: hystrix.CommandProperties{
						Execution: hystrix.ExecutionConfig{
							MaxConcurrentRequests: int64(-1),
							Timeout:               -1,
						},
						GoSpecific: hystrix.GoSpecificConfig{
							ClosedToOpenFactory: simplelogic.ConsecutiveErrOpenerFactory(simplelogic.ConfigConsecutiveErrOpener{}),
						},
					},
				}, {
					name: "UseGo",
					config: hystrix.CommandProperties{
						Execution: hystrix.ExecutionConfig{
							MaxConcurrentRequests: int64(12),
							Timeout:               -1,
						}, GoSpecific: hystrix.GoSpecificConfig{
							CustomConfig: map[interface{}]interface{}{
								"use-go": true,
							},
						},
					},
				},
			},
			funcTypes: []interface{}{passesCtx, failsCtx},
		},
		{
			name:   "GoHystrix",
			runner: goHystrixRunner,
			configs: []circuitConfigs{
				{
					name: "DefaultConfig",
					config: gohystrix.CommandConfig{
						// I don't *WANT* to pass 100,000 here.  It should just work with `concurrent`, but it doesn't.
						//MaxConcurrentRequests: concurrent,
						MaxConcurrentRequests: 100000,
					},
				},
			},
			funcTypes: []interface{}{passes, fails},
		},
		{
			name:   "rubyist",
			runner: rubyistRunner,
			configs: []circuitConfigs{
				{
					name: "Threshold-10",
					config: func() *circuit.Breaker {
						return circuit.NewThresholdBreaker(10)
					},
				},
			},
			funcTypes: []interface{}{passes, fails},
		},
		{
			name:   "gobreaker",
			runner: gobreakerRunner,
			configs: []circuitConfigs{
				{
					name:   "Default",
					config: gobreaker.Settings{},
				},
			},
			funcTypes: []interface{}{passesInter, failsInter},
		},
		{
			name:   "handy",
			runner: handyRunner,
			configs: []circuitConfigs{
				{
					name: "Default",
				},
			},
			funcTypes: []interface{}{nil, nil},
		},
		{
			name:   "iand_circuit",
			runner: iandCircuitRunner,
			configs: []circuitConfigs{
				{
					name: "Default",
					config: &iandCircuit.Breaker{
						Concurrency: 75,
					},
				},
			},
			funcTypes: []interface{}{passes, fails},
		},
	}
	for _, impl := range impls {
		b.Run(impl.name, func(b *testing.B) {
			for _, config := range impl.configs {
				b.Run(config.name, func(b *testing.B) {
					for _, pass := range passesParam {
						pass := pass
						var f interface{}
						var name string
						if pass {
							f = impl.funcTypes[0]
							name = "passing"
						} else {
							f = impl.funcTypes[1]
							name = "failing"
						}
						b.Run(name, func(b *testing.B) {
							for _, concurrent := range concurrents {
								b.Run(strconv.Itoa(concurrent), func(b *testing.B) {
									impl.runner(b, config.config, concurrent, f, pass)
								})
							}
						})
					}
				})
			}
		})
	}
}

func hystrixRunner(b *testing.B, configIn interface{}, concurrent int, funcToRun interface{}, pass bool) {
	f := funcToRun.(func(context.Context) error)
	h := hystrix.Hystrix{}
	config := configIn.(hystrix.CommandProperties)
	config.Execution.MaxConcurrentRequests = int64(concurrent)
	c := h.MustCreateCircuit("hello-world", config)
	ctx := context.Background()
	useExecute := config.GoSpecific.CustomConfig == nil
	genericBenchmarkTesting(b, concurrent, func() error {
		if useExecute {
			return c.Execute(ctx, f, nil)
		}
		return c.Go(ctx, f, nil)
	}, !pass)
}

func rubyistRunner(b *testing.B, configIn interface{}, concurrent int, funcToRun interface{}, pass bool) {
	circ := configIn.(func() *circuit.Breaker)()
	f := funcToRun.(func() error)
	ctx := context.Background()
	genericBenchmarkTesting(b, concurrent, func() error {
		return circ.CallContext(ctx, f, time.Second)
	}, !pass)
}

var iCount int64

func goHystrixRunner(b *testing.B, configIn interface{}, concurrent int, funcToRun interface{}, pass bool) {
	circuitName := fmt.Sprintf("gocircuit-%d", atomic.AddInt64(&iCount, 1))
	config := configIn.(gohystrix.CommandConfig)
	gohystrix.ConfigureCommand(circuitName, config)
	f := funcToRun.(func() error)
	genericBenchmarkTesting(b, concurrent, func() error {
		return gohystrix.Do(circuitName, f, nil)
	}, !pass)
}

func gobreakerRunner(b *testing.B, configIn interface{}, concurrent int, funcToRun interface{}, pass bool) {
	conf := configIn.(gobreaker.Settings)
	cb := gobreaker.NewCircuitBreaker(conf)
	f := funcToRun.(func() (interface{}, error))
	genericBenchmarkTesting(b, concurrent, func() error {
		_, err := cb.Execute(f)
		return err
	}, !pass)
}

func handyRunner(b *testing.B, _ interface{}, concurrent int, _ interface{}, pass bool) {
	cb := breaker.NewBreaker(.9)
	genericBenchmarkTesting(b, concurrent, func() error {
		cb.Allow()
		if pass {
			cb.Success(time.Second)
			return nil
		}
		cb.Failure(time.Second)
		return errFailure
	}, !pass)
}

func iandCircuitRunner(b *testing.B, breakerIn interface{}, concurrent int, funcToRun interface{}, pass bool) {
	bc := breakerIn.(*iandCircuit.Breaker)
	ctx := context.Background()
	f := funcToRun.(func() error)
	genericBenchmarkTesting(b, concurrent, func() error {
		return bc.Do(ctx, f)
	}, !pass)
}
