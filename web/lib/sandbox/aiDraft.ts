"use client";

// aiDraft.ts — turn a natural-language prompt into a sandbox project spec
// (schema + auth personas + seed rows + app screens) using the Yaver Gateway.
// The model only DRAFTS the plan; the result is parsed/validated and applied
// locally via createLocalProject — it never becomes hidden state. Inference
// runs through the owner's GLM capacity securely (see gateway.ts).

import type { PhoneCreateSpec } from "@/lib/agent-client";
import { chatComplete } from "./gateway";

const SYSTEM_PROMPT = `You are Yaver's app schema designer. Given an app idea, output ONLY a JSON object (no prose, no markdown fences) describing a small SQLite-backed app.

Shape:
{
  "name": "Human Name",
  "schema": { "tables": [ { "name": "snake_case", "columns": [ { "name": "id", "type": "text", "primary": true, "default": "uuid" }, { "name": "title", "type": "text", "required": true } ] } ] },
  "auth": { "personas": [ { "id": "alice", "email": "alice@example.com", "name": "Alice" } ] },
  "seed": { "table_name": [ { "id": "x", "title": "Example" } ] },
  "app": { "summary": "one line", "primaryEntity": "table_name", "screens": [ { "id": "list", "title": "Items", "kind": "list", "table": "table_name" } ] }
}

Rules:
- Column types MUST be one of: text, int, bool, real, timestamp, json, uuid.
- Every table needs a primary key column (type text, default "uuid", or an explicit id).
- Use default "now" for created_at/updated_at timestamps.
- Include a "users" table when the app has ownership, plus 1-2 seed personas.
- Keep it to 1-4 tables. Seed 1-3 example rows per primary table.
- Output strict JSON only.`;

export interface DraftedSpec extends PhoneCreateSpec {
  name: string;
}

function extractJson(raw: string): string {
  const trimmed = raw.trim();
  // Strip ```json fences if the model added them despite instructions.
  const fence = trimmed.match(/```(?:json)?\s*([\s\S]*?)```/);
  if (fence) return fence[1].trim();
  const start = trimmed.indexOf("{");
  const end = trimmed.lastIndexOf("}");
  if (start >= 0 && end > start) return trimmed.slice(start, end + 1);
  return trimmed;
}

const ALLOWED_TYPES = new Set(["text", "string", "int", "integer", "bool", "boolean", "real", "float", "timestamp", "json", "uuid"]);

/** Draft a project spec from a prompt. Throws GatewayError on auth/limit. */
export async function draftProjectFromPrompt(prompt: string, token: string, signal?: AbortSignal): Promise<DraftedSpec> {
  const content = await chatComplete({
    token,
    signal,
    maxTokens: 2048,
    messages: [
      { role: "system", content: SYSTEM_PROMPT },
      { role: "user", content: prompt },
    ],
  });

  let parsed: Record<string, unknown>;
  try {
    parsed = JSON.parse(extractJson(content)) as Record<string, unknown>;
  } catch {
    throw new Error("AI returned an invalid plan — try rephrasing the idea.");
  }

  const spec = sanitizeSpec(parsed, prompt);
  if (!spec.schema?.tables?.length) {
    throw new Error("AI plan had no tables — try a more specific idea.");
  }
  return spec;
}

// Defensive validation: keep only known fields/types so a bad draft can't
// produce invalid DDL when applied locally.
function sanitizeSpec(raw: Record<string, unknown>, prompt: string): DraftedSpec {
  const name = typeof raw.name === "string" && raw.name.trim() ? raw.name.trim() : prompt.slice(0, 40) || "New App";
  const rawSchema = (raw.schema ?? {}) as { tables?: unknown };
  const tables = Array.isArray(rawSchema.tables) ? rawSchema.tables : [];

  const cleanTables = tables
    .map((t) => {
      const tbl = t as { name?: unknown; columns?: unknown };
      if (typeof tbl.name !== "string" || !Array.isArray(tbl.columns)) return null;
      const columns = tbl.columns
        .map((c) => {
          const col = c as Record<string, unknown>;
          if (typeof col.name !== "string") return null;
          const type = typeof col.type === "string" && ALLOWED_TYPES.has(col.type.toLowerCase()) ? col.type : "text";
          return {
            name: col.name,
            type,
            primary: col.primary === true || undefined,
            required: col.required === true || undefined,
            unique: col.unique === true || undefined,
            default: typeof col.default === "string" ? col.default : undefined,
          };
        })
        .filter((c): c is NonNullable<typeof c> => c !== null);
      if (!columns.length) return null;
      // Guarantee a primary key.
      if (!columns.some((c) => c.primary)) columns[0].primary = true;
      return { name: tbl.name, columns };
    })
    .filter((t): t is NonNullable<typeof t> => t !== null);

  const rawAuth = (raw.auth ?? {}) as { personas?: unknown };
  const personas = Array.isArray(rawAuth.personas)
    ? rawAuth.personas
        .map((p) => {
          const persona = p as Record<string, unknown>;
          if (typeof persona.id !== "string" || typeof persona.email !== "string") return null;
          return { id: persona.id, email: persona.email, name: typeof persona.name === "string" ? persona.name : undefined };
        })
        .filter((p): p is NonNullable<typeof p> => p !== null)
    : [];

  const seed = (raw.seed && typeof raw.seed === "object" ? raw.seed : {}) as PhoneCreateSpec["seed"];
  const app = (raw.app && typeof raw.app === "object" ? raw.app : {}) as PhoneCreateSpec["app"];

  return {
    name,
    schema: { tables: cleanTables },
    auth: { personas },
    seed,
    app,
  };
}
