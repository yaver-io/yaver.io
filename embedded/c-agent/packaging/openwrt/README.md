# OpenWrt integration skeleton

Drop into an OpenWrt feed:

```sh
mkdir -p feeds/yaver/yaver-cagent
cp packaging/openwrt/Makefile feeds/yaver/yaver-cagent/

# Add to feeds.conf.default:
#   src-link yaver /path/to/feeds/yaver

./scripts/feeds update yaver
./scripts/feeds install yaver-cagent
make menuconfig    # enable Network → yaver-cagent
make package/yaver-cagent/compile
```

Replace `PKG_SOURCE_URL` and `PKG_HASH` with real values from your
release tarball before building for production.

The recipe builds against musl 1.2 / kernel 5.15 baseline declared
in [`../../../docs/c-agent-architecture.md`](../../../docs/c-agent-architecture.md)
§14 (open question Q7).
