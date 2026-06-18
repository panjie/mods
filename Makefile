APP := mods
BIN_DIR := bin
MAN_DIR := manpages
DESTDIR ?=
XDG ?= 0

ifeq ($(OS),Windows_NT)
DEVNULL := NUL
GOEXE ?= .exe
POWERSHELL ?= powershell
PS := $(POWERSHELL) -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command
ifeq ($(XDG),1)
ifeq ($(strip $(XDG_BIN_HOME)),)
$(error XDG=1 on Windows requires XDG_BIN_HOME)
endif
ifeq ($(strip $(XDG_DATA_HOME)),)
$(error XDG=1 on Windows requires XDG_DATA_HOME)
endif
DEFAULT_BINDIR := $(XDG_BIN_HOME)
DEFAULT_MANDIR := $(XDG_DATA_HOME)/man
else
PROGRAMFILES ?= $(if $(ProgramFiles),$(ProgramFiles),C:/Program Files)
PREFIX ?= $(PROGRAMFILES)/$(APP)
DEFAULT_BINDIR := $(PREFIX)/bin
DEFAULT_MANDIR := $(PREFIX)/share/man
endif
MANPAGE := $(MAN_DIR)/$(APP).1
define make_dir
$(PS) "New-Item -ItemType Directory -Force -LiteralPath '$(1)' | Out-Null"
endef
define remove_dir
$(PS) "if (Test-Path -LiteralPath '$(1)') { Remove-Item -LiteralPath '$(1)' -Recurse -Force }"
endef
define remove_file
$(PS) "Remove-Item -LiteralPath '$(1)' -Force -ErrorAction SilentlyContinue"
endef
define install_program
$(PS) "Copy-Item -LiteralPath '$(1)' -Destination '$(2)' -Force"
endef
define install_data
$(PS) "Copy-Item -LiteralPath '$(1)' -Destination '$(2)' -Force"
endef
define write_manpage
go run . man > "$(MANPAGE)"
endef
else
DEVNULL := /dev/null
GOEXE ?= $(shell go env GOEXE)
PREFIX ?= /usr/local
MANPAGE := $(MAN_DIR)/$(APP).1.gz
XDG_DATA_HOME ?= $(HOME)/.local/share
XDG_BIN_HOME ?= $(HOME)/.local/bin
INSTALL ?= install
INSTALL_PROGRAM ?= $(INSTALL) -m 755
INSTALL_DATA ?= $(INSTALL) -m 644
ifeq ($(XDG),1)
DEFAULT_BINDIR := $(XDG_BIN_HOME)
DEFAULT_MANDIR := $(XDG_DATA_HOME)/man
else
DEFAULT_BINDIR := $(PREFIX)/bin
DEFAULT_MANDIR := $(PREFIX)/share/man
endif
define make_dir
mkdir -p "$(1)"
endef
define remove_dir
rm -rf "$(1)"
endef
define remove_file
rm -f "$(1)"
endef
define install_program
$(INSTALL_PROGRAM) "$(1)" "$(2)"
endef
define install_data
$(INSTALL_DATA) "$(1)" "$(2)"
endef
define write_manpage
go run . man | gzip -c > "$(MANPAGE)"
endef
endif

VERSION := $(shell git describe --tags --always --dirty 2>$(DEVNULL) || echo unknown)
COMMIT  := $(shell git rev-parse --short HEAD 2>$(DEVNULL) || echo unknown)
BINDIR ?= $(DEFAULT_BINDIR)
MANDIR ?= $(DEFAULT_MANDIR)
MAN1DIR := $(MANDIR)/man1
BIN := $(BIN_DIR)/$(APP)$(GOEXE)
RELEASE_BIN := $(BIN_DIR)/$(APP)-release$(GOEXE)

.PHONY: build check test clean release man clean-man install uninstall

build:
	$(call make_dir,$(BIN_DIR))
	go build -trimpath -ldflags="-X main.Version=$(VERSION) -X main.CommitSHA=$(COMMIT)" -o $(BIN) .

release:
	$(call make_dir,$(BIN_DIR))
	go build -trimpath -ldflags="-s -w -X main.Version=$(VERSION) -X main.CommitSHA=$(COMMIT)" -o $(RELEASE_BIN) .

check:
	go build ./...

test:
	go test ./...

man:
	$(call make_dir,$(MAN_DIR))
	$(call write_manpage)

install: build man
	$(call make_dir,$(DESTDIR)$(BINDIR))
	$(call make_dir,$(DESTDIR)$(MAN1DIR))
	$(call install_program,$(BIN),$(DESTDIR)$(BINDIR)/$(APP)$(GOEXE))
	$(call install_data,$(MANPAGE),$(DESTDIR)$(MAN1DIR)/$(notdir $(MANPAGE)))

uninstall:
	$(call remove_file,$(DESTDIR)$(BINDIR)/$(APP)$(GOEXE))
	$(call remove_file,$(DESTDIR)$(MAN1DIR)/$(notdir $(MANPAGE)))

clean-man:
	$(call remove_file,$(MANPAGE))

clean:
	$(call remove_dir,$(BIN_DIR))
