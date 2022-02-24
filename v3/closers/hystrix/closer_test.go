package hystrix

import (
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
	c := Closer{}
	wg := sync.WaitGroup{}
	// None of these should panic
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.ErrBadRequest(time.Now(), time.Second)
			c.ErrInterrupt(time.Now(), time.Second)
			c.ErrConcurrencyLimitReject(time.Now())
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
	now := time.Now()

	c := Closer{}
	c.SetConfigNotThreadSafe(ConfigureCloser{
		RequiredConcurrentSuccessful: 3,
	})
	c.Opened(now)
	assertBool(t, !c.ShouldClose(now), "Expected the circuit to not yet close")
	c.Success(now, time.Second)
	assertBool(t, !c.ShouldClose(now), "Expected the circuit to not yet close")
	c.Success(now, time.Second)
	assertBool(t, !c.ShouldClose(now), "Expected the circuit to not yet close")
	c.Success(now, time.Second)
	assertBool(t, c.ShouldClose(now), "Expected the circuit to now close")

	// None of these should matter
	c.ErrBadRequest(now, time.Second)
	c.ErrInterrupt(now, time.Second)
	c.ErrConcurrencyLimitReject(now)
	assertBool(t, c.ShouldClose(now), "Expected the circuit to now close")

	c.ErrTimeout(now, time.Second)
	// Should reset closer
	assertBool(t, !c.ShouldClose(now), "Expected the circuit to not yet close")
}

func TestCloser_UsesAfterFunc(t *testing.T) {
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
	c.Opened(now)
	c.Success(now, time.Second)
	c.Success(now, time.Second)
	c.Success(now, time.Second)
	c.Success(now, time.Second)

	if invocations == 0 {
		t.Error("Expected mock AfterFunc to be used")
	}
}
