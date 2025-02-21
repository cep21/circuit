package simplelogic

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cep21/circuit/v4"
)

func TestConsecutiveErrOpenerFactory(t *testing.T) {
	f := ConsecutiveErrOpenerFactory(ConfigConsecutiveErrOpener{
		ErrorThreshold: 5,
	})
	opener := f().(*ConsecutiveErrOpener)
	if opener.closeThreshold.Get() != 5 {
		t.Errorf("Expected threshold to be 5, got %d", opener.closeThreshold.Get())
	}

	// Test default config
	f = ConsecutiveErrOpenerFactory(ConfigConsecutiveErrOpener{})
	opener = f().(*ConsecutiveErrOpener)
	if opener.closeThreshold.Get() != 10 {
		t.Errorf("Expected default threshold to be 10, got %d", opener.closeThreshold.Get())
	}
}

func TestConsecutiveErrOpener_Merge(t *testing.T) {
	c := &ConfigConsecutiveErrOpener{}
	c.Merge(ConfigConsecutiveErrOpener{
		ErrorThreshold: 15,
	})
	if c.ErrorThreshold != 15 {
		t.Errorf("Expected threshold to be 15, got %d", c.ErrorThreshold)
	}

	// Don't override if already set
	c = &ConfigConsecutiveErrOpener{
		ErrorThreshold: 5,
	}
	c.Merge(ConfigConsecutiveErrOpener{
		ErrorThreshold: 15,
	})
	if c.ErrorThreshold != 5 {
		t.Errorf("Expected threshold to remain 5, got %d", c.ErrorThreshold)
	}
}

func TestConsecutiveErrOpener_ShouldOpen(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	opener := &ConsecutiveErrOpener{}
	opener.SetConfigThreadSafe(ConfigConsecutiveErrOpener{
		ErrorThreshold: 3,
	})

	// Initially should not open
	if opener.ShouldOpen(ctx, now) {
		t.Error("Circuit should not open initially")
	}

	// Add errors and check when it should open
	opener.ErrFailure(ctx, now, time.Second)
	if opener.ShouldOpen(ctx, now) {
		t.Error("Circuit should not open after 1 error")
	}

	opener.ErrTimeout(ctx, now, time.Second)
	if opener.ShouldOpen(ctx, now) {
		t.Error("Circuit should not open after 2 errors")
	}

	opener.ErrFailure(ctx, now, time.Second)
	if !opener.ShouldOpen(ctx, now) {
		t.Error("Circuit should open after 3 errors")
	}

	// Reset on success
	opener.Success(ctx, now, time.Second)
	if opener.ShouldOpen(ctx, now) {
		t.Error("Circuit should not open after success resets counter")
	}

	// Reset when closed
	opener.ErrFailure(ctx, now, time.Second)
	opener.ErrFailure(ctx, now, time.Second)
	opener.Closed(ctx, now)
	if opener.ShouldOpen(ctx, now) {
		t.Error("Circuit should not open after closed resets counter")
	}

	// Reset when opened
	opener.ErrFailure(ctx, now, time.Second)
	opener.ErrFailure(ctx, now, time.Second)
	opener.ErrFailure(ctx, now, time.Second)
	if !opener.ShouldOpen(ctx, now) {
		t.Error("Circuit should open after 3 errors")
	}
	opener.Opened(ctx, now)
	if opener.ShouldOpen(ctx, now) {
		t.Error("Circuit should not open after opened resets counter")
	}
}

func TestConsecutiveErrOpener_Config(t *testing.T) {
	opener := &ConsecutiveErrOpener{}

	// Test thread-safe config
	opener.SetConfigThreadSafe(ConfigConsecutiveErrOpener{
		ErrorThreshold: 7,
	})
	if opener.closeThreshold.Get() != 7 {
		t.Errorf("Expected threshold to be 7, got %d", opener.closeThreshold.Get())
	}

	// Test non-thread-safe config
	opener.SetConfigNotThreadSafe(ConfigConsecutiveErrOpener{
		ErrorThreshold: 9,
	})
	if opener.closeThreshold.Get() != 9 {
		t.Errorf("Expected threshold to be 9, got %d", opener.closeThreshold.Get())
	}
}

func TestConsecutiveErrOpener_OtherMethods(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	opener := &ConsecutiveErrOpener{}

	// Test methods that don't affect counts
	if opener.Prevent(ctx, now) {
		t.Error("Prevent should always return false")
	}

	// These should not affect the counter
	opener.ErrBadRequest(ctx, now, time.Second)
	opener.ErrInterrupt(ctx, now, time.Second)
	opener.ErrConcurrencyLimitReject(ctx, now)
	opener.ErrShortCircuit(ctx, now)

	// These should not increment the counter
	if opener.consecutiveCount.Get() != 0 {
		t.Error("Methods should not have incremented error counter")
	}
}

func TestConsecutiveErrOpener_CircuitIntegration(t *testing.T) {
	configuration := circuit.Config{
		General: circuit.GeneralConfig{
			OpenToClosedFactory: nil, // Use default
			ClosedToOpenFactory: ConsecutiveErrOpenerFactory(ConfigConsecutiveErrOpener{
				ErrorThreshold: 2,
			}),
		},
	}

	h := circuit.Manager{}
	c := h.MustCreateCircuit("SimpleLogic", configuration)

	// Circuit should start closed
	if c.IsOpen() {
		t.Error("Circuit should start in a closed state")
	}

	// Bad requests shouldn't count towards failures
	err := c.Execute(context.Background(), func(_ context.Context) error {
		return circuit.SimpleBadRequest{} // Bad requests don't count
	}, nil)
	if err == nil {
		t.Error("Expected an error from bad request")
	}
	if c.IsOpen() {
		t.Error("Circuit should remain closed after a bad request")
	}

	// Two failures should open the circuit
	for i := 0; i < 2; i++ {
		err = c.Execute(context.Background(), func(_ context.Context) error {
			return fmt.Errorf("failure")
		}, nil)
		if err == nil {
			t.Error("Expected an error from failure")
		}
	}

	// Circuit should now be open
	if !c.IsOpen() {
		t.Error("Circuit should be open after threshold failures")
	}
}
