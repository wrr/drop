.DEFAULT_GOAL := build
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

VERSION ?= $(shell git describe --tags --always --dirty | sed 's/^v//')
LDFLAGS_RELEASE = -ldflags "-s -w -X main.Version=$(VERSION)"

.PHONY: fmt vet get-deps build install uninstall build-release build-race test test-integration test-select test-race test-all lint cover clean imports vulncheck gen-example-config all

fmt:
	go fmt ./...

vet: fmt
	go vet ./...

get-deps:
	go get ./...

# Disable cgo, force version of the libcap lib that does not use it.
build: vet
	CGO_ENABLED=0 go build ./cmd/drop

install:
	install -D -m 0755 drop $(DESTDIR)$(BINDIR)/drop

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/drop

build-release:
	@mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS_RELEASE) -o ./dist/drop-linux-amd64 ./cmd/drop
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS_RELEASE) -o ./dist/drop-linux-arm64 ./cmd/drop

# Build a devel binary with race detection
build-race:
	go build -race ./cmd/drop

test: vet
	go test -fullpath ./...

test-race:
	go test -fullpath -race ./...

# go install honnef.co/go/tools/cmd/staticcheck@latest
lint: build
	staticcheck ./...

test-integration: build
	mkdir -p cover
	python3 -m unittest discover tests/integration/

LAST_ARG := $(lastword $(MAKECMDGOALS))
test-one: build
	python3 -m unittest discover tests/integration/ -p "$(LAST_ARG)"

# Prevent make from treating the test file argument as a target
%.py:
	@:


test-all: test test-integration 

# Gather coverage information for unit tests, integration tests and
# all tests combined.
cover:
	go build -cover -covermode=atomic ./cmd/drop
	rm -rf cover
	mkdir -p cover/unit cover/int cover/all
	go test -v -cover -covermode=atomic ./... -args -test.gocoverdir="$(PWD)/cover/unit"
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

gen-example-config: build
	DROP_HOME=$$(mktemp -d) && \
	DROP_HOME=$$DROP_HOME ./drop ps && \
	cp $$DROP_HOME/base.toml config.example.toml && \
	rm -rf $$DROP_HOME

all: test-race test-integration vulncheck imports lint build

