# Makefile for lazysql — build and install the SQL TUI.
#
# Usage:
#   make            # build ./lazysql
#   make install    # install to ~/.local/bin/lazysql
#   make install BINDIR=/usr/local/bin
#   make uninstall
#   make clean
#   make test
#   make vet
#   make version    # print the version the next build will carry

BINARY  := lazysql
PACKAGE := .
BINDIR  ?= $(HOME)/.local/bin
GO      ?= go

# Build stamp for `lazysql --version`. The version number lives in the VERSION
# file; a build outside a git checkout still works (the dirty marker falls back
# to empty).
VERSION := $(shell cat VERSION 2>/dev/null)
DIRTY   := $(shell git diff --quiet 2>/dev/null || echo -dirty)
VERPKG  := lazysql/internal/version
LDFLAGS := -X $(VERPKG).Version=$(VERSION)$(DIRTY)

.PHONY: all build install uninstall clean test vet version

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PACKAGE)

install:
	mkdir -p $(BINDIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(BINARY) $(PACKAGE)

uninstall:
	rm -f $(BINDIR)/$(BINARY)

clean:
	rm -f $(BINARY)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# Print what `lazysql --version` will report for a build from this tree.
version: build
	./$(BINARY) --version
