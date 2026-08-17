package circuit

import (
	"context"
	"errors"
	"testing"

	"github.com/cep21/circuit/v4/faststats"
)

var errBenchFailure = errors.New("bench failure")

func benchPasses(context.Context) error          { return nil }
func benchFails(context.Context) error           { return errBenchFailure }
func benchFallback(context.Context, error) error { return nil }

func BenchmarkExecute(b *testing.B) {
	ctx := context.Background()
	b.Run("success/default-timeout", func(b *testing.B) {
		c := NewCircuitFromConfig("b", Config{Execution: ExecutionConfig{MaxConcurrentRequests: -1}})
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = c.Execute(ctx, benchPasses, nil)
		}
	})
	b.Run("success/no-timeout", func(b *testing.B) {
		c := NewCircuitFromConfig("b", Config{Execution: ExecutionConfig{MaxConcurrentRequests: -1, Timeout: -1}})
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = c.Execute(ctx, benchPasses, nil)
		}
	})
	b.Run("success/no-timeout/parallel", func(b *testing.B) {
		c := NewCircuitFromConfig("b", Config{Execution: ExecutionConfig{MaxConcurrentRequests: -1, Timeout: -1}})
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = c.Execute(ctx, benchPasses, nil)
			}
		})
	})
	b.Run("open/no-fallback", func(b *testing.B) {
		c := NewCircuitFromConfig("b", Config{Execution: ExecutionConfig{MaxConcurrentRequests: -1, Timeout: -1}})
		c.OpenCircuit(ctx)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = c.Execute(ctx, benchPasses, nil)
		}
	})
	b.Run("open/with-fallback", func(b *testing.B) {
		c := NewCircuitFromConfig("b", Config{Execution: ExecutionConfig{MaxConcurrentRequests: -1, Timeout: -1}, Fallback: FallbackConfig{MaxConcurrentRequests: -1}})
		c.OpenCircuit(ctx)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = c.Execute(ctx, benchPasses, benchFallback)
		}
	})
	b.Run("open/with-fallback/parallel", func(b *testing.B) {
		c := NewCircuitFromConfig("b", Config{Execution: ExecutionConfig{MaxConcurrentRequests: -1, Timeout: -1}, Fallback: FallbackConfig{MaxConcurrentRequests: -1}})
		c.OpenCircuit(ctx)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = c.Execute(ctx, benchPasses, benchFallback)
			}
		})
	})
	b.Run("failure/with-fallback", func(b *testing.B) {
		c := NewCircuitFromConfig("b", Config{Execution: ExecutionConfig{MaxConcurrentRequests: -1, Timeout: -1}, Fallback: FallbackConfig{MaxConcurrentRequests: -1}})
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = c.Execute(ctx, benchFails, benchFallback)
		}
	})
	b.Run("fallback-throttled", func(b *testing.B) {
		c := NewCircuitFromConfig("b", Config{Execution: ExecutionConfig{MaxConcurrentRequests: -1, Timeout: -1}, Fallback: FallbackConfig{MaxConcurrentRequests: 1}})
		var hold faststats.AtomicInt64
		hold.Add(5)
		c.concurrentFallbacks.Add(hold.Get())
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = c.Execute(ctx, benchFails, benchFallback)
		}
	})
}

func BenchmarkIsBadRequest(b *testing.B) {
	b.Run("circuit-error", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = IsBadRequest(errCircuitOpen)
		}
	})
	b.Run("plain-error", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = IsBadRequest(errBenchFailure)
		}
	})
	b.Run("bad-request", func(b *testing.B) {
		b.ReportAllocs()
		var err error = SimpleBadRequest{Err: errBenchFailure}
		for i := 0; i < b.N; i++ {
			_ = IsBadRequest(err)
		}
	})
	b.Run("Error()", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = errCircuitOpen.Error()
		}
	})
}
