package circuit

import (
	"errors"
	"fmt"
	"testing"

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
