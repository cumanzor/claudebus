BINARY  := cbus
PKG     := ./cmd/cbus
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# CBUS_REPO is owner/repo for the release verbs. Empty by default: the slug is baked
# into released binaries by `make release CBUS_REPO=owner/repo` and stays out of
# committed source (cbus-7sg D26). A dev build leaves it empty; export CBUS_REPO at
# runtime to point selfupdate at a repo.
CBUS_REPO ?=
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.repoSlug=$(CBUS_REPO)
DIST    := dist

# unix-only: the fleet is a Mac and a Linux box (cbus-7sg D25). bdx stays the
# reference if a windows target ever materializes.
PLATFORMS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64

.DEFAULT_GOAL := build
.PHONY: build test clean dist install release $(PLATFORMS)

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test:
	go test ./...

clean:
	rm -rf $(BINARY) $(DIST)

dist: clean $(PLATFORMS)
	@echo "built $$(ls $(DIST) | wc -l | tr -d ' ') binaries in $(DIST)/"

# asset name is cbus-<os>-<arch>, the EXACT string selfupdate passes to
# `gh release download --pattern` (gh does not platform-match; cbus-7sg fold 3).
$(PLATFORMS):
	@mkdir -p $(DIST)
	@os=$$(echo $@ | cut -d/ -f1); \
	arch=$$(echo $@ | cut -d/ -f2); \
	out=$(DIST)/$(BINARY)-$$os-$$arch; \
	echo "building $$out"; \
	GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $$out $(PKG)

install:
	go install -ldflags "$(LDFLAGS)" $(PKG)

# cut a release: `git tag vX.Y.Z` on HEAD, then `make release CBUS_REPO=owner/repo`.
# WRITTEN, NEVER RUN in this effort — the remote, tag, and first release are
# Carlos-gated and sequenced after the quiesce window (cbus-7sg). Requires gh
# authenticated with write access. A tag with a '-' suffix (v0.2.0-rc1) publishes as
# a prerelease that selfupdate ignores by construction.
release: dist
	@tag=$$(git describe --tags --exact-match 2>/dev/null); \
	if [ -z "$$tag" ]; then echo "no exact tag on HEAD (git tag vX.Y.Z first)" >&2; exit 1; fi; \
	if [ -z "$(CBUS_REPO)" ]; then echo "set CBUS_REPO=owner/repo (it is baked into the assets and targets the release)" >&2; exit 1; fi; \
	flags="--generate-notes"; \
	case "$$tag" in *-*) flags="$$flags --prerelease --latest=false";; esac; \
	echo "publishing $$tag to $(CBUS_REPO)..."; \
	gh release create "$$tag" --repo "$(CBUS_REPO)" $(DIST)/* $$flags
