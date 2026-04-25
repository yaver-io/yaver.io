# c-agent

Experimental device-side runtime described in
[`../../docs/c-agent-architecture.md`](../../docs/c-agent-architecture.md).

> **Status.** Additional Yaver surface, not a pivot. The dev-machine
> agent in [`../../desktop/agent/`](../../desktop/agent/) remains
> Yaver's primary product. Nothing in this directory is wired into
> the main build, CI, or release pipelines. Building it is opt-in
> and standalone.

## Layout

```
embedded/c-agent/
├── core/             framing, RPC, transport runtime    (yvr_cagent_core)
│   ├── include/yvr/  public headers (frame.h, status.h, types.h)
│   └── src/
├── host/             vendor-side abstraction layer      (yvr_cagent_host)
│   ├── include/yvr/  public headers (host.h, module.h, event.h, manifest.h)
│   └── src/
├── policy/           auth, capabilities, redaction      (Phase 1+)
├── tools/            capability host imports            (Phase 2+)
├── transports/       TCP, WS, BLE, serial, phone bridge (Phase 0/5)
├── sdk/              public C ABI extras for OEM use    (Phase 6)
├── examples/
│   ├── cmake-find-package/   consumer using find_package
│   ├── pkg-config/           consumer using pkg-config
│   └── plain-makefile/       consumer using direct paths
├── packaging/
│   ├── buildroot/    Buildroot recipe skeleton
│   ├── openwrt/      OpenWrt feed Makefile skeleton
│   └── yocto/        Yocto recipe skeleton
├── cmake/            installed-config templates
└── tests/            ctest unit tests
```

## What ships

| Artifact | Purpose |
|---|---|
| `libyvr_cagent_core.a` | Wire framing + status codes (no I/O, no allocator) |
| `libyvr_cagent_host.a` | Vendor abstraction: module supervisor, event bus, lifecycle |
| `yvr/frame.h`, `yvr/status.h`, `yvr/types.h` | Core wire ABI |
| `yvr/host.h`, `yvr/module.h`, `yvr/event.h`, `yvr/manifest.h` | Host + module ABI |
| `yaver-cagent.pc` | pkg-config descriptor (relocatable) |
| `yaver-cagentConfig.cmake` | CMake `find_package` entry point |
| `yaver-cagent-config` | Shell config script (ncurses-style) |

## Building

Requires CMake 3.16+ and a C11 compiler.

```bash
./build.sh               # debug build + tests
./build.sh release       # release build + tests, -Werror
./build.sh asan          # debug + ASAN/UBSan
./build.sh clean         # wipe build/
```

The build is self-contained — running it does not affect any other
part of the Yaver repo.

## Installing

```bash
cmake --install build --prefix /opt/yaver-cagent
```

Layout produced:

```
/opt/yaver-cagent/
├── bin/yaver-cagent-config
├── include/yvr/{frame,status,types,host,module,event,manifest}.h
└── lib/
    ├── libyvr_cagent_core.a
    ├── libyvr_cagent_host.a
    ├── pkgconfig/yaver-cagent.pc
    └── cmake/yaver_cagent/{Config,Targets}.cmake
```

The install is **relocatable** — pkg-config and `yaver-cagent-config`
both derive the prefix from their own location at runtime, so an
install tarball can be moved to any path without re-running CMake.

## Toolchain integration

Three idioms are supported. Pick whichever matches the consuming
build system; all three produce the same binary.

### 1. CMake `find_package`

```cmake
find_package(yaver_cagent REQUIRED)

add_executable(my_app main.c)
target_link_libraries(my_app PRIVATE
    yaver::cagent_core    # required if you use yvr/frame.h
    yaver::cagent_host    # required if you use yvr/host.h
)
```

```bash
cmake -S . -B build -DCMAKE_PREFIX_PATH=/opt/yaver-cagent
cmake --build build
```

Working example: [`examples/cmake-find-package/`](examples/cmake-find-package/).

### 2. pkg-config

```bash
PKG_CONFIG_PATH=/opt/yaver-cagent/lib/pkgconfig pkg-config --cflags yaver-cagent
PKG_CONFIG_PATH=/opt/yaver-cagent/lib/pkgconfig pkg-config --libs   yaver-cagent
```

In a Makefile:

```makefile
CFLAGS  += $(shell pkg-config --cflags yaver-cagent)
LDFLAGS += $(shell pkg-config --libs   yaver-cagent)
```

Working example: [`examples/pkg-config/`](examples/pkg-config/).

### 3. Plain Makefile / vendor build systems

For build systems that pre-date pkg-config (legacy autotools,
hand-rolled BSP scripts, vendor SDKs):

```makefile
YVR_PREFIX := /opt/yaver-cagent
CFLAGS  += -I$(YVR_PREFIX)/include
LDFLAGS += -L$(YVR_PREFIX)/lib
LIBS    += -lyvr_cagent_host -lyvr_cagent_core
```

Or use the shell config script (mirrors `ncurses6-config` /
`sdl-config`):

```bash
$(/opt/yaver-cagent/bin/yaver-cagent-config --cflags)
$(/opt/yaver-cagent/bin/yaver-cagent-config --libs)
$(/opt/yaver-cagent/bin/yaver-cagent-config --version)
```

Working example: [`examples/plain-makefile/`](examples/plain-makefile/).

### Vendor build-system shortcuts

Reference recipes for the common embedded build systems live under
[`packaging/`](packaging/). Each is a starting skeleton; vendors
copy into their own tree and adapt:

- **Buildroot**: `packaging/buildroot/` — `Config.in` + `.mk` recipe
- **OpenWrt**: `packaging/openwrt/` — feed Makefile
- **Yocto**: `packaging/yocto/` — `.bb` recipe

## Cross-compiling

CMake's standard cross toolchain mechanism works:

```bash
cmake -S . -B build-arm \
    -DCMAKE_TOOLCHAIN_FILE=/path/to/arm-musl.cmake \
    -DYVR_CAGENT_TESTS=OFF \
    -DCMAKE_INSTALL_PREFIX=/opt/yaver-cagent-arm
cmake --build build-arm
cmake --install build-arm
```

The library has zero non-libc dependencies in the wire layer and
pulls in `dlfcn.h` only in the host loader (Phase 1+). Static
binaries on musl land at well under the 600 KB Tier-2 footprint
target documented in
[`../../docs/c-agent-architecture.md`](../../docs/c-agent-architecture.md)
§4.1.

## What's implemented

- Wire frame header codec (`core/`)
- Public ABI for host + modules + events + manifest (`host/`)
- Stub backend for the host runtime (returns `NOT_READY` for control
  ops; the public ABI is locked down so vendors can integrate today
  while the loader is fleshed out)
- Install layout, pkg-config / CMake / shell-config integration

## What's next

- CBOR codec (deterministic subset; `core/cbor.{h,c}`)
- Phase-0 frame body schemas: `HELLO`, `AUTH`, `ATTEST`, `HEARTBEAT`,
  `ERROR`
- TLS 1.3 transport adapter (mbedTLS)
- Real module loader (`dlopen` + signature verify + dep walk)
- Event bus dispatch
- wasm3 integration

See [`../../docs/c-agent-architecture.md`](../../docs/c-agent-architecture.md)
§13 for the phased plan.
