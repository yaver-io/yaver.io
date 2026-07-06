"use client";

// SchemaForm — renders native form controls from a JSON Schema and returns a
// typed payload on submit. Backs the generic ToolPanelView: every `ops` verb
// already publishes its payload schema via /ops/verbs, so a verb gets a form
// here instead of a hand-written panel.
//
// v1 handles the common subset: object (root + nested), string, string+enum,
// number/integer, boolean, and array-of-scalar. Anything it can't map (exotic
// items, oneOf/anyOf, no properties) falls back to a raw-JSON editor — never a
// broken form. A whole-form "raw JSON" toggle is the ultimate escape hatch.
//
// Optional `x-yaver-ui` hints are honoured additively (label, widget); absent
// hints just use sensible defaults.

import { useMemo, useState } from "react";

export type JSONSchema = {
  type?: string | string[];
  properties?: Record<string, JSONSchema>;
  required?: string[];
  enum?: any[];
  items?: JSONSchema;
  format?: string;
  description?: string;
  title?: string;
  default?: any;
  minimum?: number;
  maximum?: number;
  ["x-yaver-ui"]?: { label?: string; widget?: string; [k: string]: any };
  [k: string]: any;
};

const inputCls =
  "w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 focus:border-indigo-500 focus:outline-none";
const labelCls = "block text-xs font-semibold text-surface-400 mb-1";

function primaryType(s: JSONSchema): string {
  const t = s.type;
  if (Array.isArray(t)) return String(t.find((x) => x !== "null") || t[0] || "string");
  return String(t || (s.enum ? "string" : s.properties ? "object" : "string"));
}

function fieldLabel(name: string, s: JSONSchema): string {
  return s["x-yaver-ui"]?.label || s.title || name;
}

// getAt/setAt do immutable path-based updates on the nested form value.
function setAt(root: any, path: string[], value: any): any {
  if (path.length === 0) return value;
  const [head, ...rest] = path;
  const base = root && typeof root === "object" ? root : {};
  return { ...base, [head]: setAt(base[head], rest, value) };
}
function getAt(root: any, path: string[]): any {
  return path.reduce((acc, k) => (acc == null ? undefined : acc[k]), root);
}

// Strip empty optionals and coerce; enforce required. Returns {payload, missing}.
function buildPayload(schema: JSONSchema, value: any): { payload: any; missing: string[] } {
  const missing: string[] = [];

  function walk(s: JSONSchema, v: any, label: string): any {
    const t = primaryType(s);
    if (t === "object" && s.properties) {
      const out: Record<string, any> = {};
      const req = new Set(s.required || []);
      for (const [key, propSchema] of Object.entries(s.properties)) {
        const child = walk(propSchema, v?.[key], key);
        const isEmpty =
          child === undefined ||
          child === "" ||
          (Array.isArray(child) && child.length === 0);
        if (isEmpty) {
          if (req.has(key)) missing.push(fieldLabel(key, propSchema));
          continue;
        }
        out[key] = child;
      }
      return out;
    }
    if (t === "array") {
      const arr = Array.isArray(v) ? v : [];
      const items = s.items || {};
      const it = primaryType(items);
      return arr
        .map((el) => (it === "number" || it === "integer" ? (el === "" ? undefined : Number(el)) : el))
        .filter((el) => el !== undefined && el !== "");
    }
    if (t === "number" || t === "integer") {
      return v === "" || v === undefined || v === null ? undefined : Number(v);
    }
    if (t === "boolean") return v === true;
    // string (and unknown scalars)
    return v === undefined || v === null ? "" : v;
  }

  const payload = walk(schema, value, "root");
  return { payload, missing };
}

function ScalarField({
  schema,
  value,
  onChange,
}: {
  schema: JSONSchema;
  value: any;
  onChange: (v: any) => void;
}) {
  const t = primaryType(schema);
  const widget = schema["x-yaver-ui"]?.widget;

  if (schema.enum && schema.enum.length) {
    return (
      <select className={inputCls} value={value ?? ""} onChange={(e) => onChange(e.target.value)}>
        <option value="">—</option>
        {schema.enum.map((opt) => (
          <option key={String(opt)} value={String(opt)}>
            {String(opt)}
          </option>
        ))}
      </select>
    );
  }
  if (t === "boolean") {
    return (
      <label className="flex items-center gap-2 text-sm text-surface-300">
        <input
          type="checkbox"
          checked={value === true}
          onChange={(e) => onChange(e.target.checked)}
          className="h-4 w-4 rounded border-surface-600 bg-surface-900 text-indigo-500"
        />
        <span className="text-surface-500">{value === true ? "true" : "false"}</span>
      </label>
    );
  }
  if (t === "number" || t === "integer") {
    return (
      <input
        type="number"
        className={inputCls}
        value={value ?? ""}
        min={schema.minimum}
        max={schema.maximum}
        step={t === "integer" ? 1 : "any"}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  // string variants
  const multiline =
    widget === "textarea" ||
    schema.format === "csv" ||
    schema.format === "textarea" ||
    schema.format === "multiline";
  if (multiline) {
    return (
      <textarea
        className={`${inputCls} font-mono min-h-[96px]`}
        value={value ?? ""}
        placeholder={schema.format === "csv" ? "paste CSV…" : undefined}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  return (
    <input
      type={schema.format === "password" ? "password" : "text"}
      className={inputCls}
      value={value ?? ""}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

function ArrayField({
  schema,
  value,
  onChange,
}: {
  schema: JSONSchema;
  value: any;
  onChange: (v: any) => void;
}) {
  const items = schema.items || { type: "string" };
  const it = primaryType(items);
  const arr: any[] = Array.isArray(value) ? value : [];

  // Only scalar item arrays get a repeatable editor; objects fall back to raw.
  if (it === "object" || items.properties) {
    return <RawField value={value} onChange={onChange} hint="array of objects — edit as JSON" />;
  }
  return (
    <div className="space-y-1.5">
      {arr.map((el, i) => (
        <div key={i} className="flex gap-1.5">
          <ScalarField
            schema={items}
            value={el}
            onChange={(v) => {
              const next = arr.slice();
              next[i] = v;
              onChange(next);
            }}
          />
          <button
            type="button"
            onClick={() => onChange(arr.filter((_, j) => j !== i))}
            className="shrink-0 rounded-lg border border-surface-700 px-2 text-surface-500 hover:text-red-400"
          >
            ✕
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={() => onChange([...arr, it === "number" || it === "integer" ? "" : ""])}
        className="text-xs font-semibold text-indigo-400 hover:text-indigo-300"
      >
        + add item
      </button>
    </div>
  );
}

function RawField({
  value,
  onChange,
  hint,
}: {
  value: any;
  onChange: (v: any) => void;
  hint?: string;
}) {
  const [text, setText] = useState(() => {
    try {
      return value === undefined ? "" : JSON.stringify(value, null, 2);
    } catch {
      return "";
    }
  });
  const [err, setErr] = useState<string | null>(null);
  return (
    <div className="space-y-1">
      <textarea
        className={`${inputCls} font-mono min-h-[96px]`}
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          if (e.target.value.trim() === "") {
            setErr(null);
            onChange(undefined);
            return;
          }
          try {
            onChange(JSON.parse(e.target.value));
            setErr(null);
          } catch (x: any) {
            setErr("invalid JSON");
          }
        }}
      />
      <div className="flex justify-between text-[10px]">
        <span className="text-surface-600">{hint}</span>
        {err && <span className="text-red-400">{err}</span>}
      </div>
    </div>
  );
}

function Field({
  name,
  schema,
  path,
  root,
  onChange,
  required,
}: {
  name: string;
  schema: JSONSchema;
  path: string[];
  root: any;
  onChange: (path: string[], v: any) => void;
  required: boolean;
}) {
  const t = primaryType(schema);
  const value = getAt(root, path);

  const control = (() => {
    if (t === "object" && schema.properties) {
      return (
        <div className="space-y-3 rounded-lg border border-surface-800 bg-surface-950/40 p-3">
          <ObjectFields schema={schema} path={path} root={root} onChange={onChange} />
        </div>
      );
    }
    if (t === "object") {
      // free-form object without declared properties
      return <RawField value={value} onChange={(v) => onChange(path, v)} hint="object — edit as JSON" />;
    }
    if (t === "array") {
      return <ArrayField schema={schema} value={value} onChange={(v) => onChange(path, v)} />;
    }
    return <ScalarField schema={schema} value={value} onChange={(v) => onChange(path, v)} />;
  })();

  return (
    <div>
      <label className={labelCls}>
        {fieldLabel(name, schema)}
        {required && <span className="ml-1 text-red-400">*</span>}
        <span className="ml-1 font-normal text-surface-600">({t})</span>
      </label>
      {control}
      {schema.description && <p className="mt-1 text-[11px] text-surface-600">{schema.description}</p>}
    </div>
  );
}

function ObjectFields({
  schema,
  path,
  root,
  onChange,
}: {
  schema: JSONSchema;
  path: string[];
  root: any;
  onChange: (path: string[], v: any) => void;
}) {
  const req = new Set(schema.required || []);
  const entries = Object.entries(schema.properties || {});
  return (
    <>
      {entries.map(([key, propSchema]) => (
        <Field
          key={key}
          name={key}
          schema={propSchema}
          path={[...path, key]}
          root={root}
          onChange={onChange}
          required={req.has(key)}
        />
      ))}
    </>
  );
}

export default function SchemaForm({
  schema,
  submitting,
  submitLabel = "Run",
  onSubmit,
}: {
  schema?: JSONSchema;
  submitting?: boolean;
  submitLabel?: string;
  onSubmit: (payload: any) => void;
}) {
  const [value, setValue] = useState<any>({});
  const [raw, setRaw] = useState(false);
  const [rawText, setRawText] = useState("{}");
  const [missing, setMissing] = useState<string[]>([]);
  const [rawErr, setRawErr] = useState<string | null>(null);

  const hasProps = useMemo(
    () => !!schema && primaryType(schema) === "object" && !!schema.properties && Object.keys(schema.properties).length > 0,
    [schema],
  );

  function handleChange(path: string[], v: any) {
    setValue((prev: any) => setAt(prev, path, v));
  }

  function submit() {
    if (raw || !hasProps) {
      try {
        const parsed = rawText.trim() === "" ? {} : JSON.parse(rawText);
        setRawErr(null);
        onSubmit(parsed);
      } catch {
        setRawErr("invalid JSON");
      }
      return;
    }
    const { payload, missing: miss } = buildPayload(schema!, value);
    setMissing(miss);
    if (miss.length) return;
    onSubmit(payload);
  }

  return (
    <div className="space-y-4">
      {hasProps && (
        <div className="flex items-center justify-between">
          <span className="text-[11px] uppercase tracking-wide text-surface-600">Parameters</span>
          <button
            type="button"
            onClick={() => setRaw((r) => !r)}
            className="text-[11px] font-semibold text-surface-500 hover:text-indigo-400"
          >
            {raw ? "form editor" : "raw JSON"}
          </button>
        </div>
      )}

      {raw || !hasProps ? (
        <div className="space-y-1">
          {!hasProps && (
            <p className="text-[11px] text-surface-600">
              No declared parameters — edit the payload as JSON (or just run with {"{}"}).
            </p>
          )}
          <textarea
            className={`${inputCls} font-mono min-h-[140px]`}
            value={rawText}
            onChange={(e) => setRawText(e.target.value)}
          />
          {rawErr && <p className="text-[11px] text-red-400">{rawErr}</p>}
        </div>
      ) : (
        <div className="space-y-3">
          <ObjectFields schema={schema!} path={[]} root={value} onChange={handleChange} />
        </div>
      )}

      {missing.length > 0 && (
        <p className="text-[11px] text-red-400">Required: {missing.join(", ")}</p>
      )}

      <button
        type="button"
        onClick={submit}
        disabled={submitting}
        className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-500 disabled:opacity-50"
      >
        {submitting ? "Running…" : submitLabel}
      </button>
    </div>
  );
}
