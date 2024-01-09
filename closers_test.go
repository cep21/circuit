package circuit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNeverOpen(t *testing.T) {
	ctx := context.Background()
	c := neverOpensFactory()
	require.False(t, c.ShouldOpen(ctx, time.Now()))
	require.False(t, c.Prevent(ctx, time.Now()))
}

func TestNeverClose(t *testing.T) {
	ctx := context.Background()
	c := neverClosesFactory()
	require.False(t, c.Allow(ctx, time.Now()))
	require.False(t, c.ShouldClose(ctx, time.Now()))
}
