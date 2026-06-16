# Yaver — EV Charging Discovery (Turkey-first) + the Charge-Control Seam

> **Status:** implemented (discovery), 2026-06-17. Control = design-only seam.
> **Source of truth:** the code wins. This doc was written alongside
> `desktop/agent/ev_charging.go` and `desktop/agent/charge_controller.go`;
> re-grep before trusting any claim here — other threads move constants in
> parallel.

This is the reference for Yaver's EV-charging capability: a **generic,
public-safe discovery** layer (where can I charge, on which network, with
which connector, for my car) plus a **generic control seam** that this
open-source repo deliberately leaves empty.

---

## 1. One-paragraph thesis

Finding a charger is a neutral, public-data problem and Yaver solves it
generically: `ev_charging` queries **OpenChargeMap** (keyless-friendly,
respectfully) for nearby stations and normalizes them to a uniform shape;
`ev_networks` and `ev_connector_types` give the curated directory and
taxonomy an AI consumer needs to reason about Turkey (CCS2 + Type 2) and
beyond. **Controlling** a charge session (start/stop) is the opposite: it is
proprietary, protocol-specific (OCPP and friends), and tied to a specific
operator's back-office. That belongs in a **private overlay**, never in this
repo. So Yaver ships the discovery and a clean `ChargeController` seam, and
nothing else.

---

## 2. The IP boundary (read twice)

**OCPP and the Talos charging network are private IP and never appear in this
public repo.** Concretely, this repo contains:

- **NO** OCPP protocol code (no `BootNotification`, `RemoteStartTransaction`,
  `MeterValues`, …).
- **NO** charge start/stop control logic.
- **NO** Talos imports, hostnames, IDs, or operator-specific adapters.

Yaver compiles and is fully useful — discovery works end-to-end — with **zero
knowledge** of any control plane. The only thing this repo defines for control
is the generic `ChargeController` interface + registry
(`charge_controller.go`). A proprietary driver (an OCPP back-office adapter, a
Talos-network bridge) registers itself from a **private overlay** that imports
Yaver, or runs **out-of-process** and talks to the agent over the mesh. The
default `discoveryOnlyController` refuses Start/Stop with
`control unavailable — no charge controller registered`.

---

## 3. Discovery — `ev_charging` (live, OpenChargeMap)

- **Backend:** OpenChargeMap POI API,
  `GET https://api.openchargemap.io/v3/poi`.
- **Params used:** `output=json`, `latitude`, `longitude`, `distance` (km),
  `distanceunit=KM`, `maxresults=60`, `compact=true`, `verbose=false`,
  optional `countrycode` (ISO-2), optional `key`.
- **Keyless-friendly:** an API key raises rate limits but is **not** required
  for modest interactive use. The key is read from `OPENCHARGEMAP_API_KEY`
  (env) or the vault (`project=ev`, name `OPENCHARGEMAP_API_KEY`), else the
  public endpoint is hit directly.
- **Filters** (`connector_type`, `network`, `country`, `min_power_kw`) are
  applied client-side after the fetch — OpenChargeMap's server-side filters
  are coarse, so post-filtering gives predictable results.
- **Result shape** (per station): name, operator/network, address/town,
  lat/lon, distance (km, haversine if the API omits it), connectors (type +
  `type_id` + power kW + AC/DC + count), `max_power_kw`, a `status_hint`, and
  a `deep_link` Google-Maps directions URL.

### Policy Guard (CLAUDE.md)

The live path is built to the project's "do no harm to third parties" rule:

- **Identifies honestly.** `User-Agent: Yaver/1.0 (+https://yaver.io; EV
  charging discovery)` — a contact identity, **not** a spoofed browser UA to
  defeat bot detection.
- **Backs off on a block.** On `403 / 429 / 451` the tool returns a structured
  `{"blocked": true, …}` result and **stops** — it does not retry-spam, and
  it does not rotate identity or IP to route around the block. A block is a
  "no", not a puzzle.
- **Modest requests.** Single request per call, 20 s timeout, radius capped at
  200 km, ≤60 results.

---

## 4. Turkey networks + connectors (static)

### `ev_networks` — curated directory

Seeded Turkey set (operator coverage is wide and growing):

| id | name | note |
|---|---|---|
| `trugo` | Trugo | Togg's network; CCS2 DC fast |
| `zes` | ZES | Zorlu Enerji; wide AC+DC |
| `esarj` | Eşarj | Enerjisa |
| `sharz` | Sharz.net | |
| `voltrun` | Voltrun | |
| `beefull` | Beefull | |
| `astor` | Astor Şarj | |
| `onsarj` | On Şarj | |
| `otowatt` | Otowatt | |
| `powercity` | PowerCity | |

Plus US (`tesla`, `electrify_america`, `chargepoint`, `evgo`) and EU
(`ionity`, `fastned`, `shell`, `bp`). `ev_networks` with no country returns
all regions.

### `ev_connector_types` — taxonomy

| id | name | current | region | max kW |
|---|---|---|---|---|
| `type2` | Type 2 (Mennekes) | AC | EU/TR | 43 |
| `ccs2` | CCS2 (Combo 2) | DC | EU/TR | 350 |
| `ccs1` | CCS1 (Combo 1) | DC | US | 350 |
| `chademo` | CHAdeMO | DC | JP/legacy | 100 |
| `nacs` | Tesla NACS | AC/DC | US | 250 |
| `type1` | Type 1 (J1772) | AC | US/JP | 7.4 |

In Turkey/EU the practical pair is **CCS2** (DC fast) + **Type 2** (AC).
Any `id` here is a valid `connector_type` filter for `ev_charging`.

---

## 5. Vehicle presets — Togg T10X & MG ZS EV testbed

`ev_connector_types` also returns a `vehicle_presets` table so a caller can
default its filters by car instead of hand-picking connectors. The table is
**extensible** — add a row to `evVehiclePresets` to support a new car:

| id | name | connectors | prefer ≥ kW |
|---|---|---|---|
| `togg_t10x` | Togg T10X | ccs2, type2 | 120 |
| `mg_zs_ev` | MG ZS EV | ccs2, type2 | 50 |

These are the user's real test vehicles. Both are CCS2 DC + Type 2 AC, which
is exactly the Turkey standard — so the discovery path is directly testable at
**Trugo** stations (CCS2 DC fast) with either car.

---

## 6. The `ChargeController` seam (control = private)

`charge_controller.go` defines:

```go
type ChargeController interface {
    Name() string
    Status(ctx, stationID, connectorID) (ChargeSession, error)
    Start(ctx, stationID, connectorID) (ChargeSession, error)
    Stop(ctx, stationID, connectorID)  (ChargeSession, error)
}
```

plus a registry mirroring `machine.Engine.RegisterDriver`
(`RegisterChargeController` / `LookupChargeController` / `DefaultChargeController`
/ `SetDefaultChargeController` / `ChargeControllerIDs`). The first non-default
controller registered becomes the active default.

The **only** controller in this repo is `discoveryOnlyController`: `Status`
reports `unavailable`, and `Start`/`Stop` return
`control unavailable — no charge controller registered`. A proprietary OCPP /
Talos driver registers from a private overlay (in-process import or
out-of-process over the mesh) — it never lives here.

---

## 7. Richer real-time data — the Policy-Guarded collection path

OpenChargeMap gives static-ish POI data, not live "is this stall free right
now". Some networks expose live availability only on their own apps. If the
user wants richer real-time data, the **only** acceptable path is the
Policy-Guarded, self-hosted collection model already in the tree
(`collection_plan` runtime selection, the friend-roster / phone-runner
vantages — see `docs/user-directed-data-collection-runtimes.md` and
`docs/yaver-task-packages.md`):

- Read from the **user's own** account / residential vantage where a network's
  terms allow it — **not** a 24/7 scrape loop from a flagged datacenter IP.
- **Identify honestly**, cap concurrency, jitter, **back off on 403/429/451**,
  and stop. Blocks are recorded as findings; the layer never rotates IPs or
  impersonates to route around them.
- This is **not** a default and **not** wired into `ev_charging` — it is an
  opt-in, user-directed path, and it is never abusive scraping.

---

## 8. What's real vs static

| Surface | Real / Static |
|---|---|
| `ev_charging` | **Real** — live OpenChargeMap fetch + post-filter |
| `ev_networks` | Static curated directory |
| `ev_connector_types` | Static taxonomy + vehicle presets |
| Charge start/stop | **Refused** by default (`discoveryOnlyController`); real driver is private |

---

## 9. Testable today with the user's cars

- `ev_charging` near a known Trugo location → expect CCS2 DC stations with a
  `deep_link` to maps. Filter `connector_type=ccs2`, `min_power_kw=120` to
  mirror the Togg T10X preset.
- `ev_networks turkey` → Trugo/ZES/Eşarj/… directory.
- `ev_connector_types` → taxonomy + `togg_t10x` / `mg_zs_ev` presets.
- Charge control → confirm the default refuses Start/Stop (proves the IP
  boundary holds until a private overlay registers a real driver).

Scoped unit tests live in `desktop/agent/ev_charging_test.go`
(`go test -run TestEV .`).
