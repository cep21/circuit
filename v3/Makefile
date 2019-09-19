PROFILE_RUN ?= BenchmarkCiruits/Hystrix/Minimal/failing/75
BENCH_RUN ?= .

build:
	go build ./...

# Run unit tests
test:
	env "GORACE=halt_on_error=1" go test -v -race ./...

# Run unit tests
test_coverage:
	go test -v -covermode=count -coverprofile=coverage.out ./...

# Run unit tests
view_coverage: test_coverage
	go tool cover -html=coverage.out

upload_coverage: test_coverage
	goveralls -coverprofile coverage.out

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
	golangci-lint run

# Run the example
run:
	go run -race example/main.go

# ci installs dep by direct version.  Users install with 'go get'
setup_ci:
	GO111MODULE=on go get github.com/golangci/golangci-lint/cmd/golangci-lint@v1.18.0
	GO111MODULE=on go get github.com/mattn/goveralls@4d9899298d217719a8aea971675da567f0e3f96d
