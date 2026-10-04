#!/usr/bin/env node

import { readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const read = (path) => readFileSync(resolve(root, path), "utf8");
const filesUnder = (path) => readdirSync(resolve(root, path), { recursive: true })
  .filter((entry) => typeof entry === "string" && /\.(?:ts|tsx|js|mjs|toml)$/.test(entry))
  .map((entry) => `${path}/${entry}`);
const failures = [];
const forbid = (path, pattern, reason) => {
  if (pattern.test(read(path))) failures.push(`${path}: ${reason}`);
};

// Yaver-operated Cloudflare code is a tombstone/router only. It must not grow
// body parsing, credentials, model/provider calls, or secret-bearing storage.
for (const path of [...filesUnder("gateway/src"), "gateway/wrangler.toml"]) {
  forbid(path, /\b(?:OPENAI|ANTHROPIC|DEEPINFRA|OPENROUTER|ZAI|HCLOUD|GITHUB|GITLAB)_(?:API_)?(?:KEY|TOKEN|SECRET)\b/i,
    "provider credential entered the Cloudflare package");
  forbid(path, /\b(?:KVNamespace|R2Bucket|D1Database|Queue<|DurableObjectNamespace)\b/,
    "plaintext gateway regained a persistence binding");
}

for (const path of [
  "mobile/src/lib/codingAgent/sandboxBinding.ts",
  "web/lib/sandbox/gateway.ts",
  "desktop/agent/gateway_runner_env.go",
  "backend/convex/openrouterKeys.ts",
]) {
  forbid(path, /https:\/\/(?:api\.)?(?:openrouter|openai|anthropic|deepinfra|deepseek)\./i,
    "Yaver-managed inference/provider call re-entered a retired path");
  forbid(path, /(?:OPENROUTER|OPENAI|ANTHROPIC|DEEPINFRA|ZAI)_(?:API_)?(?:KEY|TOKEN|SECRET)/i,
    "Yaver-managed provider credential re-entered a retired path");
}
forbid("mobile/src/lib/codingAgent/sandboxBinding.ts", /managedCoding|loadManagedCodingConfig|gatewayUrl/i,
  "mobile can enable plaintext managed inference");
forbid("web/lib/sandbox/gateway.ts", /\bfetch\s*\(/,
  "browser can send plaintext prompts to the retired Yaver gateway");
forbid("desktop/agent/gateway_runner_env.go", /gatewayInjectEnv|mintGatewayToken|OPENAI_BASE_URL/i,
  "desktop can inject Yaver-managed inference credentials");
forbid("backend/convex/openrouterKeys.ts", /\bfetch\s*\(|process\.env/i,
  "Convex can provision or transmit a model-provider credential");

// Diagnostic ingestion stores classifications only. User-controlled message,
// data, and details fields are application plaintext and must be discarded.
forbid("backend/convex/http.ts", /message:\s*String\(body\.message|details:\s*body\.details|data:\s*body\.data/i,
  "cloud diagnostic ingestion stores arbitrary endpoint plaintext");
forbid("gateway/src/index.ts", /request\.(?:json|text|arrayBuffer|formData|blob)\s*\(/,
  "Cloudflare gateway reads an application request body");
forbid("gateway/src/index.ts", /headers\.get\(\s*["']authorization["']\s*\)/i,
  "Cloudflare gateway reads a bearer credential");
forbid("gateway/src/index.ts", /https:\/\/(?:api\.)?(?:openai|anthropic|deepinfra|openrouter|deepseek|hetzner|github|gitlab)\./i,
  "Cloudflare gateway calls a content/credential provider");
forbid("gateway/src/index.ts", /from\s+["'][^"']*(?:e2ee|crypto|decrypt|session)[^"']*["']/i,
  "Cloudflare code imported endpoint cryptography");

// Personal Hetzner credentials are native-endpoint-only. The direct adapter
// may call Hetzner, but must never mention Yaver/Convex/Cloudflare transports.
for (const path of ["mobile/src/lib/hetznerDirect.ts", "mobile/src/lib/hetznerDirectCore.ts"]) {
  forbid(path, /getConvexSiteUrl|convex\.site|workers\.dev|public\.yaver|relayPassword|quicClient|\/billing\/|\/byo\//i,
    "phone-direct Hetzner path can transit Yaver infrastructure");
  forbid(path, /console\.(?:log|error|warn)|analytics|posthog|sentry/i,
    "phone-direct provider path can log or report credential-adjacent data");
}
for (const path of [
  "backend/convex/http.ts",
  "backend/convex/openrouterKeys.ts",
  "gateway/src/index.ts",
]) {
  forbid(path, /clientCloudProvider|HetznerClientCloudAdapter|hetznerDirectCore/i,
    "endpoint cloud-provider adapter entered Yaver backend infrastructure");
}

// Backend/relay packages can import only the opaque envelope contract. Actual
// decryptors live under trusted endpoint code and are banned by path/name.
for (const path of ["gateway/src/index.ts", "relay/mesh.go"]) {
  forbid(path, /desktop\/agent\/e2ee|Decrypt\s*\(|deriveSessionKeys|privateKey/i,
    "control-plane process gained endpoint decryption capability");
}

if (failures.length) {
  console.error("Zero-knowledge boundary check failed:\n" + failures.map((f) => `- ${f}`).join("\n"));
  process.exit(1);
}
console.log("zero-knowledge boundary: PASS");
