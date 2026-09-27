# Yaver for Xbox

This is a dedicated x64 UWP controller client for the `Windows.Xbox` device
family. It is not the Electron desktop host and it never runs repositories,
runners, shells, or arbitrary downloaded code on the console.

The first executable slice uses Yaver's production device-code flow, stores the
resulting session in `PasswordVault`, and reads the signed-in owner's machine
registry. The next release gates are task composition/streaming, approvals,
private-relay selection, preview playback, named recovery actions, and physical
Xbox suspend/resume testing.

## Build and sideload

1. Use an MSIX/PWA Partner Center product. The current `Yaver.io` product may
   carry both its `Windows.Desktop` package and this separate `Windows.Xbox`
   package; Microsoft routes each family to its applicable package. Do not use
   the legacy `Yaver` EXE/MSI product.
2. Bind the project to that product's exact identity and reserved display name;
   never submit the development
   identity in `Package.appxmanifest`.
3. On Windows 11 with Visual Studio 2022, install **Universal Windows Platform
   development** and the Windows 10 SDK 19041 or newer.
4. Open `YaverXbox.sln`, select `x64`, and deploy to an Xbox placed in Developer
   Mode.
5. Run `node Tests/xbox-contract.test.mjs` before packaging.

Do not enable retail Xbox availability until controller-only navigation,
overscan, 1080p/4K legibility, network loss, token expiry, user switch,
suspend/resume, Store update, and clean uninstall have passed on physical
hardware. Task controls, private-relay streaming, and preview playback are
future capabilities and must not be claimed by the first listing.

The checked-in 50/44/150/310 package tiles and 620 x 300 splash image satisfy
package UI roles only. Partner Center still needs real 1920 x 1080 console
screenshots and separate Store merchandising art (584 x 800 branded key art,
1920 x 1080 titled hero, 1080 x 1080 untitled promotional square, and a titled
720 x 1080 or 1440 x 2160 poster). Do not upscale package tiles into listing
art or substitute mocked desktop screenshots for physical-console captures.
