# Makefile for panopticon

BIN    := bin/panopticon
GO     := go

.PHONY: all build test test-race vet integration install clean

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

clean:
	rm -rf bin
