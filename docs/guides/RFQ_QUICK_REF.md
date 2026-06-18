# RFQ Engine - Quick Reference for AI Agents

## TL;DR
Local-first manufacturing RFQ/BOM assist with **pixel seeding** for deep visual analysis. Ops verbs store data in `~/.yaver/mfg-rfq/*.json`. Chromedp loop runs on Hetzner server using vision/OCR from OpenRouter to extract wire harness pricing from screenshots seeded by pixel coordinates.

## Ops Verbs

```bash
# Import BOM (creates workspace)
yaver ops mfg_rfq_import_bom '{"id":"sampo","path":"/path/to/bom.csv","meta":{"customer":"simkab"}}'

# Get workspace
yaver ops mfg_rfq_get '{"id":"sampo"}'

# Update BOM line (canonical source for quantity/location)
yaver ops mfg_bom_line_update '{"id":"sampo","lineRef":"R101","quantity":500}'

# Add pixel seed for deep analysis (x,y,w,h = screen region)
yaver ops mfg_pixel_seed_upsert '{"id":"sampo","seed":{"lineRef":"R101","x":100,"y":200,"w":300,"h":50,"note":"pricing section for R101"}}'

# Delete pixel seed
yaver ops mfg_pixel_seed_delete '{"id":"sampo","seedId":"seed_r101_xyz"}}'
```

## Data Structure

### BOM Line
```json
{
  "ref": "R101",
  "qty": 500,
  "part": "A-12345-ND",
  "description": "Wire Harness 10-pin",
  "package": "ND",
  "supplierPn": "WIRE-12345",
  "lcsc": "C123456",
  "unitUsd": 0.05,
  "location": "Zone B"
}
```

### Pixel Seed (for visual analysis)
```json
{
  "id": "seed_r101_123abc",
  "lineRef": "R101",
  "x": 100,
  "y": 200,
  "w": 300,
  "h": 50,
  "location": "Zone B",
  "quantity": 500,
  "note": "pricing section for R101"
}
```

## Storage Location

```bash
~/.yaver/mfg-rfq/
├── sampo.json
└── sampo-bak-20240118.json (auto-backup before write)
```

## Sync Rules

- Seeds ↔ BOM lines auto-sync (quantity/location)
- BOM line update cascades to all associated seeds
- Delete seed does NOT delete BOM line (manual-only seed removal)

## Testing Environment

**Hetzner Test Server** (`selected-test-box`)
- Chromedp driver: `desktop/agent/testkit/driver_chromecdp.go`
- Vision/OCR: OpenRouter via `desktop/agent/testkit/visual_llm.go`
- Headless Chrome with viewport emulation

## Vision/OCR Setup

Env vars (priority order):
1. `MISTRAL_API_KEY` → Mistral Pixtral
2. `OPENAI_API_KEY` → OpenAI GPT-4o-mini
3. `ANTHROPIC_API_KEY` → Anthropic Claude Haiku
4. Local Ollama fallback (no key needed, free)

Example:
```bash
export MISTRAL_API_KEY="..."
export YAVER_VISION_MODEL="pixtral-12b-2409"
```

## Deep Analysis Flow

1. Load RFQ workspace with pixel seeds
2. Launch chromedp on Hetzner (headless Chrome)
3. For each seed:
   - Navigate to quote page
   - Screenshot region defined by (x,y,w,h)
   - Send screenshot to vision/OCR
   - Extract: unit price, lead time, part number
   - Validate against BOM line data
4. Return structured analysis (NO mutations)

## Workspaces

The per-org RFQ workspace ID is effectively the project/session ID:
- `sampo` - Wire harness RFQ for Simkab
- Org filtering via `meta` field

## Related Files

- `desktop/agent/ops_mfg.go` - Ops verbs implementation
- `desktop/agent/ops_mfg_test.go` - Unit tests
- `desktop/agent/testkit/driver_chromecdp.go` - Chromedp CDP driver
- `desktop/agent/testkit/visual_llm.go` - Vision/OCR integration
- `backend/convex/companyAIOptions.ts` - Company AI options (talos_harness_*)
- `docs/yaver-talos-ghost-erp-migration.md` - Ghost ERP architecture

## Git History

Recent commit:
```
99704bd30 Add RFQ BOM manual assist ops
```

## Next Steps

- Add `mfg_rfq_deep_analyze` ops verb to run chromedp loop
- Add Convex backend table for RFQ workspaces
- Configure orgId filtering in deep analysis
- Add Hetzner provisioning script for RFQ test environment