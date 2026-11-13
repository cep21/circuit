//go:build go1.25

package circuit

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// newBubbledTracer traces a circuit that keeps the default clock, which synctest makes exact
func newBubbledTracer(cfg Config) *tracer {
	r := newTracedCircuit(cfg)
	cfg = r.c.Config()
	cfg.General.TimeKeeper.Now = time.Now
	r.c.SetConfigNotThreadSafe(cfg)
	return r
}

func TestSynctest_DefaultClockTimesAreExact(t *testing.T) {
	// Built outside the bubble, like a package level circuit would be
	outside := newBubbledTracer(Config{})
	synctest.Test(t, func(t *testing.T) {
		for _, r := range []*tracer{newBubbledTracer(Config{}), outside} {
			for _, call := range []func(context.Context, func(context.Context) error, func(context.Context, error) error) error{r.c.Execute, r.c.Go} {
				for _, fallbackErr := range []error{nil, errors.New("fallback")} {
					r.epoch = time.Now()
					r.events = nil
					err := call(context.Background(), func(context.Context) error {
						time.Sleep(100 * time.Millisecond)
						return errors.New("run")
					}, func(context.Context, error) error {
						time.Sleep(time.Minute)
						return fallbackErr
					})
					require.Equal(t, fallbackErr, err)
					event := "Success"
					if fallbackErr != nil {
						event = "Failure"
					}
					require.Equal(t, "run.Failure(at=100ms took=100ms ctx=<nil> running=1/0) "+
						"fallback."+event+"(at=100ms took=1m0s ctx=<nil> running=0/1)", strings.Join(r.events, " "))
				}
			}
		}
	})
}

func TestSynctest_GoOutcomes(t *testing.T) {
	errFunc := errors.New("func")
	const nilPanic = "PanicNilError"
	for _, tc := range []struct {
		name   string
		end    func() error
		onTime string
		lost   string
	}{
		{"returns nil", func() error { return nil }, "return(<nil>)", ""},
		{"returns error", func() error { return errFunc }, "return(func)", "lost(func, <nil>)"},
		{"panics", func() error { panic("boom") }, "panic(boom)", "lost(<nil>, boom)"},
		{"panics with error", func() error { panic(errFunc) }, "panic(func)", "lost(<nil>, func)"},
		{"panics with nil", func() error { panic(error(nil)) }, "panic(PanicNilError)", "lost(<nil>, PanicNilError)"},
	} {
		for _, mode := range []struct{ late, inFallback bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
			want := tc.onTime
			if mode.late {
				want = strings.TrimSpace("return(context canceled) " + tc.lost)
			}
			t.Run(fmt.Sprintf("%s/%+v", tc.name, mode), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					var events []string
					var mu sync.Mutex
					logf := func(format string, args ...interface{}) {
						mu.Lock()
						defer mu.Unlock()
						for i, arg := range args {
							if _, ok := arg.(*runtime.PanicNilError); ok {
								args[i] = nilPanic
							}
						}
						events = append(events, fmt.Sprintf(format, args...))
					}
					c := NewCircuitFromConfig("go", Config{General: GeneralConfig{
						GoLostErrors: func(err error, panics interface{}) { logf("lost(%v, %v)", err, panics) },
					}})
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					body := func() error {
						if mode.late {
							cancel()
							time.Sleep(time.Minute)
						}
						return tc.end()
					}
					func() {
						defer func() {
							if p := recover(); p != nil {
								logf("panic(%v)", p)
							}
						}()
						if mode.inFallback {
							logf("return(%v)", c.Go(ctx, benchFails, func(fallbackCtx context.Context, err error) error {
								if fallbackCtx != ctx || err != errBenchFailure {
									return fmt.Errorf("unexpected fallback arguments %v %v", fallbackCtx, err)
								}
								return body()
							}))
						} else {
							logf("return(%v)", c.Go(ctx, func(context.Context) error { return body() }, nil))
						}
					}()
					time.Sleep(time.Hour)
					synctest.Wait()
					require.Equal(t, want, strings.Join(events, " "))
					require.Zero(t, c.ConcurrentCommands())
					require.Zero(t, c.ConcurrentFallbacks())
				})
			})
		}
	}
}

// When the context and runFunc end together either may win, but the result is always delivered exactly once
func TestSynctest_GoResultDeliveredOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const calls = 2000
		var returned, lost atomic.Int64
		c := NewCircuitFromConfig("go", Config{General: GeneralConfig{
			GoLostErrors: func(error, interface{}) { lost.Add(1) },
		}})
		for range calls {
			ctx, cancel := context.WithCancel(context.Background())
			err := c.Go(ctx, func(context.Context) error {
				cancel()
				return errBenchFailure
			}, nil)
			if err == errBenchFailure {
				returned.Add(1)
			} else {
				require.Equal(t, context.Canceled, err)
			}
		}
		synctest.Wait()
		require.EqualValues(t, calls, returned.Load()+lost.Load())
	})
}
