APP := mods
BIN_DIR := bin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "unknown")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GOEXE := $(shell go env GOEXE)
BIN := $(BIN_DIR)/$(APP)$(GOEXE)
RELEASE_BIN := $(BIN_DIR)/$(APP)-release$(GOEXE)

.PHONY: build check test clean release

build:
	mkdir -p $(BIN_DIR)
	go build -trimpath -o $(BIN) .

release:
	mkdir -p $(BIN_DIR)
	go build \
		-trimpath \
		-ldflags="-s -w -X main.Version=$(VERSION) -X main.CommitSHA=$(COMMIT)" \
		-o $(RELEASE_BIN) \
		.

check:
	go build ./...

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)
