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
#   make docs-serve  # live-preview the user documentation site
#   make docs-build  # build it once, the way CI does
#   make docs-deploy # publish to GitHub Pages (gh-pages branch)

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

MKDOCS ?= mkdocs

.PHONY: all build install uninstall clean test vet version docs-serve docs-build docs-deploy

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

# ---------- user documentation (userdocs/, MkDocs Material) ----------
#
# Install the toolchain once with:
#   pip install -r userdocs/requirements.txt

docs-serve:
	$(MKDOCS) serve

# What CI runs on a pull request: --strict turns a broken link into a failure.
docs-build:
	$(MKDOCS) build --strict

# Publishing is a local, manual step — there is no deploy job in CI (#148).
# --force is needed because gh-pages is generated output: a local shallow or
# diverged copy of it must not block the push with a non-fast-forward error.
docs-deploy:
	$(MKDOCS) gh-deploy --strict --force
