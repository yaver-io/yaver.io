# Yaver Xbox Partner Center submission guide

Status: development package only — no Xbox product is reserved or associated;
retail submission is also blocked on physical-console proof

1. Enroll/verify the legal company in Microsoft Partner Center.
2. Reserve **Yaver for Xbox** as a new MSIX or PWA app product, separate from
   the existing **Yaver** EXE/MSI desktop product and from talos.works. Reusing
   the desktop product would mix incompatible package and device-family
   contracts.
3. Associate `xbox/YaverXbox/YaverXbox.csproj` with the reserved Store product;
   this replaces the development identity in `Package.appxmanifest`.
4. Build an x64 Release AppX/MSIX bundle on Windows 11 with Visual Studio 2022
   and the UWP workload. After reservation, run **Build Yaver for Xbox Store
   package** in GitHub Actions with the exact identity, publisher, display name,
   four-part version, and confirmation. Download its `.appxupload` artifact for
   the manual first submission. The workflow never submits or publishes.
   Do not upload the Electron installer: it is Desktop family only.
5. Keep only the Xbox device family enabled for this product package. The
   Windows desktop listing remains a separate package/release contract.
6. Complete the privacy, age-rating, encryption, network, and account-system
   declarations truthfully. The first release is free and has no checkout.
7. Provide a synthetic reviewer account and device-code approval instructions
   only through Partner Center's private certification notes.
8. Capture 1920 x 1080 Xbox screenshots on a retail-equivalent console using
   synthetic machine names. No customer, repository, host, token, or network
   data may appear. Provide at least four screenshots plus the Xbox Store art
   set: branded key art (584 x 800, titled), titled hero art (1920 x 1080),
   featured promotional square (1080 x 1080, no title), and 2:3 poster art
   (720 x 1080 or 1440 x 2160, titled). The package tiles under
   `xbox/YaverXbox/Assets` are not substitutes for listing screenshots and
   merchandising art.
9. Submit the read-only first release only after physical-console evidence
   proves controller-only navigation, visible focus, safe areas, device-code
   sign-in, machine refresh, session revocation, token expiry, user switch,
   suspend/resume, network loss, update, and uninstall.

The first bounded release is intentionally read-only: device-code sign-in,
secure session storage, sign-out/revocation recovery, and authorized machine
availability. It does not yet open tasks or previews. Do not describe those as
Xbox capabilities until their controller-first flows and physical-console
evidence exist. The desktop EXE/MSI submission is independent and may proceed
while this gate remains closed.

Suggested listing sentence:

> Yaver for Xbox shows the availability of your authorized Yaver machines. The
> console does not store source repositories, execute coding agents, or control
> tasks in this first release.

Certification notes must disclose the external-runtime dependency at the start
and explain the device-code flow. Never claim that Xbox itself is a Yaver host.
