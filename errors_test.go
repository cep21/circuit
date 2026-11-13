package circuit

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSimpleBadRequest_NilErr(t *testing.T) {
	s := SimpleBadRequest{Err: nil}
	// Should not panic
	got := s.Error()
	if got != "bad request" {
		t.Errorf("expected 'bad request', got %q", got)
	}
}

func TestIsBadRequest(t *testing.T) {
	require.False(t, IsBadRequest(nil))
	require.False(t, IsBadRequest(errors.New("not bad")))
	require.False(t, IsBadRequest(errThrottledConcurrentCommands))
	require.False(t, IsBadRequest(errCircuitOpen))
	require.False(t, IsBadRequest(&circuitError{}))
	require.True(t, IsBadRequest(&SimpleBadRequest{}))
	wrappedErr := fmt.Errorf("wrapped: %w", &SimpleBadRequest{})
	require.True(t, IsBadRequest(wrappedErr))
	require.False(t, IsBadRequest(fmt.Errorf("wrapped: %w", errors.New("not bad"))))
}

func TestSimpleBadRequest_CauseUnwrap(t *testing.T) {
	inner := errors.New("inner")
	s := SimpleBadRequest{Err: inner}
	require.Same(t, inner, s.Cause())
	require.Same(t, inner, s.Unwrap())
	require.ErrorIs(t, s, inner)
	require.ErrorIs(t, fmt.Errorf("wrapped: %w", s), inner)
	require.Equal(t, "inner", s.Error())
}

// traceErr is embedded by the error shapes below.  Every method call is logged so two walks can be compared call by
// call, not just by their answer.
type traceErr struct {
	id  int
	log *[]string
}

func (e traceErr) Error() string { return "trace" }
func (e traceErr) trace(what string) {
	*e.log = append(*e.log, fmt.Sprintf("%d:%s", e.id, what))
}

type traceBad struct {
	traceErr
	bad bool
}

func (e traceBad) BadRequest() bool { e.trace("BadRequest"); return e.bad }

type traceWrap struct {
	traceErr
	child error
}

func (e traceWrap) Unwrap() error { e.trace("Unwrap"); return e.child }

type traceJoin struct {
	traceErr
	children []error
}

func (e traceJoin) Unwrap() []error { e.trace("Unwrap[]"); return e.children }

// notAnError is a BadRequest that only an As method can produce
type notAnError struct {
	bad bool
}

func (n notAnError) BadRequest() bool { return n.bad }

// asBehavior optionally writes to the target, whatever it then goes on to answer
type asBehavior struct {
	traceErr
	set    BadRequest
	answer bool
}

func (e asBehavior) As(target interface{}) bool {
	br := target.(*BadRequest)
	e.trace(fmt.Sprintf("As(%v)", *br))
	if e.set != nil {
		*br = e.set
	}
	return e.answer
}

type traceAsWrap struct {
	asBehavior
	child error
}

func (e traceAsWrap) Unwrap() error { e.trace("Unwrap"); return e.child }

type traceAsJoin struct {
	asBehavior
	children []error
}

func (e traceAsJoin) Unwrap() []error { e.trace("Unwrap[]"); return e.children }

type traceBadWrap struct {
	traceBad
	child error
}

func (e traceBadWrap) Unwrap() error { e.trace("Unwrap"); return e.child }

type nilSafePointerBad struct{}

func (*nilSafePointerBad) Error() string    { return "typed nil" }
func (*nilSafePointerBad) BadRequest() bool { return true }

func randomErrorTree(r *rand.Rand, log *[]string, depth int) error {
	base := traceErr{id: r.Intn(1000), log: log}
	child := func() error {
		if depth == 0 || r.Intn(8) == 0 {
			return nil
		}
		return randomErrorTree(r, log, depth-1)
	}
	children := func() []error {
		ret := make([]error, r.Intn(4))
		for i := range ret {
			ret[i] = child()
		}
		return ret
	}
	as := asBehavior{traceErr: base, answer: r.Intn(4) == 0}
	if r.Intn(3) != 0 {
		as.set = notAnError{bad: r.Intn(2) == 0}
	}
	switch r.Intn(16) {
	case 0:
		return base
	case 1:
		return traceBad{traceErr: base, bad: r.Intn(2) == 0}
	case 2:
		return as
	case 3:
		return traceBadWrap{traceBad: traceBad{traceErr: base, bad: r.Intn(2) == 0}, child: child()}
	case 4:
		return errCircuitOpen
	case 5:
		return (*nilSafePointerBad)(nil)
	case 6:
		return SimpleBadRequest{Err: child()}
	case 7:
		return fmt.Errorf("one: %w", child())
	case 8:
		return fmt.Errorf("two: %w %w", child(), child())
	case 9:
		return errors.Join(children()...)
	case 10, 11:
		return traceAsWrap{asBehavior: as, child: child()}
	case 12:
		return traceAsJoin{asBehavior: as, children: children()}
	case 13:
		return traceJoin{traceErr: base, children: children()}
	default:
		return traceWrap{traceErr: base, child: child()}
	}
}

func TestIsBadRequest_MatchesErrorsAs(t *testing.T) {
	viaErrorsAs := func(err error) bool {
		var br BadRequest
		return errors.As(err, &br) && br.BadRequest()
	}
	observe := func(log *[]string, f func(error) bool, err error) (answer bool, panicked bool, calls []string) {
		*log = nil
		defer func() {
			// An As method that answers true without setting the target leaves a nil BadRequest to call
			panicked = recover() != nil
			calls = *log
		}()
		return f(err), false, nil
	}
	r := rand.New(rand.NewSource(1))
	outcomes := map[string]int{}
	for i := 0; i < 50000; i++ {
		var log []string
		err := randomErrorTree(r, &log, 5)
		wantAnswer, wantPanic, wantCalls := observe(&log, viaErrorsAs, err)
		gotAnswer, gotPanic, gotCalls := observe(&log, IsBadRequest, err)
		require.Equal(t, wantAnswer, gotAnswer, "tree %d", i)
		require.Equal(t, wantPanic, gotPanic, "tree %d", i)
		require.Equal(t, wantCalls, gotCalls, "tree %d", i)
		outcomes[fmt.Sprint(wantAnswer, wantPanic)]++
	}
	require.Len(t, outcomes, 3, "expected true, false and panicking trees: %v", outcomes)
}

func TestIsBadRequest_WrappedDoesNotAllocate(t *testing.T) {
	err := fmt.Errorf("outer: %w", errors.Join(errors.New("plain"), SimpleBadRequest{}))
	require.Zero(t, testing.AllocsPerRun(100, func() {
		if !IsBadRequest(err) {
			t.Error("expected a bad request")
		}
	}))
}

func TestExecute_FallsBackExactlyWhenNotABadRequest(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 5000; i++ {
		var log []string
		err := randomErrorTree(r, &log, 3)
		var br BadRequest
		if err == nil || (errors.As(err, &br) && br == nil) {
			continue
		}
		wantBad := IsBadRequest(err)
		// Failure, interrupt and timeout all have to reach the same answer
		for _, tc := range []struct {
			ctx   context.Context
			takes time.Duration
		}{{context.Background(), 0}, {canceled, 0}, {context.Background(), 2 * time.Hour}} {
			tr := newTracedCircuit(Config{Execution: ExecutionConfig{Timeout: time.Hour}})
			fellBack := false
			got := tr.c.Execute(tc.ctx, func(context.Context) error {
				tr.elapsed += tc.takes
				return err
			}, func(context.Context, error) error {
				fellBack = true
				return nil
			})
			require.Equal(t, !wantBad, fellBack, "tree %d", i)
			require.Equal(t, wantBad, got != nil, "tree %d", i)
			require.Equal(t, wantBad, strings.HasPrefix(tr.events[2], "run.BadRequest"), "tree %d", i)
		}
	}
}
