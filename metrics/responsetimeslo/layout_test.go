package responsetimeslo

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestTrackerCountersAreIsolated(t *testing.T) {
	offset := func(name string) uintptr {
		f, ok := reflect.TypeOf((*Tracker)(nil)).Elem().FieldByName(name)
		if !ok {
			t.Fatalf("no field %s", name)
		}
		return f.Offset
	}
	if gap := offset("MeetsSLOCount") - offset("MaximumHealthyTime"); gap < cacheLineSize {
		t.Errorf("MaximumHealthyTime can share a cache line with the counters (gap %d)", gap)
	}
	if gap := offset("Collectors") - offset("FailsSLOCount"); gap < cacheLineSize {
		t.Errorf("Collectors can share a cache line with the counters (gap %d)", gap)
	}
}

func TestTrackerJSONShapeAndExactCounts(t *testing.T) {
	var tr Tracker
	tr.SetConfigThreadSafe(Config{MaximumHealthyTime: time.Second})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				tr.Success(context.Background(), time.Time{}, time.Second)
				tr.Success(context.Background(), time.Time{}, time.Second+1)
				tr.ErrShortCircuit(context.Background(), time.Time{})
			}
		}()
	}
	wg.Wait()
	b, err := json.Marshal(&tr)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"MaximumHealthyTime":1000000000,"MeetsSLOCount":8000,"FailsSLOCount":16000,"Collectors":null}`
	if string(b) != want {
		t.Fatalf("got %s want %s", b, want)
	}
}
