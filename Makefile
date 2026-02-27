.PHONY: all ci build test test-race lint fuzz bench fix cover help

# Default: the fast loop (build + test + lint). Use during active development.
all: build test lint

# Everything CI runs. Use before submitting a PR.
ci: build test-race lint

build:
	go build -mod=readonly ./...

# Fast unit tests — includes property tests and fuzz seed corpora
test:
	go test ./...

# What CI runs: race detector, 10 iterations, halt on first race
test-race:
	env "GORACE=halt_on_error=1" go test -race -count 10 ./...

lint:
	golangci-lint run

# Coverage HTML report (opens in browser on most systems; else see coverage.html)
cover:
	go test -covermode=count -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

# Active fuzzing. One target at a time, FUZZTIME per target (default 30s).
# Example: make fuzz FUZZTIME=2m
FUZZTIME ?= 30s
FUZZ_TARGETS := FuzzRollingCounterOps FuzzRollingCounterJSON \
	FuzzSortedDurationsPercentile FuzzRollingBucketAdvance FuzzTimedCheckJSON
fuzz:
	@for t in $(FUZZ_TARGETS); do \
		echo "=== fuzz $$t ($(FUZZTIME)) ==="; \
		go test -fuzz="^$$t$$" -fuzztime=$(FUZZTIME) ./faststats/ || exit 1; \
	done

bench:
	go test -benchmem -run=^$$ -bench=. ./...

# Auto-format: gofmt + goimports on all source
fix:
	gofmt -s -w .
	@command -v goimports >/dev/null 2>&1 && goimports -w . || echo "goimports not installed, skipped"

help:
	@echo "Targets:"
	@echo "  make             fast dev loop: build + test + lint (~5s)"
	@echo "  make ci          everything CI runs — use before submitting a PR (~1min)"
	@echo "  make build       compile all packages"
	@echo "  make test        unit tests (includes property tests, fuzz seeds)"
	@echo "  make test-race   race detector, -count 10"
	@echo "  make lint        golangci-lint run"
	@echo "  make fuzz        active fuzzing, FUZZTIME per target (default 30s)"
	@echo "  make bench       all benchmarks"
	@echo "  make cover       generate coverage.html"
	@echo "  make fix         gofmt + goimports"
