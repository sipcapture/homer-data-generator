# homer-data-generator — synthetic DuckLake data for Homer OOM/load testing
#
#   make build          build binary to ./bin/
#   make test           run tests
#   make smoke          ~50 MiB ducklake dataset in /tmp
#   make help           targets list

BINARY      := homer-data-generator
MODULE      := github.com/sipcapture/homer-data-generator
CMD         := .
BIN_DIR     := bin

GO          ?= go
GOFLAGS     ?=
LDFLAGS     ?=

# Default paths for generate targets (override: make generate CATALOG=/path …)
CATALOG     ?= /data/homer/homer_catalog.sqlite
DATA_PATH   ?= /data/homer/parquet
LAKE        ?= homer_lake
TARGET_GB   ?= 80
DAYS        ?= 14
ROWS_FILE   ?= 25000
FILES_DAY   ?= 32

# Smoke / dev (small, under /tmp)
SMOKE_CATALOG ?= /tmp/homer_catalog.sqlite
SMOKE_DATA    ?= /tmp/homer/parquet
SMOKE_GB      ?= 0.05

.PHONY: all build install test test-race vet fmt tidy clean smoke \
	init-catalog generate compact help

all: build

## build: compile binary to ./bin/homer-data-generator
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)
	@echo "built $(BIN_DIR)/$(BINARY)"

## install: install to $$GOBIN or $$GOPATH/bin
install:
	$(GO) install $(GOFLAGS) -ldflags "$(LDFLAGS)" $(CMD)

## test: unit tests
test:
	$(GO) test $(GOFLAGS) ./...

## test-race: tests with race detector
test-race:
	$(GO) test $(GOFLAGS) -race ./...

## vet: go vet
vet:
	$(GO) vet ./...

## fmt: gofmt all Go sources
fmt:
	@gofmt -w $$(find . -name '*.go' -not -path './.git/*')

## tidy: go mod tidy
tidy:
	$(GO) mod tidy

## clean: remove build artifacts and smoke output
clean:
	rm -rf $(BIN_DIR)
	rm -rf $(SMOKE_DATA) $(SMOKE_CATALOG)

## init-catalog: create catalog + hep_proto_1_call (production paths)
init-catalog: build
	$(BIN_DIR)/$(BINARY) init-catalog \
		--catalog "$(CATALOG)" \
		--data-path "$(DATA_PATH)" \
		--lake "$(LAKE)"

## generate: full dataset (default 80 GiB / 14 days)
generate: build
	$(BIN_DIR)/$(BINARY) generate \
		--catalog "$(CATALOG)" \
		--data-path "$(DATA_PATH)" \
		--lake "$(LAKE)" \
		--target-gb $(TARGET_GB) \
		--days $(DAYS) \
		--rows-per-file $(ROWS_FILE) \
		--files-per-day $(FILES_DAY)

## compact: ducklake flush + merge_adjacent_files
compact: build
	$(BIN_DIR)/$(BINARY) compact \
		--catalog "$(CATALOG)" \
		--data-path "$(DATA_PATH)" \
		--lake "$(LAKE)"

## smoke: quick check (~50 MiB in /tmp)
smoke: build
	@rm -rf "$(SMOKE_DATA)" "$(SMOKE_CATALOG)"
	$(BIN_DIR)/$(BINARY) init-catalog \
		--catalog "$(SMOKE_CATALOG)" \
		--data-path "$(SMOKE_DATA)"
	$(BIN_DIR)/$(BINARY) generate \
		--catalog "$(SMOKE_CATALOG)" \
		--data-path "$(SMOKE_DATA)" \
		--target-gb $(SMOKE_GB) \
		--days 2 \
		--files-per-day 4 \
		--rows-per-file 1000
	@echo "--- smoke output ---"
	@find "$(SMOKE_DATA)" -name 'ducklake-*.parquet' | head -3
	@du -sh "$(SMOKE_DATA)" "$(SMOKE_CATALOG)"

## help: show targets
help:
	@echo "homer-data-generator Makefile"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  make /' | sed 's/:/ -/'
	@echo ""
	@echo "Variables (override on command line):"
	@echo "  CATALOG=$(CATALOG)"
	@echo "  DATA_PATH=$(DATA_PATH)"
	@echo "  TARGET_GB=$(TARGET_GB)  DAYS=$(DAYS)"
	@echo "  ROWS_FILE=$(ROWS_FILE)  FILES_DAY=$(FILES_DAY)"
	@echo ""
	@echo "Examples:"
	@echo "  make smoke"
	@echo "  make generate TARGET_GB=10 DAYS=7"
	@echo "  make init-catalog CATALOG=/tmp/c.sqlite DATA_PATH=/tmp/p"
