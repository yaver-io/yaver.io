# Consume the c-agent runtime by setting include + library paths
# directly. Useful for vendor build systems that pre-date pkg-config
# (legacy autotools, hand-rolled Makefiles, BSP build scripts).
#
#   make YVR_PREFIX=/path/to/c-agent/install
#
# Or, equivalently, via the shell config script:
#
#   make YVR_CFLAGS="$(yaver-cagent-config --cflags)" \
#        YVR_LIBS="$(yaver-cagent-config --libs)"

CC          ?= cc
CFLAGS      ?= -O2 -Wall -Wextra -Wpedantic -std=c11

YVR_PREFIX  ?= /usr/local

# Path-based wiring (the most common vendor pattern). Order matters:
# host depends on core, so -lyvr_cagent_host appears before
# -lyvr_cagent_core in the link line.
YVR_CFLAGS  ?= -I$(YVR_PREFIX)/include
YVR_LIBS    ?= -L$(YVR_PREFIX)/lib -lyvr_cagent_host -lyvr_cagent_core

example: main.c
	$(CC) $(CFLAGS) $(YVR_CFLAGS) -o $@ main.c $(YVR_LIBS)

clean:
	rm -f example

.PHONY: clean
