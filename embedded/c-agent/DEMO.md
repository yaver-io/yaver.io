# Demo — End-User-Fixable Hardware

> The Stallman printer story, AI-edition. A 1980 MIT lab couldn't fix
> their networked printer because the source was a black box. In 2026
> on a router that ships c-agent, an end user opens the Yaver mobile
> app, asks *"how many clients are connected?"*, the cloud brain
> writes a small probe, ships it to the device, runs it, and the
> answer comes back to the phone. No source-code holy war required.

## Status

This document is the bench-demo recipe for the architecture in
[`../../docs/c-agent-architecture.md`](../../docs/c-agent-architecture.md)
+ [`FIRMWARE_ARCHITECTURE.md`](FIRMWARE_ARCHITECTURE.md). Slow but
valid is fine for v1; multi-second round-trip is acceptable on a
test bench. Production characteristics come later.

## Three nodes, one round-trip

```
┌────────────┐         ┌────────────┐         ┌────────────┐
│  Mobile    │  HTTPS  │  Hetzner   │  TCP    │  OpenWrt   │
│  (Yaver    │ ───────►│   brain    │ ───────►│  (c-agent  │
│   app)     │         │  + relay   │         │   client)  │
│            │ ◄───────│            │ ◄───────│            │
└────────────┘  answer └────────────┘  result └────────────┘
                                         │
                                "how many clients?"
                                  ↓
                          [brain selects probe]
                                  ↓
                          [probe runs on device]
                                  ↓
                          [result → brain → mobile]
```

- **Hetzner box**: runs the existing `yaver serve` daemon (the
  control plane) + a new minimal "iot-brain" process that does the
  natural-language → probe → answer mapping.
- **OpenWrt router**: runs the c-agent client (cross-compiled
  `.ipk`), connects outbound to the relay, executes signed probe
  modules on demand.
- **Mobile**: existing Yaver app, with a new "Ask Device" entry
  point.

## What we have today

| Component | State |
|---|---|
| c-agent wire framing + CBOR codec | done · `core/{frame,cbor,body}` · 55 unit tests |
| HELLO / AUTH / ATTEST / HEARTBEAT / ERROR body schemas | done |
| Vendor abstraction ABI (host, module, event, manifest) | done · stubbed loader |
| Host runtime: subscribe/emit, manifest registration, pause/resume/quiesce | done |
| Toolchain integration (CMake, pkg-config, sh-config, examples) | done |
| Buildroot / OpenWrt / Yocto recipe skeletons | done |
| Existing Yaver control plane (auth, relay, mobile transport) | done · `desktop/agent/`, `relay/`, `mobile/` |

## What still needs building for the demo

For a v1 bench demo the missing pieces fall into three buckets:

### 1. Device side (c-agent)

- **TCP transport adapter** — open outbound TCP to the relay,
  frame the conversation. ~150 lines under
  [`transports/`](transports/).
- **Native probe registry** — vendor host registers built-in C
  functions as "modules" without dlopen. Lets the demo run probes
  compiled into the c-agent binary today, before the dynamic
  loader lands. ~80 lines added to `host/src/`.
- **One real probe**: `wifi_client_count` reading nl80211
  `NL80211_CMD_GET_STATION` over generic netlink. ~120 lines.
- **OpenWrt build verification** — cross-compile the agent + probe
  via the OpenWrt SDK, package as `yaver-cagent.ipk`. Recipe
  skeleton already in [`packaging/openwrt/`](packaging/openwrt/);
  needs source URL + actual cross-build.

### 2. Brain side (Hetzner)

- A small Go service `iot-brain/` that exposes `POST /ask` with
  body `{"device_id": "...", "question": "..."}`. For v1, hand-
  rolled `question → probe_name` mapping (exact phrases or
  keyword match). Calls into the c-agent over the existing relay
  and returns the formatted answer.
- The "AI-driven" piece is Phase 2 — once the round-trip works
  with hardcoded probes, plug an LLM in front so it can:
  (a) pick the probe from a catalog;
  (b) compose a multi-probe answer;
  (c) eventually author NEW probes if no existing one fits.

### 3. Mobile side

- New "Ask Device" surface. Input box + answer area. Hits the
  brain's `POST /ask` over HTTPS.
- Reuses existing Yaver auth + device-list. Only ~1 new screen.

## Build sequence — concrete commands

### A. Cross-compile c-agent for OpenWrt

This is the part most likely to bite. OpenWrt SDK builds cleanly
when set up correctly; the recipe skeleton is the starting point.

```bash
# Once: install the OpenWrt SDK matching your target router's
# build (architecture + version). For a typical mt7621 router on
# OpenWrt 22.03:
wget https://downloads.openwrt.org/releases/22.03.5/targets/ramips/mt7621/openwrt-sdk-22.03.5-ramips-mt7621_gcc-11.2.0_musl.Linux-x86_64.tar.xz
tar xf openwrt-sdk-*.tar.xz
cd openwrt-sdk-*

# Drop the c-agent feed in:
mkdir -p package/yaver-cagent
cp -r /path/to/embedded/c-agent/packaging/openwrt/* package/yaver-cagent/

# Pin the source URL to a git checkout you can reach:
sed -i 's|^PKG_SOURCE_URL.*|PKG_SOURCE_URL:=https://github.com/kivanccakmak/yaver.io/raw/main|' \
    package/yaver-cagent/Makefile

./scripts/feeds update -a
./scripts/feeds install -a
make defconfig
make package/yaver-cagent/compile V=s
# → bin/packages/<arch>/base/yaver-cagent_0.0.1-1_<arch>.ipk
```

Install on the router:

```bash
scp yaver-cagent_*.ipk root@router.local:/tmp/
ssh root@router.local "opkg install /tmp/yaver-cagent_*.ipk"
```

### B. Spin up the Hetzner brain

```bash
# On a Hetzner CX22 (€4.50/mo, ~2 GB RAM, 40 GB SSD — plenty for v1):
ssh root@<hetzner-ip>

# Install yaver server (existing path, see ../../desktop/agent/):
curl -fsSL https://yaver.io/install | bash
yaver serve --install-systemd

# Add the iot-brain service. (For v1, this is an ~150-line Go
# program. Source lives in `iot-brain/` of this repo when added.)
go install github.com/kivanccakmak/yaver.io/iot-brain@latest
sudo cp ~/go/bin/iot-brain /usr/local/bin/
sudo systemctl enable --now iot-brain
```

The Hetzner box is now reachable via the existing relay
infrastructure; the OpenWrt device's c-agent connects out to it
just like the existing dev-machine agent does.

### C. Wire the mobile app

In the existing `mobile/` app, add a screen that:

1. Lists devices (already done — uses Yaver's device registry).
2. For each device, exposes a "Ask Device" button that opens an
   input box.
3. On submit, hits `POST https://<hetzner>/ask` with
   `{device_id, question}`.
4. Renders the brain's response.

For a quick test before the screen exists, hit the brain
directly with curl:

```bash
curl -X POST https://<hetzner>/ask \
    -H "Authorization: Bearer <token>" \
    -d '{"device_id":"<openwrt-device-id>","question":"how many clients are connected?"}'
```

### D. End-to-end probe

On the OpenWrt router, start the c-agent service (the `.ipk`
postinst can do this; manual command is `service yaver-cagent
start`). Then ask from your laptop:

```
$ curl -X POST https://<hetzner>/ask -d '{"question": "how many clients are connected?", "device_id": "..."}'
{"answer": "4 clients on radio0 (5 GHz), 2 clients on radio1 (2.4 GHz). 6 total.", "elapsed_ms": 1837}
```

That's the demo.

## What the round-trip looks like internally

```
1. Mobile → Hetzner brain
   POST /ask { question: "how many clients?", device_id: "abc..." }

2. Hetzner brain → c-agent on OpenWrt (over relay)
   INVOKE { tool_hash: <wifi_client_count>, args: {} }

3. c-agent on OpenWrt → kernel
   nl80211 NL80211_CMD_GET_STATION on each wlanN
   ↓
   parse station info, count entries per radio

4. c-agent on OpenWrt → Hetzner brain
   TOOL_RSP { result: { radio0: 4, radio1: 2, total: 6 } }

5. Hetzner brain → Mobile
   { answer: "<natural-language formatted>", elapsed_ms: 1837 }
```

For v1, step 1's `question` is keyword-matched to a probe name
("clients" → `wifi_client_count`). For Phase 2, an LLM at the
brain handles the matching + the formatting; for Phase 3, the
LLM authors new probes when no existing one fits.

## Known limitations (alpha-grade demo)

- **Slow on first run.** Module fetch + verify + cache adds ~1 s
  on cold cache. Subsequent calls hit cache and respond in
  ~50–200 ms.
- **Single-probe per call.** Composite questions ("how many
  clients AND what channel?") are two round-trips today. Phase 2
  bundles them.
- **No signature verification yet.** The demo runs in the trust
  model: anyone with the relay password can invoke probes. Real
  signing lands when we wire mbedTLS + the per-tenant key
  infrastructure.
- **Hand-curated probe catalog.** The brain has a hardcoded list
  of probe names → device-side compiled-in C functions. AI-
  authored probes happen on top of this once the WASM module
  loader is in.
- **No remediation in v1.** The demo is read-only — the AI tells
  you the answer, doesn't fix anything yet. Phase 4 of the
  architecture roadmap adds approval-gated write actions.

## Why this matters

Once the round-trip works, every router that ships c-agent
becomes:

- **End-user diagnosable.** "Why is my Wi-Fi slow?" is no longer
  "call ISP support and read the LED colors." It's a phone
  question with a real answer derived from real device state.
- **Field-engineer leverageable.** A junior tech with the Yaver
  app gets the same diagnostic depth a senior engineer with a
  laptop and SSH would.
- **AI-iteratable.** Once the brain can author probes, the same
  pipeline that answers questions can also iterate toward fixes.
  The Stallman printer would have been fixed in 5 minutes by
  this pipeline. The 2026 router is.

That second-order effect — "open-source-able hardware that's
AI-fixable end-to-end" — is the long-term reason c-agent exists.
The demo is the first concrete step on that path.

## What lands next in this repo

After the body codecs + host event bus shipped in this iteration:

1. TCP transport adapter (`transports/tcp.{c,h}`).
2. Native probe registry (`host/src/native_modules.c`).
3. The first real probe: `wifi_client_count` for OpenWrt
   (`probes/wifi_client_count.c`).
4. The `iot-brain/` Go service skeleton (separate top-level
   directory; pairs with the existing `desktop/agent/` and
   `relay/` so the demo works inside the existing control plane).
5. Mobile "Ask Device" screen.

Each is a small, independently verifiable slice. Doing them in
order gets to the working demo without a single big-bang
integration.
