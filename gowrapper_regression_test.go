package circuit

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// waitersRunning counts the goroutines spawned by goroutineWrapper that are still alive
func waitersRunning() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "created by github.com/cep21/circuit/v4.(*goroutineWrapper)") +
		strings.Count(string(buf), "created by github.com/cep21/circuit/v4.(*Circuit).Go")
}

// waitForWaiters blocks until at most want goroutineWrapper goroutines remain
func waitForWaiters(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(regDeadlockTimeout)
	for waitersRunning() > want {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutine(s) spawned by Go never exited", waitersRunning()-want)
		}
		time.Sleep(time.Millisecond)
	}
}

// A callback that calls runtime.Goexit (t.Fatal / require.*) must not leave Go hanging, holding its concurrency slot.
func TestGoReportsGoexit(t *testing.T) {
	exits := func(context.Context) error { runtime.Goexit(); return nil }
	for name, call := range map[string]func(c *Circuit) error{
		"run": func(c *Circuit) error { return c.Go(context.Background(), exits, nil) },
		"fallback": func(c *Circuit) error {
			return c.Go(context.Background(),
				func(context.Context) error { return errors.New("boom") },
				func(ctx context.Context, _ error) error { return exits(ctx) })
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Config{}
			if name == "run" {
				// The fallback never has a circuit timeout, so that case hangs with the default config
				cfg.Execution.Timeout = -1
			}
			c := NewCircuitFromConfig("goexit", cfg)
			done := make(chan struct{})
			var err error
			go func() {
				defer close(done)
				err = call(c)
			}()
			regWait(t, done, "Go to notice its callback called runtime.Goexit")
			require.Equal(t, errGoexit, err)
			require.Zero(t, c.ConcurrentCommands())
			require.Zero(t, c.ConcurrentFallbacks())
		})
	}
}

// An abandoned callback that exits through Goexit must not leave the GoLostErrors waiter behind forever.
func TestGoAbandonedGoexitDoesNotLeakWaiter(t *testing.T) {
	lost := make(chan error, 1)
	c := NewCircuitFromConfig("goexit", Config{
		General:   GeneralConfig{GoLostErrors: func(err error, _ interface{}) { lost <- err }},
		Execution: ExecutionConfig{Timeout: -1},
	})
	before := waitersRunning()
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	err := c.Go(ctx, func(context.Context) error {
		cancel()
		<-release
		runtime.Goexit()
		return nil
	}, nil)
	require.ErrorIs(t, err, context.Canceled)
	close(release)
	waitForWaiters(t, before)
	require.Equal(t, errGoexit, <-lost)
}

// GoLostErrors receives errors and panics.  An abandoned call that eventually succeeds lost nothing, and a handler
// handed (nil, nil) typically dereferences err on a goroutine the user cannot recover.
func TestGoLateSuccessIsNotALostError(t *testing.T) {
	type lost struct {
		err    error
		panics interface{}
	}
	lostCh := make(chan lost, 8)
	c := NewCircuitFromConfig("lost", Config{
		General:   GeneralConfig{GoLostErrors: func(err error, panics interface{}) { lostCh <- lost{err, panics} }},
		Execution: ExecutionConfig{Timeout: -1},
	})
	before := waitersRunning()
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	err := c.Go(ctx, func(context.Context) error {
		cancel()
		<-release
		return nil
	}, func(context.Context, error) error {
		<-release
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	close(release)
	waitForWaiters(t, before)
	select {
	case l := <-lostCh:
		t.Fatalf("GoLostErrors(%v, %v) called although nothing was lost", l.err, l.panics)
	default:
	}
}

// Once Go has returned the circuit is idle, so SetConfigNotThreadSafe is allowed, but the abandoned call is still
// out there.  Its late error belongs to the handler that was configured when it was abandoned.
func TestGoLostErrorsSurvivesReconfigure(t *testing.T) {
	lost := make(chan struct{})
	c := NewCircuitFromConfig("reconf", Config{
		General:   GeneralConfig{GoLostErrors: func(error, interface{}) { close(lost) }},
		Execution: ExecutionConfig{Timeout: -1},
	})
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	err := c.Go(ctx, func(context.Context) error {
		cancel()
		<-release
		return errors.New("late")
	}, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, c.ConcurrentCommands())

	cfg := c.Config()
	cfg.General.GoLostErrors = nil
	c.SetConfigNotThreadSafe(cfg)
	close(release)
	regWait(t, lost, "the late error to be reported")
}

// With no GoLostErrors handler nobody receives an abandoned call's result: its goroutine must still be able to exit.
func TestGoAbandonedWithoutHandlerDoesNotLeak(t *testing.T) {
	c := NewCircuitFromConfig("abandoned", Config{Execution: ExecutionConfig{Timeout: -1}})
	before := waitersRunning()
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	err := c.Go(ctx, func(context.Context) error {
		cancel()
		<-release
		return errors.New("late run")
	}, func(context.Context, error) error {
		<-release
		return errors.New("late fallback")
	})
	require.ErrorIs(t, err, context.Canceled)
	close(release)
	waitForWaiters(t, before)
}
