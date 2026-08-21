VERSION := $(shell cat VERSION 2>/dev/null)
PYTHON := $(shell [ -f venv/bin/python ] && echo venv/bin/python || echo python3)

.PHONY: all test version build publish clean

all: test

test:
	$(PYTHON) -m pytest

version:
	@echo $(VERSION)

build:
	@rm -rf dist/ build/ *.egg-info
	@$(PYTHON) -m build

publish: build
	@bash scripts/publish.sh

clean:
	rm -rf dist/ build/ *.egg-info .pytest_cache
