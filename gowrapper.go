package circuit

import (
	"context"
	"errors"

	"github.com/cep21/circuit/v4/faststats"
)

// goroutineWrapper contains logic to wrap normal run methods inside a goroutine so they can end early
// if the goroutine continues to run
type goroutineWrapper struct {
	skipCatchPanics faststats.AtomicBoolean
	lostErrors      func(err error, panics interface{})
}

// errGoexit is the result of a wrapped function that neither returned nor panicked
var errGoexit = errors.New("function exited its goroutine without returning (runtime.Goexit)")

// goResult is how a wrapped function ended: it panicked if panics is not nil, and ended with err otherwise
type goResult struct {
	err    error
	panics interface{}
}

func (g *goroutineWrapper) run(runFunc func(context.Context) error) func(context.Context) error {
	if runFunc == nil {
		return nil
	}
	return func(ctx context.Context) error {
		catchPanics := !g.skipCatchPanics.Get()
		result := make(chan goResult, 1)
		go func() {
			r := goResult{err: errGoexit}
			defer sendResult(result, &r, catchPanics)
			r.err = runFunc(ctx)
		}()
		return g.wait(ctx, result)
	}
}

// fallback repeats run's body rather than adapting runFunc to it, since every adapter is another allocation
func (g *goroutineWrapper) fallback(runFunc func(context.Context, error) error) func(context.Context, error) error {
	if runFunc == nil {
		return nil
	}
	return func(ctx context.Context, err error) error {
		catchPanics := !g.skipCatchPanics.Get()
		result := make(chan goResult, 1)
		go func() {
			r := goResult{err: errGoexit}
			defer sendResult(result, &r, catchPanics)
			r.err = runFunc(ctx, err)
		}()
		return g.wait(ctx, result)
	}
}

// sendResult must be deferred directly (it recovers).  It always sends, however the wrapped function ended, so
// whoever is waiting for the result is never stranded.
func sendResult(result chan<- goResult, r *goResult, catchPanics bool) {
	if catchPanics {
		if panics := recover(); panics != nil {
			*r = goResult{panics: panics}
		}
	}
	result <- *r
}

func (g *goroutineWrapper) wait(ctx context.Context, result <-chan goResult) error {
	select {
	case <-ctx.Done():
		// result is a lost error.  Capture the handler now: the circuit may be reconfigured long before the abandoned
		// call finishes.
		if lostErrors := g.lostErrors; lostErrors != nil {
			go waitForErrors(lostErrors, result)
		}
		return ctx.Err()
	case r := <-result:
		if r.panics != nil {
			panic(r.panics)
		}
		return r.err
	}
}

func waitForErrors(lostErrors func(err error, panics interface{}), result <-chan goResult) {
	// A late success lost nothing
	if r := <-result; r.err != nil || r.panics != nil {
		lostErrors(r.err, r.panics)
	}
}
