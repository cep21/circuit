package benchmarking

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func genericBenchmarkTesting(b *testing.B, concurrent int, f func() error, expectError bool) {
	wg := sync.WaitGroup{}
	for j := 0; j < concurrent; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Distribute the N requests between each goroutine to even out allocs/runtimes
			for i := 0; i < (b.N / concurrent); i++ {
				err := f()
				if expectError {
					if err == nil {
						b.Errorf("I expect an error, but got nil")
					}
				} else {
					if err != nil {
						b.Errorf("Saw error when there should be none: %s", err)
					}
				}
			}
		}()
	}
	wg.Wait()
}

func passes() error {
	return nil
}

var errFailure = errors.New("failures")

func fails() error {
	return errFailure
}

func passesCtx(_ context.Context) error {
	return nil
}

func failsCtx(_ context.Context) error {
	return errFailure
}

func passesInter() (interface{}, error) {
	return nil, nil
}

func failsInter() (interface{}, error) {
	return nil, errFailure
}
