EXECUTABLE ?= spot-price-exporter
IMAGE ?= cloud104/$(EXECUTABLE)
TAG ?= dev-$(shell git log -1 --pretty=format:"%h")
REGISTRY = us-east1-docker.pkg.dev/tks-gcr-pub/spot-price-exporter

LD_FLAGS = -X "main.version=$(TAG)"
GOFILES_NOVENDOR = $(shell find . -type f -name '*.go' -not -path "./vendor/*")
PKGS=$(shell go list ./... | grep -v /vendor)

.PHONY: _no-target-specified
_no-target-specified:
	$(error Please specify the target to make - `make list` shows targets.)

.PHONY: list
list:
	@$(MAKE) -pRrn : -f $(MAKEFILE_LIST) 2>/dev/null | awk -v RS= -F: '/^# File/,/^# Finished Make data base/ {if ($$1 !~ "^[#.]") {print $$1}}' | egrep -v -e '^[^[:alnum:]]' -e '^$@$$' | sort

LICENSEI_VERSION = 0.0.7
bin/licensei: ## Install license checker
	@mkdir -p ./bin/
	curl -sfL https://raw.githubusercontent.com/goph/licensei/master/install.sh | bash -s v${LICENSEI_VERSION}

.PHONY: license-check
license-check: bin/licensei ## Run license check
	@bin/licensei check

.PHONY: license-cache
license-cache: bin/licensei ## Generate license cache
	@bin/licensei cache

DEP_VERSION = 0.5.0
bin/dep:
	@mkdir -p ./bin/
	@curl https://raw.githubusercontent.com/golang/dep/master/install.sh | INSTALL_DIRECTORY=./bin DEP_RELEASE_TAG=v${DEP_VERSION} sh

.PHONY: vendor
vendor: bin/dep ## Install dependencies
	bin/dep ensure -vendor-only

all: clean vendor deps fmt vet docker push

clean:
	go clean -i ./...

deps:
	go get ./...

fmt:
	@gofmt -w ${GOFILES_NOVENDOR}

vet:
	@go vet -composites=false ./...

docker:
	@echo "Building container image"
	docker buildx create
	docker buildx build --push --platform linux/amd64 --platform linux/arm64 -t $(REGISTRY)/$(EXECUTABLE):${TAG} .

push:
	docker push $(IMAGE):$(TAG)

run-dev:
	go run $(wildcard *.go)

build:
	go build -o $(EXECUTABLE) $(wildcard *.go)

build-all: fmt lint vet build

misspell: install-misspell
	misspell -w ${GOFILES_NOVENDOR}

lint: install-golint
	golint -min_confidence 0.9 -set_exit_status $(PKGS)

install-golint:
	GOLINT_CMD=$(shell command -v golint 2> /dev/null)
ifndef GOLINT_CMD
	go get github.com/golang/lint/golint
endif

install-misspell:
	MISSPELL_CMD=$(shell command -v misspell 2> /dev/null)
ifndef MISSPELL_CMD
	go get -u github.com/client9/misspell/cmd/misspell
endif

install-ineffassign:
	INEFFASSIGN_CMD=$(shell command -v ineffassign 2> /dev/null)
ifndef INEFFASSIGN_CMD
	go get -u github.com/gordonklaus/ineffassign
endif

install-gocyclo:
	GOCYCLO_CMD=$(shell command -v gocyclo 2> /dev/null)
ifndef GOCYCLO_CMD
	go get -u github.com/fzipp/gocyclo
endif

ineffassign: install-ineffassign
	ineffassign ${GOFILES_NOVENDOR}

gocyclo: install-gocyclo
	gocyclo -over 19 ${GOFILES_NOVENDOR}
