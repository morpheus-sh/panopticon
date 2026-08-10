# Makefile for panopticon

BIN    := bin/panopticon
GO     := go

.PHONY: all build test test-race vet integration install clean ci

all: build

build:
	$(GO) build -o $(BIN) ./cmd/panopticon

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

# End-to-end smoke test against a real tmux backend.
integration: build
	./integration.sh

# Install to ~/.local/bin (matches the Herdr-installer convention).
install: build
	install -m 0755 $(BIN) "$(HOME)/.local/bin/panopticon"

# Everything the CI workflow checks, locally.
ci: test-race vet
	gofmt -l cmd internal
	$(MAKE) build
	$(MAKE) integration

clean:
	rm -rf bin
