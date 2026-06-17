# Yaver Anywhere Real-Device Testing

Last updated: 2026-06-17

This is the hardware proof plan for the Yaver Anywhere work in this folder. Unit
tests prove the wiring; this plan proves that a real Android phone and an
off-network viewer can actually use it.

## Scope

Run this whenever changing:

- Android `SandboxService` / `YaverSandbox` native module.
- `--relay-only` serving, relay registration, or TURN credentials.
- `RemoteSessionView` / interactive WebRTC.
- redroid/testkit flows that are supposed to substitute for physical Android.
- The reset / colo preparation UI.

## Safety Rules

- Use a spare Android phone for reset testing. Factory reset erases apps, local
  accounts, files, and app data.
- Do not expose ADB on `0.0.0.0` or a public interface.
- Do not store relay passwords, TURN secrets, customer IPs, device serials, or
  account tokens in tracked files.
- Do not treat redroid as a substitute for the final off-network device proof.
  Redroid is excellent for automation, not for battery, Android settings, OEM
  restrictions, notification permission, or cellular/Wi-Fi network reality.

## Preconditions

- A physical Android phone with the current Yaver app installed.
- USB debugging enabled, or a manual tester ready to operate the phone.
- A Yaver account signed into the app.
- A relay/TURN environment configured on the agent side:
  - `YAVER_TURN_URL`
  - `TURN_AUTH_SECRET` or `RELAY_PASSWORD`
- A viewer on a different network. Preferred: phone host on Wi-Fi, viewer on
  cellular, or the reverse.
- Optional Android tools:
  - `adb`
  - `ffmpeg`
  - Docker on a Linux host for redroid.

## Quick Host Checks

From repo root:

```bash
adb devices
docker info --format '{{.ServerVersion}} {{.OSType}} {{.Architecture}}'
cd desktop/agent && go test -run 'Test.*Redroid|TestAndroid.*UI|TestAndroid.*Selector|TestStudio' ./studio ./testkit .
cd mobile && npx tsc --noEmit
cd mobile/android && ./gradlew :app:compileDebugKotlin
```

On this Mac on 2026-06-17, Docker was not reachable and `adb devices` showed no
attached device. The redroid/testkit unit-level Go tests passed, but no actual
redroid container or phone could be exercised locally.

## Phone Home-Host Smoke

Use the scripted smoke when a phone is attached:

```bash
./scripts/smoke-yaver-anywhere-android.sh
START_HOME_HOST=1 ./scripts/smoke-yaver-anywhere-android.sh
```

Expected:

- The package `io.yaver.mobile` is installed.
- `SandboxService` is visible after starting home-host mode.
- Port `18080`, if present, is not bound to `0.0.0.0` or `::`.
- Recent `YaverSandbox` logs are captured.

If the script cannot start the foreground service by ADB because of platform
restrictions, open the app and enable **Host my assistant on this phone**, then
run the script again without `START_HOME_HOST`.

## Manual Home-Host Proof

1. Open the Android app.
2. Enable **Host my assistant on this phone**.
3. Confirm the foreground notification says the phone is hosting through the
   relay.
4. Confirm the app status shows:
   - online/running
   - home-host mode
   - relay-only inbound
   - battery percent
   - charging state
5. On a different network, open the web dashboard as the same user and connect to
   this phone.
6. Confirm the owner can reach the assistant through relay.
7. Try with a different account or guest-scoped token. Expect rejection.
8. From another LAN machine, try the phone's LAN IP and direct port. Expect no
   LAN-reachable HTTP listener.

Evidence to save outside git:

- Screenshot of the Android status card.
- Screenshot/video of the off-network dashboard connection.
- Output from `./scripts/smoke-yaver-anywhere-android.sh`.
- Sanitized logcat around `YaverSandbox`.

## Interactive WebRTC / TURN Proof

1. Start a managed remote session on a laptop/agent with `YAVER_TURN_URL` and
   the TURN secret set.
2. Open the dashboard from a device on cellular.
3. Start the interactive session.
4. Confirm the browser fetches `GET /stream/webrtc/ice`.
5. Confirm video connects and input works across networks.
6. Remove the TURN env and repeat. Expect direct/LAN cases to work, but hard NAT
   or cellular cases may fail.

Pass condition: off-network video and input connect with TURN configured.

## Reset Wizard Walkthrough

Use a spare phone only.

1. Open the local box / phone-node screen.
2. Go to **Prepare this phone for colo**.
3. Confirm the copy says the reset erases the device, including apps.
4. Confirm the reset button is disabled until all checklist items are checked.
5. Tap **Open Android reset settings**.
6. Confirm Android Settings opens to reset/privacy settings, or falls back to
   general Settings.
7. Complete reset only on a phone that is safe to erase.

Pass condition: a non-technical tester can reach the Android reset UI without
believing Yaver can preserve apps or personal data.

## Redroid Coverage

Redroid is the automation lane for Android UI and QA, not the final hardware
proof.

Local feasibility:

- macOS Docker Desktop usually cannot run redroid correctly because redroid
  needs Linux binder support in the host kernel.
- Use a Linux runner or managed Yaver box with Docker and `binder_linux`.
- The code path is `desktop/agent/studio/redroid.go` and
  `desktop/agent/testkit/driver_androidredroid.go`.

Useful commands:

```bash
cd desktop/agent
go test -run 'Test.*Redroid|TestAndroid.*UI|TestAndroid.*Selector|TestStudio' ./studio ./testkit .
```

For a full redroid run, use the ops verbs from a running agent:

- `testkit_deps_check`
- `testkit_deps_install`
- `qa_base_build`
- `qa_base_up`
- `qa_run`
- `qa_report`

Pass condition: redroid boots on Linux, the app installs, at least one flow runs,
and the report includes screenshots/logs. This still must be followed by the
physical phone checks above.

## Evidence Template

```text
Date:
Tester:
Yaver app build:
Agent version:
Phone model / Android version:
Network shape:
Relay/TURN configured: yes/no

Home-host:
- App toggle:
- Foreground notification:
- Relay-only listener:
- Owner off-network connect:
- Non-owner rejected:

WebRTC/TURN:
- /stream/webrtc/ice fetched:
- Video connected:
- Input worked:

Reset wizard:
- Checklist copy clear:
- Settings deep-link:

Redroid:
- Host:
- Docker:
- Image:
- Flow/report:

Artifacts:
- Screenshots:
- Logs:
- Command outputs:
```
