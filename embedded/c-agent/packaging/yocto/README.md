# Yocto integration skeleton

```sh
mkdir -p meta-yaver/recipes-extended/yaver-cagent
cp packaging/yocto/yaver-cagent_0.0.1.bb \
    meta-yaver/recipes-extended/yaver-cagent/

# Add to your build's conf/bblayers.conf:
#   BBLAYERS += "/path/to/meta-yaver"

bitbake yaver-cagent
```

Replace `SRC_URI` and `SRCREV` with the actual source revision you
want to build. The recipe assumes the layout
`embedded/c-agent/CMakeLists.txt` at the source root.
