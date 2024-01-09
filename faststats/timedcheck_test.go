package faststats

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cep21/circuit/v4/internal/clock"
	"github.com/cep21/circuit/v4/internal/testhelp"
)

func TestTimedCheck_Empty(t *testing.T) {
	x := TimedCheck{}
	now := time.Now()
	if !x.Check(now) {
		t.Error("First check should pass on empty object")
	}
}

func TestTimedCheck_MarshalJSON(t *testing.T) {
	x := TimedCheck{}
	if x.String() != fmt.Sprintf("TimedCheck(open=%s)", time.Time{}) {
		t.Fatal("unexpected toString", x.String())
	}
	x.SetSleepDuration(time.Second)
	x.SetEventCountToAllow(12)
	b, err := json.Marshal(&x)
	if err != nil {
		t.Fatal("unexpected err", err)
	}
	var y TimedCheck
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal("unexpected err", err)
	}
	if y.eventCountToAllow.Get() != 12 {
		t.Fatal("expect 10 event counts to allow")
	}
	if y.sleepDuration.Get() != time.Second.Nanoseconds() {
		t.Fatal("expect 1 sec sleep duration")
	}
}

func TestTimedCheck_Check(t *testing.T) {
	c := clock.MockClock{}
	x := TimedCheck{
		TimeAfterFunc: c.AfterFunc,
	}
	x.SetSleepDuration(time.Second)
	now := time.Now()
	c.Set(now)
	x.SleepStart(now)
	if x.Check(now) {
		t.Fatal("Should not check at first")
	}
	if x.Check(c.Set(now.Add(time.Millisecond * 999))) {
		t.Fatal("Should not check close to end")
	}
	if !x.Check(c.Set(now.Add(time.Second))) {
		t.Fatal("Should check at barrier")
	}
	if x.Check(c.Set(now.Add(time.Second))) {
		t.Fatal("Should only check once")
	}
	if x.Check(c.Set(now.Add(time.Second + time.Millisecond))) {
		t.Fatal("Should only double check")
	}
	if !x.Check(c.Set(now.Add(time.Second * 2))) {
		t.Fatal("Should check again at 2 sec")
	}
}

func TestTimedCheck(t *testing.T) {
	sleepDuration := time.Millisecond * 100
	now := time.Now()
	neverFinishesBefore := now.Add(sleepDuration)
	// Travis is so slow we need a big buffer
	alwaysFinishesBy := now.Add(sleepDuration + time.Second)
	x := TimedCheck{}
	x.SetEventCountToAllow(1)
	x.SetSleepDuration(sleepDuration)
	x.SleepStart(time.Now())
	hasFinished := false
	var wg sync.WaitGroup
	testhelp.DoTillTime(alwaysFinishesBy, &wg, func() {
		if x.Check(time.Now()) {
			if time.Now().Before(neverFinishesBefore) {
				t.Error("It should never finish by this time")
			}
			hasFinished = true
		}
	})
	wg.Wait()
	if !hasFinished {
		t.Error("It should be finished by this late")
	}
}

func TestTimedCheckRaces(_ *testing.T) {
	x := TimedCheck{}
	x.SetSleepDuration(time.Nanosecond * 100)
	endTime := time.Now().Add(time.Millisecond * 50)
	wg := sync.WaitGroup{}
	doTillTime(endTime, &wg, func() {
		x.Check(time.Now())
	})
	doTillTime(endTime, &wg, func() {
		x.SetEventCountToAllow(2)
	})
	doTillTime(endTime, &wg, func() {
		x.SetSleepDuration(time.Millisecond * 100)
	})
	wg.Wait()
}
