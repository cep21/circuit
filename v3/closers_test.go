package circuit

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestNeverOpen(t *testing.T) {
	c := neverOpensFactory()
	require.False(t, c.ShouldOpen(time.Now()))
	require.False(t, c.Prevent(time.Now()))
}

func TestNeverClose(t *testing.T) {
	c := neverClosesFactory()
	require.False(t, c.Allow(time.Now()))
	require.False(t, c.ShouldClose(time.Now()))
}