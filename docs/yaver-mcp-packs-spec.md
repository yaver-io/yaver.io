# Yaver MCP Packs — Spec (Apartman / NGO / Muhtar / Home)

> Reference spec for the per-segment "appliance" packs that run on the hub box.
> Build later. Companion to `yaver-hub-kit-radio-offgrid.md` (§6.6 daily use,
> §6.7 wrapping + MCP selection layer) and `yaver-personal-assistant-mvp.md`
> (gateway engine, ACT approval, vision-heal).
> Notation per tool: `name(params)` — **engine** — **read** / **write:risk** —
> {roles}.  engine ∈ api | web(playwright) | redroid | local.
> risk ∈ low | high | financial (financial ⇒ human last-mile, MVP §4).

## Common conventions (all packs)

- **Read-mostly:** reads run auto (no gate). **Writes are approval-gated**
  (`gateway_act` confirm + audit). **Never auto-write financial** — financial
  ends in a human tap (WebRTC last-mile).
- **Role-scoped:** the **MCP selection layer surfaces only tools allowed for the
  caller's role** (selection = authorization, §6.7). Residents can't even select
  a write tool.
- **Mode-gated:** in **emergency/blackout mode every pack collapses to
  comms/roster/SOS**; daily packs disabled (no internet/auth). Daily mode reopens
  the pack.
- **Creds in vault**, acts as the account **with explicit consent**, **local
  audit** (never Convex). Engine ladder **API > web > redroid**.
- **Briefing:** each role has a proactive morning digest (`routine_*`/`cron`,
  read-only) — the daily-engagement heartbeat.

---

## 1. Apartman Pack

**Roles:** `resident`, `yönetici` (manager), `kapıcı` (caretaker).
**Wraps:** **Apsiyon** (api-first, redroid/web fallback), building IoT
(`shelly_*`/Zigbee/cameras, local), kargo APIs.

### Read tools
| Tool | engine | scope |
|---|---|---|
| `aidat_status(flat?)` dues status/history | apsiyon | resident:own · yönetici:all |
| `announce_list()` building announcements | apsiyon | all |
| `complaint_list(status?)` | apsiyon | resident:own · kapıcı:assigned · yönetici:all |
| `cargo_status(flat?)` package log | local | resident:own · kapıcı:all |
| `elevator_status()` · `watertank_level()` | local IoT | all |
| `common_area_bookings()` | local | all |
| `building_directory()` | apsiyon/roster | yönetici:full · resident:limited |
| `budget_summary()` expenses/budget | apsiyon | yönetici |
| `camera_view(cam)` | local | resident:common · yönetici:all |
| `kapici_tasks()` | local | kapıcı · yönetici |
| `vote_list()` open decisions | apsiyon/local | all |

### Write tools (approval-gated)
| Tool | engine | risk · role |
|---|---|---|
| `complaint_create(text,loc)` resident report → yönetici inbox | apsiyon | low · resident |
| `complaint_assign(id,to)` · `complaint_resolve(id)` | apsiyon | low · yönetici,kapıcı |
| `announce_post(text)` → app+mesh | apsiyon | low · yönetici |
| `aidat_remind(flats)` | apsiyon | low · yönetici |
| `aidat_receipt(flat,amount)` | apsiyon | **financial** · yönetici |
| `common_area_book(area,time)` | local | low · resident |
| `cargo_log(flat,desc)` → notify resident | local | low · kapıcı |
| `visitor_log(...)` | local | low · kapıcı |
| `vote_create(q)` · `vote_cast(id,choice)` | apsiyon/local | low · yönetici/resident |
| `common_device_control(zone,action)` common-area only | local | low · yönetici,kapıcı |
| `kapici_task_create(...)` · `kapici_task_done(id)` | local | low · yönetici/kapıcı |

### Briefings
- **resident:** own aidat status, new announcements, own cargo, own complaint updates.
- **yönetici:** new-complaint count, late-aidat flats, watertank %, scheduled maintenance, booking requests, budget alerts.
- **kapıcı:** today's tasks, pending packages, assigned complaints.

**Net-new:** Apsiyon connector, building-IoT bindings, kargo connector, role routing.

---

## 2. NGO Pack

**Roles:** `field`, `coordinator`, `management`.
**Wraps:** beneficiary DB / CRM (wrap existing, else Yaver-native `forms`/`data`),
donor platform, accounting, `email`/`newsletter`. **Beneficiary data local-only
(KVKK / vulnerable people)** — the wedge.

### Read tools
| Tool | engine | scope |
|---|---|---|
| `beneficiary_lookup(id/name)` | local/api | field · coordinator (privacy-scoped) |
| `case_status(id)` | local/api | field · coordinator |
| `inventory_status(item?)` supplies | local | all |
| `program_calendar()` visits/events | local/api | all |
| `volunteer_schedule()` | local | coordinator |
| `distribution_today()` aid given | local | field · coordinator |
| `donor_summary()` · `grant_deadlines()` | api/web | management |
| `financial_report(period)` | api/local | management |

### Write tools (approval-gated)
| Tool | engine | risk · role |
|---|---|---|
| `beneficiary_intake(form)` new record | local | low (privacy) · field |
| `case_update(id,note)` | local/api | low · field,coordinator |
| `inventory_adjust(item,delta)` log distribution | local | low · field,coordinator |
| `report_generate(template)` | local | low · coordinator,management |
| `donor_message_send(...)` | api/email | low · management |
| `volunteer_assign(...)` | local | low · coordinator |

Field: **offline forms sync over mesh/LAN**; **`translate`** + voice STT/TTS for
beneficiary comms.

### Briefings
- **field:** today's visits, cases needing follow-up, translation ready.
- **coordinator:** stock levels, volunteer gaps, report deadlines.
- **management:** grant deadlines, financial alerts, donor pipeline.

**Net-new:** beneficiary-DB connector (or Yaver-native), donor/accounting connectors.

---

## 3. Muhtar Pack

**Roles:** `muhtar`, `staff` (aza/assistant).
**Wraps:** **e-Devlet muhtar module** (read-only, careful, explicit consent),
local resident registry (**Yaver-hosted, KVKK-local**), belediye portals.

### Read tools
| Tool | engine | scope |
|---|---|---|
| `resident_lookup(name?)` | local (KVKK) | muhtar · staff |
| `household_count()` · `mahalle_stats()` | local | all |
| `request_list(status?)` document/service requests | local | all |
| `complaint_list()` neighborhood issues | local | all |
| `belediye_notices()` | web/api | all |
| `env_dashboard()` air quality/noise | local IoT | all |
| `meeting_agenda()` | local | all |

### Write tools (approval-gated)
| Tool | engine | risk · role |
|---|---|---|
| `document_issue(type,resident)` ikametgah-style pdf | local | **high (official)** · muhtar |
| `announce_post(text)` → app+mesh+print | local | low · muhtar,staff |
| `request_resolve(id)` | local | low · muhtar,staff |
| `letter_draft(...)` official letter pdf | local | low (finalize=approval) · muhtar |
| `meeting_minutes_save(...)` | local | low · staff |
| `complaint_forward(id,belediye)` | web | low · muhtar,staff |

### Briefing (muhtar)
Pending document requests, today's meeting, new complaints, belediye notices, env status.

**Net-new:** e-Devlet muhtar connector (read-only, careful), KVKK-local registry,
belediye connector.

---

## 4. Home Pack

**Roles:** `adult` (admin), `member`, `kid` (limited). Family-scoped.
**Wraps:** personal gateway connectors (shared with assistant MVP) — bills,
bank(read), cargo, e-Devlet(read), email, calendar, e-commerce — + home IoT +
media.

### Read tools
| Tool | engine | scope |
|---|---|---|
| `bills_summary()` · `bill_due_soon()` aggregate utilities | web/api | adult,member |
| `bank_balance()` · `card_balance()` | api (open-banking) **read-only** | adult |
| `cargo_track(no?)` · `order_status(platform)` | api/web | all |
| `fine_check()` traffic fines (e-Devlet, careful) | web | adult |
| `appointment_slots(service)` MHRS/e-Devlet | web/redroid | adult,member |
| `pharmacy_oncall()` | api (**exists** `nobetci_eczane`) | all |
| `weather()` · `news_digest()` · `traffic_eta(dest)` · `transit_next(stop)` | api | all |
| `email_summary()` · `calendar_today()` | api | adult,member |
| `home_state()` sensors/devices · `camera_view(cam)` | local | adult,member |

### Write tools (approval-gated)
| Tool | engine | risk · role |
|---|---|---|
| `bill_pay(bill)` | web | **financial → last-mile** · adult |
| `order_place(...)` | web/redroid | **financial → last-mile** · adult |
| `appointment_book(slot)` | web/redroid | low · adult,member |
| `email_send(draft)` | api | low · adult,member |
| `home_control(device,action)` | local | low (auto for benign) · adult,member |
| `routine_run(name)` ("iyi geceler") | local | low · all |
| `shopping_list_add(item)` · `reminder_set(...)` | local | trivial (auto) · all |

### Briefing (morning digest)
Bills due, cargo in transit, weather, calendar, reminders, important emails, home status.

**Net-new:** the personal gateway connectors (bills/bank-read/cargo/e-Devlet-read/
e-commerce) — **shared with the assistant MVP**, so this pack is mostly assembly.

---

## Cross-pack build order

1. **Home pack first** — it shares connectors with the assistant MVP (most reuse),
   validates the read-mostly + approval + briefing loop with the simplest roles.
2. **Apartman pack** — the best commercial wedge; gated on the **Apsiyon connector**
   (verify their API first → sizes the effort).
3. **Muhtar pack** — KVKK-local registry + careful e-Devlet read; civic wedge.
4. **NGO pack** — beneficiary DB + donor connectors; privacy-local differentiator.

**Shared infrastructure (build once, all packs use):** the **MCP selection layer**
(role/mode/consent gating), the **approval inbox** + role routing, the **briefing
engine** (`routine_*`-driven role digests), **vision-heal** (MVP §3) for any
web/redroid connector, and the **vault/audit** plumbing (exists).

**Connector priority (by value × cleanliness):** kargo, bill-aggregation,
pharmacy(exists), weather/news/transit, email/calendar (clean APIs) → Apsiyon
(wedge) → e-Devlet-read & bank-read (high value, careful/official-only).
