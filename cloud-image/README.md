# cloud-image — Yaver cloud-image overlay

This is the provider-agnostic root-filesystem overlay applied on top of a
vanilla Ubuntu 24.04 cloud image (Hetzner snapshot, GCP custom image, AWS
AMI). Built by `scripts/build-cloud-image.sh`.

## Layout

```
cloud-init/
  user-data         # cloud-config: apt packages + start firstboot service
  meta-data         # instance-id, hostname
  network-config    # DHCP on common Linux interface names
rootfs/
  etc/systemd/system/
    yaver-cloud-firstboot.service   # one-shot on first boot
    yaver-agent.service             # the long-running agent
  usr/local/lib/yaver/
    cloud-firstboot.sh              # provider-agnostic first-boot logic
```

The build script also injects:

```
/usr/local/bin/yaver                # static linux/amd64 or linux/arm64 binary
/etc/yaver/cloud-image-release.json # version, provider, base image, build time
```

## Why provider-agnostic

The image is built once per arch (amd64 + arm64) and uploaded to each
provider as the same content. Differences between providers (network drive
mounts, metadata service quirks) are detected at runtime by
`cloud-firstboot.sh` — no per-provider image variants. This means we can
ship a single "Yaver Cloud Image vX.Y" line and version it linearly.

## Differences vs `pi-image/`

- No bootloader / firmware partition handling — these are cloud-init seed
  drives that drop into existing provider-provided cloud images, not
  flashable boot media.
- Network config covers cloud NIC names (`ens3`, `enp1s0`) in addition to
  `eth0`.
- First-boot script detects the provider rather than assuming Pi.
- `yaver-agent.service` ordering depends on `docker.service`, not
  `multi-user.target` alone — cloud boxes always have Docker.

## Manual test (per provider)

```bash
# Hetzner
HCLOUD_TOKEN=... ./scripts/build-cloud-image.sh --provider hetzner --arch arm64

# GCP (needs gcloud configured)
./scripts/build-cloud-image.sh --provider gcp --arch amd64

# AWS (needs aws CLI configured)
./scripts/build-cloud-image.sh --provider aws --arch amd64
```

Each writes a JSON record to `dist/cloud-image/<provider>-<version>-<arch>.json`
with the image-id you can pass to `hcloud server create --image <id>` /
`gcloud compute instances create --image <id>` / `aws ec2 run-instances
--image-id <id>`.
