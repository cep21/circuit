PROFILE_RUN ?= BenchmarkCiruits/Hystrix/Minimal/passing/75
BENCH_RUN ?= .
# Run unit tests
test:
	env "GORACE=halt_on_error=1" go test -v -race ./...

# Format the code
fix:
	find . -iname '*.go' -not -path '*/vendor/*' -print0 | xargs -0 gofmt -s -w
	find . -iname '*.go' -not -path '*/vendor/*' -print0 | xargs -0 goimports -w

# Run benchmark examples
bench:
	go test -v -benchmem -run=^$$ -bench=$(BENCH_RUN) ./...

# Get some memory profiles
profile_memory:
	go test -benchmem -run=^$$ -bench=$(PROFILE_RUN) -memprofile=mem.out ./benchmarking
	go tool pprof --alloc_space benchmarking.test mem.out
	rm -f mem.out benchmarking.test

# Get some cpu profiles
profile_cpu:
	go test -run=^$$ -bench=$(PROFILE_RUN) -benchtime=15s -cpuprofile=cpu.out ./benchmarking
	go tool pprof benchmarking.test cpu.out
	rm -f cpu.out benchmarking.test

# Get some cpu profiles
profile_blocking:
	go test -run=^$$ -bench=$(PROFILE_RUN) -benchtime=15s -blockprofile=block.out ./benchmarking
	go tool pprof benchmarking.test block.out
	rm -f block.out benchmarking.test

# Lint the code
lint:
	gometalinter --enable-all -D lll --dupl-threshold=100 ./...

setup:
	go get -u github.com/alecthomas/gometalinter
	go get -t -d ./benchmarking/... ./metric_implementations/...
	gometalinter --install

# Run the example
run:
	go run -race example/main.go
