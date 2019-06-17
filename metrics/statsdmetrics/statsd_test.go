package statsdmetrics

import (
	"testing"
	"time"
)

type nilStats struct {
}

func (n *nilStats) Inc(stat string, val int64, sampleRate float32) error {
	return nil
}

func (n *nilStats) Gauge(stat string, val int64, sampleRate float32) error {
	return nil
}

func (n *nilStats) TimingDuration(stat string, val time.Duration, sampleRate float32) error {
	return nil
}

var _ StatSender = &nilStats{}

func TestCommandFactory_config(t *testing.T) {
	x := CommandFactory{
		StatSender: &nilStats{},
		SampleRate: .5,
		SanitizeStatsdFunction: func(_ string) string {
			return "_bob_"
		},
	}
	if sr := x.sampleRate(); sr != .5 {
		t.Fatalf("expected .5 sample rate, not %f", sr)
	}
	if s := x.sanitizeStatsdFunction()(""); s != "_bob_" {
		t.Fatalf("expected _bob_, not %s", s)
	}
}

func TestSanitizeStatsd(t *testing.T) {
	if s := sanitizeStatsd("aA0"); s != "aA0" {
		t.Fatalf("expect aA0 back")
	}
	if s := sanitizeStatsd("abc.123.*&#"); s != "abc_123____" {
		t.Fatalf("expect abc_123____ back, got %s", s)
	}
}

func TestAppendStatsdParts(t *testing.T) {
	if x := appendStatsdParts(sanitizeStatsd, "hello", "", "world"); x != "hello.world" {
		t.Fatalf("expect hello.world, got %s", x)
	}
}
