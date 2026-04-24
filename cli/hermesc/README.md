# hermesc binaries for yaver-cli

Hermes bytecode compiler binaries matched to `react-native@0.81.5`
(Hermes BC version 96, the version baked into the Yaver container app).

## How this directory is populated

- `darwin-arm64/` and `darwin-x64/` ship with the tarball as a macOS
  universal binary. They're small (~6 MB) and cover the vast majority
  of `yaver-push` users.
- Other platforms (`linux-x64`, `win32-x64`) are installed at
  `npm install -g yaver-cli` time by `src/postinstall.js` → it extracts
  the right binary from the `react-native` npm tarball.
- `linux-arm64` has no upstream prebuilt. On first push, `bundler.js`
  falls back to `getHermescPathAsync({ allowBuildFromSource: true })`,
  which builds hermesc from the project's own
  `node_modules/react-native/sdks/hermes/` sources via CMake. Takes
  ~3–5 min, cached afterwards.

## How bundler.js resolves the right one

1. Platform-matched cache (this dir, per `<platform>-<arch>` key)
2. `~/.yaver/hermesc/<key>/hermesc` (per-user mirror)
3. Legacy flat layout (top-level `hermesc` or `hermesc.exe`)
4. Project-local `node_modules/react-native/sdks/hermesc/...`

Never commit binaries into platform subdirs other than the two
macOS ones — the installer writes them at install time and
committing them would make every `git pull` churn large files.
