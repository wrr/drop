.DEFAULT_GOAL := build

.PHONY: fmt vet build build-race test test-integration test-race test-all lint cover cover-inspect clean imports vulncheck all

fmt:
	go fmt ./...

vet: fmt
	go vet ./...

build: vet
	go build ./cmd/drop

# Build a devel binary with race detection
build-race:
	go build -race ./cmd/drop

test: vet
	go test ./...

test-race:
	go test -race ./...

# go install honnef.co/go/tools/cmd/staticcheck@latest
lint: build
	staticcheck ./...

test-integration: build
	mkdir -p cover
	python3 -m unittest discover tests/integration/

test-all: test test-integration 

# Gather coverage information for unit tests, integration tests and
# all tests combined.
cover:
	go build -cover ./cmd/drop
	rm -rf cover
	mkdir -p cover/unit cover/int cover/all
	go test -v -cover ./... -args -test.gocoverdir="$(PWD)/cover/unit"
	GOCOVERDIR="$(PWD)/cover/int/" python3 -m unittest discover tests/integration/
	go tool covdata merge -i=cover/unit,cover/int -o=cover/all
	go tool covdata textfmt -i=./cover/unit -o=cover/unit/unit.cov
	go tool covdata textfmt -i=./cover/int -o=cover/int/int.cov
	go tool covdata textfmt -i=./cover/all -o=cover/all/all.cov
	go tool cover -html=cover/int/int.cov -o=cover/integration.html
	go tool cover -html=cover/unit/unit.cov -o=cover/unit.html
	go tool cover -html=cover/all/all.cov -o=cover/all.html
	rm drop

clean:
	go clean

# go install golang.org/x/tools/cmd/goimports@latest
imports: build
	goimports -l -w .

# go install golang.org/x/vuln/cmd/govulncheck@latest
vulncheck: build
	govulncheck ./...

all: cover-inspect test-race test-integration vulncheck imports lint build

