VHS ?= vhs

ROOT_DIR := ..
TAPES := $(wildcard *.tape)
GIF_TARGETS := $(patsubst %.tape,%,$(TAPES))

.PHONY: all gifs $(GIF_TARGETS)

all: gifs

gifs: $(GIF_TARGETS)

$(GIF_TARGETS):
	cd $(ROOT_DIR) && $(VHS) examples/$@.tape
