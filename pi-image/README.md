# Yaver Pi Image

This directory defines the Raspberry Pi 5 / arm64 image lane for Yaver.

Current approach:

- base image: Ubuntu Server preinstalled Raspberry Pi arm64 image
- customization: offline rootfs + boot partition overlay
- first boot: cloud-init enables a one-shot bootstrap service
- runtime: `yaver-agent.service` runs `yaver serve` as a system service in bootstrap mode until the device is paired

Provisioning model:

- baked into the raw image: base OS, `yaver`, cloud-init payload, first-boot scripts, systemd units
- installed automatically on first boot: the heavy `pi-dev-node` stack such as `ollama`, `aider`, `opencode`, TDD tools, `sqlite3`, `vercel`, `convex`, PostgreSQL, Redis, Supabase, and MQTT tooling
- rationale: keep the downloadable image smaller and let the dev stack update independently after release

The built artifact is:

- `yaver-pi5-devnode-arm64.img.xz`

Boot-partition parameters:

- `boot/firmware/yaver-firstboot.env`
  - `YAVER_AUTO_UPDATE=daily`
  - `YAVER_AUTO_UPDATE=weekly`
  - `YAVER_AUTO_UPDATE=off`

The first-boot service reads that file and configures a systemd timer that updates:

- the Yaver binary
- apt-managed system/dev packages
- selected npm/pip tools in the Pi dev-node stack
- npx-backed cloud/backend CLIs such as Vercel, Convex, and Supabase

Build locally:

```bash
./scripts/build-pi-image.sh --version 0.1.0
```

From macOS, use Docker Desktop/Engine:

```bash
./scripts/build-pi-image.sh --docker --version 0.1.0
```

The native build path requires Linux loop-device tooling (`losetup`, `mount`, `findmnt`), so CI and Linux hosts are the primary supported builders.

Release flow:

- bump `versions.json.piImage`
- tag `pi-image/v<version>`
- GitHub Actions builds the image, drafts a GitHub release, and can upload the artifact into the Convex-backed public downloads pipeline
