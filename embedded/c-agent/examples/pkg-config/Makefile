# Consume the c-agent runtime via pkg-config. Set PKG_CONFIG_PATH
# (or use a system install) and `make`:
#
#   PKG_CONFIG_PATH=/path/to/c-agent/install/lib/pkgconfig make
#
# This is the path most embedded build systems already use.

CC      ?= cc
CFLAGS  ?= -O2 -Wall -Wextra -Wpedantic -std=c11
PKGCONF ?= pkg-config

PKG_CFLAGS := $(shell $(PKGCONF) --cflags yaver-cagent)
PKG_LIBS   := $(shell $(PKGCONF) --libs   yaver-cagent)

example: main.c
	$(CC) $(CFLAGS) $(PKG_CFLAGS) -o $@ main.c $(PKG_LIBS)

clean:
	rm -f example

.PHONY: clean
