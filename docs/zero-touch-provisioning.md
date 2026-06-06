# Zero-touch device provisioning (DPP-style)

> Status: Yaver-core + auto-SSH **built and tested** 2026-06-06 (uncommitted).
> Third-party flash SDK polish + Talos integration are follow-ups.
>
> Code is the source of truth. Before acting on anything below, grep:
> `backend/convex/provisioning.ts`, `backend/convex/schema.ts`
> (`provisionedDevices`, `deviceProducts`), `desktop/agent/provision.go`,
> `desktop/agent/provision_cmd.go`, `desktop/agent/shell_cmd.go`.

## What it is

Ship a Yaver-powered box (a Talos edge node, a "blackbox" Raspberry Pi, any
third-party hardware) pre-flashed. The buyer **scans the QR on the label —
before powering it on — and becomes the owner.** On first boot the box
self-credentials to that account over the network (through NAT, no LAN, no
relay password, no human at the device) and is immediately reachable by
shell from terminal / web / mobile.

This is WiFi **DPP / Easy-Connect** applied to Yaver: a per-device
**bootstrapping key pair** is minted at flash time; the **public** key + a
one-time `claimSecret` go in the QR, the **private** key + `claimSecret` go
on the SD boot seed. Possession of the QR authorizes ownership; possession
of the genuine SD seed authorizes the device.

## The three steps

```
1. MINT (builder, flash time)
   yaver provision mint --product talos-edge-v1 --model "Talos Edge Node"
   → generates Ed25519 keypair + 256-bit claimSecret
   → POST /devices/provision-mint  {deviceId, publicKey, sha256(claimSecret)}
   → writes the SD seed file + prints the QR (public key + claimSecret)
   Convex stores ONLY the public key + the claimSecret HASH.

2. CLAIM (buyer, device still off)
   Scan QR in the Yaver app  (or: yaver provision claim "<qr-uri>")
   → POST /devices/provision-claim  {deviceId, claimSecret}
   → sha256(claimSecret) matched, ownerUserId bound. First-claim-wins.
   The buyer owns the box now, before it has booted.

3. ATTEST (device, first boot)
   Agent reads the seed, signs `provision-attest|<deviceId>|<ts>` with the
   Ed25519 private key, POSTs {claimSecret, signature, timestamp} to
   /devices/provision-attest.
   → server verifies signature vs the pre-claimed public key + secret hash
     + fresh timestamp → mints an owner-bound session token.
   → if nobody has claimed yet: {awaiting-claim}, the box keeps polling and
     credentials the instant the buyer scans.
```

After attestation the agent saves the token and re-execs into normal
`yaver serve` (the same handoff manual pairing uses), then registers via
the usual `registerDevice` path.

## Where the seed lives

`yaver provision mint` writes `provision-<deviceId>.json`. Flash it onto the
SD **boot partition** as `yaver-provision.json` (rides the FAT partition
like cloud-init user-data). On first boot the agent migrates it to
`~/.yaver/provision.json` (0600) and **deletes the boot-partition copy** so
the private key doesn't linger on a removable volume. Lookup order:
`$YAVER_PROVISION_SEED` → `<config>/provision.json` → `/boot/firmware/…` →
`/boot/…`.

## Security model

- The QR carries only the **public** key. A leaked/photographed label lets
  someone steal the *binding* (claim into their account) but **never**
  impersonate the hardware — only the genuine SD seed (private key) passes
  attestation. Mitigate binding theft with the high-entropy `claimSecret`
  (only someone who saw the real label has it), optional factory
  serial→order binding, and re-flash → `revoke` reset. Same trust model as
  DPP.
- Convex stores `claimSecretHash` + public key only — never the raw secret
  or the private key (enforced by `convex_privacy_test.go`; the raw
  `claimSecret` / `ed25519Seed` field names are on the forbidden list).
- `claimSecret` and the signature travel only over the dedicated
  `/devices/provision-{attest,claim}` HTTPS routes (same precedent as the
  bootstrap-pending relay password) — never on a sync payload.

## Auto-SSH (connect from anywhere once owned)

A headless provisioned box behind CGNAT can't be reached by real OpenSSH.
The agent's owner-gated `/ws/terminal` PTY (mounted by normal `yaver serve`,
relay-reachable) is the NAT-friendly path used by web xterm and the mobile
terminal — and now by the CLI:

```
yaver shell <device>        # interactive PTY over LAN → mesh → public → relay
yaver shell                 # local agent
yaver ssh <device>          # falls back to the relay PTY when no direct
                            # SSH host resolves (interactive, no passthrough)
```

`yaver shell` reuses the existing remote-agent transport resolver
(`resolveRemoteAgentCandidates`) and dials `wss://…/ws/terminal?token=…`.

## CLI reference

```
yaver provision mint [--product <slug>] [--model <name>] [--platform linux]
                     [--name <n>] [--out <path>] [--no-register]
yaver provision qr <seed.json>
yaver provision claim <qr-uri|deviceId> [--secret <s>] [--name <n>]
yaver provision product --id <slug> --name <name> [--vendor <v>]
```

## HTTP / Convex surface

- `POST /devices/provision-mint`   (bearer)  → `provisioning.mintProvisionedDevice`
- `POST /devices/provision-claim`  (bearer)  → `provisioning.claimProvisionedDevice`
- `POST /devices/provision-attest` (no auth; signature is the proof) → `provisioning.attest`
- `POST /devices/provision-register-product` (bearer)
- mutations also callable directly by the app: `claimProvisionedDevice`,
  `peekProvisionedDevice`, `listMine`, `revoke`.

## For third parties (Talos-alike) — `yaver.provision.yaml`

Everything is driven by one declarative file the builder commits to their
image (schema: `desktop/agent/provision_manifest.go`):

```yaml
version: 1
product: talos-edge-v1
model: "Talos Edge Node"
vendor: Talos
platform: linux
services: [modbus-rtu-master, edge-loop, screenlog]   # claim-UI summary
setup:                                                 # one-time post-claim bring-up
  - name: bring up the workload
    run: "yaver companion up"
```

Builder flow:

```
yaver provision mint --manifest yaver.provision.yaml --count 100 --out-dir batch/
# -> registers the product (model shown in the claim UI), mints 100 SD seeds
#    into batch/, and writes:
#      batch/labels.html  printable QR sticker sheet (embedded PNGs, no network)
#      batch/labels.csv   deviceId,productId,model,seedPath,qrPayload (for tooling)

yaver provision flash --seed batch/provision-<id>.json --boot /Volumes/bootfs
# -> after you flash the base image with your usual tool and mount the boot
#    partition, installs the seed as yaver-provision.json + prints the label QR.
#    (Deliberately does NOT dd raw block devices — that risks wiping the wrong
#    disk; use your normal flasher for the image, this just injects the seed.)

yaver provision qr batch/provision-<id>.json --png label.png   # single QR image
```

When a box self-credentials (claim + attest), the agent runs the manifest's
`setup` steps **once** (idempotent marker; gated on the box actually being
provisioned) — see `provision_postclaim.go`. Long-running services delegate
to the existing companion engine (`yaver companion up`) rather than a second
runtime. A complete example: `examples/talos-edge/yaver.provision.yaml`.

End users claim three ways:

- **Mobile**: Devices tab → "+ Add a device (scan QR)" → camera scan
  (`mobile/app/provision-add.tsx` → `src/components/ProvisionScanner.tsx` →
  `claimProvisionedDevice`).
- **Web**: `/add-device` (`web/app/add-device/page.tsx`) — browser-native
  `BarcodeDetector` camera scan with a paste-the-payload / enter-id+secret
  fallback. Posts to `/devices/provision-claim`.
- **CLI**: `yaver provision claim "<qr-uri>"`.

## Not yet built

- `yaver flash` does NOT write the base OS image to a raw block device (that's
  destructive / risks wiping the wrong disk untested). Flash the image with
  your normal tool; `yaver provision flash` then injects the seed onto the
  mounted boot partition.
- On-device camera testing for the mobile scan screen (built + typechecks; not
  yet exercised on a physical device).
- Talos repo wiring (separate repo, owned elsewhere): ship
  `examples/talos-edge/yaver.provision.yaml` in the Talos image + run
  `yaver provision product --id talos-edge-v1 --name "Talos Edge Node"` on the
  Talos account.
