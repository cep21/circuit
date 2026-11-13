package circuit

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type errWaiter struct {
	err    error
	panics interface{}

	errChan   chan error
	panicChan chan interface{}
}

func (e *errWaiter) init() {
	e.errChan = make(chan error, 1)
	e.panicChan = make(chan interface{}, 1)
}

func (e *errWaiter) lostErrors(err error, panics interface{}) {
	if err == nil && panics == nil {
		panic("expect one")
	}
	if e.err != nil || e.panics != nil {
		panic("unexpected double set")
	}
	e.err = err
	e.panics = panics
	close(e.errChan)
	close(e.panicChan)
}

func Test_goroutineWrapper_waitForErrors(t *testing.T) {
	tests := []struct {
		lostCapture errWaiter
		name        string
		result      goResult
		expected    errWaiter
	}{
		{
			name:     "onFuncErr",
			result:   goResult{err: errors.New("bad")},
			expected: errWaiter{err: errors.New("bad")},
		},
		{
			name:     "onPanic",
			result:   goResult{panics: "bad panic"},
			expected: errWaiter{panics: "bad panic"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			tt.lostCapture.init()
			result := make(chan goResult)
			go func() { result <- tt.result }()
			waitForErrors(tt.lostCapture.lostErrors, result)
			// Reset these so deep equal works ...
			tt.lostCapture.panicChan = nil
			tt.lostCapture.errChan = nil
			// --- end reset
			if !reflect.DeepEqual(tt.expected, tt.lostCapture) {
				t.Errorf("goroutineWrapper.waitForErrors failure %v vs %v", tt.expected, tt.lostCapture)
			}
		})
	}
}

// noinspection GoNilness
func Test_goroutineWrapper_nil(t *testing.T) {
	var g goroutineWrapper
	if g.run(nil) != nil {
		t.Error("expect nil when given nil")
	}
	if g.fallback(nil) != nil {
		t.Error("expect nil when given nil on fallback")
	}
}

// noinspection GoNilness
func Test_goroutineWrapper_foreground(t *testing.T) {
	var g goroutineWrapper
	err := g.fallback(func(ctx context.Context, err error) error {
		return errors.New("bob")
	})(context.Background(), errors.New("ignore me"))
	if err.Error() != "bob" {
		t.Errorf("expected bob back, not %s", err.Error())
	}
}

func Test_goroutineWrapper_run(t *testing.T) {
	deadCtx, onEnd := context.WithCancel(context.Background())
	onEnd()
	type args struct {
		runFunc func(context.Context) error
	}
	tests := []struct {
		name        string
		lostCapture errWaiter
		ctx         context.Context
		args        args
		want        error
		expectPanic interface{}
	}{
		{
			name: "normal",
			args: args{
				runFunc: func(ctx context.Context) error {
					return nil
				},
			},
		},
		{
			name: "normal_error",
			args: args{
				runFunc: func(ctx context.Context) error {
					return errors.New("bad")
				},
			},
			want: errors.New("bad"),
		},
		{
			name: "timeout",
			args: args{
				runFunc: func(ctx context.Context) error {
					time.Sleep(time.Hour)
					return nil
				},
			},
			ctx:  deadCtx,
			want: context.Canceled,
		},
		{
			name: "panic",
			args: args{
				runFunc: func(ctx context.Context) error {
					panic("bob")
				},
			},
			expectPanic: "bob",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			g := &goroutineWrapper{
				lostErrors: tt.lostCapture.lostErrors,
			}
			f := g.run(tt.args.runFunc)
			if tt.expectPanic != nil {
				defer func() {
					if err := recover(); err != nil {
						if err != tt.expectPanic {
							panic(err)
						}
					} else {
						panic("i expect to panic!")
					}
				}()
			}
			if tt.ctx == nil {
				tt.ctx = context.Background()
			}
			if got := f(tt.ctx); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("goroutineWrapper.run() = %v, want %v", got, tt.want)
			}
		})
	}
}
