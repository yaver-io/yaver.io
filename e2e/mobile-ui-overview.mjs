#!/usr/bin/env node
// Source-checkout entrypoint. The canonical runner is embedded in the Yaver
// agent so installed binaries can execute the same headless lane without this
// repository being present.
import path from 'node:path';
import { fileURLToPath } from 'node:url';

process.env.YAVER_PLAYWRIGHT_ROOT ||= path.dirname(fileURLToPath(import.meta.url));
await import('../desktop/agent/mobile_ui_overview.mjs');
