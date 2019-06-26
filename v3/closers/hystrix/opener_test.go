package hystrix

import (
	"strings"
	"testing"
	"time"
)

func TestOpener_MarshalJSON(t *testing.T) {
	o := Opener{}
	_, err := o.MarshalJSON()
	if err != nil {
		t.Fatal("expect no error doing initial marshal")
	}
	// 3 failures should exist in the output
	o.ErrFailure(time.Now(), time.Second)
	o.ErrFailure(time.Now(), time.Second)
	o.ErrFailure(time.Now(), time.Second)
	b, err := o.MarshalJSON()
	if err != nil {
		t.Fatal("expect no error doing marshal")
	}
	if !strings.Contains(string(b), "3") {
		t.Fatal("expect a 3 back")
	}
}

func TestOpener(t *testing.T) {
	o := OpenerFactory(ConfigureOpener{
		RequestVolumeThreshold: 3,
	})().(*Opener)
	if o.Config().RequestVolumeThreshold != 3 {
		t.Fatal("Should start at 3")
	}
	now := time.Now()
	if o.ShouldOpen(now) {
		t.Fatal("Should not start open")
	}
	o.ErrTimeout(now, time.Second)
	o.ErrFailure(now, time.Second)
	if o.ShouldOpen(now) {
		t.Fatal("Not enough requests to open")
	}
	// These should be ignored
	o.ErrBadRequest(now, time.Second)
	o.ErrInterrupt(now, time.Second)
	o.ErrConcurrencyLimitReject(now)
	if o.ShouldOpen(now) {
		t.Fatal("Not enough requests to open")
	}
	o.ErrFailure(now, time.Second)
	if !o.ShouldOpen(now) {
		t.Fatal("should now open")
	}
}
