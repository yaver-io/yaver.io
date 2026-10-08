# Yaver Physical KVM input bridge

The first supported M5Stack board is **AtomS3U**. It has native USB OTG and
plugs directly into the computer being controlled as a composite USB keyboard
and mouse. The Raspberry Pi runs the Yaver Go agent and owns the UVC HDMI
capture card. M5Stack's official AtomS3U specification identifies the module as
ESP32-S3FN8 (8 MB flash), which is why this firmware uses an explicit 8 MB
dual-OTA-slot partition map: <https://docs.m5stack.com/en/core/AtomS3U>.

The bridge is local-first. Wi-Fi credentials, its random device credential,
the binding, lease, and input state do not use Convex. Convex is not required
after account/OAuth bootstrap.

## First setup

1. Flash `yaver-kvm-atoms3u.factory.bin` with `esptool.py` at address `0x0`.
2. Plug AtomS3U into the target PC. Read its serial output once to get the
   device-specific `Yaver-KVM-*` setup Wi-Fi password.
3. Join that setup Wi-Fi and put the destination 2.4 GHz credentials in a new
   owner-only JSON file containing `ssid` and `password`. Press the AtomS3U
   button, then run `commission-m5.sh --wifi-credentials <json>
   --token-output <new-owner-only-file>`. It sends the credentials through the
   physically local setup network and saves the one-time response without
   printing the 256-bit pairing token or putting it in shell history.
4. Press the button again for the pairing window. On the Pi, run `yaver kvm
   pair --auto --token-file <owner-only-file>`. Auto-pair succeeds only when
   exactly one nearby bridge announces `mode=keyboard` and
   `pairingState=unpaired`; otherwise use `yaver kvm discover` followed by
   `--device-id`.
5. Plug the target PC's HDMI output into a UVC capture card on the Pi and run
   `yaver kvm doctor`.
   If more than one video input is present, run `yaver kvm capture list` and
   select the intended stable path with `yaver kvm capture select --device ...`.
   Yaver refuses to guess between multiple PCs/cameras.
6. Press the AtomS3U button to arm input for 60 seconds. Yaver renews only a
   short exclusive lease; disconnect, timeout, USB loss, or local lock sends
   release-all.

Hold the AtomS3U button for eight seconds to erase Wi-Fi and pairing state.

## Reproducible build

```sh
cd hardware/yaver-physical-kvm/firmware
pio run
pio run -t mergefs # optional filesystem image; firmware uses Preferences
```

Release packaging uses `scripts/build-physical-kvm-release.sh`. The factory
image includes the bootloader and partition table and is the artifact intended
for normie setup tools. The source tree never contains a Wi-Fi password, bearer
token, or per-device identity.

The `hardware-kvm/v*` GitHub release is separate from CLI/npm and mobile-store
releases. It contains the factory image, OTA image, Linux ARM64 Pi agent,
checksums, BOM, and enclosure CAD, and stays marked prerelease until the
physical USB/capture/failure matrix passes on the frozen production SKU.
GitHub Actions also emits build-provenance attestations for the release files;
the Pi verifies the selected OTA image checksum again before the locally-armed
HMAC-authenticated transfer to AtomS3U.

On a factory or DIY Pi image, `install-pi.sh` installs the exact released ARM64
agent, ffmpeg/v4l2 operation dependencies, and a least-privilege boot service.
The only cloud-facing setup step is the existing headless account/OAuth claim;
KVM binding, video, leases, and input remain local/direct/tunnel traffic.

For a sealed Ethernet-upstream appliance, the factory may run
`configure-private-link.sh --interface <wifi-interface>`. It creates a unique
WPA2 NetworkManager access point for AtomS3U and writes its generated credential
to an owner-only file without printing it. The script refuses to overwrite an
existing connection or credential. This mode dedicates that Wi-Fi interface to
the M5 link, so the Pi needs Ethernet or a second adapter for its upstream.
Use `commission-m5.sh` from a factory station with a second Wi-Fi interface (or
temporarily from another computer) while connected to the AtomS3U setup AP;
then place its token file under the Pi's `yaver` account and run the owner-only
`pair --auto` step. A single radio cannot host the Pi private AP and join the
AtomS3U setup AP simultaneously.

## Security and limits of v0.1

- Runtime calls use a one-use nonce plus HMAC-SHA256 from the per-device
  256-bit credential, and the Pi controller identity is included in every
  signed request. After the physically-local commissioning response, the
  long-lived secret is never sent to the M5 again. Pairing, input, unpairing,
  and firmware mutation also require a physical arm.
- One 10-second input lease exists at a time. Actions are sequenced and bounded.
- v0 HID text is an explicitly advertised US keyboard layout. Printable ASCII,
  tab, newline, named keys, and chords are supported; Unicode is rejected
  visibly instead of emitting corrupted keystrokes. Layout-aware Unicode text
  needs a cooperating target-side paste/input channel in a later version.
- Credentials and typed text are not logged or broadcast.
- The bridge adds no HDCP/DRM bypass; capture is exactly what the UVC card emits.
- AtomS3U reports USB bus mount/resume state. Physical hardware-in-loop proof on
  each target OS remains required before promoting the GitHub prerelease to GA.
