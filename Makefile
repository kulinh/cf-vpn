.PHONY: lint test all install smoke clean

SHELL := /bin/bash
SCRIPTS := $(shell find scripts -name '*.sh' 2>/dev/null)

lint:
	@command -v shellcheck >/dev/null || { echo "shellcheck not installed"; exit 1; }
	@echo "==> shellcheck"
	@shellcheck -x $(SCRIPTS)

test:
	@command -v bats >/dev/null || { echo "bats not installed"; exit 1; }
	@echo "==> bats"
	@bats tests/

all: lint test

install:
	@bash scripts/install.sh

smoke:
	@bash scripts/healthcheck.sh || true
	@docker compose ps

clean:
	@echo "Use 'docker compose down -v' explicitly to remove state"
