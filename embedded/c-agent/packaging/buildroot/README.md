# Buildroot integration skeleton

Drop this directory into your Buildroot tree as
`package/yaver-cagent/` and wire it in:

```sh
mkdir -p $BR/package/yaver-cagent
cp -r packaging/buildroot/* $BR/package/yaver-cagent/
```

Add `source "package/yaver-cagent/Config.in"` to the appropriate
package category in `package/Config.in`, then:

```sh
make menuconfig    # select Target packages → Networking → yaver-cagent
make
```

Adjust `YAVER_CAGENT_SITE` to point at whatever source URL you ship
internally (a tarball mirror, an internal git fork, a private GitHub
release artifact). The recipe assumes the upstream layout
`embedded/c-agent/CMakeLists.txt`.
