package circuit

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// tracer records everything a circuit does that user code can see, in order: clock reads and metric events, with
// the times, durations and concurrency counts they were given.
type tracer struct {
	c       *Circuit
	epoch   time.Time
	elapsed time.Duration
	events  []string
	panicOn string
}

func (r *tracer) logf(format string, args ...interface{}) {
	r.events = append(r.events, fmt.Sprintf(format, args...))
}

// now advances one second per read
func (r *tracer) now() time.Time {
	r.elapsed += time.Second
	r.logf("now")
	return r.epoch.Add(r.elapsed)
}

type tracedMetrics struct {
	*tracer
	prefix string
}

func (m tracedMetrics) event(ctx context.Context, name string, now time.Time, d time.Duration) {
	name = m.prefix + name
	m.logf("%s(at=%s took=%s ctx=%v running=%d/%d)", name, now.Sub(m.epoch), d, ctx.Err(), m.c.ConcurrentCommands(), m.c.ConcurrentFallbacks())
	if m.panicOn == name {
		panic(name)
	}
}

func (m tracedMetrics) Success(ctx context.Context, now time.Time, d time.Duration) {
	m.event(ctx, "Success", now, d)
}
func (m tracedMetrics) ErrFailure(ctx context.Context, now time.Time, d time.Duration) {
	m.event(ctx, "Failure", now, d)
}
func (m tracedMetrics) ErrTimeout(ctx context.Context, now time.Time, d time.Duration) {
	m.event(ctx, "Timeout", now, d)
}
func (m tracedMetrics) ErrBadRequest(ctx context.Context, now time.Time, d time.Duration) {
	m.event(ctx, "BadRequest", now, d)
}
func (m tracedMetrics) ErrInterrupt(ctx context.Context, now time.Time, d time.Duration) {
	m.event(ctx, "Interrupt", now, d)
}
func (m tracedMetrics) ErrConcurrencyLimitReject(ctx context.Context, now time.Time) {
	m.event(ctx, "Reject", now, 0)
}
func (m tracedMetrics) ErrShortCircuit(ctx context.Context, now time.Time) {
	m.event(ctx, "ShortCircuit", now, 0)
}

func newTracedCircuit(cfg Config) *tracer {
	r := &tracer{epoch: time.Now()}
	cfg.General.TimeKeeper.Now = r.now
	cfg.Metrics.Run = []RunMetrics{tracedMetrics{r, "run."}}
	cfg.Metrics.Fallback = []FallbackMetrics{tracedMetrics{r, "fallback."}}
	r.c = NewCircuitFromConfig("traced", cfg)
	return r
}

// execute runs on its own goroutine so that runFunc may call runtime.Goexit
func (r *tracer) execute(ctx context.Context, run func(context.Context) error, fallback func(context.Context, error) error) string {
	var seen context.Context
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil {
				r.logf("panic(%v)", p)
			}
		}()
		err := r.c.Execute(ctx, func(ctx context.Context) error {
			seen = ctx
			return run(ctx)
		}, fallback)
		r.logf("return(%v)", err)
	}()
	<-done
	if seen != nil {
		r.logf("runCtx=%v", seen.Err())
	}
	r.logf("running=%d/%d", r.c.ConcurrentCommands(), r.c.ConcurrentFallbacks())
	return strings.Join(r.events, " ")
}

func TestExecuteTrace(t *testing.T) {
	const slow = 10 * time.Hour
	errRun := errors.New("run")
	errBad := fmt.Errorf("wrapped: %w", SimpleBadRequest{Err: errors.New("bad")})
	fallbackOK := func(context.Context, error) error { return nil }
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	never := func(error) bool { return false }
	for _, tc := range []struct {
		name     string
		cfg      Config
		ctx      context.Context
		takes    time.Duration
		ret      error
		do       func()
		fallback func(context.Context, error) error
		setup    func(r *tracer)
		want     string
	}{
		{name: "success", fallback: fallbackOK,
			want: "now now run.Success(at=2s took=1s ctx=<nil> running=1/0) return(<nil>) runCtx=context canceled running=0/0"},
		{name: "success without timeout", cfg: Config{Execution: ExecutionConfig{Timeout: -1}}, takes: slow,
			want: "now now run.Success(at=10h0m2s took=10h0m1s ctx=<nil> running=1/0) return(<nil>) runCtx=<nil> running=0/0"},
		{name: "success with canceled parent", ctx: canceled,
			want: "now now run.Success(at=2s took=1s ctx=context canceled running=1/0) return(<nil>) runCtx=context canceled running=0/0"},
		{name: "failure", ret: errRun,
			want: "now now run.Failure(at=2s took=1s ctx=<nil> running=1/0) return(run) runCtx=context canceled running=0/0"},
		{name: "failure with fallback", ret: errRun, fallback: fallbackOK,
			want: "now now run.Failure(at=2s took=1s ctx=<nil> running=1/0) now now fallback.Success(at=3s took=1s ctx=<nil> running=0/1) return(<nil>) runCtx=context canceled running=0/0"},
		{name: "failure with failing fallback", ret: errRun, fallback: func(_ context.Context, err error) error { return fmt.Errorf("fallback of %w", err) },
			want: "now now run.Failure(at=2s took=1s ctx=<nil> running=1/0) now now fallback.Failure(at=3s took=1s ctx=<nil> running=0/1) return(fallback of run) runCtx=context canceled running=0/0"},
		{name: "failure with disabled fallback", cfg: Config{Fallback: FallbackConfig{Disabled: true}}, ret: errRun, fallback: fallbackOK,
			want: "now now run.Failure(at=2s took=1s ctx=<nil> running=1/0) return(run) runCtx=context canceled running=0/0"},
		{name: "failure with throttled fallback", ret: errRun, fallback: fallbackOK, setup: func(r *tracer) { r.c.concurrentFallbacks.Add(10) },
			want: "now now run.Failure(at=2s took=1s ctx=<nil> running=1/10) now fallback.Reject(at=3s took=0s ctx=<nil> running=0/10) return(" + errThrottledConcurrentFallbacks.Error() + ") runCtx=context canceled running=0/10"},
		{name: "bad request skips fallback", ret: errBad, fallback: fallbackOK,
			want: "now now run.BadRequest(at=2s took=1s ctx=<nil> running=1/0) return(wrapped: bad) runCtx=context canceled running=0/0"},
		{name: "bad request beats timeout and interrupt", ctx: canceled, ret: errBad, takes: slow, fallback: fallbackOK,
			want: "now now run.BadRequest(at=10h0m2s took=10h0m1s ctx=context canceled running=1/0) return(wrapped: bad) runCtx=context canceled running=0/0"},
		{name: "timeout with nil result", takes: slow, fallback: fallbackOK,
			want: "now now run.Timeout(at=10h0m2s took=10h0m1s ctx=<nil> running=1/0) return(<nil>) runCtx=context canceled running=0/0"},
		{name: "timeout beats interrupt", ctx: canceled, takes: slow, ret: errRun,
			want: "now now run.Timeout(at=10h0m2s took=10h0m1s ctx=context canceled running=1/0) return(run) runCtx=context canceled running=0/0"},
		{name: "interrupt", ctx: canceled, ret: errRun, fallback: fallbackOK,
			want: "now now run.Interrupt(at=2s took=1s ctx=context canceled running=1/0) now now fallback.Success(at=3s took=1s ctx=context canceled running=0/1) return(<nil>) runCtx=context canceled running=0/0"},
		{name: "interrupt ignored", cfg: Config{Execution: ExecutionConfig{IgnoreInterrupts: true}}, ctx: canceled, ret: errRun,
			want: "now now run.Failure(at=2s took=1s ctx=context canceled running=1/0) return(run) runCtx=context canceled running=0/0"},
		{name: "interrupt declined", cfg: Config{Execution: ExecutionConfig{IsErrInterrupt: never}}, ctx: canceled, ret: errRun,
			want: "now now run.Failure(at=2s took=1s ctx=context canceled running=1/0) return(run) runCtx=context canceled running=0/0"},
		{name: "open", cfg: Config{General: GeneralConfig{ForceOpen: true}},
			want: "now run.ShortCircuit(at=1s took=0s ctx=<nil> running=0/0) return(" + errCircuitOpen.Error() + ") running=0/0"},
		{name: "open with fallback", cfg: Config{General: GeneralConfig{ForceOpen: true}}, fallback: fallbackOK,
			want: "now run.ShortCircuit(at=1s took=0s ctx=<nil> running=0/0) now now fallback.Success(at=2s took=1s ctx=<nil> running=0/1) return(<nil>) running=0/0"},
		{name: "throttled", setup: func(r *tracer) { r.c.concurrentCommands.Add(10) },
			want: "now run.Reject(at=1s took=0s ctx=<nil> running=10/0) return(" + errThrottledConcurrentCommands.Error() + ") running=10/0"},
		{name: "throttled and collector panics", setup: func(r *tracer) { r.c.concurrentCommands.Add(10); r.panicOn = "run.Reject" },
			want: "now run.Reject(at=1s took=0s ctx=<nil> running=10/0) panic(run.Reject) running=10/0"},
		{name: "run panics", do: func() { panic("boom") }, fallback: fallbackOK,
			want: "now panic(boom) runCtx=context canceled running=0/0"},
		{name: "run panics without timeout", cfg: Config{Execution: ExecutionConfig{Timeout: -1}}, do: func() { panic("boom") },
			want: "now panic(boom) runCtx=<nil> running=0/0"},
		{name: "run exits its goroutine", do: runtime.Goexit, fallback: fallbackOK,
			want: "now runCtx=context canceled running=0/0"},
		{name: "collector panics", setup: func(r *tracer) { r.panicOn = "run.Success" },
			want: "now now run.Success(at=2s took=1s ctx=<nil> running=1/0) panic(run.Success) runCtx=context canceled running=0/0"},
		{name: "fallback panics", ret: errRun, fallback: func(context.Context, error) error { panic("boom") },
			want: "now now run.Failure(at=2s took=1s ctx=<nil> running=1/0) now panic(boom) runCtx=context canceled running=0/0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Merge(Config{Execution: ExecutionConfig{Timeout: time.Hour}})
			r := newTracedCircuit(cfg)
			if tc.setup != nil {
				tc.setup(r)
			}
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			got := r.execute(ctx, func(context.Context) error {
				r.elapsed += tc.takes
				if tc.do != nil {
					tc.do()
				}
				return tc.ret
			}, tc.fallback)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestInterruptClassificationFollowsLiveConfig(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	defer cancel()
	errRun := errors.New("run")

	r := newTracedCircuit(Config{Execution: ExecutionConfig{Timeout: time.Hour}})
	answers := func(answer bool) func(error) bool {
		return func(err error) bool {
			r.logf("IsErrInterrupt(%v)", err)
			return answer
		}
	}
	const unset, accepts, declines = "unset", "accepts", "declines"
	// One circuit is reconfigured through every combination, so each setting is both set and cleared while live
	for _, reconfigure := range []func(Config){r.c.SetConfigThreadSafe, r.c.SetConfigNotThreadSafe} {
		for _, ignore := range []bool{false, true, false} {
			for _, callback := range []string{unset, declines, unset, accepts, declines} {
				cfg := r.c.Config()
				cfg.Execution.IgnoreInterrupts = ignore
				cfg.Execution.IsErrInterrupt = nil
				if callback != unset {
					cfg.Execution.IsErrInterrupt = answers(callback == accepts)
				}
				reconfigure(cfg)
				for _, parent := range []context.Context{context.Background(), canceled, expired} {
					for _, ret := range []error{nil, errRun} {
						var want []string
						eligible := ret != nil && parent.Err() != nil && !ignore
						if eligible && callback != unset {
							want = append(want, fmt.Sprintf("IsErrInterrupt(%v)", parent.Err()))
						}
						switch {
						case ret == nil:
							want = append(want, "run.Success")
						case eligible && callback != declines:
							want = append(want, "run.Interrupt")
						default:
							want = append(want, "run.Failure")
						}

						r.events = nil
						require.Equal(t, ret, r.c.Run(parent, func(context.Context) error { return ret }))
						var got []string
						for _, e := range r.events {
							if e != "now" {
								got = append(got, strings.Split(e, "(at=")[0])
							}
						}
						require.Equal(t, want, got, "ignore=%t callback=%s parent=%v ret=%v", ignore, callback, parent.Err(), ret)
					}
				}
			}
		}
	}
}
