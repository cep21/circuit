package responsetimeslo

import (
	"testing"
	"time"
)

func checkSLO(t *testing.T, r *Tracker, expectFail int64, expectPass int64) {
	if r.FailsSLOCount.Get() != expectFail {
		t.Error("Unexpected failing count", r.FailsSLOCount.Get(), expectFail)
	}
	if r.MeetsSLOCount.Get() != expectPass {
		t.Error("Unexpected meets count", r.MeetsSLOCount.Get(), expectPass)
	}
}

func TestTracker(t *testing.T) {
	r := &Tracker{}
	r.MaximumHealthyTime.Set(time.Second.Nanoseconds())
	r.ErrInterrupt(time.Now(), time.Second)
	checkSLO(t, r, 0, 0)
	r.ErrInterrupt(time.Now(), time.Second*2)
	checkSLO(t, r, 1, 0)
	r.ErrBadRequest(time.Now(), time.Second*2)
	checkSLO(t, r, 1, 0)
	r.ErrConcurrencyLimitReject(time.Now())
	checkSLO(t, r, 2, 0)
	r.ErrFailure(time.Now(), time.Nanosecond)
	checkSLO(t, r, 3, 0)
	r.ErrShortCircuit(time.Now())
	checkSLO(t, r, 4, 0)
	r.ErrTimeout(time.Now(), time.Second)
	checkSLO(t, r, 5, 0)
	r.Success(time.Now(), time.Second)
	checkSLO(t, r, 5, 1)
	r.Success(time.Now(), time.Second*2)
	checkSLO(t, r, 6, 1)

	if r.Var().String() == "" {
		t.Error("Expect something out of Var")
	}

}
