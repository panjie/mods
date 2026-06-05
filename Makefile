APP := mods
BIN_DIR := bin
GOEXE := $(shell go env GOEXE)
BIN := $(BIN_DIR)/$(APP)$(GOEXE)

.PHONY: build check test clean

build:
	mkdir -p $(BIN_DIR)
	go build -trimpath -o $(BIN) .

check:
	go build ./...

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)
