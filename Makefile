SHELL := /usr/bin/env bash

APP := tg-channel-archive-api
IMAGE ?= tg-channel-archive-api
VERSION ?=

.PHONY: help test build docker-build release

help:
	@echo "Targets:"
	@echo "  make test"
	@echo "  make build"
	@echo "  make docker-build"
	@echo "  make release VERSION=v0.1.1"

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -buildvcs=false -o server ./cmd/server
	rm -f server

docker-build:
	docker build -t $(IMAGE):ci .

release:
	@if [[ -z "$(VERSION)" ]]; then \
		echo "VERSION is required, for example: make release VERSION=v0.1.1" >&2; \
		exit 1; \
	fi
	@if [[ ! "$(VERSION)" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$$ ]]; then \
		echo "VERSION must look like v0.1.1 or v0.1.1-rc.1" >&2; \
		exit 1; \
	fi
	@if [[ -n "$$(git status --porcelain)" ]]; then \
		echo "working tree is dirty; commit or stash changes before release" >&2; \
		git status --short; \
		exit 1; \
	fi
	@git fetch --tags origin
	@if git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null; then \
		echo "tag $(VERSION) already exists locally" >&2; \
		exit 1; \
	fi
	@if git ls-remote --exit-code --tags origin "refs/tags/$(VERSION)" >/dev/null 2>&1; then \
		echo "tag $(VERSION) already exists on origin" >&2; \
		exit 1; \
	fi
	$(MAKE) test
	$(MAKE) build
	$(MAKE) docker-build IMAGE=$(IMAGE)
	git tag "$(VERSION)"
	git push origin "$(VERSION)"
	@echo "Release $(VERSION) pushed. GitHub Actions will publish ghcr.io/reonokiy/tg_channel_archive_api:$(VERSION)."
