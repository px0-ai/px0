.PHONY: all build web test dist publish clean help

VERSION ?= $(shell cat VERSION 2>/dev/null | tr -d ' \r\n')
# Support 'make publish 0.2.0' where target argument is passed as next goal
VERSION_ARG := $(filter-out publish,$(MAKECMDGOALS))
ifeq ($(strip $(VERSION_ARG)),)
  TARGET_VERSION := $(VERSION)
else
  TARGET_VERSION := $(strip $(VERSION_ARG))
  # Treat extra goal as no-op so make doesn't attempt to build it
  %:
	@:
endif

# Clean leading 'v' from version string if present
CLEAN_VERSION := $(patsubst v%,%,$(TARGET_VERSION))

POSTHOG_KEY ?= $(PX0_POSTHOG_KEY)
LDFLAGS := -s -w
ifneq ($(strip $(POSTHOG_KEY)),)
  LDFLAGS += -X main.posthogKey=$(strip $(POSTHOG_KEY))
endif

all: build

help:
	@echo "px0 make targets:"
	@echo "  make build             - build px0 binary for current platform (optional: POSTHOG_KEY=phc_...)"
	@echo "  make web               - bundle web assets (JS/CSS/themes)"
	@echo "  make test              - run go test suite"
	@echo "  make dist              - compile cross-platform binaries into dist/"
	@echo "  make publish <version> - bump VERSION, commit, tag, and build dist binaries"
	@echo "  make clean             - remove build artifacts"

web:
	@echo "Bundling web assets..."
	@node ./scripts/build-web.js

build: web
	@echo "Building px0 for local system..."
	go build -trimpath -ldflags="$(LDFLAGS)" -o px0 .
	@echo "Built ./px0 ($$(du -h px0 | cut -f1))"

test: web
	go test -v ./...

dist: web
	@./build.sh

publish:
	@if [ -z "$(CLEAN_VERSION)" ]; then \
		echo "Error: Version cannot be empty. Usage: make publish <version> (e.g. make publish 0.2.0)"; \
		exit 1; \
	fi
	@echo "==> Preparing release v$(CLEAN_VERSION) (previous: $$(cat VERSION))"
	@echo "$(CLEAN_VERSION)" > VERSION
	@echo "==> Bundling web assets and building dist binaries..."
	@./build.sh
	@echo "==> Updating git repository..."
	@git add VERSION
	@git commit -m "Release v$(CLEAN_VERSION)" || true
	@git tag -fa "v$(CLEAN_VERSION)" -m "Release v$(CLEAN_VERSION)"
	@echo ""
	@echo "✓ Successfully prepared release v$(CLEAN_VERSION) and updated dist/!"
	@echo "Tag v$(CLEAN_VERSION) has been created."
	@echo ""
	@echo "To publish to GitHub and trigger release workflow, run:"
	@echo "  git push origin master --tags"

clean:
	rm -f px0 web/app.js
	rm -rf dist/
