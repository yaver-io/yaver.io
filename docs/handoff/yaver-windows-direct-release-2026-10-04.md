# Yaver Windows direct-release handoff — 2026-10-04

This is a dated, sanitized release snapshot. Code, release assets, and the live
download response are authoritative if they later disagree with this document.
Never add certificate credentials, private keys, PINs, access tokens, customer
data, or machine-specific secrets here.

## Shipped result

Yaver GUI `0.1.16` was built from the immutable `gui/v0.1.16` tag at commit
`14105ffe49001b2f0722beced38db91ff7a8f782`, Authenticode-signed through the
non-exportable SIMKAB SimplySign identity, and published to the public GitHub
release:

```text
https://github.com/yaver-io/yaver.io/releases/tag/gui/v0.1.16
```

The Windows x64 NSIS installer SHA-256 is:

```text
fad4fa5eb1ea9eb8cb93254ed76f96676aca2182942c1dbf201ebfc2951ab4ce
```

The landing-page download route is live at:

```text
https://yaver.io/download/desktop/windows-x64
```

That route resolves to the released Windows installer. All packaged PE files
and the outer installer passed the release signature checks before publication.
The Electron test suite passed all 104 tests for the release workstream.

## Direct EXE versus Microsoft Store package

This session shipped the signed direct-download `.exe`; it did **not** create or
submit a Yaver MSIX to Partner Center. Keep these channels distinct:

- Direct Windows distribution uses the signed NSIS installer from the
  `gui/v*` GitHub release and the `/download/desktop/windows-x64` route.
- Microsoft Store distribution uses the separate client/full-node AppX/MSIX
  contract in `electron/store/README.md`; Microsoft signs an accepted Store
  package. A direct NSIS `.exe` is not a substitute for that submission.
- Talos Partner Center work and Talos Store identities/packages must never be
  reused for Yaver.

## Release safety improvement

Commit `77b0bbcab` (`fix(release): fail fast when SimplySign card is inactive`)
was pushed to `main` after the tagged release. The local Windows release path
now checks that the SimplySign virtual card/session is usable before doing the
expensive cross-platform build. This turns an eventual signing failure into an
early, named preflight failure.

The release remains certificate-pinned and fail-closed. Do not upload unsigned
bytes, do not replace an immutable release asset with a different binary, and
do not infer signing readiness merely from the PKCS#11 library or certificate
being present. Probe an actual signing operation.

## Reproduction and verification

Use the canonical owner-only deploy entrypoint from a clean checkout:

```sh
./deploy/deploy.sh desktop-windows
```

If publication is intentionally separated from the build, the guarded publish
target requires the exact signed installer path:

```sh
./deploy/deploy.sh desktop-windows-publish <signed-installer.exe>
```

Before declaring a release complete:

1. Verify the source tag resolves to the intended commit and package version.
2. Require the SimplySign operation preflight to pass.
3. Verify Authenticode on the outer installer and every packaged PE.
4. Verify the installer SHA-256 against the public release evidence.
5. Probe the live `/download/desktop/windows-x64` route rather than inferring
   availability from a deployment status.
6. Run the Windows install/start/update/uninstall checks described by the
   Electron release contract.

Do not record an active SimplySign session as durable state: it is ephemeral
and must be re-probed on every release.

