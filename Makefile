SHORT_COMMIT := $(shell git rev-parse --short HEAD)
BRANCH_NAME:=$(shell git rev-parse --abbrev-ref HEAD | tr '/' '-')
COMMIT := $(shell git rev-parse HEAD)
VERSION := $(shell git describe --tags --abbrev=2 --match "v*" --match "ponos*" 2>/dev/null)
UNAME := $(shell uname)
PLATFORMS := linux/amd64,linux/arm64

# docker container registry
CONTAINER_REGISTRY := ghcr.io/blockopsnetwork
IMAGE_NAME := $(CONTAINER_REGISTRY)/ponos-server
export DOCKER_BUILDKIT := 1

# Used when building within docker
GOARCH := $(shell go env GOARCH)

# Image tag: if image tag is not set, set it with version (or short commit if empty)
ifeq (${IMAGE_TAG},)
IMAGE_TAG := ${VERSION}
endif

ifeq (${IMAGE_TAG},)
IMAGE_TAG := ${SHORT_COMMIT}
endif


.PHONY: tidy
tidy:
	go mod tidy -v ./...

.PHONY: docker-buildx
docker-buildx:
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		-t $(IMAGE_NAME):latest \
		.

.PHONY: docker-pushx
docker-pushx:
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(IMAGE_NAME):$(IMAGE_TAG) \
		-t $(IMAGE_NAME):latest \
		--push \
		.

.PHONY: docker-build-local
docker-build-local:
	docker buildx build --platform linux/amd64 -t $(IMAGE_NAME):$(IMAGE_TAG) --load .


.PHONY: build-ponos
build-ponos:
	@echo "Building Ponos..."
	go build -o bin/ponos ./cmd

.PHONY: run-ponos
run-ponos: build-ponos
	@echo "Running Ponos Server..."
	go run ./cmd server
