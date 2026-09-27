# Yaver Microsoft Store submission kit

Yaver is a self-hostable developer tool with publicly available source under
FSL-1.1-Apache-2.0 (and Apache-2.0 client SDKs). The Microsoft Store product is
an x64 full-node AppX/MSIX-family desktop package. It supports client, local
agent, and combined modes. Use the **MSIX or PWA app**
product type. Partner Center signs and hosts the accepted package.

## Current candidate verdict

Do not submit the immutable EXE 0.1.11 candidate. Audit of its actual NSIS payload
found the signed x64 Windows app and x64 Windows agent, but also a 59 MB macOS
ARM64 `resources/bin/yaver` binary copied by the shared desktop resource rule.
That build can also let the embedded agent register its own Windows Scheduled
Task even while the desktop Start at login preference is off. The public URL
cannot be replaced. Partner Center validation 58809486 also could not prove
silent installation or identify Yaver's Installed apps name and publisher.
Build, sign, publish, and validate a new version after the Windows-only
resource, single-consent auto-start, and explicit NSIS publisher/display-name
guards are in place. That candidate is superseded by the Store-managed AppX
architecture; the separately signed EXE remains a direct-download alternative.

## Release sequence

1. Create/reserve **Yaver.io** as an **MSIX or PWA app** product. The existing
   `Yaver` EXE/MSI draft cannot accept this package family; do not touch
   talos.works and do not delete the old draft merely to free its name.
2. Copy the exact case-sensitive Package/Identity/Name, Publisher, and
   Publisher display name from Partner Center Product identity into the
   protected GitHub environment variables documented below.
3. Run the manual **Microsoft Store AppX release** workflow. It builds the x64
   full-node package on `windows-2022`, runs the Electron and rendering-lane
   contracts, operation-probes the embedded agent, and inspects the final AppX
   for identity, architecture, capabilities, license, and forbidden WSL payloads.
4. Download `yaver-microsoft-store-x64-<run-id>` and upload its AppX for the
   unavoidable first Partner Center submission. Do not use a Package URL.
5. Run Windows App Certification Kit and retain its report.
6. Add a synthetic, least-privilege certification account only in Partner
   Center's private certification-notes field.
7. Capture at least four current Windows screenshots using only synthetic
   projects, accounts, device names, and network details.
8. Copy the English listing and declarations from this directory. The current
   Windows UI is English-only, so declare only English for the package.
9. Submit only after Partner Center accepts the exact package identity.

The first submission is a manual bootstrap so the application record and
listing exist. After that, a protected `gui-store/v<version>` tag builds the
same package and submits it through the Partner Center API. Partner Center and
Entra identifiers stay in GitHub environment variables/secrets; the client
secret is never tracked.

The production GitHub workflow uses the protected
`microsoft-store-production` environment. Manual dispatch builds and retains
the first-submission package but never submits it. Only a matching protected
`gui-store/v<version>` tag enables the submission job. The Store package uses
no SimplySign/PFX credential because Microsoft signs accepted AppX packages.

Configure that environment with variables `YAVER_STORE_IDENTITY_NAME`,
`YAVER_STORE_PUBLISHER`, `YAVER_STORE_PUBLISHER_DISPLAY_NAME`,
`YAVER_STORE_DISPLAY_NAME` (the exact reserved product name, `Yaver.io`),
`YAVER_MS_STORE_APPLICATION_ID`, and `YAVER_MS_STORE_CERTIFICATION_NOTES`.
Store `YAVER_MS_STORE_TENANT_ID`, `YAVER_MS_STORE_CLIENT_ID`, and
`YAVER_MS_STORE_CLIENT_SECRET` as environment secrets. Require a human reviewer.
Never store the certification test account in GitHub; enter it only in Partner
Center's private certification notes.

## Hard blockers before submission

- Final AppX inspection proves x64 Electron and agent binaries, exact production
  identity, Windows.Desktop, bounded network/full-trust capabilities, FSL
  license present, and no WSL, VHDX, service, driver, or startup task.
- Client-only, local-agent, and combined switching pass on real Windows hardware.
- Browser Lane, compatible React Native/Expo Hermes Lane, and WebRTC paths pass
  their source contracts plus an appropriate closed-loop test project.
- Windows App Certification Kit passes.
- Store screenshots and certification credentials contain no real customer,
  repository, machine, address, token, or network data.
- Privacy policy and terms have been reviewed against the shipped multi-runner,
  managed-workspace, relay, generative-AI, Microsoft Store, and FSL behavior.
  The current public legal pages predate several of those features and must not
  be treated as complete merely because their URLs return HTTP 200.
- A synthetic remote Yaver node remains available to certification reviewers.
- Device-family availability remains **PC/Desktop only**. Do not select Xbox,
  Mobile, Holographic, Team, or automatic future-device-family availability
  for this full-trust Electron package.

## Packaging decision

Reuse Talos's Microsoft-managed AppX signing and hosting mechanism, but keep
Yaver's native x64 agent because local development and rendering are core Yaver
functions. The package does not bundle WSL and does not install a service or
startup task. The Electron app supervises the child agent while it is open.
Partner Center can therefore sign and host the package without exporting
SIMKAB's SimplySign identity.

The Xbox surface remains a separate x64 UWP package and Partner Center product. Its
package is built by `.github/workflows/xbox-store-package.yml`; that does not
convert the Windows desktop node into an Xbox app.
