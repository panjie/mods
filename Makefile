APP := mods
BIN_DIR := bin

ifeq ($(OS),Windows_NT)
DEVNULL := NUL
MKDIR_P := if not exist "$(BIN_DIR)" mkdir "$(BIN_DIR)"
RM_RF := if exist "$(BIN_DIR)" rmdir /S /Q "$(BIN_DIR)"
else
DEVNULL := /dev/null
MKDIR_P := mkdir -p "$(BIN_DIR)"
RM_RF := rm -rf "$(BIN_DIR)"
endif

VERSION := $(shell git describe --tags --always --dirty 2>$(DEVNULL) || echo unknown)
COMMIT  := $(shell git rev-parse --short HEAD 2>$(DEVNULL) || echo unknown)
GOEXE := $(shell go env GOEXE)
BIN := $(BIN_DIR)/$(APP)$(GOEXE)
RELEASE_BIN := $(BIN_DIR)/$(APP)-release$(GOEXE)

.PHONY: build check test clean release

build:
	$(MKDIR_P)
	go build -trimpath -ldflags="-X main.Version=$(VERSION) -X main.CommitSHA=$(COMMIT)" -o $(BIN) .

release:
	$(MKDIR_P)
	go build -trimpath -ldflags="-s -w -X main.Version=$(VERSION) -X main.CommitSHA=$(COMMIT)" -o $(RELEASE_BIN) .

check:
	go build ./...

test:
	go test ./...

clean:
	$(RM_RF)
