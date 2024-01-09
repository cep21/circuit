package hystrix

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOpener_MarshalJSON(t *testing.T) {
	ctx := context.Background()
	o := Opener{}
	_, err := o.MarshalJSON()
	if err != nil {
		t.Fatal("expect no error doing initial marshal")
	}
	// 3 failures should exist in the output
	o.ErrFailure(ctx, time.Now(), time.Second)
	o.ErrFailure(ctx, time.Now(), time.Second)
	o.ErrFailure(ctx, time.Now(), time.Second)
	b, err := o.MarshalJSON()
	if err != nil {
		t.Fatal("expect no error doing marshal")
	}
	if !strings.Contains(string(b), "3") {
		t.Fatal("expect a 3 back")
	}
}

func TestOpener(t *testing.T) {
	ctx := context.Background()
	o := OpenerFactory(ConfigureOpener{
		RequestVolumeThreshold: 3,
	})().(*Opener)
	if o.Config().RequestVolumeThreshold != 3 {
		t.Fatal("Should start at 3")
	}
	now := time.Now()
	if o.ShouldOpen(ctx, now) {
		t.Fatal("Should not start open")
	}
	o.ErrTimeout(ctx, now, time.Second)
	o.ErrFailure(ctx, now, time.Second)
	if o.ShouldOpen(ctx, now) {
		t.Fatal("Not enough requests to open")
	}
	// These should be ignored
	o.ErrBadRequest(ctx, now, time.Second)
	o.ErrInterrupt(ctx, now, time.Second)
	o.ErrConcurrencyLimitReject(ctx, now)
	if o.ShouldOpen(ctx, now) {
		t.Fatal("Not enough requests to open")
	}
	o.ErrFailure(ctx, now, time.Second)
	if !o.ShouldOpen(ctx, now) {
		t.Fatal("should now open")
	}
}
