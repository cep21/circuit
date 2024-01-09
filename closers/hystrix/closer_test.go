package hystrix

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCloser_MarshalJSON(t *testing.T) {
	c := Closer{
		config: ConfigureCloser{
			HalfOpenAttempts: 12345,
		},
	}
	asJSON, err := c.MarshalJSON()
	if err != nil {
		t.Fatal("unexpected error marshalling JSON")
	}
	if !strings.Contains(string(asJSON), "12345") {
		t.Fatal("Expect JSON to contain 12345")
	}
}

func TestCloser_NoPanics(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatal("Expected no panic")
		}
	}()
	ctx := context.Background()
	c := Closer{}
	wg := sync.WaitGroup{}
	// None of these should panic
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.ErrBadRequest(ctx, time.Now(), time.Second)
			c.ErrInterrupt(ctx, time.Now(), time.Second)
			c.ErrConcurrencyLimitReject(ctx, time.Now())
		}()
	}
	wg.Wait()
}

func assertBool(t *testing.T, b bool, msg string) {
	if !b {
		t.Fatal(msg)
	}
}

func TestCloser_ConcurrentAttempts(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	c := Closer{}
	c.SetConfigNotThreadSafe(ConfigureCloser{
		RequiredConcurrentSuccessful: 3,
	})
	c.Opened(ctx, now)
	assertBool(t, !c.ShouldClose(ctx, now), "Expected the circuit to not yet close")
	c.Success(ctx, now, time.Second)
	assertBool(t, !c.ShouldClose(ctx, now), "Expected the circuit to not yet close")
	c.Success(ctx, now, time.Second)
	assertBool(t, !c.ShouldClose(ctx, now), "Expected the circuit to not yet close")
	c.Success(ctx, now, time.Second)
	assertBool(t, c.ShouldClose(ctx, now), "Expected the circuit to now close")

	// None of these should matter
	c.ErrBadRequest(ctx, now, time.Second)
	c.ErrInterrupt(ctx, now, time.Second)
	c.ErrConcurrencyLimitReject(ctx, now)
	assertBool(t, c.ShouldClose(ctx, now), "Expected the circuit to now close")

	c.ErrTimeout(ctx, now, time.Second)
	// Should reset closer
	assertBool(t, !c.ShouldClose(ctx, now), "Expected the circuit to not yet close")
}

func TestCloser_AfterFunc(t *testing.T) {
	ctx := context.Background()
	t.Run("afterfunc is used", func(t *testing.T) {
		var invocations int
		c := Closer{}
		c.SetConfigNotThreadSafe(ConfigureCloser{
			AfterFunc: func(d time.Duration, f func()) *time.Timer {
				invocations++
				return time.AfterFunc(d, f)
			},
			RequiredConcurrentSuccessful: 3,
		})

		now := time.Now()
		c.Opened(ctx, now)
		c.Success(ctx, now, time.Second)
		c.Success(ctx, now, time.Second)
		c.Success(ctx, now, time.Second)
		c.Success(ctx, now, time.Second)

		if invocations == 0 {
			t.Error("Expected mock AfterFunc to be used")
		}
		t.Log("invocations: ", invocations)
	})
	t.Run("afterfunc is set if previously nil", func(t *testing.T) {
		var (
			countD int
			c      = ConfigureCloser{AfterFunc: nil}
			d      = ConfigureCloser{AfterFunc: func(d time.Duration, f func()) *time.Timer {
				countD++
				return time.AfterFunc(d+1, f)
			}}
		)
		c.Merge(d)
		_ = c.AfterFunc(time.Second, func() {})

		if countD != 1 {
			t.Errorf("expected merge to assign newer AfterFunc")
		}
	})
	t.Run("afterfunc is not merged if already set", func(t *testing.T) {
		var (
			countC, countD int

			c = ConfigureCloser{AfterFunc: func(d time.Duration, f func()) *time.Timer {
				countC++
				return time.AfterFunc(d, f)
			}}
			d = ConfigureCloser{AfterFunc: func(d time.Duration, f func()) *time.Timer {
				countD++
				return time.AfterFunc(d+1, f)
			}}
		)
		c.Merge(d)
		_ = c.AfterFunc(time.Second, func() {})

		if countD > 0 {
			t.Errorf("expected merge to maintain an already set AfterFunc")
		}
		if countC != 1 {
			t.Errorf("expected post-merge to invoke initially set AfterFunc")
		}
	})
}
