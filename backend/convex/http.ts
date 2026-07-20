import { httpRouter } from "convex/server";
import { v } from "convex/values";
import { httpAction, internalAction } from "./_generated/server";
import { api, internal } from "./_generated/api";
import { sha256Hex, randomHex } from "./auth";
import { managedDeviceIdFor } from "./cloudMachines";
import { createRemoteJWKSet, jwtVerify } from "jose";
import { isOwnerEmail, isOwner } from "./ownerAllowlist";
import { decryptStoredOidcSecret } from "./admin";
import { estimatedHourlyCents, minimumReserveCents } from "./cloudLifecycle";
import {
  cloudMachineEligibleForPlacement,
  cloudMachineMeetsPlacement,
  cloudMachineTypeForPlacement,
  selectCloudMachineForPlacement,
  selectResizeSourceForPlacement,
} from "./cloudPlacementCapacity";
import {
  emailPasswordAuthEnabled,
  emailPasswordEmailAllowed,
  hashPassword,
  verifyPassword,
} from "./authPasswordPolicy";
import {
  githubAppEnvFromProcess,
  parseGitHubRepoFullName,
  requestGitHubInstallationTokenForRepo,
} from "./githubAppAuth";

// Apple Sign-In identity-token verification (audit 2026-07-13). JWKS is fetched
// + cached by jose. Audience = the native app bundle id; override via env if the
// bundle id changes so this never needs a code edit to stay correct.
const APPLE_JWKS = createRemoteJWKSet(
  new URL("https://appleid.apple.com/auth/keys"),
);
const APPLE_NATIVE_AUDIENCE = process.env.APPLE_NATIVE_AUDIENCE || "io.yaver.mobile";

const http = httpRouter();

// ── Helpers ──────────────────────────────────────────────────────────

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      "Content-Type": "application/json",
      "Access-Control-Allow-Origin": "*",
    },
  });
}

function errorResponse(message: string, status = 400): Response {
  return jsonResponse({ error: message }, status);
}

const PROMPT_FREE_METADATA_DENIED_KEYS = new Set([
  "title",
  "description",
  "prompt",
  "userprompt",
  "input",
  "body",
  "bodyjson",
  "workdir",
  "filepath",
  "filename",
  "files",
  "images",
  "imagedata",
  "stdout",
  "stderr",
  "secret",
  "token",
  "vault",
  "diff",
  "patch",
  "sourcecode",
  "customcommand",
  "gitremote",
  "gitbranch",
]);

function normalizedPayloadKey(key: string): string {
  return key.trim().toLowerCase().replace(/[_-]/g, "");
}

export function promptFreeMetadataBodyDeniedReason(value: unknown): string | null {
  const visit = (node: unknown, path: string, depth: number): string | null => {
    if (!node || typeof node !== "object" || depth > 8) return null;
    if (Array.isArray(node)) {
      for (let i = 0; i < node.length; i++) {
        const denied = visit(node[i], `${path}[${i}]`, depth + 1);
        if (denied) return denied;
      }
      return null;
    }
    for (const [key, child] of Object.entries(node as Record<string, unknown>)) {
      const normalized = normalizedPayloadKey(key);
      if (PROMPT_FREE_METADATA_DENIED_KEYS.has(normalized)) {
        const label = path ? `${path}.${key}` : key;
        return `Prompt-free metadata request must not include '${label}'`;
      }
      const denied = visit(child, path ? `${path}.${key}` : key, depth + 1);
      if (denied) return denied;
    }
    return null;
  };
  return visit(value, "", 0);
}

export function nonYaverManagedMachineDeniedMessage(): string {
  return "Refusing to mutate a non-Yaver-managed machine";
}

export function unsupportedProviderResourceDeniedMessage(): string {
  return "Refusing to mutate an unsupported provider resource";
}

export function yaverManagedMachineMutationDeniedReason(machine: any): string | null {
  if (!machine) return "Machine not found";
  if ((machine.origin ?? "managed") !== "managed") {
    return nonYaverManagedMachineDeniedMessage();
  }
  const provider = String(machine.provider || "hetzner").trim().toLowerCase();
  if (provider && provider !== "hetzner") {
    return unsupportedProviderResourceDeniedMessage();
  }
  return null;
}

function denyNonYaverManagedMachine(machine: any): Response | null {
  const reason = yaverManagedMachineMutationDeniedReason(machine);
  if (!reason) return null;
  return errorResponse(reason, reason === "Machine not found" ? 404 : 403);
}

function errorMessageIncludes(err: any, code: string): boolean {
  return String(err?.message || "").includes(code);
}

// Owner / private-preview check. Delegates to the single source of
// truth in ownerAllowlist.ts (env-var allowlist, never hardcoded).
function isCloudPreviewUser(
  email?: string | null,
  userId?: string | null,
): boolean {
  // Email OR userId allowlist — OAuth accounts often have no email,
  // so id-based is the reliable owner check (CLOUD_PREVIEW_OWNER_USER_IDS).
  return isOwner(email, userId);
}

// Who may touch managed-cloud control surfaces. Owner allowlist is always in;
// flip YAVER_CLOUD_PUBLIC=true to open Cloud Workspace controls to every
// authenticated user at launch. New paid access is subscription-based; legacy
// wallet routes remain fail-closed or internal so public source cannot mint
// extra compute outside the product guardrails.
function cloudAccessAllowed(email?: string | null, userId?: string | null): boolean {
  if (isCloudPreviewUser(email, userId)) return true;
  return parseBooleanEnv(process.env.YAVER_CLOUD_PUBLIC, false);
}

function parseBooleanEnv(value: string | undefined, fallback: boolean): boolean {
  if (!value) return fallback;
  const normalized = value.trim().toLowerCase();
  if (normalized === "true" || normalized === "1" || normalized === "yes" || normalized === "on") return true;
  if (normalized === "false" || normalized === "0" || normalized === "no" || normalized === "off") return false;
  return fallback;
}

function managedCloudIdleAutoOffEnabled(): boolean {
  return !parseBooleanEnv(process.env.YAVER_CLOUD_IDLE_DISABLE, false);
}

export function validateCustomerAutoParkRequest(body: any):
  | { ok: true; machineId: string; enabled: true; idleMinutes?: number }
  | { ok: false; error: string } {
  const machineId = String(body?.machineId ?? "").trim();
  if (!machineId) return { ok: false, error: "machineId is required" };
  if (typeof body?.enabled !== "boolean") {
    return { ok: false, error: "enabled (boolean) is required" };
  }
  if (body.enabled === false) {
    return { ok: false, error: "Cloud Workspace auto-close is required to protect your usage and Yaver's compute costs" };
  }
  const idleMinutes =
    typeof body.idleMinutes === "number" && body.idleMinutes > 0 ? body.idleMinutes : undefined;
  return { ok: true, machineId, enabled: true, ...(idleMinutes ? { idleMinutes } : {}) };
}

export function hasReusableManagedRelayForReconcile(
  relays: Array<{ status?: string | null }> | null | undefined,
): boolean {
  if (!Array.isArray(relays)) return false;
  return relays.some((relay) => {
    const status = String(relay?.status || "").trim().toLowerCase();
    return status !== "stopped" && status !== "error";
  });
}

export function legacyCreditPacksDisabledMessage(): string {
  return "Credit packs are not sold. Use Relay Pro or Cloud Workspace subscription billing.";
}

export function legacyCreditPackWebhookDisabledMessage(): string {
  return "Legacy one-time credit-pack webhooks are ignored for the flat subscription model.";
}

export function legacyPrepaidProvisionDisabledMessage(): string {
  return "Legacy prepaid workspace provisioning is disabled. Subscribe to Cloud Workspace on web.";
}

export function managedServiceCapabilitiesRetiredMessage(): string {
  return "Managed service capability toggles are retired. Use Relay Pro or Cloud Workspace subscription billing.";
}

export function genericMachineCreateDisabledMessage(): string {
  return "Direct Cloud Workspace machine creation is disabled. Subscribe to Cloud Workspace on web.";
}

function requireEmailPasswordAuthEnabled(): Response | null {
  if (emailPasswordAuthEnabled()) return null;
  return errorResponse("Email/password sign-in is disabled on this deployment", 403);
}

function requireEmailPasswordEmailAllowed(email: unknown): Response | null {
  if (emailPasswordEmailAllowed(email)) return null;
  return errorResponse("Email/password sign-in is not enabled for this email", 403);
}

// LemonSqueezy docs use LEMONSQUEEZY_* (no underscore between LEMON+SQUEEZY),
// but some deploys store LEMON_SQUEEZY_* (Convex default in this project).
// Accept either so a rename on one side doesn't silently break checkout.
function lsEnv(suffix: string): string | undefined {
  return process.env["LEMONSQUEEZY_" + suffix] ?? process.env["LEMON_SQUEEZY_" + suffix];
}

// Constant-time HMAC-SHA256 verification for LemonSqueezy webhooks.
// LS signs the raw body with LEMONSQUEEZY_WEBHOOK_SECRET and sends the hex
// digest in the X-Signature header. If no secret is configured we log a
// warning and accept — matches the previous "TODO" behaviour but surfaces it.
async function verifyLemonSqueezySignature(body: string, signatureHeader: string | null): Promise<boolean> {
  const secret = lsEnv("WEBHOOK_SECRET");
  if (!secret) {
    // FAIL CLOSED. The webhook credits wallets + provisions paid boxes;
    // an unsigned-accept here lets anyone who knows the (public, open-
    // source) Convex URL POST a forged order_created to mint unlimited
    // free credit. Reject unless the operator EXPLICITLY opts into
    // unsigned webhooks for LOCAL DEV ONLY via
    // LEMONSQUEEZY_ALLOW_UNSIGNED=true. Default (env unset) = reject.
    if (parseBooleanEnv(lsEnv("ALLOW_UNSIGNED"), false)) {
      console.warn("[lemonsqueezy] WEBHOOK_SECRET unset + ALLOW_UNSIGNED=true — accepting UNSIGNED webhook (DEV ONLY)");
      return true;
    }
    console.error("[lemonsqueezy] WEBHOOK_SECRET not set — rejecting webhook (fail-closed). Set the secret, or LEMONSQUEEZY_ALLOW_UNSIGNED=true for local dev only.");
    return false;
  }
  if (!signatureHeader) return false;
  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const sigBuf = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(body));
  const expected = Array.from(new Uint8Array(sigBuf))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  // Constant-time compare — same length + byte-by-byte xor.
  const a = expected;
  const b = signatureHeader.trim().toLowerCase();
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

async function hmacSha256Hex(secret: string, message: string): Promise<string> {
  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const sigBuf = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(message));
  return Array.from(new Uint8Array(sigBuf))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function constantTimeHexEqual(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

function normalizeCloudHost(baseUrl: string): string {
  try {
    return new URL(baseUrl).host;
  } catch {
    return baseUrl.replace(/^https?:\/\//, "").replace(/\/.*$/, "");
  }
}

async function createLemonSqueezyCheckout(args: {
  email: string;
  custom: Record<string, string>;
  // Defaults to the managed-cloud subscription variant. Credit-pack
  // checkout passes the resolved one-time pack variant id here.
  variantId?: string;
  variantEnvName?: string;
}): Promise<string> {
  const apiKey = lsEnv("API_KEY");
  const storeId = lsEnv("STORE_ID");
  const variantId = args.variantId ?? lsEnv("YAVER_CLOUD_VARIANT_ID");
  if (!apiKey || !storeId || !variantId) {
    const missing = [
      !apiKey && "API_KEY",
      !storeId && "STORE_ID",
      !variantId && (args.variantEnvName ?? "YAVER_CLOUD_VARIANT_ID"),
    ].filter(Boolean).join(", ");
    throw new Error(`Missing Lemon Squeezy configuration: ${missing}`);
  }

  const receiptLink =
    lsEnv("YAVER_CLOUD_RECEIPT_LINK_URL") ||
    lsEnv("CHECKOUT_REDIRECT_URL") ||
    process.env.NEXT_PUBLIC_BASE_URL ||
    "https://yaver.io/";
  const receiptButtonText =
    lsEnv("YAVER_CLOUD_RECEIPT_BUTTON_TEXT") || "Open Yaver";

  const payload = {
    data: {
      type: "checkouts",
      attributes: {
        checkout_data: {
          email: args.email,
          custom: args.custom,
        },
        checkout_options: {
          embed: false,
          media: false,
          logo: true,
        },
        product_options: {
          receipt_button_text: receiptButtonText,
          receipt_link_url: receiptLink,
          redirect_url: receiptLink,
        },
      },
      relationships: {
        store: { data: { type: "stores", id: storeId } },
        variant: { data: { type: "variants", id: variantId } },
      },
    },
  };

  const response = await fetch("https://api.lemonsqueezy.com/v1/checkouts", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${apiKey}`,
      Accept: "application/vnd.api+json",
      "Content-Type": "application/vnd.api+json",
    },
    body: JSON.stringify(payload),
  });
  const raw = await response.text();
  if (!response.ok) {
    throw new Error(`Lemon Squeezy checkout failed (${response.status}): ${raw}`);
  }

  const parsed = JSON.parse(raw);
  const url = parsed?.data?.attributes?.url;
  if (typeof url !== "string" || !url) {
    throw new Error("Lemon Squeezy checkout URL missing");
  }
  return url;
}

// Legacy credit-pack catalog. Public checkout and webhook crediting are disabled
// for the flat subscription model; keep the catalog only for old code references
// and explicit tests that prove it cannot mint customer balance.
const CREDIT_PACKS: Array<{ id: string; cents: number; label: string }> = [
  { id: "p10", cents: 1000, label: "$10" },
  { id: "p25", cents: 2500, label: "$25" },
  { id: "p50", cents: 5000, label: "$50" },
  { id: "p100", cents: 10000, label: "$100" },
];

function creditPackById(id: string) {
  return CREDIT_PACKS.find((p) => p.id === id) || null;
}

function creditPackVariantEnvName(id: string): string {
  return `CREDIT_PACK_${id.toUpperCase()}_VARIANT_ID`;
}

type CloudPurchasePlanId = "cloud-agent" | "cloud-workspace";
type BillingProductId = "relay-pro" | "cloud-workspace";

function normalizeCloudPurchasePlan(value: unknown): CloudPurchasePlanId {
  const normalized = String(value || "").trim();
  if (normalized === "cloud-workspace") return "cloud-workspace";
  return "cloud-agent";
}

export function normalizeBillingProduct(value: unknown): BillingProductId | null {
  const normalized = String(value || "").trim().toLowerCase();
  if (!normalized || normalized === "relay-pro" || normalized === "relay-monthly" || normalized === "relay-yearly" || normalized === "managed-relay") {
    return "relay-pro";
  }
  if (
    normalized === "cloud-workspace" ||
    normalized === "yaver-cloud" ||
    normalized === "cloud-agent" ||
    normalized === "cpu" ||
    normalized === "gpu"
  ) {
    return "cloud-workspace";
  }
  return null;
}

export function variantForBillingProduct(productId: BillingProductId): { variantId?: string; envName: string } {
  if (productId === "cloud-workspace") {
    return {
      variantId:
        lsEnv("YAVER_CLOUD_WORKSPACE_VARIANT_ID") ??
        lsEnv("YAVER_CLOUD_BYOK_VARIANT_ID") ??
        lsEnv("YAVER_CLOUD_VARIANT_ID"),
      envName: "YAVER_CLOUD_WORKSPACE_VARIANT_ID",
    };
  }
  return {
    variantId:
      lsEnv("YAVER_RELAY_PRO_VARIANT_ID") ??
      lsEnv("MANAGED_RELAY_VARIANT_ID") ??
      lsEnv("YAVER_RELAY_VARIANT_ID"),
    envName: "YAVER_RELAY_PRO_VARIANT_ID",
  };
}

export function productForSubscriptionPlan(plan: unknown): BillingProductId | "free" {
  const value = String(plan || "").trim();
  if (!value) return "free";
  if (value === "cloud-workspace" || value === "cloud-agent" || value.startsWith("yaver-cloud")) {
    return "cloud-workspace";
  }
  if (value === "relay-pro" || value === "relay-monthly" || value === "relay-yearly" || value === "managed-relay") {
    return "relay-pro";
  }
  return "free";
}

export function normalizeLemonSqueezySubscriptionStatus(status: unknown): string {
  const value = String(status || "").trim().toLowerCase();
  if (!value) return "unknown";
  return value.replace(/[^a-z0-9_-]/g, "_").slice(0, 80) || "unknown";
}

function normalizeCloudMachineType(value: unknown): "standard" | "heavy" | "build" | "cpu" | "gpu" {
  const normalized = String(value || "").trim().toLowerCase();
  if (normalized === "standard" || normalized === "heavy" || normalized === "build" || normalized === "cpu" || normalized === "gpu") {
    return normalized;
  }
  return "standard";
}

// Resolve a pack from the LemonSqueezy variant id that was ACTUALLY
// purchased (it rides in the signed webhook payload, so a buyer can't
// forge which pack they bought). This is the authoritative resolution —
// never trust pack_id / amount from client-set custom_data. Returns
// null if the variant isn't one of our configured packs.
function creditPackByVariantId(variantId: string | number | undefined | null) {
  if (variantId === undefined || variantId === null) return null;
  const want = String(variantId).trim();
  if (!want) return null;
  for (const p of CREDIT_PACKS) {
    const configured = lsEnv(creditPackVariantEnvName(p.id));
    if (configured && String(configured).trim() === want) return p;
  }
  return null;
}

// Cancel a subscription on LemonSqueezy itself (DELETE = cancel at
// period end). Yaver-initiated decommission / reconcile orphan-sweep
// must do this: cancelling only the Convex row stops Yaver's own
// reconcile, but a real paying customer would keep being billed by
// LemonSqueezy. Best-effort — a missing API key, a non-numeric
// (e2e/test) id, a 404, or a network error is logged and swallowed;
// the Convex row is already cancelled and is the source of truth for
// Yaver's gates. Scheduled from subscriptions.cancelById.
export const cancelLemonSqueezySubscription = internalAction({
  args: { lemonSqueezyId: v.string() },
  handler: async (_ctx, { lemonSqueezyId }) => {
    const apiKey = lsEnv("API_KEY");
    if (!apiKey) {
      console.warn("[lemonsqueezy] subscription cancel skipped — API_KEY not configured");
      return;
    }
    // Real LemonSqueezy subscription ids are numeric strings; e2e
    // fixtures use "e2e-…". Calling the API for a synthetic id just 404s.
    if (!/^[0-9]+$/.test(lemonSqueezyId)) {
      console.log(`[lemonsqueezy] subscription cancel skipped — non-numeric id "${lemonSqueezyId}" (test/dev sub)`);
      return;
    }
    try {
      const resp = await fetch(
        `https://api.lemonsqueezy.com/v1/subscriptions/${lemonSqueezyId}`,
        {
          method: "DELETE",
          headers: {
            Authorization: `Bearer ${apiKey}`,
            Accept: "application/vnd.api+json",
          },
        },
      );
      if (resp.ok || resp.status === 404) {
        console.log(`[lemonsqueezy] subscription ${lemonSqueezyId} cancelled (HTTP ${resp.status})`);
      } else {
        console.error(
          `[lemonsqueezy] subscription ${lemonSqueezyId} cancel returned HTTP ${resp.status}: ${await resp.text()}`,
        );
      }
    } catch (e) {
      console.error(`[lemonsqueezy] subscription ${lemonSqueezyId} cancel failed:`, e);
    }
  },
});

// LemonSqueezy variant swap helper for upgrading an existing subscription into
// Cloud Workspace. The public catalog no longer exposes Cloud Agent; hosted is
// retained as a legacy/operator tier, but the customer-facing target is the
// BYOK Cloud Workspace variant.
export const updateLemonSqueezyVariant = internalAction({
  args: { lemonSqueezyId: v.string(), tier: v.union(v.literal("hosted"), v.literal("byok")) },
  handler: async (_ctx, { lemonSqueezyId, tier }): Promise<{ ok: boolean; reason?: string }> => {
    const apiKey = lsEnv("API_KEY");
    const variantId = tier === "hosted"
      ? lsEnv("YAVER_CLOUD_HOSTED_VARIANT_ID")
      : (
          lsEnv("YAVER_CLOUD_WORKSPACE_VARIANT_ID") ??
          lsEnv("YAVER_CLOUD_BYOK_VARIANT_ID") ??
          lsEnv("YAVER_CLOUD_VARIANT_ID")
        );
    if (!apiKey) return { ok: false, reason: "no-api-key" };
    if (!variantId) return { ok: false, reason: "variant-unconfigured" };
    if (!/^[0-9]+$/.test(lemonSqueezyId)) {
      console.log(`[lemonsqueezy] variant swap skipped — non-numeric id "${lemonSqueezyId}" (test/dev sub)`);
      return { ok: true, reason: "test-sub" };
    }
    try {
      const resp = await fetch(`https://api.lemonsqueezy.com/v1/subscriptions/${lemonSqueezyId}`, {
        method: "PATCH",
        headers: {
          Authorization: `Bearer ${apiKey}`,
          Accept: "application/vnd.api+json",
          "Content-Type": "application/vnd.api+json",
        },
        body: JSON.stringify({
          data: {
            type: "subscriptions",
            id: lemonSqueezyId,
            attributes: { variant_id: Number(variantId) },
          },
        }),
      });
      if (resp.ok) return { ok: true };
      console.error(`[lemonsqueezy] variant swap HTTP ${resp.status}: ${await resp.text()}`);
      return { ok: false, reason: `http-${resp.status}` };
    } catch (e) {
      console.error("[lemonsqueezy] variant swap failed:", e);
      return { ok: false, reason: "threw" };
    }
  },
});

async function attachPreviewMachineToSharedServer(
  ctx: { runMutation: (mutation: any, args: any) => Promise<any> },
  machineId: string,
  region: string,
) {
  const baseUrl =
    process.env.YAVER_CLOUD_PREVIEW_BASE_URL ||
    process.env.YAVER_CLOUD_BASE_URL ||
    process.env.YAVER_CLOUD_PUBLIC_BASE_URL ||
    "https://cloud.yaver.io";
  const hostname =
    process.env.YAVER_CLOUD_PREVIEW_HOSTNAME ||
    process.env.YAVER_CLOUD_PUBLIC_HOSTNAME ||
    normalizeCloudHost(baseUrl);
  const serverIp =
    process.env.YAVER_CLOUD_PREVIEW_SERVER_IP ||
    process.env.YAVER_CLOUD_PUBLIC_SERVER_IP;
  const providerId =
    process.env.YAVER_CLOUD_PREVIEW_PROVIDER_ID ||
    `preview-${region}`;

  await ctx.runMutation(internal.cloudMachines.updateStatus, {
    machineId,
    status: "active",
    hostname,
    serverIp,
    hetznerServerId: providerId,
  });
}

async function ensurePreviewCloudMachine(
  ctx: {
    runQuery: (query: any, args: any) => Promise<any>;
    runMutation: (mutation: any, args: any) => Promise<any>;
  },
  userDocId: string,
  region: string,
) {
  const machines = await ctx.runQuery(internal.cloudMachines.listForUser, { userId: userDocId });
  const existing = Array.isArray(machines)
    ? machines.find((machine) => (machine.machineType === "standard" || machine.machineType === "cpu") && machine.status !== "stopped")
    : null;
  if (existing?._id) {
    await attachPreviewMachineToSharedServer(ctx, existing._id, region);
    return existing._id;
  }

  const machineId = await ctx.runMutation(internal.cloudMachines.createPreviewSharedMachine, {
    userId: userDocId,
    region,
  });
  await attachPreviewMachineToSharedServer(ctx, machineId, region);
  return machineId;
}

/** Extract Bearer token from Authorization header, hash it, and validate. */
async function authenticateRequest(
  ctx: { runQuery: (query: any, args: any) => Promise<any> },
  request: Request
): Promise<{
  userDocId: string;
  userId: string;
  email: string;
  fullName: string;
  provider: string;
  avatarUrl?: string;
  surveyCompleted: boolean;
  emailVerified: boolean;
  isOwner: boolean;
  /** Auth scope. "machine" = a managed-box token, denied on account-level +
   *  spend routes; "full"/undefined = a normal owner login. */
  scope: "full" | "machine";
} | null> {
  const authHeader = request.headers.get("Authorization");
  if (!authHeader?.startsWith("Bearer ")) return null;

  const token = authHeader.slice(7);
  const tokenHash = await sha256Hex(token);

  const result = await ctx.runQuery(api.auth.validateSession, { tokenHash });
  if (!result) return null;
  return {
    userDocId: String(result.userDocId),
    userId: result.userId,
    email: result.email,
    fullName: result.fullName,
    provider: result.provider,
    avatarUrl: result.avatarUrl,
    // Forward survey state so /auth/validate's response includes it.
    // Without this, mobile's AuthContext sees surveyCompleted=false on
    // every cold start and falls back to /survey — and any hiccup on
    // that fallback (5s timeout, transient 5xx) silently shows the
    // onboarding form to a returning user.
    surveyCompleted: !!result.surveyCompleted,
    // emailVerified gates the "Add OAuth provider via email match" flow
    // and powers Settings UI banners. OAuth-signup users are verified
    // by construction; email + passkey signups start unverified and
    // graduate via /auth/verify-email/confirm.
    emailVerified: result.emailVerified === true,
    // Owner flag drives owner-only hardware-cell visibility on every client
    // (web/mobile/daemon) without shipping any owner identity to the client.
    isOwner: (result as { isOwner?: boolean }).isOwner === true,
    scope: (result as { scope?: "full" | "machine" }).scope === "machine" ? "machine" : "full",
  };
}

/**
 * requireFullScope — deny a MACHINE-scoped token (a managed box's session) on
 * account-level / spend routes. A rooted box holds a full-owner *bearer* today;
 * this collapses its blast radius: the box can heartbeat + run its own tasks,
 * but it can NEVER provision, spend, or park/wake machines from the billing API.
 * Returns an error Response to return early, or null to proceed.
 */
function requireFullScope(
  auth: { scope: "full" | "machine" },
): Response | null {
  if (auth.scope === "machine") {
    return errorResponse(machineScopeDeniedMessage(), 403);
  }
  return null;
}

export function machineScopeDeniedMessage(): string {
  return "This token is machine-scoped and cannot perform account-level operations.";
}

/**
 * requireServerSecret — gate the server-to-server auth-provisioning routes
 * (`/auth/create-session`, `/auth/upsert-user`, `/auth/totp/create-pending`).
 * These are called by the web OAuth callback AFTER it has verified the provider
 * assertion; they trust a body-supplied `userId`/`email`, so without this gate
 * anyone who knows the deployment URL can mint a session or upsert-identity for
 * an arbitrary user (full account takeover — audit C1/C2/C7).
 *
 * The caller proves it is our own web backend by presenting the shared secret
 * `CONVEX_INTERNAL_SECRET` in the `X-Internal-Secret` header. Compared via SHA-256
 * hex (constant-length, constant-time) so neither the value nor its length leaks.
 *
 * STAGED ROLLOUT (fail-open until provisioned): when `CONVEX_INTERNAL_SECRET` is
 * unset on the deployment the check is skipped, so deploying this code cannot
 * break login before the web side is shipped with the header. Sequence:
 *   1. deploy this (env unset → no enforcement),
 *   2. ship web sending `X-Internal-Secret`,
 *   3. `npx convex env set CONVEX_INTERNAL_SECRET <secret>` → enforcement turns on.
 */
async function requireServerSecret(request: Request): Promise<Response | null> {
  const expected = process.env.CONVEX_INTERNAL_SECRET;
  if (!expected) return null; // not yet provisioned — staged rollout, allow
  const provided = request.headers.get("X-Internal-Secret") || "";
  const [a, b] = await Promise.all([sha256Hex(provided), sha256Hex(expected)]);
  if (a !== b) return errorResponse("Forbidden", 403);
  return null;
}

async function createSessionToken(
  ctx: { runMutation: (m: any, args: any) => Promise<any> },
  userId: any,
  deviceId?: string,
) {
  const tokenBytes = new Uint8Array(32);
  crypto.getRandomValues(tokenBytes);
  const token = Array.from(tokenBytes)
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  const tokenHash = await sha256Hex(token);
  const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;
  await ctx.runMutation(internal.auth.createSession, { tokenHash, userId, deviceId, expiresAt });
  return token;
}

// Best-effort per-instance login throttling. Convex may run multiple action
// instances, but this still blocks rapid password spraying against a hot shard.
type LoginAttempt = {
  failures: number;
  resetAt: number;
  lockedUntil: number;
};

const loginAttempts = new Map<string, LoginAttempt>();
const LOGIN_FAILURE_WINDOW_MS = 15 * 60 * 1000;
const LOGIN_LOCK_MS = 15 * 60 * 1000;
const LOGIN_MAX_FAILURES = 5;

function clientIp(request: Request): string {
  const cf = request.headers.get("CF-Connecting-IP");
  if (cf) return cf.trim();
  const forwarded = request.headers.get("X-Forwarded-For");
  if (forwarded) return forwarded.split(",")[0]?.trim() || "unknown";
  return request.headers.get("X-Real-IP")?.trim() || "unknown";
}

function loginAttemptKey(request: Request, email: string): string {
  return `${email.toLowerCase().trim()}|${clientIp(request)}`;
}

function loginLocked(key: string): boolean {
  const entry = loginAttempts.get(key);
  if (!entry) return false;
  const now = Date.now();
  if (entry.lockedUntil > now) return true;
  if (entry.resetAt <= now) loginAttempts.delete(key);
  return false;
}

function recordLoginFailure(key: string) {
  const now = Date.now();
  const current = loginAttempts.get(key);
  const entry: LoginAttempt = current && current.resetAt > now
    ? current
    : { failures: 0, resetAt: now + LOGIN_FAILURE_WINDOW_MS, lockedUntil: 0 };
  entry.failures += 1;
  if (entry.failures >= LOGIN_MAX_FAILURES) {
    entry.lockedUntil = now + LOGIN_LOCK_MS;
  }
  loginAttempts.set(key, entry);
}

function clearLoginFailures(key: string) {
  loginAttempts.delete(key);
}

// ── CORS Preflight for Auth Endpoints ───────────────────────────────
// Browsers send OPTIONS preflight when requests include Authorization headers.

const corsHeaders = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
  "Access-Control-Allow-Headers": "Authorization, Content-Type",
  "Access-Control-Max-Age": "86400",
};

function corsPreflightResponse(): Response {
  return new Response(null, { status: 204, headers: corsHeaders });
}

for (const path of [
  "/auth/config", "/auth/validate", "/auth/signup", "/auth/login", "/auth/refresh",
  "/auth/logout", "/auth/update-profile", "/auth/delete-account",
  "/auth/forgot-password", "/auth/reset-password", "/auth/change-password", "/auth/set-password",
  "/auth/verify-totp", "/auth/providers", "/auth/oauth-link/start", "/auth/oauth-link/complete",
  "/auth/test/oauth-signin",
  "/auth/device-code/authorize", "/auth/device-code/broker",
  "/auth/passkey/register/start", "/auth/passkey/register/finish",
  "/auth/passkey/login/start", "/auth/passkey/login/finish",
  "/auth/passkey/signup/start", "/auth/passkey/signup/finish",
  "/auth/passkey/list", "/auth/passkey/remove", "/auth/passkey/check",
  "/auth/email-providers", "/auth/verify-email/request", "/auth/verify-email/confirm",
  "/devices/list", "/devices/owner-by-hardware", "/devices/pending-list", "/devices/pending-claim", "/devices/alias", "/devices/tags", "/devices/select", "/devices/request-update", "/devices/claim-update", "/config", "/settings", "/settings/repair-relay", "/packages",
  "/mesh/peers", "/mesh/acls", "/mesh/acls/set", "/mesh/tags", "/mesh/tags/set", "/mesh/node/config", "/mesh/join", "/mesh/leave",
  "/support/invite", "/support/invite/info", "/support/connections", "/support/grant/revoke", "/support/deny-all",
  "/shortcuts", "/shortcuts/delete",
  "/subscription",
  "/billing/checkout",
  "/billing/yaver-cloud/checkout",
  "/billing/yaver-cloud/change-plan",
  "/billing/yaver-cloud/balance",
  "/billing/yaver-cloud/provision",
  "/billing/yaver-cloud/start",
  "/billing/yaver-cloud/stop",
  "/billing/yaver-cloud/auto-park",
  "/billing/yaver-cloud/dev-activate",
  "/billing/yaver-cloud/dev-adopt",
  "/billing/yaver-cloud/dev-deprovision",
  "/billing/yaver-cloud/reconcile",
  "/billing/yaver-cloud/runners-authorized",
  "/billing/yaver-cloud/usage",
  "/billing/credits/checkout",
  "/billing/credits/packs",
  "/billing/status",
  "/billing/portal",
  "/billing/cancel",
  "/managed/cockpit",
  "/managed/burn",
  "/managed/services",
  "/byo/machines",
  "/gateway/policy", "/gateway/policy/set",
  "/gateway/token/mint", "/gateway/token/revoke", "/gateway/token/rotate",
  "/guests/invite", "/guests/accept", "/guests/accept-code",
  "/guests/find-by-code", "/guests/revoke", "/guests/leave", "/guests/list", "/guests/hosts",
  "/guests/allowed", "/guests/config", "/guests/usage", "/guests/conversion",
  "/connections/request", "/connections/accept", "/connections/remove",
  "/connections/block", "/connections/unblock", "/connections/nickname",
  "/connections/list", "/connections/search", "/connections/suggested",
  "/project-shares/create", "/project-shares/invite", "/project-shares/accept",
  "/project-shares/list", "/project-shares/find-by-code", "/project-shares/set-role",
  "/project-shares/revoke-member", "/project-shares/archive",
  "/project-artifacts", "/project-artifacts/upload-url", "/project-artifacts/hide",
  "/project-artifacts/usage", "/project-artifacts/cleanup", "/project-artifacts/public",
  "/feedback-work-items", "/feedback-work-items/claim", "/feedback-work-items/status",
  "/feedback-work-items/route", "/feedback-work-items/queue-relay-source",
  "/host-share/create", "/host-share/invite", "/host-share/join",
  "/host-share/revoke", "/host-share/list", "/host-share/sessions",
  "/host-share/access", "/host-share/touch",
  "/host-share/peer-access",
  "/users/lookup",
  "/agent-rescue/queue", "/agent-rescue/list",
  "/publish-jobs/queue", "/publish-jobs/list",
  "/packages/allocation", "/packages/accept", "/packages/shared",
  "/tasks/placement/preview", "/tasks/placement/record",
  "/tasks/placement/recent", "/tasks/placement/status",
  "/tasks/placement/activate", "/tasks/placement/rebind",
  "/tasks/dispatch-intents", "/tasks/dispatch-intents/status",
  "/tasks/relay-source-intents", "/tasks/relay-source-intents/status",
  "/tasks/relay-source-intents/claim", "/tasks/relay-source-intents/github-app-token",
  "/tasks/relay-source-intents/gitlab-token",
  "/tasks/project-profile",
  "/cloud/wake-runs/recent",
]) {
  http.route({
    path,
    method: "OPTIONS",
    handler: httpAction(async () => corsPreflightResponse()),
  });
}

// ── Email/Password Auth Endpoints ───────────────────────────────────

/** GET /auth/config — public auth capability flags for every surface. */
http.route({
  path: "/auth/config",
  method: "GET",
  handler: httpAction(async () => {
    return jsonResponse({
      emailPasswordEnabled: emailPasswordAuthEnabled(),
      emailPasswordRequiresAllowlist: true,
      passwordMinLength: 8,
      passwordStorage: "pbkdf2-sha256:100000",
    });
  }),
});

/** POST /auth/signup — Email/password signup. */
http.route({
  path: "/auth/signup",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const disabled = requireEmailPasswordAuthEnabled();
    if (disabled) return disabled;
    const body = await request.json();
    const { email, fullName, password } = body;

    if (!email || !fullName || !password) {
      return errorResponse("Missing required fields", 400);
    }
    const notAllowed = requireEmailPasswordEmailAllowed(email);
    if (notAllowed) return notAllowed;
    if (password.length < 8) {
      return errorResponse("Password must be at least 8 characters", 400);
    }

    const passwordHash = await hashPassword(password);

    let userId;
    try {
      userId = await ctx.runMutation(internal.auth.createEmailUser, {
        email: email.toLowerCase().trim(),
        fullName: fullName.trim(),
        passwordHash,
      });
    } catch (e: any) {
      if (e.message?.includes("EMAIL_EXISTS")) {
        return errorResponse("An account with this email already exists", 409);
      }
      return errorResponse("Signup failed", 500);
    }

    const token = await createSessionToken(ctx, userId);
    // userId returned to the client is the public userId string (stable,
    // shareable). Keep userDocId in its own field for back-compat.
    const publicUser = await ctx.runQuery(internal.auth.getUserPublicProfile, { userDocId: userId });
    return jsonResponse({
      token,
      userId: publicUser?.userId ?? String(userId),
      userDocId: String(userId),
    });
  }),
});

/** POST /auth/login — Email/password login. */
http.route({
  path: "/auth/login",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const disabled = requireEmailPasswordAuthEnabled();
    if (disabled) return disabled;
    const body = await request.json();
    const { email, password } = body;

    if (!email || !password) {
      return errorResponse("Missing email or password", 400);
    }

    const normEmail = email.toLowerCase().trim();
    const notAllowed = requireEmailPasswordEmailAllowed(normEmail);
    if (notAllowed) return notAllowed;
    const attemptKey = loginAttemptKey(request, normEmail);
    if (loginLocked(attemptKey)) {
      return errorResponse("Too many failed attempts. Try again later.", 429);
    }

    const user = await ctx.runQuery(internal.auth.lookupEmailUser, {
      email: normEmail,
    });

    if (!user || !user.passwordHash) {
      recordLoginFailure(attemptKey);
      return errorResponse("Invalid email or password", 401);
    }

    const valid = await verifyPassword(password, user.passwordHash);
    if (!valid) {
      recordLoginFailure(attemptKey);
      return errorResponse("Invalid email or password", 401);
    }
    clearLoginFailures(attemptKey);

    // Check if 2FA is enabled
    const fullUser = await ctx.runQuery(internal.auth.getUserWithTotp, { userId: user._id });
    if (fullUser?.totpEnabled) {
      const { pendingToken } = await ctx.runMutation(internal.totp.createPendingAuth, { userId: user._id });
      return jsonResponse({ requires2fa: true, pendingToken });
    }

    const token = await createSessionToken(ctx, user._id);
    return jsonResponse({ token, userId: user.userId });
  }),
});

// ── Passkey (WebAuthn) Endpoints ───────────────────────────────────
// Strictly additive to the existing email/OAuth flows. The four
// routes below mint the same session token /auth/login does, so all
// downstream auth (validate/refresh/etc.) is unchanged. Origin is
// validated inside the action against an allowlist.

/** POST /auth/passkey/register/start — signed-in user begins enrollment. */
http.route({
  path: "/auth/passkey/register/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => ({} as any));
    const origin = request.headers.get("Origin") || "";
    try {
      const options = await ctx.runAction(internal.passkeys.registerStart, {
        userDocId: session.userDocId as any,
        origin,
        deviceLabel: typeof body?.deviceLabel === "string" ? body.deviceLabel : undefined,
      });
      return jsonResponse({ options });
    } catch (e: any) {
      return errorResponse(e?.message || "registerStart failed", 400);
    }
  }),
});

/** POST /auth/passkey/register/finish — signed-in user completes enrollment. */
http.route({
  path: "/auth/passkey/register/finish",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => ({} as any));
    if (!body?.response) return errorResponse("Missing response", 400);
    const origin = request.headers.get("Origin") || "";
    try {
      const result = await ctx.runAction(internal.passkeys.registerFinish, {
        userDocId: session.userDocId as any,
        origin,
        response: body.response,
        deviceLabel: typeof body?.deviceLabel === "string" ? body.deviceLabel : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "registerFinish failed", 400);
    }
  }),
});

/** POST /auth/passkey/signup/start — anonymous; brand-new email + display name.
 *  Returns either { ok:true, options } or { ok:false, error, hasPasskey } so
 *  the client can route email-already-registered users to sign-in instead. */
http.route({
  path: "/auth/passkey/signup/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({} as any));
    const email = String(body?.email || "").trim();
    const fullName = String(body?.fullName || "").trim();
    if (!email) return errorResponse("Missing email", 400);
    const origin = request.headers.get("Origin") || "";
    try {
      const result = await ctx.runAction(api.passkeys.signupStart, {
        origin,
        email,
        fullName,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "signupStart failed", 400);
    }
  }),
});

/** POST /auth/passkey/signup/finish — anonymous; mints session for the new user. */
http.route({
  path: "/auth/passkey/signup/finish",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({} as any));
    if (!body?.response) return errorResponse("Missing response", 400);
    const email = String(body?.email || "").trim();
    const fullName = String(body?.fullName || "").trim();
    if (!email) return errorResponse("Missing email", 400);
    const origin = request.headers.get("Origin") || "";
    try {
      const result = await ctx.runAction(api.passkeys.signupFinish, {
        origin,
        email,
        fullName,
        response: body.response,
        deviceLabel: body?.deviceLabel,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "signupFinish failed", 400);
    }
  }),
});

/** POST /auth/passkey/login/start — anonymous; returns assertion challenge. */
http.route({
  path: "/auth/passkey/login/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const origin = request.headers.get("Origin") || "";
    try {
      const options = await ctx.runAction(api.passkeys.loginStart, { origin });
      return jsonResponse({ options });
    } catch (e: any) {
      return errorResponse(e?.message || "loginStart failed", 400);
    }
  }),
});

/** POST /auth/passkey/login/finish — anonymous; mints session on success. */
http.route({
  path: "/auth/passkey/login/finish",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({} as any));
    if (!body?.response) return errorResponse("Missing response", 400);
    const origin = request.headers.get("Origin") || "";
    try {
      const result = await ctx.runAction(api.passkeys.loginFinish, {
        origin,
        response: body.response,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "loginFinish failed", 401);
    }
  }),
});

/**
 * GET /auth/email-providers?email=X — anonymous lookup of which sign-in
 * methods are already attached to an email address. Used by mobile / web
 * after a signup collision (EMAIL_EXISTS) so the UI can route the user
 * to "Continue with Apple to link" instead of dead-ending at the error.
 *
 * Returns the same shape regardless of whether the email exists, so
 * timing differences are minimal and an attacker can't enumerate by
 * response shape. Existence + provider hints DO leak — same signal as
 * /auth/login wrong-password vs unknown-user, which is unavoidable.
 */
http.route({
  path: "/auth/email-providers",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const disabled = requireEmailPasswordAuthEnabled();
    if (disabled) {
      return jsonResponse({
        exists: false,
        providers: [] as string[],
        hasPasskey: false,
        emailPasswordEnabled: false,
      });
    }
    const url = new URL(request.url);
    const email = (url.searchParams.get("email") || "").trim().toLowerCase();
    const notAllowed = requireEmailPasswordEmailAllowed(email);
    if (notAllowed) {
      return jsonResponse({
        exists: false,
        providers: [] as string[],
        hasPasskey: false,
        emailPasswordEnabled: true,
      });
    }
    const data = await ctx.runQuery(internal.auth.lookupExistingProvidersByEmail, { email });
    return jsonResponse({ ...data, emailPasswordEnabled: true });
  }),
});

/**
 * POST /auth/verify-email/request — authenticated user asks for a
 * fresh verification email (re-send path or post-signup banner).
 */
http.route({
  path: "/auth/verify-email/request",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const result = await ctx.runMutation(api.auth.requestEmailVerification, { tokenHash });
      return jsonResponse(result);
    } catch (e: any) {
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      return errorResponse(e?.message || "Could not request verification email", 400);
    }
  }),
});

/**
 * POST /auth/verify-email/confirm — anonymous: consume the token from
 * the email link, flip users.emailVerified=true. Body: { token }.
 */
http.route({
  path: "/auth/verify-email/confirm",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({} as any));
    const token = String(body?.token || "");
    if (!token) return errorResponse("token required", 400);
    const result = await ctx.runMutation(api.auth.confirmEmailVerification, { token });
    return jsonResponse(result);
  }),
});

/**
 * GET /auth/passkey/check?email=X — anonymous preflight.
 *
 * iOS returns ASAuthorizationError.canceled (1001) for BOTH "user
 * dismissed the sheet" and "no credentials found for this rpId" — the
 * sheet never appears in the no-credentials case. Without a preflight
 * the mobile UI looks dead: tap → spinner → silent revert.
 *
 * Mobile calls this with the email the user typed so it can decide
 * whether to invoke the platform sheet at all. Returns:
 *   { hasPasskey: true,  emailRegistered: true  }  — passkey available
 *   { hasPasskey: false, emailRegistered: true  }  — OAuth/email user
 *                                                    needs to enroll first
 *   { hasPasskey: false, emailRegistered: false }  — brand-new email
 *
 * Reveals "this email is registered" to anonymous callers — but
 * /auth/passkey/signup/start and /auth/login already leak the same
 * signal (EMAIL_EXISTS / wrong-password), so net new surface is zero.
 */
http.route({
  path: "/auth/passkey/check",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const email = (url.searchParams.get("email") || "").trim().toLowerCase();
    if (!email || !email.includes("@")) {
      return jsonResponse({ hasPasskey: false, emailRegistered: false });
    }
    const result = await ctx.runQuery(internal.passkeysDb.emailAvailable, { email });
    return jsonResponse({
      hasPasskey: result.available === false && result.hasPasskey === true,
      emailRegistered: result.available === false,
    });
  }),
});

/** GET /auth/passkey/list — signed-in user lists their passkeys. */
http.route({
  path: "/auth/passkey/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const rows = await ctx.runQuery(internal.passkeysDb.listForUser, {
      userId: session.userDocId as any,
    });
    return jsonResponse({ passkeys: rows });
  }),
});

/** POST /auth/passkey/remove — signed-in user revokes one of their passkeys. */
http.route({
  path: "/auth/passkey/remove",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => ({} as any));
    const credentialId = String(body?.credentialId || "").trim();
    if (!credentialId) return errorResponse("Missing credentialId", 400);
    try {
      const result = await ctx.runMutation(internal.passkeysDb.removeCredential, {
        userId: session.userDocId as any,
        credentialId,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e?.message || "remove failed", 400);
    }
  }),
});

// ── Password Reset Endpoints ────────────────────────────────────────

/** POST /auth/forgot-password — Request a password reset email. */
http.route({
  path: "/auth/forgot-password",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const disabled = requireEmailPasswordAuthEnabled();
    if (disabled) return disabled;
    const body = await request.json();
    const { email } = body;

    if (!email) return errorResponse("Email is required", 400);
    const notAllowed = requireEmailPasswordEmailAllowed(email);
    if (notAllowed) return notAllowed;

    // Generate a random reset token
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const rawToken = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const tokenHash = await sha256Hex(rawToken);

    const result = await ctx.runMutation(api.auth.createPasswordReset, {
      email: email.toLowerCase().trim(),
      tokenHash,
    });

    // Send email only if a valid email user was found and not rate-limited
    if (result.sent) {
      const { passwordResetHtml } = await import("./email");
      const resetUrl = `https://yaver.io/auth/reset-password?token=${rawToken}`;
      await ctx.scheduler.runAfter(0, internal.email.send, {
        from: "Yaver <noreply@yaver.io>",
        to: email.toLowerCase().trim(),
        subject: "Reset your Yaver password",
        html: passwordResetHtml(resetUrl),
      });
    }

    // Always return success — don't reveal whether the email exists
    return jsonResponse({ ok: true });
  }),
});

/** POST /auth/reset-password — Set a new password using a reset token. */
http.route({
  path: "/auth/reset-password",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const disabled = requireEmailPasswordAuthEnabled();
    if (disabled) return disabled;
    const body = await request.json();
    const { token, password } = body;

    if (!token || !password) {
      return errorResponse("Token and password are required", 400);
    }
    if (password.length < 8) {
      return errorResponse("Password must be at least 8 characters", 400);
    }

    const tokenHash = await sha256Hex(token);
    const resetTarget = await ctx.runQuery(internal.auth.lookupPasswordResetTarget, { tokenHash });
    if (!resetTarget) return errorResponse("Invalid or expired reset link", 400);
    const notAllowed = requireEmailPasswordEmailAllowed(resetTarget.email);
    if (notAllowed) return notAllowed;
    const newPasswordHash = await hashPassword(password);

    try {
      await ctx.runMutation(api.auth.resetPassword, {
        tokenHash,
        newPasswordHash,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      const msg = e.message || "";
      if (msg.includes("INVALID_TOKEN")) {
        return errorResponse("Invalid or expired reset link", 400);
      }
      if (msg.includes("TOKEN_USED")) {
        return errorResponse("This reset link has already been used", 400);
      }
      if (msg.includes("TOKEN_EXPIRED")) {
        return errorResponse("This reset link has expired", 400);
      }
      return errorResponse("Password reset failed", 500);
    }
  }),
});

/** POST /auth/change-password — Change password while logged in (email users only). */
http.route({
  path: "/auth/change-password",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const disabled = requireEmailPasswordAuthEnabled();
    if (disabled) return disabled;
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const user = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!user) return errorResponse("Unauthorized", 401);

    const body = await request.json();
    const { currentPassword, newPassword } = body;
    const notAllowed = requireEmailPasswordEmailAllowed(user.email);
    if (notAllowed) return notAllowed;

    if (!currentPassword || !newPassword) {
      return errorResponse("Current password and new password are required", 400);
    }
    if (newPassword.length < 8) {
      return errorResponse("New password must be at least 8 characters", 400);
    }

    // Look up the email user to get the password hash
    const emailUser = await ctx.runQuery(internal.auth.lookupEmailUser, { email: user.email });
    if (!emailUser || !emailUser.passwordHash) {
      return errorResponse("Password change is only available for email accounts", 400);
    }

    // Verify current password
    const valid = await verifyPassword(currentPassword, emailUser.passwordHash);
    if (!valid) {
      return errorResponse("Current password is incorrect", 401);
    }

    const newPasswordHash = await hashPassword(newPassword);
    await ctx.runMutation(api.auth.changePassword, {
      tokenHash,
      newPasswordHash,
    });

    return jsonResponse({ ok: true });
  }),
});

/** POST /auth/set-password — Add first email/password credential to the signed-in account. */
http.route({
  path: "/auth/set-password",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const disabled = requireEmailPasswordAuthEnabled();
    if (disabled) return disabled;
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const user = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!user) return errorResponse("Unauthorized", 401);

    const body = await request.json();
    const { password } = body;
    if (!password) return errorResponse("Password is required", 400);
    if (password.length < 8) return errorResponse("Password must be at least 8 characters", 400);
    if (!user.email) return errorResponse("This account has no email address", 400);
    const notAllowed = requireEmailPasswordEmailAllowed(user.email);
    if (notAllowed) return notAllowed;

    const existing = await ctx.runQuery(internal.auth.lookupEmailUser, {
      email: user.email.toLowerCase().trim(),
    });
    if (existing?.passwordHash) {
      return errorResponse("This account already has an email/password credential. Use change password.", 409);
    }

    const passwordHash = await hashPassword(password);
    try {
      await ctx.runMutation(api.auth.setOwnPassword, { tokenHash, passwordHash });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      const msg = e?.message || "";
      if (msg === "Unauthorized") return errorResponse("Unauthorized", 401);
      return errorResponse(msg || "Failed to set password", 400);
    }
  }),
});

// ── Survey Endpoints ────────────────────────────────────────────────

/** POST /survey/submit — Submit developer survey (authed). */
http.route({
  path: "/survey/submit",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.survey.submitSurvey, {
        tokenHash,
        isDeveloper: body.isDeveloper ?? true,
        fullName: body.fullName,
        languages: body.languages,
        experienceLevel: body.experienceLevel,
        role: body.role,
        companySize: body.companySize,
        useCase: body.useCase,
      });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to submit survey", 500);
    }
  }),
});

/** GET /survey — Get survey status (authed). */
http.route({
  path: "/survey",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const survey = await ctx.runQuery(api.survey.getSurvey, { tokenHash });
    if (!survey) return errorResponse("Unauthorized", 401);
    return jsonResponse(survey);
  }),
});

// ── Auth Endpoints (called by Next.js API routes) ────────────────────

/** POST /auth/upsert-user — Create or update a user (called from web server). */
http.route({
  path: "/auth/upsert-user",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const denied = await requireServerSecret(request);
    if (denied) return denied;
    const body = await request.json();
    const userId = await ctx.runMutation(internal.auth.createOrUpdateUser, {
      email: body.email,
      fullName: body.fullName,
      provider: body.provider,
      providerId: body.providerId,
      avatarUrl: body.avatarUrl,
    });
    return jsonResponse({ userId });
  }),
});

/** GET /auth/providers — List linked sign-in methods for current account. */
http.route({
  path: "/auth/providers",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const identities = await ctx.runQuery(api.auth.listAuthIdentities, { tokenHash });
    if (identities === null) return errorResponse("Unauthorized", 401);
    return jsonResponse({ identities });
  }),
});

/** POST /auth/oauth-link/start — Start linking another OAuth provider to this account. */
http.route({
  path: "/auth/oauth-link/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    if (!body.provider) return errorResponse("provider required", 400);
    try {
      const result = await ctx.runMutation(api.auth.createOAuthLinkIntent, {
        tokenHash,
        provider: body.provider,
        client: body.client || "web",
        returnTo: body.returnTo || undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      return errorResponse(e?.message || "Failed to start OAuth linking", 400);
    }
  }),
});

/** POST /auth/oauth-link/complete — Complete provider linking/merge using a one-time intent token. */
http.route({
  path: "/auth/oauth-link/complete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const denied = await requireServerSecret(request);
    if (denied) return denied;
    const body = await request.json().catch(() => ({}));
    if (!body.linkToken || !body.provider || !body.providerId || !body.email) {
      return errorResponse("linkToken, provider, providerId, email required", 400);
    }
    try {
      const result = await ctx.runMutation(internal.auth.completeOAuthLink, {
        linkToken: body.linkToken,
        provider: body.provider,
        providerId: body.providerId,
        email: body.email,
        fullName: body.fullName || "",
        avatarUrl: body.avatarUrl || undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e?.message === "INVALID_LINK_TOKEN") return errorResponse("Invalid or expired link token", 410);
      if (e?.message === "TARGET_USER_NOT_FOUND") return errorResponse("Target user not found", 404);
      if (e?.message === "IDENTITY_ALREADY_LINKED") return errorResponse("Identity already linked to another account", 409);
      return errorResponse(e?.message || "Failed to complete OAuth linking", 400);
    }
  }),
});

/** DELETE /auth/oauth-link/:provider — Unlink an OAuth provider from the current account. */
http.route({
  pathPrefix: "/auth/oauth-link/",
  method: "DELETE",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    const provider = url.pathname.replace(/^.*\/auth\/oauth-link\//, "").trim();
    if (!provider || !["google", "microsoft", "apple", "github", "gitlab", "email"].includes(provider)) {
      return errorResponse("unknown provider", 400);
    }
    const body = await request.json().catch(() => ({}));
    try {
      const result = await ctx.runMutation(api.auth.unlinkAuthIdentity, {
        tokenHash,
        provider: provider as "google" | "microsoft" | "apple" | "github" | "gitlab" | "email",
        totpCode: typeof body?.totpCode === "string" ? body.totpCode : undefined,
      });
      if (!result.ok) {
        return errorResponse(result.reason || "not linked", 404);
      }
      return jsonResponse(result);
    } catch (e: any) {
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse("Unauthorized", 401);
      if (errorMessageIncludes(e, "ONLY_IDENTITY")) {
        return errorResponse("Refusing to unlink the only sign-in method — add another provider first.", 409);
      }
      if (errorMessageIncludes(e, "TOTP_REQUIRED")) {
        return errorResponse("TOTP code required (2FA is enabled on this account).", 412);
      }
      if (errorMessageIncludes(e, "INVALID_TOTP")) {
        return errorResponse("Invalid 2FA code.", 403);
      }
      return errorResponse(e?.message || "Failed to unlink provider", 400);
    }
  }),
});

/** POST /auth/account/merge/start — Target user starts a merge intent. */
http.route({
  path: "/auth/account/merge/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      const result = await ctx.runMutation(api.auth.createAccountMergeIntent, {
        tokenHash,
        client: body?.client || undefined,
        totpCode: typeof body?.totpCode === "string" ? body.totpCode : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse("Unauthorized", 401);
      if (errorMessageIncludes(e, "TOTP_REQUIRED")) {
        return errorResponse("TOTP code required (2FA is enabled on this account).", 412);
      }
      if (errorMessageIncludes(e, "INVALID_TOTP")) {
        return errorResponse("Invalid 2FA code.", 403);
      }
      if (errorMessageIncludes(e, "TOO_MANY_PENDING_MERGES")) {
        return errorResponse("Too many pending merge intents. Cancel an existing one first.", 429);
      }
      if (errorMessageIncludes(e, "MERGE_RATE_LIMIT")) {
        return errorResponse("Too many merge requests in the last hour. Try again later.", 429);
      }
      return errorResponse(e?.message || "Failed to start merge", 400);
    }
  }),
});

/** GET /auth/account/merge/status?token=... — Public status for approval-page UX. */
http.route({
  path: "/auth/account/merge/status",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const token = url.searchParams.get("token");
    if (!token) return errorResponse("token required", 400);
    const result = await ctx.runQuery(api.auth.getAccountMergeIntentStatus, { token });
    return jsonResponse(result);
  }),
});

/**
 * POST /auth/account/merge/complete
 *
 * Called from a session signed into the SOURCE account (the one that will
 * be merged away). Body: { mergeToken }. The Authorization header carries
 * the source's bearer token.
 */
http.route({
  path: "/auth/account/merge/complete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const sourceTokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    if (!body?.mergeToken) return errorResponse("mergeToken required", 400);
    try {
      const result = await ctx.runMutation(api.auth.completeAccountMerge, {
        mergeToken: body.mergeToken,
        sourceTokenHash,
        sourceTotpCode: typeof body?.sourceTotpCode === "string" ? body.sourceTotpCode : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse("Unauthorized", 401);
      if (errorMessageIncludes(e, "INVALID_MERGE_TOKEN")) return errorResponse("Invalid merge token", 404);
      if (errorMessageIncludes(e, "MERGE_ALREADY_RESOLVED")) return errorResponse("Merge already completed or cancelled", 409);
      if (errorMessageIncludes(e, "MERGE_TOKEN_EXPIRED")) return errorResponse("Merge token expired", 410);
      if (errorMessageIncludes(e, "CANNOT_MERGE_SELF")) return errorResponse("Cannot merge an account into itself", 400);
      if (errorMessageIncludes(e, "TARGET_USER_NOT_FOUND")) return errorResponse("Target account no longer exists", 404);
      if (errorMessageIncludes(e, "TOTP_REQUIRED")) {
        return errorResponse("TOTP code required (source account has 2FA enabled).", 412);
      }
      if (errorMessageIncludes(e, "INVALID_TOTP")) {
        return errorResponse("Invalid 2FA code.", 403);
      }
      return errorResponse(e?.message || "Merge failed", 400);
    }
  }),
});

/** POST /auth/account/merge/cancel — target cancels a pending merge intent. */
http.route({
  path: "/auth/account/merge/cancel",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    if (!body?.mergeToken) return errorResponse("mergeToken required", 400);
    try {
      const result = await ctx.runMutation(api.auth.cancelAccountMergeIntent, {
        tokenHash,
        mergeToken: body.mergeToken,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      return errorResponse(e?.message || "Failed to cancel merge", 400);
    }
  }),
});

/** POST /auth/create-session — Create a session (called from web server). */
http.route({
  path: "/auth/create-session",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const denied = await requireServerSecret(request);
    if (denied) return denied;
    const body = await request.json();
    const sessionId = await ctx.runMutation(internal.auth.createSession, {
      tokenHash: body.tokenHash,
      userId: body.userId,
      deviceId: body.deviceId,
      expiresAt: body.expiresAt,
    });
    return jsonResponse({ sessionId });
  }),
});

/** POST /auth/test/oauth-signin — Test-only OAuth shortcut for mocked-provider CI. */
http.route({
  path: "/auth/test/oauth-signin",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    if (!parseBooleanEnv(process.env.TEST_MODE_ENABLED, false)) {
      return errorResponse("Not found", 404);
    }

    const body = await request.json().catch(() => ({}));
    if (!body?.provider || !body?.providerId || !body?.email) {
      return errorResponse("provider, providerId, email required", 400);
    }

    const provider = String(body.provider).trim();
    if (!["google", "microsoft", "apple", "github", "gitlab", "email"].includes(provider)) {
      return errorResponse("unknown provider", 400);
    }

    const userId = await ctx.runMutation(internal.auth.createOrUpdateUser, {
      email: String(body.email).trim().toLowerCase(),
      fullName: typeof body.fullName === "string" ? body.fullName.trim() : "",
      provider: provider as "google" | "microsoft" | "apple" | "github" | "gitlab" | "email",
      providerId: String(body.providerId).trim(),
      avatarUrl: typeof body.avatarUrl === "string" ? body.avatarUrl : undefined,
    });

    const token = await createSessionToken(ctx, userId, typeof body.deviceId === "string" ? body.deviceId : undefined);
    return jsonResponse({ ok: true, token, userId: String(userId) });
  }),
});

// ── Profile Update Endpoint ──────────────────────────────────────────

/** POST /auth/update-profile — Update user profile (authed). */
http.route({
  path: "/auth/update-profile",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.auth.updateProfile, {
        tokenHash,
        fullName: body.fullName,
      });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to update profile", 500);
    }
  }),
});

// ── Auth Validation Endpoint ─────────────────────────────────────────

/** GET /auth/validate — Validate bearer token, return user info. */
http.route({
  path: "/auth/validate",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) {
      return errorResponse("Unauthorized", 401);
    }

    // Also return team memberships (needed by multi-user agents)
    const authHeader = request.headers.get("Authorization")!;
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });

    let teams: { teamId: string; role: string }[] = [];
    if (userDocId) {
      const userTeams = await ctx.runQuery(internal.teams.getTeamsForUser, { userId: userDocId });
      teams = (userTeams || []).map((t: any) => ({ teamId: t.teamId, role: t.role }));
    }

    return jsonResponse({ user: { ...user, teams } });
  }),
});

// ── Token Refresh ────────────────────────────────────────────────────

/**
 * POST /auth/refresh — Extend session by 1 year + rotate the bearer
 * token so a leaked token only lives until the next daily refresh
 * (~24 h max blast radius). The agent writes the returned `token` back
 * to ~/.yaver/config.json atomically. Older agents that ignore the
 * `token` field still work — they just don't get the rotation benefit.
 *
 * Query param `?rotate=0` disables rotation for tooling that wants a
 * simple "extend only" refresh (rare).
 */
http.route({
  path: "/auth/refresh",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    // Rotation is OPT-IN, not default-on. An older agent (<1.99.12)
    // that hits this endpoint calls POST /auth/refresh with no opt-in
    // signal and only reads {ok, expiresAt} from the response — if
    // we rotated unconditionally, the server would move to a new
    // tokenHash while the agent keeps writing the old one to disk,
    // locking that device out on the next call. Require explicit
    // opt-in via ?rotate=1 OR X-Yaver-Rotate-Token: 1 so only agents
    // that know how to persist the new token trigger a rotation.
    const url = new URL(request.url);
    const optInFromQuery = url.searchParams.get("rotate") === "1";
    const optInFromHeader = request.headers.get("X-Yaver-Rotate-Token") === "1";
    const wantRotation = optInFromQuery || optInFromHeader;

    let newToken: string | undefined;
    let newTokenHash: string | undefined;
    if (wantRotation) {
      const tokenBytes = new Uint8Array(32);
      crypto.getRandomValues(tokenBytes);
      newToken = Array.from(tokenBytes)
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
      newTokenHash = await sha256Hex(newToken);
    }

    const result = await ctx.runMutation(api.auth.refreshSession, {
      tokenHash,
      newTokenHash,
    });
    if (!result) {
      return errorResponse("Session expired or invalid", 401);
    }
    return jsonResponse({
      ok: true,
      expiresAt: result.expiresAt,
      // Only surface the new token when the backend actually rotated
      // (guarded against hash collision). Otherwise omit so old agents
      // don't accidentally persist an identical value.
      token: result.rotated ? newToken : undefined,
      rotated: !!result.rotated,
    });
  }),
});

// ── Apple Sign-In ────────────────────────────────────────────────────

/** POST /auth/apple-native — Native iOS Apple Sign-In (receives identityToken). */
http.route({
  path: "/auth/apple-native",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    const { identityToken, fullName } = body;

    if (!identityToken) {
      return errorResponse("Missing identityToken", 400);
    }

    // SECURITY (audit 2026-07-13): VERIFY Apple's identity token signature
    // against Apple's JWKS before trusting any claim. Previously this handler
    // base64-decoded the payload WITHOUT verifying the signature, so anyone
    // could POST a self-crafted unsigned JWT carrying a victim's email and mint
    // a session for that account (zero-secret account takeover). jwtVerify
    // checks the RS256 signature, issuer, audience, and expiry.
    let payload: Record<string, unknown>;
    try {
      const { payload: verified } = await jwtVerify(identityToken, APPLE_JWKS, {
        issuer: "https://appleid.apple.com",
        audience: APPLE_NATIVE_AUDIENCE,
      });
      payload = verified as Record<string, unknown>;
    } catch (e: any) {
      console.error("[apple-native] identity token verification failed:", e?.message);
      return errorResponse("Invalid Apple identity token", 401);
    }

    const email = payload.email as string;
    const sub = payload.sub as string;

    if (!email || !sub) {
      return errorResponse("Token missing email or sub", 400);
    }
    // Apple sets email_verified as string "true"/"false" or boolean.
    const emailVerifiedClaim = payload.email_verified;
    const emailVerified =
      emailVerifiedClaim === true || emailVerifiedClaim === "true";
    if (!emailVerified && !payload.is_private_email) {
      // Only trust the email for auto-link/verified-provider flows when Apple
      // says it's verified (private-relay emails are Apple-controlled → trusted).
      return errorResponse("Apple email not verified", 401);
    }

    // Upsert user
    const userId = await ctx.runMutation(internal.auth.createOrUpdateUser, {
      email: email.toLowerCase(),
      fullName: fullName || "",
      provider: "apple",
      providerId: sub,
    });

    // Check if 2FA is enabled
    const totpCheck = await ctx.runQuery(internal.auth.getUserWithTotp, { userId });
    if (totpCheck?.totpEnabled) {
      const { pendingToken } = await ctx.runMutation(internal.totp.createPendingAuth, { userId });
      return jsonResponse({ requires2fa: true, pendingToken });
    }

    // Create session
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const token = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");

    const tokenHash = await sha256Hex(token);
    const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000; // 30 days

    await ctx.runMutation(internal.auth.createSession, {
      tokenHash,
      userId,
      deviceId: body.deviceId || undefined,
      expiresAt,
    });

    return jsonResponse({ token, userId });
  }),
});

/** POST /auth/apple-notifications — Apple sends account events here. */
http.route({
  path: "/auth/apple-notifications",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    console.log("Apple notification received:", JSON.stringify(body));
    return new Response(null, { status: 200 });
  }),
});

// ── Device Endpoints ─────────────────────────────────────────────────

/** POST /devices/register — Register a device (authed). */
http.route({
  path: "/devices/register",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    const deviceId = await ctx.runMutation(api.devices.registerDevice, {
      tokenHash,
      deviceId: body.deviceId,
      name: body.name,
      platform: body.platform,
      deviceClass: body.deviceClass || undefined,
      edgeProfile: body.edgeProfile || undefined,
      publicKey: body.publicKey || undefined,
      quicHost: body.quicHost,
      quicPort: body.quicPort,
      publicEndpoints: Array.isArray(body.publicEndpoints) ? body.publicEndpoints : undefined,
      hardwareId: body.hardwareId || undefined,
      hardwareProfile: body.hardwareProfile || undefined,
      recoveryPosture: body.recoveryPosture || undefined,
      connectionPreferences: Array.isArray(body.connectionPreferences)
        ? body.connectionPreferences
        : body.connectionPreferences === null
          ? []
          : undefined,
      agentVersion: typeof body.agentVersion === "string" ? body.agentVersion : undefined,
    });

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session?.userDocId) {
      return errorResponse("Unauthorized", 401);
    }

    await ctx.runMutation(internal.auth.deleteSessionsByDeviceId, {
      userId: session.userDocId,
      deviceId: body.deviceId,
    });
    const dedicatedToken = await createSessionToken(ctx, session.userDocId, body.deviceId);

    return jsonResponse({ deviceId, token: dedicatedToken });
  }),
});

/**
 * POST /devices/owner-by-hardware — Look up the owner of a
 * device by stable hardware fingerprint. Used by the agent's
 * /auth/recover handler to verify that a Convex bearer token
 * (presented by a mobile app trying to remote-reauth a box)
 * actually belongs to the same user that originally registered
 * this hardware. The agent makes the call on behalf of the
 * caller; we authenticate the bearer token here and reply with
 * a simple boolean + the device record.
 *
 * Body: { hardwareId: string }
 * Headers: Authorization: Bearer <caller's convex token>
 */
http.route({
  path: "/devices/owner-by-hardware",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const caller = await authenticateRequest(ctx, request);
    if (!caller) return errorResponse("Unauthorized", 401);

    const body = await request.json();
    if (!body.hardwareId || typeof body.hardwareId !== "string") {
      return errorResponse("hardwareId required", 400);
    }
    // Use caller-aware lookup so duplicate device rows with the same
    // hardwareId (test fixtures, prior owners, re-claims) don't shadow
    // the row the caller actually owns. The agent's verifyHostToken
    // (auth_recover.go) requires isOwner=true; with .first() on a
    // non-unique index this was deterministically returning whichever
    // row the index encountered first — frequently NOT the caller's,
    // breaking remote re-auth for users whose hardwareId is shared.
    // Guests still correctly fail isOwner here (they don't own any row
    // with this hwid) so security is unchanged.
    const owner = await ctx.runQuery(internal.devices.ownerByHardwareIdForCaller, {
      hardwareId: body.hardwareId,
      callerUserId: caller.userDocId,
    });
    if (!owner) {
      return jsonResponse({ ok: true, isOwner: false, deviceFound: false });
    }
    return jsonResponse({
      ok: true,
      isOwner: String(owner.ownerUserId) === caller.userDocId,
      deviceFound: true,
      deviceId: owner.deviceId,
      name: owner.name,
      duplicateCount: owner.duplicateCount,
    });
  }),
});

/** POST /devices/bootstrap — Mark device as in bootstrap mode (no token; uses hardwareId+publicKey). */
http.route({
  path: "/devices/bootstrap",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => null);
    if (!body || !body.deviceId || !body.hardwareId || !body.publicKey) {
      return errorResponse("deviceId, hardwareId, publicKey required", 400);
    }
    try {
      const res = await ctx.runMutation(internal.devices.markBootstrap, {
        deviceId: body.deviceId,
        hardwareId: body.hardwareId,
        publicKey: body.publicKey,
        quicHost: body.quicHost || undefined,
        quicPort: body.quicPort || undefined,
      });
      // SECURITY (audit 2026-07-13): do NOT return the owning account's userId
      // to this route's caller — it authenticates only by the device triple
      // (deviceId+hardwareId+publicKey), and the agent needs nothing beyond
      // {ok:true}. Returning userId leaked device→account linkage.
      void res;
      return jsonResponse({ ok: true });
    } catch (e: any) {
      // "Device not found" is the structured signal an agent uses to
      // decide whether to fall back to /devices/bootstrap-pending.
      // Return 404 (not 400) so the agent's retry path is unambiguous.
      if (errorMessageIncludes(e, "Device not found")) {
        return errorResponse("Device not found", 404);
      }
      return errorResponse(e?.message || "bootstrap failed", 400);
    }
  }),
});

/** POST /devices/bootstrap-pending — A truly-fresh box (no prior Convex
 *  row) registers itself for later claim by its relay-password-bearing
 *  user. The agent calls this only after /devices/bootstrap returned 404.
 *
 *  Why a separate endpoint: the original /devices/bootstrap requires a
 *  pre-existing devices row keyed by (deviceId, hardwareId, publicKey)
 *  for trust. There is no pre-existing row for a clean install, but we
 *  still need a way to make the box visible to ITS owner — the user
 *  whose relay password is configured on the box. We hash the relay
 *  password server-side and store it on a holding row in
 *  `pendingDeviceClaims`. The dashboard scopes its listing to the
 *  caller's managedRelay password hash, so only the rightful user
 *  ever sees the pending row.
 *
 *  Auth: relay password presence is the proof. We rely on the fact
 *  that the agent could only have registered its tunnel with the relay
 *  by also presenting that password — i.e. the password is already
 *  validated by the relay before this endpoint sees it. The user
 *  proves possession on their end by hashing managedRelays.password
 *  and matching it to relayPasswordHash on the row they want to claim.
 */
http.route({
  path: "/devices/bootstrap-pending",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => null);
    if (
      !body ||
      !body.deviceId ||
      !body.hardwareId ||
      !body.publicKey ||
      !body.relayPassword
    ) {
      return errorResponse(
        "deviceId, hardwareId, publicKey, relayPassword required",
        400,
      );
    }
    try {
      const relayPasswordHash = await sha256Hex(String(body.relayPassword));
      const result = await ctx.runMutation(internal.pendingDeviceClaims.createOrUpdate, {
        deviceId: String(body.deviceId),
        hardwareId: String(body.hardwareId),
        publicKey: String(body.publicKey),
        relayPasswordHash,
        name: typeof body.name === "string" ? body.name : undefined,
        platform: typeof body.platform === "string" ? body.platform : undefined,
        quicHost: typeof body.quicHost === "string" ? body.quicHost : undefined,
        quicPort: typeof body.quicPort === "number" ? body.quicPort : undefined,
        relayLabel: typeof body.relayLabel === "string" ? body.relayLabel : undefined,
      });
      // alreadyClaimed=true means the agent got confused: a real
      // devices row exists for this triple, the agent should have
      // succeeded on /devices/bootstrap. We answer 200 anyway so the
      // agent stops retrying, but flag it so log forwarders can spot
      // misrouted calls.
      return jsonResponse({
        ok: true,
        pending: !result.alreadyClaimed,
        alreadyClaimed: !!result.alreadyClaimed,
      });
    } catch (e: any) {
      return errorResponse(e?.message || "bootstrap-pending failed", 400);
    }
  }),
});

/**
 * POST /devices/provision-attest — zero-touch first-boot attestation.
 *
 * No bearer. The proof is cryptographic: the device signs
 * `provision-attest|<deviceId>|<timestampMs>` with the Ed25519 private key
 * baked into its SD seed at flash time, and presents the one-time
 * claimSecret. Convex verifies both against the public key + secret-hash
 * registered at mint time. Responses:
 *   200 {status:"active", token}   — proofs ok + a human has claimed it
 *   202 {status:"awaiting-claim"}  — proofs ok but unclaimed; keep polling
 *   403 {status:"revoked"}         — ownership reset; stop
 *   401 {status:"bad-*"|"stale"}   — proof failed
 *   404 {status:"not-found"}       — no such provisioned device
 * See provisioning.ts for the full trust model.
 */
http.route({
  path: "/devices/provision-attest",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => null);
    if (
      !body ||
      typeof body.deviceId !== "string" ||
      typeof body.claimSecret !== "string" ||
      typeof body.signature !== "string" ||
      typeof body.timestampMs !== "number"
    ) {
      return errorResponse(
        "deviceId, claimSecret, timestampMs, signature required",
        400,
      );
    }
    let result;
    try {
      result = await ctx.runMutation(internal.provisioning.attest, {
        deviceId: body.deviceId,
        claimSecret: body.claimSecret,
        timestampMs: body.timestampMs,
        signature: body.signature,
      });
    } catch (e: any) {
      return errorResponse(e?.message || "attest failed", 500);
    }
    switch (result.status) {
      case "active":
        return jsonResponse(result, 200);
      case "awaiting-claim":
        return jsonResponse(result, 202);
      case "revoked":
        return jsonResponse(result, 403);
      case "not-found":
        return jsonResponse(result, 404);
      default:
        // bad-secret / bad-signature / stale — don't leak which check
        // failed beyond the status string; all are 401.
        return jsonResponse(result, 401);
    }
  }),
});

/**
 * POST /devices/provision-claim — claim a provisioned device by scanned QR
 * (HTTP variant for CLI / non-Convex-client callers; the mobile/web apps
 * can also call the api.provisioning.claimProvisionedDevice mutation
 * directly). Bearer-authed; body carries the scanned {deviceId,
 * claimSecret} and an optional name.
 */
http.route({
  path: "/devices/provision-claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string" || typeof body.claimSecret !== "string") {
      return errorResponse("deviceId, claimSecret required", 400);
    }
    try {
      const result = await ctx.runMutation(api.provisioning.claimProvisionedDevice, {
        tokenHash,
        deviceId: body.deviceId,
        claimSecret: body.claimSecret,
        name: typeof body.name === "string" ? body.name : undefined,
      });
      return jsonResponse(result, 200);
    } catch (e: any) {
      return errorResponse(e?.message || "claim failed", 400);
    }
  }),
});

/**
 * POST /devices/provision-mint — register a flash-time device identity
 * (builder/manufacturer side). Bearer-authed. The CLI generates the
 * Ed25519 keypair + claimSecret locally and sends only the PUBLIC key +
 * the claimSecret HASH; Convex never sees the private key or raw secret.
 */
http.route({
  path: "/devices/provision-mint",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (
      !body ||
      typeof body.deviceId !== "string" ||
      typeof body.publicKey !== "string" ||
      typeof body.claimSecretHash !== "string"
    ) {
      return errorResponse("deviceId, publicKey, claimSecretHash required", 400);
    }
    try {
      const result = await ctx.runMutation(api.provisioning.mintProvisionedDevice, {
        tokenHash,
        deviceId: body.deviceId,
        publicKey: body.publicKey,
        claimSecretHash: body.claimSecretHash,
        productId: typeof body.productId === "string" ? body.productId : undefined,
        name: typeof body.name === "string" ? body.name : undefined,
        platform: typeof body.platform === "string" ? body.platform : undefined,
      });
      return jsonResponse(result, 200);
    } catch (e: any) {
      return errorResponse(e?.message || "mint failed", 400);
    }
  }),
});

/** POST /devices/provision-register-product — builder declares a SKU. */
http.route({
  path: "/devices/provision-register-product",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (!body || typeof body.productId !== "string" || typeof body.name !== "string") {
      return errorResponse("productId, name required", 400);
    }
    try {
      const result = await ctx.runMutation(api.provisioning.registerProduct, {
        tokenHash,
        productId: body.productId,
        name: body.name,
        vendor: typeof body.vendor === "string" ? body.vendor : undefined,
        defaultServices: Array.isArray(body.defaultServices) ? body.defaultServices : undefined,
      });
      return jsonResponse(result, 200);
    } catch (e: any) {
      return errorResponse(e?.message || "register-product failed", 400);
    }
  }),
});

/** POST /devices/heartbeat — Device heartbeat (authed). */
http.route({
  path: "/devices/heartbeat",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    // Validate session at HTTP layer — return 401, not 500
    const user = await authenticateRequest(ctx, request);
    if (!user) {
      return errorResponse("Session expired", 401);
    }

    // Auto-extend session on heartbeat (keeps CLI sessions alive indefinitely)
    await ctx.runMutation(api.auth.refreshSession, { tokenHash }).catch(() => {});

    const body = await request.json();
    const heartbeatResult = await ctx.runMutation(api.devices.heartbeat, {
      tokenHash,
      deviceId: body.deviceId,
      runners: body.runners,
      installedRunnerIds: Array.isArray(body.installedRunnerIds)
        ? body.installedRunnerIds
        : body.installedRunnerIds === null
          ? []
          : undefined,
      // Pass quicHost as-is (including ""). The mutation now treats "" as
      // a deliberate clear (e.g. an upgraded agent retracting a stale
      // Docker-bridge address). Pre-fix `body.quicHost || undefined`
      // collapsed empty-string to undefined, leaving the stale value in
      // the DB forever.
      quicHost: typeof body.quicHost === "string" ? body.quicHost : undefined,
      // Multi-IP rollout: the agent advertises every reachable IPv4 it has
      // (Wi-Fi LAN, Tailscale 100.x, Ethernet, VPNs) so the mobile connect
      // path can race them in parallel. Older agents don't send the field
      // at all — then undefined is correct and the mutation leaves the
      // stored list untouched.
      // Treat both [] and null as deliberate clear. Pre-fix only Array
      // values made it through, so a Go agent sending nil-slice → JSON
      // null was silently ignored, leaving stale Docker-bridge IPs on
      // the device row across upgrades.
      localIps: Array.isArray(body.localIps)
        ? body.localIps
        : body.localIps === null
          ? []
          : undefined,
      publicEndpoints: Array.isArray(body.publicEndpoints)
        ? body.publicEndpoints
        : body.publicEndpoints === null
          ? []
          : undefined,
      relayConnected:
        typeof body.relayConnected === "boolean" ? body.relayConnected : undefined,
      canReboot:
        typeof body.canReboot === "boolean" ? body.canReboot : undefined,
      hardwareId: body.hardwareId || undefined,
      hardwareProfile: body.hardwareProfile || undefined,
      deviceClass: body.deviceClass || undefined,
      geoRegion: typeof body.geoRegion === "string" ? body.geoRegion : undefined,
      edgeProfile: body.edgeProfile || undefined,
      recoveryPosture: body.recoveryPosture || undefined,
      agentVersion: typeof body.agentVersion === "string" ? body.agentVersion : undefined,
      publishCapabilities: Array.isArray(body.publishCapabilities)
        ? body.publishCapabilities
        : body.publishCapabilities === null
          ? []
          : undefined,
      // Probed deploy capability. Unlike publishCapabilities there is no
      // null->[] coercion: the agent omits these entirely until its first
      // probe completes, and an omitted field must leave the stored result
      // alone rather than clear it.
      connStatus:
        body.connStatus && typeof body.connStatus === "object" ? body.connStatus : undefined,
      deployCapabilities: Array.isArray(body.deployCapabilities)
        ? body.deployCapabilities
        : undefined,
      deployCapabilitiesBlocked: Array.isArray(body.deployCapabilitiesBlocked)
        ? body.deployCapabilitiesBlocked
        : undefined,
      deployCapabilitiesAt:
        typeof body.deployCapabilitiesAt === "string" ? body.deployCapabilitiesAt : undefined,
      // Batched CPU/RAM samples folded into the heartbeat (replaces the
      // separate /devices/metrics 60s poll). Validated by the mutation's
      // array schema; only forwarded when it's actually an array.
      metricsSamples: Array.isArray(body.metricsSamples) ? body.metricsSamples : undefined,
    });

    return jsonResponse({
      ok: true,
      connectionPreferences: heartbeatResult?.connectionPreferences ?? [],
      // Tell the agent whether to bother polling the claim endpoints this
      // cycle — a quiet box then makes zero rescue/publish claim calls.
      pendingRescue: heartbeatResult?.pendingRescue ?? false,
      pendingPublish: heartbeatResult?.pendingPublish ?? false,
      // Non-null when some surface asked this box to update while it
      // was unreachable. `?? null` (not `?? false`) so the agent can
      // tell "no request" from "a request for a version". Absent on an
      // old backend, which older agents correctly read as no request.
      desiredAgentVersion: heartbeatResult?.desiredAgentVersion ?? null,
    });
  }),
});

/** POST /devices/request-update — Any surface: ask a box to update its
 *  agent, reachable or not. Owner-only.
 *
 *  Unlike /agent-rescue/queue this never expires, which is the whole
 *  point: the caller may be a TV on a different network, or a watch
 *  that has no route to the box at all. The request waits on the device
 *  row until the box next heartbeats. Body: {deviceId, version?}. */
http.route({
  path: "/devices/request-update",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string") {
      return errorResponse("deviceId required", 400);
    }
    try {
      const out = await ctx.runMutation(api.devices.requestAgentUpdate, {
        tokenHash,
        deviceId: body.deviceId,
        version: typeof body.version === "string" ? body.version : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "request failed", 400);
    }
  }),
});

/** POST /devices/claim-update — Agent: atomically read-and-clear its own
 *  pending update request. Called only when a heartbeat response carried
 *  a non-null desiredAgentVersion. */
http.route({
  path: "/devices/claim-update",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string") {
      return errorResponse("deviceId required", 400);
    }
    try {
      const out = await ctx.runMutation(api.devices.claimAgentUpdateRequest, {
        tokenHash,
        deviceId: body.deviceId,
      });
      return jsonResponse({ ok: true, version: out.version });
    } catch (e: any) {
      return errorResponse(e?.message || "claim failed", 400);
    }
  }),
});

/** POST /agent-rescue/queue — Web/mobile/CLI: queue a rescue command
 *  for a wedged device. Owner-only. Returns the existing pending row
 *  if one with the same (deviceId, command) is still alive (5-min
 *  TTL) so impatient double-clicks don't pile up restarts. */
http.route({
  path: "/agent-rescue/queue",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string" || typeof body.command !== "string") {
      return errorResponse("deviceId + command required", 400);
    }
    if (!["restart", "reinstall-latest", "tunnel-reset", "auth-reset"].includes(body.command)) {
      return errorResponse("unknown command", 400);
    }
    try {
      const out = await ctx.runMutation(api.agentRescue.queueRescueCommand, {
        tokenHash,
        deviceId: body.deviceId,
        command: body.command,
        params: body.params,
        sourceSurface: typeof body.sourceSurface === "string" ? body.sourceSurface : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "queue failed", 400);
    }
  }),
});

/** POST /agent-rescue/claim — Agent: pull next pending command for
 *  its own device. Atomically transitions pending → claimed. The
 *  agent calls this from its heartbeat loop; null result is the
 *  steady state (nothing to do). */
http.route({
  path: "/agent-rescue/claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string") {
      return errorResponse("deviceId required", 400);
    }
    try {
      const out = await ctx.runMutation(api.agentRescue.claimNextRescueCommand, {
        tokenHash,
        deviceId: body.deviceId,
      });
      return jsonResponse({ ok: true, command: out });
    } catch (e: any) {
      return errorResponse(e?.message || "claim failed", 400);
    }
  }),
});

/** POST /agent-rescue/report — Agent: report claimed command's
 *  outcome (completed/failed + short result tail). Idempotent —
 *  retries after a network hiccup are safe. */
http.route({
  path: "/agent-rescue/report",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.commandId !== "string" || typeof body.status !== "string") {
      return errorResponse("commandId + status required", 400);
    }
    if (!["completed", "failed"].includes(body.status)) {
      return errorResponse("status must be completed|failed", 400);
    }
    try {
      const out = await ctx.runMutation(api.agentRescue.reportRescueResult, {
        tokenHash,
        commandId: body.commandId as any,
        status: body.status,
        result: typeof body.result === "string" ? body.result.slice(0, 2048) : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "report failed", 400);
    }
  }),
});

/** GET /agent-rescue/list?deviceId=... — UI: list recent rescue
 *  commands for a device. Owner-only. */
http.route({
  path: "/agent-rescue/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId") || "";
    if (!deviceId) {
      return errorResponse("deviceId required", 400);
    }
    const limitParam = url.searchParams.get("limit");
    const limit = limitParam ? Math.max(1, Math.min(parseInt(limitParam, 10) || 10, 50)) : 10;
    try {
      const rows = await ctx.runQuery(api.agentRescue.listRescueCommandsForDevice, {
        tokenHash,
        deviceId,
        limit,
      });
      return jsonResponse({ ok: true, commands: rows ?? [] });
    } catch (e: any) {
      return errorResponse(e?.message || "list failed", 400);
    }
  }),
});

// ── Publish-job queue (Phase 2 — async "tap Publish, walk away"). ──
// Same shape as /agent-rescue/* above, pairing with publishJobs.ts +
// desktop/agent/publish_worker.go. Privacy: app NAME + targets only,
// never a path or build log.

/** POST /publish-jobs/queue — CLI/mobile/web: enqueue a publish for a
 *  Mac-farm node. Owner-only. Returns the existing job if an
 *  identical one is still in flight. */
http.route({
  path: "/publish-jobs/queue",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (
      !body ||
      typeof body.deviceId !== "string" ||
      typeof body.app !== "string" ||
      typeof body.stack !== "string" ||
      !Array.isArray(body.targets)
    ) {
      return errorResponse("deviceId + app + stack + targets[] required", 400);
    }
    try {
      const out = await ctx.runMutation(api.publishJobs.queuePublishJob, {
        tokenHash,
        deviceId: body.deviceId,
        app: body.app,
        stack: body.stack,
        targets: body.targets.map((t: any) => String(t)),
        sourceSurface:
          typeof body.sourceSurface === "string" ? body.sourceSurface : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "queue failed", 400);
    }
  }),
});

/** POST /publish-jobs/claim — Farm node: pull next queued job for its
 *  own device. Atomic queued → claimed. Called from the heartbeat
 *  loop; null is the steady state. */
http.route({
  path: "/publish-jobs/claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string") {
      return errorResponse("deviceId required", 400);
    }
    try {
      const out = await ctx.runMutation(api.publishJobs.claimNextPublishJob, {
        tokenHash,
        deviceId: body.deviceId,
      });
      return jsonResponse({ ok: true, job: out });
    } catch (e: any) {
      return errorResponse(e?.message || "claim failed", 400);
    }
  }),
});

/** POST /publish-jobs/progress — Farm node: keep a long build alive
 *  (refreshes lastProgressAt; claimed → running). Short message only,
 *  never log output. */
http.route({
  path: "/publish-jobs/progress",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.jobId !== "string") {
      return errorResponse("jobId required", 400);
    }
    try {
      const out = await ctx.runMutation(api.publishJobs.reportPublishJobProgress, {
        tokenHash,
        jobId: body.jobId,
        message:
          typeof body.message === "string" ? body.message.slice(0, 200) : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "progress failed", 400);
    }
  }),
});

/** POST /publish-jobs/report — Farm node: terminal outcome
 *  (done|failed + per-target metadata). Idempotent. */
http.route({
  path: "/publish-jobs/report",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || typeof body.jobId !== "string" || typeof body.status !== "string") {
      return errorResponse("jobId + status required", 400);
    }
    if (!["done", "failed"].includes(body.status)) {
      return errorResponse("status must be done|failed", 400);
    }
    try {
      const out = await ctx.runMutation(api.publishJobs.reportPublishJobResult, {
        tokenHash,
        jobId: body.jobId,
        status: body.status,
        result: Array.isArray(body.result) ? body.result : undefined,
        message:
          typeof body.message === "string" ? body.message.slice(0, 500) : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "report failed", 400);
    }
  }),
});

/** GET /publish-jobs/list?deviceId=... — UI/CLI: recent publish jobs
 *  for the caller. Owner-only. */
http.route({
  path: "/publish-jobs/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId") || undefined;
    const limitParam = url.searchParams.get("limit");
    const limit = limitParam
      ? Math.max(1, Math.min(parseInt(limitParam, 10) || 20, 100))
      : 20;
    try {
      const rows = await ctx.runQuery(api.publishJobs.listPublishJobsForOwner, {
        tokenHash,
        deviceId,
        limit,
      });
      return jsonResponse({ ok: true, jobs: rows ?? [] });
    } catch (e: any) {
      return errorResponse(e?.message || "list failed", 400);
    }
  }),
});

/** POST /devices/report-version — Owner-side seed for agentVersion.
 *
 * The dashboard probes /info on a device it can reach, observes the
 * `version` string, and pushes it here so Convex has the value even
 * before the agent itself is upgraded to a build that sends it in
 * register/heartbeat.
 */
http.route({
  path: "/devices/report-version",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (
      !body ||
      typeof body.deviceId !== "string" ||
      typeof body.agentVersion !== "string" ||
      !body.agentVersion.trim()
    ) {
      return errorResponse("deviceId + agentVersion required", 400);
    }
    try {
      await ctx.runMutation(api.devices.reportAgentVersion, {
        tokenHash,
        deviceId: body.deviceId,
        agentVersion: body.agentVersion,
      });
    } catch (e: any) {
      return errorResponse(e?.message || "report failed", 400);
    }
    return jsonResponse({ ok: true });
  }),
});

/** POST /devices/presence — Relay-driven tunnel-up/down notifier.
 *
 * Called by the Yaver relay binary when a device's QUIC tunnel opens or
 * closes. Lets the reactive Convex UI flip online/offline within ~100ms
 * of the event, without waiting for the device's 30s heartbeat.
 *
 * Auth is a shared secret header (X-Relay-Secret) matched against
 * `platformConfig["relay_presence_secret"]`. The relay is trusted
 * platform infrastructure; this is distinct from user session auth.
 * Dropping the platformConfig entry disables the endpoint (every call
 * returns 401). Rotation = write a new entry + update the relay's
 * CONVEX_PRESENCE_SECRET env var.
 */
http.route({
  path: "/devices/presence",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const provided = request.headers.get("X-Relay-Secret") ?? "";
    if (!provided) return errorResponse("Missing X-Relay-Secret", 401);
    const expected = await ctx.runQuery(internal.platformConfig.get, {
      key: "relay_presence_secret",
    });
    if (!expected) {
      // Feature not enabled on this deployment — opt in by writing the
      // secret via `npx convex run platformConfig:set --key=relay_presence_secret --value=<secret>`.
      return errorResponse("Presence endpoint disabled", 404);
    }
    if (provided !== expected) {
      return errorResponse("Bad relay secret", 401);
    }
    const body = await request.json().catch(() => ({}));
    if (!body?.deviceId || typeof body.deviceId !== "string") {
      return errorResponse("deviceId required", 400);
    }
    await ctx.runMutation(internal.devices.presenceUpdate, {
      deviceId: body.deviceId,
      online: body.online === true,
      peerAddr: typeof body.peerAddr === "string" ? body.peerAddr : undefined,
      connectedAt: typeof body.connectedAt === "number" ? body.connectedAt : undefined,
      durationSec: typeof body.durationSec === "number" ? body.durationSec : undefined,
      // Relay-auto-provisioned <id>.dev.yaver.io URL. Stored in
      // device.publicEndpoints so the dashboard's transport
      // classifier picks it instantly (no waiting for next agent
      // heartbeat). Only sent on online=true presence pushes.
      assignedUrl: typeof body.assignedUrl === "string" ? body.assignedUrl : undefined,
    });
    return jsonResponse({ ok: true });
  }),
});

/** POST /devices/placement/recommend — Recommend edge vs infra placement for a task. */
http.route({
  path: "/devices/placement/recommend",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body?.taskKind || typeof body.taskKind !== "string") {
      return errorResponse("taskKind required", 400);
    }
    try {
      const recommendation = await ctx.runQuery((api as any).devices.recommendTaskPlacement, {
        tokenHash,
        taskKind: body.taskKind,
      });
      return jsonResponse(recommendation);
    } catch (e: any) {
      return errorResponse(e?.message || "could not recommend placement", 400);
    }
  }),
});

/** GET /devices/list — List user's devices (authed). */
http.route({
  path: "/devices/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    // listMyDevices THROWS "Unauthorized" when the session token is
    // stale/rotated/revoked. An uncaught throw in an httpAction returns
    // HTTP 500 — which clients (mobile, web) treat as a transient server
    // error and silently swallow, leaving the user "signed in" but with
    // an empty device list and no hint to re-auth (a registered Mac goes
    // invisible on the phone). Map the auth throw to a real 401 so every
    // client can route it through its token-refresh / sign-in recovery.
    let devices;
    try {
      devices = await ctx.runQuery(api.devices.listMyDevices, { tokenHash });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (msg.includes("Unauthorized")) {
        return errorResponse("Unauthorized", 401);
      }
      throw err;
    }

    return jsonResponse({ devices });
  }),
});

/**
 * GET /devices/flight?deviceId=<id>[&limit=<n>] — read a device's black box.
 *
 * The readback half of the flight recorder: when a box goes silent, its last
 * records say whether it stopped gracefully (`shutdown`) or died hard
 * (anything else). Written by the /devices/heartbeat piggyback.
 */
http.route({
  path: "/devices/flight",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const url = new URL(request.url);
    const deviceId = (url.searchParams.get("deviceId") ?? "").trim();
    if (!deviceId) return errorResponse("deviceId is required", 400);
    const rawLimit = url.searchParams.get("limit");
    const limit = rawLimit ? Number(rawLimit) : undefined;
    if (limit !== undefined && (!Number.isFinite(limit) || limit <= 0)) {
      return errorResponse("limit must be a positive number", 400);
    }

    try {
      const events = await ctx.runQuery(api.devices.flightEvents, { tokenHash, deviceId, limit });
      return jsonResponse({ events });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (msg.includes("Unauthorized")) return errorResponse("Unauthorized", 401);
      if (msg.includes("Device not found")) return errorResponse("Device not found", 404);
      throw err;
    }
  }),
});

// --- Yaver Mesh console routes (web/mobile) — session-token-hash auth ---

http.route({
  path: "/mesh/peers",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const res = await ctx.runQuery(api.mesh.listMeshPeersWeb, { tokenHash });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/acls",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const rules = await ctx.runQuery(api.mesh.listMeshAclsWeb, { tokenHash });
      return jsonResponse({ rules });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/acls/set",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.mesh.setMeshAclsWeb, { tokenHash, rules: body.rules ?? [] });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/tags",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const tags = await ctx.runQuery(api.mesh.listMeshTagsWeb, { tokenHash });
      return jsonResponse({ tags });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/tags/set",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.mesh.tagDeviceWeb, { tokenHash, deviceId: body.deviceId ?? "", tags: body.tags ?? [] });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg.includes("Unauthorized") ? "Unauthorized" : msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/join",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      const res = await ctx.runMutation(api.mesh.joinMeshWeb, {
        tokenHash,
        deviceId: body.deviceId ?? "",
        wgPublicKey: body.wgPublicKey ?? "",
        endpoints: body.endpoints ?? [],
        meshIPv6: body.meshIPv6,
        advertisedRoutes: body.advertisedRoutes,
        isExitNode: body.isExitNode,
      });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/leave",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.mesh.leaveMeshWeb, { tokenHash, deviceId: body.deviceId ?? "" });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

// --- Yaver Support Link routes (web/landing) ---

http.route({
  path: "/support/invite",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      const res = await ctx.runMutation(api.support_link.createSupportInviteWeb, {
        tokenHash,
        offerTerminal: body.offerTerminal,
        offerDesktopControl: body.offerDesktopControl,
        defaultTtlHours: body.defaultTtlHours,
        label: body.label,
        singleUse: body.singleUse,
      });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

// Public (no auth) — the landing/consent page reads invite metadata before the
// friend signs in. Returns only the inviter's display identity + offered scope.
http.route({
  path: "/support/invite/info",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const code = new URL(request.url).searchParams.get("code") ?? "";
    if (!code) return errorResponse("code required", 400);
    const res = await ctx.runQuery(api.support_link.getSupportInviteInfo, { code });
    return jsonResponse(res);
  }),
});

http.route({
  path: "/support/connections",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const res = await ctx.runQuery(api.support_link.listSupportConnectionsWeb, { tokenHash });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/support/grant/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.support_link.revokeSupportGrantWeb, { tokenHash, grantId: body.grantId });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      const code = msg.includes("Unauthorized") ? 401 : msg.includes("Forbidden") ? 403 : 500;
      return errorResponse(msg, code);
    }
  }),
});

http.route({
  path: "/support/deny-all",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const res = await ctx.runMutation(api.support_link.denyAllSupportWeb, { tokenHash });
      return jsonResponse(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return errorResponse(msg, msg.includes("Unauthorized") ? 401 : 500);
    }
  }),
});

http.route({
  path: "/mesh/node/config",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      await ctx.runMutation(api.mesh.setMeshNodeConfigWeb, {
        tokenHash,
        deviceId: body.deviceId ?? "",
        wantEnabled: body.wantEnabled,
        wantExitNode: body.wantExitNode,
        wantUseExitNode: body.wantUseExitNode,
        wantRoutes: body.wantRoutes,
      });
      return jsonResponse({ ok: true });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      const code = msg.includes("Unauthorized") ? 401 : msg.includes("Forbidden") ? 403 : 500;
      return errorResponse(msg, code);
    }
  }),
});

/** GET /devices/pending-list — list bootstrap-pending claims that came
 *  in via the caller's managed-relay password. Returns [] when the
 *  user has no managed relay or no pending claims. The dashboard polls
 *  this alongside /devices/list and surfaces "pending" rows so a
 *  freshly-installed remote box becomes claimable in one tap. */
http.route({
  path: "/devices/pending-list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const result = await ctx.runQuery(api.pendingDeviceClaims.listForUser, {
      tokenHash,
    });
    return jsonResponse(result);
  }),
});

/** POST /devices/pending-claim — claim a bootstrap-pending box. The
 *  agent's relay-password hash must match the caller's managedRelay
 *  password hash; on success, a real `devices` row is created with
 *  needsAuth=true so the existing reauth/owner-claim UI can take over
 *  and finish the pairing handshake. */
http.route({
  path: "/devices/pending-claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => null);
    if (!body || !body.deviceId) {
      return errorResponse("deviceId required", 400);
    }
    try {
      const result = await ctx.runMutation(api.pendingDeviceClaims.claim, {
        tokenHash,
        deviceId: String(body.deviceId),
        name: typeof body.name === "string" ? body.name : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e?.message || "claim failed";
      // Map known shapes to clean status codes so the dashboard can
      // distinguish 404 (no row) from 403 (relay-password mismatch)
      // from 409 (already owned by another user).
      if (errorMessageIncludes(e, "no pending claim")) return errorResponse(msg, 404);
      if (errorMessageIncludes(e, "another user")) return errorResponse(msg, 409);
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse(msg, 401);
      if (errorMessageIncludes(e, "relay-password mismatch")) return errorResponse(msg, 403);
      if (errorMessageIncludes(e, "no managed relay")) return errorResponse(msg, 403);
      return errorResponse(msg, 400);
    }
  }),
});

/** POST /devices/offline — Mark device offline (authed). */
http.route({
  path: "/devices/offline",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    await ctx.runMutation(api.devices.markOffline, {
      tokenHash,
      deviceId: body.deviceId,
    });

    return jsonResponse({ ok: true });
  }),
});

/** POST /devices/remove — Remove a device (authed). */
http.route({
  path: "/devices/remove",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    await ctx.runMutation(api.devices.removeDevice, {
      tokenHash,
      deviceId: body.deviceId,
    });

    return jsonResponse({ ok: true });
  }),
});

/** POST /devices/alias — Set or clear a device alias (authed).
 *  Body: { deviceId, alias?: string }. Pass alias: "" or omit to
 *  clear. Aliases are scoped to the caller's user and must be unique
 *  across that user's devices — used by `yaver ssh <alias>`, the
 *  dashboard device list, and the mobile app. */
http.route({
  path: "/devices/alias",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string" || !body.deviceId.trim()) {
      return errorResponse("deviceId required", 400);
    }
    try {
      const result = await ctx.runMutation(api.devices.setDeviceAlias, {
        tokenHash,
        deviceId: body.deviceId,
        alias: typeof body.alias === "string" ? body.alias : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e?.message || "alias update failed";
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse(msg, 401);
      if (errorMessageIncludes(e, "Device not found")) return errorResponse(msg, 404);
      if (errorMessageIncludes(e, "alias already used")) return errorResponse(msg, 409);
      if (errorMessageIncludes(e, "alias invalid")) return errorResponse(msg, 400);
      return errorResponse(msg, 400);
    }
  }),
});

/** POST /devices/voice-hints — the SPOKEN names for a device (authed).
 *  Body: { deviceId, hints?: string[] } to replace, or { deviceId, add?, remove? }
 *  to mutate. These are what a driver says out loud — "my mac mini", "the box
 *  at maltepe" — as opposed to `alias`, which is one short token typed at a
 *  shell. They exist because CarPlay's voice category forbids drawing a device
 *  picker on the car screen, so speaking the name is the only way to retarget a
 *  turn while driving. Consumed by carMachineSwitch.ts on the phone. */
http.route({
  path: "/devices/voice-hints",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string" || !body.deviceId.trim()) {
      return errorResponse("deviceId required", 400);
    }
    const strArray = (v: unknown): string[] | undefined =>
      Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : undefined;

    try {
      const result = await ctx.runMutation(api.devices.setDeviceVoiceHints, {
        tokenHash,
        deviceId: body.deviceId,
        hints: strArray(body.hints),
        add: strArray(body.add),
        remove: strArray(body.remove),
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e?.message || "voice-hints update failed";
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse(msg, 401);
      if (errorMessageIncludes(e, "Device not found")) return errorResponse(msg, 404);
      return errorResponse(msg, 400);
    }
  }),
});

// Fleet tags — set/add/remove labels on a device for selector-based ops.
http.route({
  path: "/devices/tags",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (!body || typeof body.deviceId !== "string" || !body.deviceId.trim()) {
      return errorResponse("deviceId required", 400);
    }
    try {
      const result = await ctx.runMutation(api.devices.setDeviceTags, {
        tokenHash,
        deviceId: body.deviceId,
        tags: Array.isArray(body.tags) ? body.tags : undefined,
        add: Array.isArray(body.add) ? body.add : undefined,
        remove: Array.isArray(body.remove) ? body.remove : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e?.message || "tag update failed";
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse(msg, 401);
      if (errorMessageIncludes(e, "Device not found")) return errorResponse(msg, 404);
      return errorResponse(msg, 400);
    }
  }),
});

// Fleet selector — resolve the caller's devices matching tags/platform/online.
// The Fleet SDK calls this to turn `select({tags,...})` into machines.
http.route({
  path: "/devices/select",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    try {
      const result = await ctx.runQuery(api.devices.selectDevices, {
        tokenHash,
        tags: Array.isArray(body.tags) ? body.tags : undefined,
        match: body.match === "any" ? "any" : body.match === "all" ? "all" : undefined,
        platform: typeof body.platform === "string" ? body.platform : undefined,
        online: typeof body.online === "boolean" ? body.online : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e?.message || "select failed";
      if (errorMessageIncludes(e, "Unauthorized")) return errorResponse(msg, 401);
      return errorResponse(msg, 400);
    }
  }),
});

// ── Device Metrics & Events ──────────────────────────────────────────

/** POST /devices/metrics — Report CPU/RAM metrics (authed, called by agent every 60s). */
http.route({
  path: "/devices/metrics",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }

    // Validate session at HTTP layer — return 401, not 500
    const user = await authenticateRequest(ctx, request);
    if (!user) {
      return errorResponse("Session expired", 401);
    }

    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.deviceMetrics.report, {
        tokenHash,
        deviceId: body.deviceId,
        cpuPercent: body.cpuPercent,
        memoryUsedMb: body.memoryUsedMb,
        memoryTotalMb: body.memoryTotalMb,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to report metrics", 500);
    }
  }),
});

/** GET /devices/metrics?deviceId=xxx — Get metrics for a device (authed). */
http.route({
  path: "/devices/metrics",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId");
    if (!deviceId) return errorResponse("deviceId required", 400);

    const metrics = await ctx.runQuery(api.deviceMetrics.getMetrics, {
      tokenHash,
      deviceId,
    });
    return jsonResponse({ metrics });
  }),
});

/** POST /devices/event — Record a device event (crash, restart, etc.) (authed). */
http.route({
  path: "/devices/event",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.deviceEvents.record, {
        tokenHash,
        deviceId: body.deviceId,
        event: body.event,
        details: body.details,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to record event", 500);
    }
  }),
});

/** GET /devices/events?deviceId=xxx — Get recent events for a device (authed). */
http.route({
  path: "/devices/events",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId");
    if (!deviceId) return errorResponse("deviceId required", 400);

    const events = await ctx.runQuery(api.deviceEvents.getEvents, {
      tokenHash,
      deviceId,
    });
    return jsonResponse({ events });
  }),
});

/** POST /usage/record — Record runner usage when a task finishes (authed). */
http.route({
  path: "/usage/record",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.runnerUsage.record, {
        tokenHash,
        deviceId: body.deviceId,
        taskId: body.taskId,
        runner: body.runner,
        model: body.model,
        durationSec: body.durationSec,
        startedAt: body.startedAt,
        finishedAt: body.finishedAt,
        source: body.source,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to record usage", 500);
    }
  }),
});

/** GET /usage — Get usage summary with daily aggregation (authed). */
http.route({
  path: "/usage",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const since = url.searchParams.get("since");

    const usage = await ctx.runQuery(api.runnerUsage.getUsage, {
      tokenHash,
      since: since ? parseInt(since) : undefined,
    });
    return jsonResponse(usage);
  }),
});

/** POST /tasks/placement/preview — Decide where a task should run without storing it. */
http.route({
  path: "/tasks/placement/preview",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const result = await ctx.runQuery(api.taskPlacement.preview, {
        tokenHash,
        kind: body.kind ?? "unknown",
        projectSlug: body.projectSlug,
        requestedRunner: body.requestedRunner,
        targetDeviceId: body.targetDeviceId,
        forceCloud: body.forceCloud,
        forceRelaySource: body.forceRelaySource,
        sourceSurface: body.sourceSurface,
        appCount: body.appCount,
        repoSizeMb: body.repoSizeMb,
        fileCount: body.fileCount,
        hasNativeMobile: body.hasNativeMobile,
        hasDocker: body.hasDocker,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to preview task placement", 500);
    }
  }),
});

/** POST /tasks/placement/record — Store the placement decision for a task. */
http.route({
  path: "/tasks/placement/record",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const result = await ctx.runMutation(api.taskPlacement.record, {
        tokenHash,
        taskId: body.taskId,
        kind: body.kind ?? "unknown",
        sourceSurface: body.sourceSurface,
        projectSlug: body.projectSlug,
        requestedRunner: body.requestedRunner,
        targetDeviceId: body.targetDeviceId,
        forceCloud: body.forceCloud,
        forceRelaySource: body.forceRelaySource,
        appCount: body.appCount,
        repoSizeMb: body.repoSizeMb,
        fileCount: body.fileCount,
        hasNativeMobile: body.hasNativeMobile,
        hasDocker: body.hasDocker,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to record task placement", 500);
    }
  }),
});

/** GET /tasks/placement/recent — Recent placement decisions for the caller. */
http.route({
  path: "/tasks/placement/recent",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    try {
      const result = await ctx.runQuery(api.taskPlacement.listRecent, {
        tokenHash,
        projectSlug: url.searchParams.get("projectSlug") || undefined,
        limit: url.searchParams.get("limit") ? Number(url.searchParams.get("limit")) : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to list task placements", 500);
    }
  }),
});

/** GET /tasks/placement/status — Read a stored placement's lifecycle state plus
 *  latest wake/provision progress. Metadata only; no prompt, source, logs, IPs,
 *  hostnames, or repo paths. */
http.route({
  path: "/tasks/placement/status",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    const placementId = url.searchParams.get("placementId") || undefined;
    const taskId = url.searchParams.get("taskId") || undefined;
    try {
      const result = await ctx.runQuery(api.taskPlacement.getStatus, {
        tokenHash,
        placementId: placementId as any,
        taskId,
      });
      return jsonResponse(result);
    } catch (e: any) {
      const msg = e.message || "Failed to read task placement";
      return errorResponse(msg, /not found/i.test(msg) ? 404 : 500);
    }
  }),
});

/** POST /tasks/placement/status — Update a stored placement's lifecycle state. */
http.route({
  path: "/tasks/placement/status",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const result = await ctx.runMutation(api.taskPlacement.markStatus, {
        tokenHash,
        placementId: body.placementId,
        status: body.status,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to update task placement", 500);
    }
  }),
});

/** POST /tasks/placement/rebind — Replace a local pending task id with the
 *  real agent task id after a client-held Cloud Workspace dispatch fires.
 *  This keeps Convex metadata linked to the actual task without storing the
 *  prompt/body centrally while the workspace wakes. */
http.route({
  path: "/tasks/placement/rebind",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const result = await ctx.runMutation(api.taskPlacement.rebindTask, {
        tokenHash,
        placementId: body.placementId,
        taskId: body.taskId,
        status: body.status,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to rebind task placement", 500);
    }
  }),
});

/** POST /tasks/dispatch-intents — prompt-free durable dispatch metadata.
 *  The body MUST NOT contain prompt, description, workDir, files, image data,
 *  stdout, or secrets. It only records ids/target/status so a cloud-bound local
 *  task can survive refresh while the actual task body stays client-held. */
http.route({
  path: "/tasks/dispatch-intents",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const result = await ctx.runMutation(api.taskDispatchIntents.create, {
        tokenHash,
        localTaskId: body.localTaskId,
        placementId: body.placementId || undefined,
        sourceSurface: body.sourceSurface,
        lane: body.lane,
        targetDeviceId: body.targetDeviceId || undefined,
        cloudMachineId: body.cloudMachineId || undefined,
        requestedRunner: body.requestedRunner,
        projectSlug: body.projectSlug,
        reason: body.reason,
        ttlMs: body.ttlMs,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to create dispatch intent", 500);
    }
  }),
});

/** POST /tasks/dispatch-intents/status — update prompt-free dispatch state. */
http.route({
  path: "/tasks/dispatch-intents/status",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const result = await ctx.runMutation(api.taskDispatchIntents.update, {
        tokenHash,
        intentId: body.intentId,
        localTaskId: body.localTaskId,
        status: body.status,
        taskId: body.taskId,
        targetDeviceId: body.targetDeviceId,
        lastError: body.lastError,
        reason: body.reason,
        blockedAction: body.blockedAction,
        clearBlockedAction: body.clearBlockedAction,
        bumpAttempt: body.bumpAttempt,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to update dispatch intent", 500);
    }
  }),
});

/** GET /tasks/dispatch-intents — recent prompt-free dispatch metadata. */
http.route({
  path: "/tasks/dispatch-intents",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    try {
      const result = await ctx.runQuery(api.taskDispatchIntents.listRecent, {
        tokenHash,
        limit: url.searchParams.get("limit") ? Number(url.searchParams.get("limit")) : undefined,
        includeTerminal: url.searchParams.get("includeTerminal") === "1",
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to list dispatch intents", 500);
    }
  }),
});

/** POST /tasks/relay-source-intents — prompt-free, branch-scoped relay source
 *  work for Yaver-managed project shares. The body MUST NOT contain prompts,
 *  diffs, file paths, stdout, tokens, vault refs, or runner OAuth. */
http.route({
  path: "/tasks/relay-source-intents",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const result = await ctx.runMutation(api.relaySourceIntents.create, {
        tokenHash,
        localTaskId: body.localTaskId,
        placementId: body.placementId || undefined,
        shareId: body.shareId || undefined,
        projectSlug: body.projectSlug,
        sourceSurface: body.sourceSurface,
        kind: body.kind,
        branch: body.branch,
        reason: body.reason,
        ttlMs: body.ttlMs,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to create relay source intent", 500);
    }
  }),
});

/** POST /tasks/relay-source-intents/claim — owner relay pulls the next queued
 *  prompt-free source intent for one of its project shares. */
http.route({
  path: "/tasks/relay-source-intents/claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const result = await ctx.runMutation(api.relaySourceIntents.claimNext, {
        tokenHash,
        projectSlug: body.projectSlug,
        relayId: body.relayId,
      });
      return jsonResponse(result ?? { ok: true, intent: null });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to claim relay source intent", 500);
    }
  }),
});

/** POST /tasks/relay-source-intents/status — update relay source metadata. */
http.route({
  path: "/tasks/relay-source-intents/status",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const result = await ctx.runMutation(api.relaySourceIntents.update, {
        tokenHash,
        intentId: body.intentId,
        localTaskId: body.localTaskId,
        status: body.status,
        taskId: body.taskId,
        relayId: body.relayId,
        reason: body.reason,
        lastError: body.lastError,
        providerKind: body.providerKind,
        providerHost: body.providerHost,
        providerRepo: body.providerRepo,
        providerBranch: body.providerBranch,
        providerBranchUrl: body.providerBranchUrl,
        providerAppInstallationId: body.providerAppInstallationId,
        providerAuthMode: body.providerAuthMode,
        providerAuthStatus: body.providerAuthStatus,
        bumpAttempt: body.bumpAttempt,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to update relay source intent", 500);
    }
  }),
});

/** POST /tasks/relay-source-intents/github-app-token — owner-only short-lived
 *  GitHub App installation token broker for one relay-source intent. The
 *  response token is not stored in Convex; only non-secret installation/auth
 *  status metadata is recorded back onto the intent. */
http.route({
  path: "/tasks/relay-source-intents/github-app-token",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const env = githubAppEnvFromProcess();
    if (!env) {
      return errorResponse("GitHub App token broker is not configured", 503);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const target = await ctx.runQuery(api.relaySourceIntents.githubAppAuthTarget, {
        tokenHash,
        intentId: body.intentId,
        localTaskId: body.localTaskId,
      });
      if (target.providerHost !== "github.com") {
        return errorResponse("GitHub App token broker currently supports github.com only", 400);
      }
      const repo = parseGitHubRepoFullName(target.providerRepo);
      if (!repo) return errorResponse("Invalid GitHub repo target", 400);
      const token = await requestGitHubInstallationTokenForRepo({ env, repo });
      const branchUrl = `https://github.com/${repo.fullName}/tree/${String(target.providerBranch).split("/").map(encodeURIComponent).join("/")}`;
      await ctx.runMutation(api.relaySourceIntents.update, {
        tokenHash,
        intentId: target.id,
        status: "handoff_ready",
        providerKind: "github",
        providerHost: target.providerHost,
        providerRepo: repo.fullName,
        providerBranch: target.providerBranch,
        providerBranchUrl: branchUrl,
        providerAppInstallationId: token.installationId,
        providerAuthMode: "app_installation",
        providerAuthStatus: "available",
        reason: "GitHub App installation token minted for scoped relay-source branch push",
      });
      return jsonResponse({
        ok: true,
        providerKind: "github",
        providerHost: target.providerHost,
        providerRepo: repo.fullName,
        providerBranch: target.providerBranch,
        providerBranchUrl: branchUrl,
        providerAppInstallationId: token.installationId,
        providerAuthMode: "app_installation",
        providerAuthStatus: "available",
        token: token.token,
        expiresAt: token.expiresAt ?? null,
        permissions: token.permissions ?? null,
      });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to mint GitHub App token", 400);
    }
  }),
});

/** POST /tasks/relay-source-intents/gitlab-token — explicit GitLab scoped
 *  token boundary. GitLab has write_repository OAuth/PAT/project-token paths,
 *  but no GitHub-App-style server-side installation token Yaver can mint
 *  without holding a user/project secret. We record a non-secret unsupported
 *  status so the relay/UI can fall back honestly. */
http.route({
  path: "/tasks/relay-source-intents/gitlab-token",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    try {
      const target = await ctx.runQuery(api.relaySourceIntents.gitlabScopedAuthTarget, {
        tokenHash,
        intentId: body.intentId,
        localTaskId: body.localTaskId,
      });
      await ctx.runMutation(api.relaySourceIntents.update, {
        tokenHash,
        intentId: target.id,
        status: "handoff_ready",
        providerKind: "gitlab",
        providerHost: target.providerHost,
        providerRepo: target.providerRepo,
        providerBranch: target.providerBranch,
        providerBranchUrl: target.providerBranchUrl || undefined,
        providerAuthMode: "none",
        providerAuthStatus: "unsupported",
        reason: "GitLab has no backend-minted app installation token equivalent; use owner-local GitLab token fallback",
      });
      return jsonResponse({
        ok: false,
        providerKind: "gitlab",
        providerHost: target.providerHost,
        providerRepo: target.providerRepo,
        providerBranch: target.providerBranch,
        providerBranchUrl: target.providerBranchUrl ?? null,
        providerAuthMode: "none",
        providerAuthStatus: "unsupported",
        reason: "GitLab scoped write requires a user OAuth token, PAT, project access token, group access token, deploy token, or CI job token. Yaver will not mint or store those as Convex secrets; owner-local GitLab token fallback remains supported.",
      }, 501);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to evaluate GitLab scoped token support", 400);
    }
  }),
});

/** GET /tasks/relay-source-intents — recent relay-source metadata. */
http.route({
  path: "/tasks/relay-source-intents",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    try {
      const result = await ctx.runQuery(api.relaySourceIntents.listRecent, {
        tokenHash,
        projectSlug: url.searchParams.get("projectSlug") || undefined,
        limit: url.searchParams.get("limit") ? Number(url.searchParams.get("limit")) : undefined,
        includeTerminal: url.searchParams.get("includeTerminal") === "1",
        scope: (url.searchParams.get("scope") as any) || undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to list relay source intents", 500);
    }
  }),
});

/** POST /tasks/placement/activate — Ensure a recorded cloud placement has
 *  backing capacity. Cloud lanes wake an existing managed workspace or schedule
 *  one from the caller's active Cloud Workspace subscription. Relay/owned/manual
 *  lanes are idempotent no-ops. */
http.route({
  path: "/tasks/placement/activate",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;

    const body = await request.json().catch(() => ({}));
    const denied = promptFreeMetadataBodyDeniedReason(body);
    if (denied) return errorResponse(denied, 400);
    const placementId = String(body.placementId ?? "").trim();
    const taskId = String(body.taskId ?? "").trim();
    if (!placementId && !taskId) return errorResponse("placementId or taskId is required", 400);

    const placement = await ctx.runQuery(internal.taskPlacement.getForActivation, {
      userId: session.userDocId as any,
      placementId: placementId ? placementId as any : undefined,
      taskId: taskId || undefined,
    });
    if (!placement) return errorResponse("Placement not found", 404);

    const lane = String(placement.lane || "");
    if (!lane.startsWith("cloud_")) {
      return jsonResponse({
        ok: true,
        action: "none",
        lane,
        status: placement.status,
        reason: "placement does not require managed cloud activation",
      });
    }

    const sub = await ctx.runQuery(internal.subscriptions.getByUser, {
      userId: session.userDocId as any,
    });
    const hasActiveCloudWorkspace =
      (sub?.status === "active" || sub?.status === "past_due") &&
      productForSubscriptionPlan(sub.plan) === "cloud-workspace";
    const ownerDev = isOwner(session.email, String(session.userDocId));
    if (!hasActiveCloudWorkspace && !ownerDev) {
      return jsonResponse({
        ok: false,
        action: "billing_required",
        productId: "cloud-workspace",
        reason: "Cloud Workspace activation requires an active Cloud Workspace subscription",
      }, 402);
    }

    const existingMachinesRaw = await ctx.runQuery(internal.cloudMachines.listForUser, {
      userId: session.userDocId as any,
    });
    const existingMachines = Array.isArray(existingMachinesRaw)
      ? existingMachinesRaw.filter((machine: any) =>
          String(machine.userId) === String(session.userDocId) &&
          cloudMachineEligibleForPlacement(machine)
        )
      : [];
    const machine = selectCloudMachineForPlacement(
      existingMachines,
      placement.resourceClass,
      placement.cloudMachineId,
    );

    if (machine) {
      const managedDenied = denyNonYaverManagedMachine(machine);
      if (managedDenied) return managedDenied;
      const targetDeviceId = machine.deviceId ?? managedDeviceIdFor(String(machine._id));
      const profileMatched = cloudMachineMeetsPlacement(machine, placement.resourceClass);
      await ctx.runMutation(internal.taskPlacement.attachCloudMachine, {
        userId: session.userDocId as any,
        placementId: placement._id,
        cloudMachineId: machine._id,
        targetDeviceId,
        status: machine.status === "active" && machine.runnersAuthorized !== false ? "running" : "queued",
      });
      if (machine.provisionPhase === "awaiting-yaver-auth") {
        const wakeRunId = await ctx.runMutation(internal.wakeRuns.start, {
          userId: session.userDocId as any,
          machineId: machine._id,
          placementId: placement._id,
          taskId: placement.taskId,
          kind: machine.status === "provisioning" ? "provision" : "wake",
          status: "blocked",
          phase: "awaiting-yaver-auth",
          progress: typeof machine.provisionProgress === "number" ? machine.provisionProgress : 88,
          resourceClass: placement.resourceClass,
          machineType: machine.machineType,
          targetDeviceId,
          reason: "machine is awake but the Yaver agent needs sign-in",
        });
        return jsonResponse({
          ok: false,
          action: "yaver_auth_required",
          machineId: machine._id,
          targetDeviceId,
          machineStatus: machine.status,
          phase: "awaiting-yaver-auth",
          profileMatched,
          wakeRunId,
          reason: "Cloud Workspace is awake but its Yaver agent needs sign-in before tasks can run.",
        }, 409);
      }
      if (machine.status === "active" && machine.runnersAuthorized === false) {
        const wakeRunId = await ctx.runMutation(internal.wakeRuns.start, {
          userId: session.userDocId as any,
          machineId: machine._id,
          placementId: placement._id,
          taskId: placement.taskId,
          kind: "wake",
          status: "blocked",
          phase: "authorizing-runners",
          progress: 90,
          resourceClass: placement.resourceClass,
          machineType: machine.machineType,
          targetDeviceId,
          reason: "machine is awake but coding runners need subscription sign-in",
        });
        return jsonResponse({
          ok: false,
          action: "runner_auth_required",
          machineId: machine._id,
          targetDeviceId,
          machineStatus: machine.status,
          phase: "authorizing-runners",
          profileMatched,
          wakeRunId,
          reason: "Cloud Workspace is awake but Claude Code/Codex/OpenCode needs sign-in before tasks can run.",
        }, 409);
      }
      if (machine.status === "active" || machine.status === "provisioning" || machine.status === "resuming") {
        let wakeRunId: any = null;
        if (machine.status !== "active") {
          wakeRunId = await ctx.runMutation(internal.wakeRuns.start, {
            userId: session.userDocId as any,
            machineId: machine._id,
            placementId: placement._id,
            taskId: placement.taskId,
            kind: machine.status === "provisioning" ? "provision" : "wake",
            status: "running",
            phase: machine.provisionPhase ?? machine.status,
            progress: typeof machine.provisionProgress === "number" ? machine.provisionProgress : undefined,
            resourceClass: placement.resourceClass,
            machineType: machine.machineType,
            targetDeviceId,
            reason: placement.reason,
          });
        }
        return jsonResponse({
          ok: true,
          action: machine.status === "active" ? "already_active" : "already_in_flight",
          machineId: machine._id,
          targetDeviceId,
          machineStatus: machine.status,
          profileMatched,
          wakeRunId,
        });
      }
      const wake = await ctx.runMutation(internal.cloudMachines.wake, {
        userId: session.userDocId as any,
        machineId: machine._id,
      });
      const wakeRunId = await ctx.runMutation(internal.wakeRuns.start, {
        userId: session.userDocId as any,
        machineId: machine._id,
        placementId: placement._id,
        taskId: placement.taskId,
        kind: "wake",
        status: wake.ok === false ? "failed" : "queued",
        phase: wake.ok === false ? "error" : "queued",
        resourceClass: placement.resourceClass,
        machineType: machine.machineType,
        targetDeviceId,
        reason: placement.reason,
      });
      return jsonResponse({
        ok: wake.ok !== false,
        action: wake.ok === false ? "wake_failed" : "wake_scheduled",
        machineId: machine._id,
        targetDeviceId,
        machineStatus: wake.status ?? machine.status,
        profileMatched,
        wakeRunId,
        error: wake.error,
      }, wake.ok === false ? 409 : 200);
    }

    const resizeSource = selectResizeSourceForPlacement(
      existingMachines,
      placement.resourceClass,
      placement.cloudMachineId,
    );
    if (resizeSource) {
      const managedDenied = denyNonYaverManagedMachine(resizeSource);
      if (managedDenied) return managedDenied;
      const machineType = cloudMachineTypeForPlacement(placement.resourceClass);
      const targetDeviceId = resizeSource.deviceId ?? managedDeviceIdFor(String(resizeSource._id));
      const resizeReason = `Cloud Workspace needs ${machineType} capacity for ${String(placement.resourceClass || "this task")}`;
      const resize = await ctx.runMutation(internal.cloudMachines.requestResize, {
        userId: session.userDocId as any,
        machineId: resizeSource._id,
        targetMachineType: machineType,
        placementId: placement._id,
        reason: resizeReason,
      });
      await ctx.runMutation(internal.taskPlacement.attachCloudMachine, {
        userId: session.userDocId as any,
        placementId: placement._id,
        cloudMachineId: resizeSource._id,
        targetDeviceId,
        status: "queued",
      });
      const wakeRunId = await ctx.runMutation(internal.wakeRuns.start, {
        userId: session.userDocId as any,
        machineId: resizeSource._id,
        placementId: placement._id,
        taskId: placement.taskId,
        kind: "provision",
        status: resize.ok === false ? "failed" : "blocked",
        phase: resize.ok === false ? "error" : "resize-required",
        progress: 0,
        resourceClass: placement.resourceClass,
        machineType,
        targetDeviceId,
        reason: resize.ok === false ? resize.error : resizeReason,
      });
      return jsonResponse({
        ok: false,
        action: resize.ok === false ? "resize_failed" : "resize_required",
        productId: "cloud-workspace",
        machineId: resizeSource._id,
        machineType,
        currentMachineType: resizeSource.machineType,
        targetDeviceId,
        machineStatus: resizeSource.status,
        phase: resize.ok === false ? "error" : "resize-required",
        profileMatched: false,
        wakeRunId,
        reason: resize.ok === false
          ? resize.error
          : "Cloud Workspace needs a larger profile before this task can run. Yaver recorded the resize request against the existing persisted workspace.",
        error: resize.ok === false ? resize.error : undefined,
      }, 409);
    }

    if (ownerDev && !hasActiveCloudWorkspace) {
      const machineType = cloudMachineTypeForPlacement(placement.resourceClass);
      const machineId = await ctx.runMutation(internal.cloudMachines.create, {
        userId: session.userDocId as any,
        machineType,
        region: "eu",
        tier: "byok",
      });
      const targetDeviceId = managedDeviceIdFor(String(machineId));
      await ctx.runMutation(internal.taskPlacement.attachCloudMachine, {
        userId: session.userDocId as any,
        placementId: placement._id,
        cloudMachineId: machineId,
        targetDeviceId,
        status: "queued",
      });
      const wakeRunId = await ctx.runMutation(internal.wakeRuns.start, {
        userId: session.userDocId as any,
        machineId,
        placementId: placement._id,
        taskId: placement.taskId,
        kind: "provision",
        status: "queued",
        phase: "creating",
        progress: 5,
        resourceClass: placement.resourceClass,
        machineType,
        targetDeviceId,
        reason: placement.reason,
      });
      return jsonResponse({
        ok: true,
        action: "provision_scheduled",
        machineId,
        targetDeviceId,
        wakeRunId,
      }, 202);
    }

    if (hasActiveCloudWorkspace && sub?._id) {
      const machineType = cloudMachineTypeForPlacement(placement.resourceClass);
      const machineId = await ctx.runMutation(internal.cloudMachines.ensureForSubscription, {
        userId: session.userDocId as any,
        machineType,
        region: "eu",
        subscriptionId: sub._id,
        tier: "byok",
      });
      const targetDeviceId = managedDeviceIdFor(String(machineId));
      await ctx.runMutation(internal.taskPlacement.attachCloudMachine, {
        userId: session.userDocId as any,
        placementId: placement._id,
        cloudMachineId: machineId,
        targetDeviceId,
        status: "queued",
      });
      const wakeRunId = await ctx.runMutation(internal.wakeRuns.start, {
        userId: session.userDocId as any,
        machineId,
        placementId: placement._id,
        taskId: placement.taskId,
        kind: "provision",
        status: "queued",
        phase: "creating",
        progress: 5,
        resourceClass: placement.resourceClass,
        machineType,
        targetDeviceId,
        reason: placement.reason,
      });
      return jsonResponse({
        ok: true,
        action: "provision_scheduled",
        productId: "cloud-workspace",
        machineId,
        machineType,
        targetDeviceId,
        wakeRunId,
      }, 202);
    }

    await ctx.scheduler.runAfter(0, internal.cloudMachines.reconcileSubscriptions, {
      onlyUserId: session.userDocId as any,
    });
    const authHeader = request.headers.get("Authorization");
    if (authHeader?.startsWith("Bearer ")) {
      await ctx.runMutation(api.taskPlacement.markStatus, {
        tokenHash: await sha256Hex(authHeader.slice(7)),
        placementId: placement._id,
        status: "queued",
      });
    }
    return jsonResponse({
      ok: true,
      action: "reconcile_scheduled",
      productId: "cloud-workspace",
      reason: "No existing managed workspace row; subscription reconcile will provision one",
    }, 202);
  }),
});

/** POST /tasks/project-profile — Persist privacy-safe project classification
 *  hints for later placement decisions. This stores only a basename slug,
 *  coarse stack label, counts/buckets, and resource class. No absolute path,
 *  prompt, dependency list, file content, or secret is accepted. */
http.route({
  path: "/tasks/project-profile",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    const projectSlug = String(body.projectSlug ?? "").trim();
    if (!projectSlug) return errorResponse("projectSlug required", 400);
    const resourceClass = String(body.resourceClass ?? "").trim();
    const allowedResourceClass = ["phone", "relay-source", "standard", "heavy", "build"].includes(resourceClass)
      ? resourceClass
      : undefined;
    try {
      const result = await ctx.runMutation(api.taskPlacement.upsertProjectProfile, {
        tokenHash,
        projectSlug,
        sourceDeviceId: typeof body.sourceDeviceId === "string" ? body.sourceDeviceId : undefined,
        stack: typeof body.stack === "string" ? body.stack.slice(0, 80) : undefined,
        appCount: typeof body.appCount === "number" ? body.appCount : undefined,
        repoSizeMb: typeof body.repoSizeMb === "number" ? body.repoSizeMb : undefined,
        fileCount: typeof body.fileCount === "number" ? body.fileCount : undefined,
        hasNativeMobile: typeof body.hasNativeMobile === "boolean" ? body.hasNativeMobile : undefined,
        hasDocker: typeof body.hasDocker === "boolean" ? body.hasDocker : undefined,
        resourceClass: allowedResourceClass as any,
        confidence: typeof body.confidence === "number" ? body.confidence : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to update project profile", 500);
    }
  }),
});

/** GET /cloud/wake-runs/recent — Recent Cloud Workspace wake/provision/park
 *  attempts for the caller. Metadata only; no provider IPs, hostnames, logs, or
 *  task content. */
http.route({
  path: "/cloud/wake-runs/recent",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    try {
      const result = await ctx.runQuery(api.wakeRuns.listRecent, {
        tokenHash,
        limit: url.searchParams.get("limit") ? Number(url.searchParams.get("limit")) : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to list wake runs", 500);
    }
  }),
});

/** POST /devices/runner-down — Set runner down/up flag (authed). */
http.route({
  path: "/devices/runner-down",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    try {
      await ctx.runMutation(api.devices.setRunnerDown, {
        tokenHash,
        deviceId: body.deviceId,
        runnerDown: body.runnerDown,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to update runner status", 500);
    }
  }),
});

// ── Logout (current session only — other devices stay signed in) ─────

/** POST /auth/logout — Delete the current session only.
 *  Mobile signout does NOT kill CLI or other device sessions. */
http.route({
  path: "/auth/logout",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    try {
      await ctx.runMutation(api.auth.deleteSession, { tokenHash });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to logout", 500);
    }
  }),
});

/** POST /auth/logout-all — Delete ALL sessions (sign out everywhere).
 *  Only for explicit "sign out all devices" action. */
http.route({
  path: "/auth/logout-all",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    try {
      await ctx.runMutation(api.auth.deleteAllSessions, { tokenHash });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to logout", 500);
    }
  }),
});

// ── Account Deletion ────────────────────────────────────────────────

/** POST /auth/delete-account — Delete user account and all data (authed). */
http.route({
  path: "/auth/delete-account",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    try {
      await ctx.runMutation(api.auth.deleteAccount, { tokenHash });
      return jsonResponse({ ok: true });
    } catch {
      return errorResponse("Failed to delete account", 500);
    }
  }),
});

// ── Auth Logging (unauthenticated — for debugging OAuth) ───────────

/** POST /auth/log — Log an auth event (unauthenticated, called from web OAuth flow). */
http.route({
  path: "/auth/log",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    try {
      const body = await request.json();
      await ctx.runMutation(internal.authLogs.writeLog, {
        level: body.level || "info",
        provider: body.provider || "unknown",
        step: body.step || "unknown",
        message: body.message || "",
        details: body.details ? String(body.details).slice(0, 2000) : undefined,
      });
      return jsonResponse({ ok: true });
    } catch (e) {
      console.error("Auth log error:", e);
      return jsonResponse({ ok: false }, 500);
    }
  }),
});

// ── User Settings ───────────────────────────────────────────────────

http.route({
  path: "/settings",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const settings = await ctx.runQuery(api.userSettings.getByToken, { tokenHash });
    const safeSettings = settings ? { ...settings, speechApiKey: undefined } : null;
    return jsonResponse({
      ok: true,
      settings: safeSettings || { forceRelay: false, runnerId: undefined, customRunnerCommand: undefined, relayUrl: undefined, relayPassword: undefined, tunnelUrl: undefined },
    });
  }),
});

http.route({
  path: "/settings",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    await ctx.runMutation(api.userSettings.setByToken, {
      tokenHash,
      forceRelay: body.forceRelay,
      runnerId: body.runnerId,
      customRunnerCommand: body.customRunnerCommand,
      relayUrl: body.relayUrl,
      relayPassword: body.relayPassword,
      tunnelUrl: body.tunnelUrl,
      speechProvider: body.speechProvider,
      ttsEnabled: body.ttsEnabled,
      ttsProvider: body.ttsProvider,
      verbosity: body.verbosity,
      keyStorage: body.keyStorage,
      multiTargetMode: body.multiTargetMode,
      moreOptionalTools: body.moreOptionalTools,
      // Client sends null to clear the preference, undefined to leave untouched.
      primaryDeviceId: body.primaryDeviceId,
      secondaryDeviceId: body.secondaryDeviceId,
      // Per-device coding agent — forwarded to the mutation's
      // primaryRunnerByDevice merge logic. Without this forward the
      // field was silently dropped at the HTTP boundary, every
      // Confirm click no-op'd, and the sidebar kept falling back
      // to whichever runner was first in device.runners[]
      // (= Claude Code on a Linux box that happens to have it
      // installed).
      primaryRunnerForDevice: body.primaryRunnerForDevice,
      // Per-subsystem managed toggle. Client sends only the
      // subsystem(s) it's changing; backend merges the patch into
      // the existing record so other subsystems' toggles are
      // preserved. null on any key clears that subsystem.
      managed: body.managed,
    });
    return jsonResponse({ ok: true });
  }),
});

// ── User Shortcuts (mobile Shortcuts tab) ────────────────────────────
// One-tap action chains. See backend/convex/shortcuts.ts for the privacy
// contract: deviceId + slug + flags + labels only, never paths/prompts.

http.route({
  path: "/shortcuts",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const shortcuts = await ctx.runQuery(api.shortcuts.listByToken, { tokenHash });
    return jsonResponse({ ok: true, shortcuts });
  }),
});

http.route({
  path: "/shortcuts",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (!body?.name || !Array.isArray(body?.steps)) {
      return errorResponse("name and steps[] are required", 400);
    }
    const id = await ctx.runMutation(api.shortcuts.upsertByToken, {
      tokenHash,
      id: body.id,
      name: body.name,
      icon: body.icon,
      color: body.color,
      order: body.order,
      steps: body.steps,
    });
    return jsonResponse({ ok: true, id });
  }),
});

http.route({
  path: "/shortcuts/delete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (!body?.id) return errorResponse("id is required", 400);
    await ctx.runMutation(api.shortcuts.deleteByToken, { tokenHash, id: body.id });
    return jsonResponse({ ok: true });
  }),
});

/** POST /settings/repair-relay — Re-sync caller's relayPassword with the
 *  current platform-managed value. Used by the web dashboard as a last-ditch
 *  recovery step when the relay keeps returning 401 "invalid relay password".
 *  Never generates new secrets — only re-copies what every synced user has. */
http.route({
  path: "/settings/repair-relay",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const result = await ctx.runMutation(internal.userSettings.repairRelayPassword, { tokenHash });
    if (!result.ok) return errorResponse(result.reason || "repair failed", 401);
    return jsonResponse(result);
  }),
});

// ── Relay Password Validation ────────────────────────────────────────

/** POST /relay/validate — Relay servers call this to validate per-user passwords. No user auth required (relay auth). */
http.route({
  path: "/relay/validate",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    try {
      const body = await request.json();
      if (!body.password || typeof body.password !== "string") {
        return jsonResponse({ ok: false, error: "password required" }, 400);
      }
      const tokenHash =
        typeof body.token === "string" && body.token
          ? await sha256Hex(body.token)
          : undefined;
      const result = await ctx.runQuery(internal.userSettings.validateRelayPassword, {
        password: body.password,
        deviceId: typeof body.deviceId === "string" ? body.deviceId : undefined,
        action: typeof body.action === "string" ? body.action : undefined,
        tokenHash,
      });
      // Audit §3 (2026-07-19): reason distinguishes bad-password from
      // dead-token from device-mismatch. The relay maps that to a distinct
      // client-facing rejection so the desktop's recovery routes to re-auth
      // instead of a hopeless password refetch.
      if (!result || result.ok !== true) {
        return jsonResponse(
          { ok: false, reason: result?.reason ?? "bad_password" },
          401,
        );
      }
      return jsonResponse({
        ok: true,
        userId: result.userId,
        plan: result.plan ?? "free",
        isPaid: result.isPaid === true,
      });
    } catch {
      return jsonResponse({ ok: false, error: "internal error" }, 500);
    }
  }),
});

/** POST /relay/resolve-sig — the relay's asymmetric-auth resolver. Given the
 *  SIGNER device and the TARGET device, returns the signer's ed25519 signing
 *  PUBLIC key (so the relay verifies the signature locally) and whether the
 *  signer's owner owns the target. No user auth (relay-auth); returns only
 *  public material + a userId, never a secret. docs/yaver-relay-asymmetric-auth.md */
http.route({
  path: "/relay/resolve-sig",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    try {
      const body = await request.json();
      if (!body.signerDeviceId || typeof body.signerDeviceId !== "string") {
        return jsonResponse({ ok: false, error: "signerDeviceId required" }, 400);
      }
      const res = await ctx.runQuery(internal.devices.resolveDeviceSig, {
        signerDeviceId: body.signerDeviceId,
        targetDeviceId:
          typeof body.targetDeviceId === "string" ? body.targetDeviceId : undefined,
      });
      if (!res.ok) return jsonResponse({ ok: false }, 401);
      return jsonResponse({ ok: true, userId: res.userId, signerPublicKey: res.signerPublicKey });
    } catch {
      return jsonResponse({ ok: false, error: "internal error" }, 500);
    }
  }),
});

// ── Push token registration (device-auth approval channel, P2) ──────

/** POST /push/register — a signed-in phone registers its push token so a
 *  remote box's re-auth can ring it for Face ID approval. Authed. */
http.route({
  path: "/push/register",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    if (!body.installId || !body.pushToken) {
      return errorResponse("installId and pushToken required", 400);
    }
    try {
      await ctx.runMutation(api.pushNotifications.registerPushToken, {
        tokenHash,
        installId: String(body.installId),
        pushToken: String(body.pushToken),
        transport: String(body.transport || "expo"),
        platform: String(body.platform || "unknown"),
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      if (e?.message === "Unauthorized") return errorResponse("Unauthorized", 401);
      return errorResponse(e?.message || "Failed to register push token", 400);
    }
  }),
});

// ── Mobile Stream Logs ──────────────────────────────────────────────

http.route({
  path: "/mobile/log",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    try {
      const body = await request.json();
      // Best-effort user identification
      let userId: string | undefined;
      const user = await authenticateRequest(ctx, request);
      if (user) userId = user.userId;

      await ctx.runMutation(internal.mobileStreamLogs.writeLog, {
        userId,
        platform: body.platform || "unknown",
        appVersion: body.appVersion || "unknown",
        buildNumber: body.buildNumber || "unknown",
        level: body.level || "info",
        step: body.step || "unknown",
        message: body.message || "",
        details: body.details ? String(body.details).slice(0, 2000) : undefined,
      });
      return jsonResponse({ ok: true });
    } catch (e) {
      console.error("Mobile log error:", e);
      return jsonResponse({ ok: false }, 500);
    }
  }),
});

// ── Developer Logs (developer-only debugging) ──────────────────────

/** POST /dev/log — Write a developer log (only accepted from developer emails). */
http.route({
  path: "/dev/log",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    try {
      const body = await request.json();
      // Best-effort user identification
      let email: string | undefined = body.email;
      let userId: string | undefined;
      const user = await authenticateRequest(ctx, request);
      if (user) {
        email = user.email;
        userId = user.userId;
      }

      await ctx.runMutation(internal.developerLogs.writeLog, {
        email,
        userId,
        source: body.source || "agent",
        level: body.level || "info",
        tag: body.tag || "general",
        message: body.message || "",
        data: body.data ? String(body.data).slice(0, 8000) : undefined,
      });
      return jsonResponse({ ok: true });
    } catch (e) {
      console.error("Dev log error:", e);
      return jsonResponse({ ok: false }, 500);
    }
  }),
});

/** GET /dev/logs — Read developer logs (no auth — dev-only data). */
http.route({
  path: "/dev/logs",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const limit = parseInt(url.searchParams.get("limit") || "50");
    const email = url.searchParams.get("email") || undefined;
    const logs = await ctx.runQuery(internal.developerLogs.getLogs, { limit, email });
    return jsonResponse({ logs });
  }),
});

// ── TOTP 2FA Endpoints ──────────────────────────────────────────────

/** POST /auth/totp/setup — Generate TOTP secret (authenticated). */
http.route({
  path: "/auth/totp/setup",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    try {
      const result = await ctx.runMutation(api.totp.setupTotp, { tokenHash });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to setup TOTP", 400);
    }
  }),
});

/** POST /auth/totp/enable — Verify code and enable 2FA (authenticated). */
http.route({
  path: "/auth/totp/enable",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const body = await request.json();
    if (!body.code) return errorResponse("code required", 400);

    try {
      const result = await ctx.runMutation(api.totp.verifyAndEnableTotp, {
        tokenHash,
        code: body.code,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e.message === "INVALID_CODE") return errorResponse("Invalid verification code", 401);
      return errorResponse(e.message || "Failed to enable TOTP", 400);
    }
  }),
});

/** POST /auth/totp/disable — Disable 2FA (authenticated, requires TOTP code). */
http.route({
  path: "/auth/totp/disable",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const body = await request.json();
    if (!body.code) return errorResponse("code required", 400);

    try {
      await ctx.runMutation(api.totp.disableTotp, { tokenHash, code: body.code });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      if (e.message === "INVALID_CODE") return errorResponse("Invalid verification code", 401);
      return errorResponse(e.message || "Failed to disable TOTP", 400);
    }
  }),
});

/** GET /auth/totp/status — Get 2FA status (authenticated). */
http.route({
  path: "/auth/totp/status",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const status = await ctx.runQuery(api.totp.getTotpStatus, { tokenHash });
    if (!status) return errorResponse("Unauthorized", 401);
    return jsonResponse(status);
  }),
});

/** POST /auth/totp/check-user — Check if a user has 2FA enabled (server-to-server, takes userId). */
http.route({
  path: "/auth/totp/check-user",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const denied = await requireServerSecret(request);
    if (denied) return denied;
    const body = await request.json();
    if (!body.userId) return errorResponse("userId required", 400);
    const result = await ctx.runQuery(internal.auth.getUserWithTotp, { userId: body.userId });
    return jsonResponse({ totpEnabled: result?.totpEnabled ?? false });
  }),
});

/** POST /auth/totp/create-pending — Create a pending auth for 2FA (server-to-server, takes userId). */
http.route({
  path: "/auth/totp/create-pending",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const denied = await requireServerSecret(request);
    if (denied) return denied;
    const body = await request.json();
    if (!body.userId) return errorResponse("userId required", 400);
    const result = await ctx.runMutation(internal.totp.createPendingAuth, { userId: body.userId });
    return jsonResponse(result);
  }),
});

/** POST /auth/verify-totp — Verify TOTP for pending auth, get session token (unauthenticated). */
http.route({
  path: "/auth/verify-totp",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json();
    if (!body.pendingToken || !body.code) {
      return errorResponse("pendingToken and code required", 400);
    }

    try {
      const result = await ctx.runMutation(internal.totp.verifyTotpForLogin, {
        pendingToken: body.pendingToken,
        code: body.code,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (e.message === "INVALID_CODE") return errorResponse("Invalid code", 401);
      if (e.message === "INVALID_PENDING") return errorResponse("Invalid or expired session", 404);
      if (e.message === "PENDING_EXPIRED") return errorResponse("Session expired, please login again", 410);
      if (e.message === "TOO_MANY_ATTEMPTS") return errorResponse("Too many attempts, please login again", 429);
      return errorResponse(e.message || "Verification failed", 400);
    }
  }),
});

// ── Device Code Auth (Headless) ─────────────────────────────────────

/** POST /auth/device-code — Create a new device code for headless auth (unauthenticated). */
http.route({
  path: "/auth/device-code",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({}));
    const result = await ctx.runMutation(api.deviceCode.createDeviceCode, {
      machineName: typeof body?.machineName === "string" ? body.machineName : undefined,
      platform: typeof body?.platform === "string" ? body.platform : undefined,
      arch: typeof body?.arch === "string" ? body.arch : undefined,
      shell: typeof body?.shell === "string" ? body.shell : undefined,
      environment: typeof body?.environment === "string" ? body.environment : undefined,
      runtimeVersion: typeof body?.runtimeVersion === "string" ? body.runtimeVersion : undefined,
      preferredProvider: typeof body?.preferredProvider === "string" ? body.preferredProvider : undefined,
      isWsl: typeof body?.isWsl === "boolean" ? body.isWsl : undefined,
    });
    return jsonResponse(result);
  }),
});

/** GET /auth/device-code/poll — Poll device code status (unauthenticated, called by CLI). */
http.route({
  path: "/auth/device-code/poll",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const deviceCode = url.searchParams.get("device_code");
    if (!deviceCode) {
      return errorResponse("device_code required", 400);
    }
    const result = await ctx.runMutation(api.deviceCode.pollDeviceCode, { deviceCode });
    return jsonResponse(result);
  }),
});

/** GET /auth/device-code/info — Public machine info for a waiting device code. */
http.route({
  path: "/auth/device-code/info",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const userCode = (url.searchParams.get("user_code") || "").toUpperCase().trim();
    if (!userCode) {
      return errorResponse("user_code required", 400);
    }
    const result = await ctx.runQuery(api.deviceCode.getDeviceCodeInfo, { userCode });
    if (!result) {
      return errorResponse("Not found", 404);
    }
    return jsonResponse(result);
  }),
});

/** POST /auth/device-code/authorize — Authorize a device code (authenticated). */
http.route({
  path: "/auth/device-code/authorize",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) {
      return errorResponse("Unauthorized", 401);
    }

    const body = await request.json();
    const { userCode } = body;
    if (!userCode) {
      return errorResponse("userCode required", 400);
    }

    // Look up the user's _id from the session
    const authHeader = request.headers.get("Authorization")!;
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) {
      return errorResponse("Unauthorized", 401);
    }

    // We need the user's document _id for the mutation. Get it via a dedicated query.
    const userDoc = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDoc) {
      return errorResponse("User not found", 404);
    }

    try {
      await ctx.runMutation(internal.deviceCode.authorizeDeviceCode, {
        userCode: userCode.toUpperCase().trim(),
        userId: userDoc,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      if (e.message === "INVALID_CODE") return errorResponse("Invalid code", 404);
      if (e.message === "CODE_EXPIRED") return errorResponse("Code expired", 410);
      if (e.message === "CODE_ALREADY_USED") return errorResponse("Code already used", 409);
      return errorResponse("Failed to authorize", 500);
    }
  }),
});

/** POST /auth/device-code/broker — Seamless remote-box onboarding. An
 *  AUTHENTICATED caller (the user's CLI daemon or mobile app) mints a
 *  PRE-AUTHORIZED device code for a NEW box, so the box inherits the caller's
 *  identity with NO interactive OAuth on the box. Returns the short-TTL
 *  deviceCode HANDLE to inject into the box's cloud-init; the box exchanges it
 *  exactly once via GET /auth/device-code/poll. Bound to the caller's own user —
 *  you can only broker a box into your OWN account. */
http.route({
  path: "/auth/device-code/broker",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) return errorResponse("Unauthorized", 401);
    const authHeader = request.headers.get("Authorization")!;
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const body = await request.json().catch(() => ({}) as any);
    try {
      const result = await ctx.runMutation(internal.deviceCode.createAuthorizedDeviceCode, {
        tokenHash,
        machineName: typeof body?.machineName === "string" ? body.machineName : undefined,
        platform: typeof body?.platform === "string" ? body.platform : undefined,
        arch: typeof body?.arch === "string" ? body.arch : undefined,
        deviceId: typeof body?.deviceId === "string" ? body.deviceId : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      if (String(e?.message || "").includes("Unauthorized")) return errorResponse("Unauthorized", 401);
      return errorResponse("Failed to broker device code", 500);
    }
  }),
});

// ── Download Endpoints ──────────────────────────────────────────────

/** GET /downloads/list — List all available downloads (public, no auth). */
http.route({
  path: "/downloads/list",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const downloads = await ctx.runQuery(internal.downloads.listDownloads, {});
    return new Response(JSON.stringify({ downloads }), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        "Access-Control-Allow-Origin": "*",
      },
    });
  }),
});

// ── Platform Config ──────────────────────────────────────────────────

/** GET /config — Public platform config (relay servers, runners, models). No auth required. */
http.route({
  path: "/config",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const [config, runners, models] = await Promise.all([
      ctx.runQuery(internal.platformConfig.getClientConfig, {}),
      ctx.runQuery(api.aiRunners.list, {}),
      ctx.runQuery(api.aiModels.list, {}),
    ]);
    // Parse relay_servers from JSON string to array for client convenience
    let relayServers: unknown[] = [];
    if (config.relay_servers) {
      try {
        const parsed = JSON.parse(config.relay_servers);
        relayServers = Array.isArray(parsed)
          ? parsed.map((server) => {
              if (!server || typeof server !== "object" || Array.isArray(server)) return server;
              const { password: _password, ...publicServer } = server as Record<string, unknown>;
              return publicServer;
            })
          : [];
      } catch {
        // ignore parse errors
      }
    }
    return new Response(
      JSON.stringify({
        relayServers,
        runners,
        models,
        cliVersion: config.cli_version || null,
        mobileVersion: config.mobile_version || null,
        relayVersion: config.relay_version || null,
        webVersion: config.web_version || null,
        backendVersion: config.backend_version || null,
      }),
      {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Access-Control-Allow-Origin": "*",
          // Cache for 5 minutes — config doesn't change often
          "Cache-Control": "public, max-age=300",
        },
      }
    );
  }),
});

// ── Public install catalogue ────────────────────────────────────────

/** GET /packages — public tool catalogue for Go agent + mobile/web UIs.
 * Returns the whole packageRegistry table sorted by (kind, sortOrder).
 * Nothing in here is user-specific or sensitive (see packages.ts). */
http.route({
  path: "/packages",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const rows = await ctx.runQuery(internal.packages.list, {});
    return new Response(JSON.stringify(rows), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        "Access-Control-Allow-Origin": "*",
        // 10 minutes — the catalogue changes rarely, and the agent
        // refreshes on its own 6h schedule anyway.
        "Cache-Control": "public, max-age=600",
      },
    });
  }),
});

// ── AI Runners ──────────────────────────────────────────────────────

/** GET /runners — List all AI runners (public, no auth). */
http.route({
  path: "/runners",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const runners = await ctx.runQuery(api.aiRunners.list, {});
    return jsonResponse({ runners });
  }),
});

/** POST /runners/seed — Seed predefined AI runners (idempotent, no auth). */
http.route({
  path: "/runners/seed",
  method: "POST",
  handler: httpAction(async (ctx) => {
    await ctx.runMutation(api.aiRunners.seed, {});
    return jsonResponse({ ok: true });
  }),
});

// ── AI Models ────────────────────────────────────────────────────────

/** GET /models — List all AI models (public, no auth). */
http.route({
  path: "/models",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const models = await ctx.runQuery(api.aiModels.list, {});
    return jsonResponse({ models });
  }),
});

/** POST /models/seed — Seed predefined AI models (idempotent, no auth). */
http.route({
  path: "/models/seed",
  method: "POST",
  handler: httpAction(async (ctx) => {
    await ctx.runMutation(api.aiModels.seed, {});
    return jsonResponse({ ok: true });
  }),
});

// ── Subscription & Managed Relay ─────────────────────────────────────

/** Generate a random relay password. */
function generateRelayPassword(): string {
  const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
  let result = "";
  for (let i = 0; i < 32; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return result;
}

/** POST /webhooks/lemonsqueezy — LemonSqueezy webhook (no auth — validated by signature). */
http.route({
  path: "/webhooks/lemonsqueezy",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    // Verify webhook signature (HMAC-SHA256 over the raw body using
    // LEMONSQUEEZY_WEBHOOK_SECRET). Without verification, anyone could
    // POST a forged "subscription_created" to provision hardware on your
    // Hetzner account.
    const signature = request.headers.get("x-signature");
    const body = await request.text();
    if (!(await verifyLemonSqueezySignature(body, signature))) {
      return errorResponse("Invalid signature", 401);
    }

    let payload;
    try {
      payload = JSON.parse(body);
    } catch {
      return errorResponse("Invalid JSON", 400);
    }

    const eventName = payload.meta?.event_name;
    const data = payload.data?.attributes;

    if (!eventName || !data) {
      return errorResponse("Invalid payload", 400);
    }

    // Extract user email from custom data or customer email
    const userEmail = payload.meta?.custom_data?.user_email || data.user_email;
    if (!userEmail) {
      return errorResponse("No user email", 400);
    }

    // Find user by email
    const user = await ctx.runQuery(internal.auth.getUserByEmail, { email: userEmail });
    if (!user) {
      return errorResponse("User not found", 404);
    }

    const lemonSqueezyId = String(payload.data.id);
    const customerId = String(data.customer_id);

    switch (eventName) {
      case "subscription_created":
      case "subscription_updated":
      case "subscription_resumed": {
        const rawProductType = payload.meta?.custom_data?.product_type || "relay-pro";
        const billingProductId = normalizeBillingProduct(rawProductType) ?? "relay-pro";
        const productType = billingProductId === "cloud-workspace" ? "cloud-workspace" : "relay-pro";
        const machineType =
          rawProductType === "gpu" || payload.meta?.custom_data?.machine_type === "gpu"
            ? "gpu"
            : cloudMachineTypeForPlacement(payload.meta?.custom_data?.machine_profile);
        const isCloudWorkspaceProduct = billingProductId === "cloud-workspace";
        const plan = isCloudWorkspaceProduct
          ? "cloud-workspace"
          : data.variant_name?.includes("yearly")
            ? "relay-yearly"
            : "relay-pro";
        const status = normalizeLemonSqueezySubscriptionStatus(data.status);
        const periodEnd = new Date(data.renews_at || data.ends_at).getTime();

        const subId = await ctx.runMutation(internal.subscriptions.upsertFromWebhook, {
          lemonSqueezyId,
          lemonSqueezyCustomerId: customerId,
          userId: user._id,
          plan,
          status,
          currentPeriodEnd: periodEnd,
        });

        // Apply plan entitlements (included active-hours + gateway
        // inference policy + monthly wallet budget) for managed-cloud
        // products on EVERY active create/update/resume — idempotent per
        // billing period, so monthly renewals refresh the allowance and a
        // re-delivered webhook never double-credits. Relay subs are
        // unaffected. tier mirrors the provision branch below
        // (custom_data.tier="hosted" ⇒ managed AI on; absent ⇒ byok).
        const isManagedProduct = isCloudWorkspaceProduct;
        if (isManagedProduct && status === "active") {
          const tier = payload.meta?.custom_data?.tier === "hosted" ? "hosted" : "byok";
          await ctx.scheduler.runAfter(0, internal.plans.applyPlanEntitlements, {
            userId: user._id,
            subscriptionId: lemonSqueezyId,
            tier,
            plan,
          });
        }

        // If new subscription, provision the appropriate resource
        if (eventName === "subscription_created" && status === "active") {
          const region = payload.meta?.custom_data?.region || "eu";

          if (isCloudWorkspaceProduct) {
            // Cloud dev machine — create and provision
            const teamId = payload.meta?.custom_data?.team_id;
            await ctx.runMutation(internal.cloudMachines.ensureForSubscription, {
              userId: user._id,
              machineType,
              teamId,
              region,
              subscriptionId: subId,
              // Hosted SKU opts in via checkout custom_data.tier="hosted"
              // (SANDBOX_HOSTED_HANDOFF.md §5). Absent ⇒ byok, so the
              // current single SKU is unchanged until a hosted variant
              // sets it. createCloudMachine already accepts `tier`.
              tier:
                payload.meta?.custom_data?.tier === "hosted"
                  ? "hosted"
                  : "byok",
            });
            // SECURITY (per-tenant isolation): do NOT attach a PAID
            // subscription to the shared preview box.
            // ensureForSubscription → createCloudMachine already
            // schedules internal.cloudMachines.provision, which spins a
            // DEDICATED per-tenant Hetzner box (its own cloud-init, its
            // own per-user 1-year session token, behind the fail-closed
            // canProvisionManaged gate). Repointing real buyers at one
            // shared host let any buyer SSH / open a shell into another
            // buyer's source tree — a code-leak that breaks Yaver's
            // privacy contract. The shared box is retained ONLY for the
            // owner-gated, no-Hetzner-spend /billing/yaver-cloud/
            // dev-activate path (ensurePreviewCloudMachine). Never
            // re-add a shared-server attach on this paid webhook path.
            // isCloudWorkspaceProduct still selects the `plan` label above.
          } else {
            // Managed relay (default)
            const password = generateRelayPassword();
            const relayId = await ctx.runMutation(internal.managedRelays.create, {
              userId: user._id,
              subscriptionId: subId,
              region,
              password,
            });

            // Trigger automated provisioning (Hetzner + Cloudflare + SSL)
            await ctx.scheduler.runAfter(0, internal.provisionRelay.provision, {
              userId: user._id,
              subscriptionId: subId,
              relayId,
              region,
              password,
            });
          }
        }
        break;
      }

      case "subscription_cancelled":
      case "subscription_expired": {
        const sub = await ctx.runMutation(internal.subscriptions.cancel, { lemonSqueezyId });

        // Revoke managed-inference entitlement immediately (disable the
        // gateway policy) so a lapsed subscriber can't keep spending our
        // gateway key on leftover balance. Box teardown happens below.
        await ctx.scheduler.runAfter(0, internal.plans.revokePlanEntitlements, {
          userId: user._id,
        });

        // Tear the box down on BOTH cancel and expiry. A managed box
        // costs Yaver money every hour it runs; the moment the user
        // cancels we stop that spend (deprovision snapshots first, so
        // a resubscribe can be restored from the snapshot). Previously
        // only `subscription_expired` deprovisioned, leaving a paid
        // box running through the rest of a cancelled period.
        if (sub && (eventName === "subscription_expired" || eventName === "subscription_cancelled")) {
          const [relays, machines] = await Promise.all([
            ctx.runQuery(internal.managedRelays.listBySubscription, { subscriptionId: sub }),
            ctx.runQuery(internal.cloudMachines.listBySubscription, { subscriptionId: sub }),
          ]);

          for (const relay of relays) {
            if (relay.hetznerServerId && relay.domain) {
              await ctx.scheduler.runAfter(0, internal.provisionRelay.deprovision, {
                relayId: relay._id,
                hetznerServerId: relay.hetznerServerId,
                domain: relay.domain,
              });
            }
          }

          for (const machine of machines) {
            if (machine.status !== "stopped" && machine.status !== "stopping") {
              await ctx.runMutation(internal.cloudMachines.deprovision, {
                machineId: machine._id,
              });
            }
          }
        }
        break;
      }

      case "subscription_payment_failed": {
        const rawProductType = payload.meta?.custom_data?.product_type || "relay-pro";
        const billingProductId = normalizeBillingProduct(rawProductType) ?? "relay-pro";
        await ctx.runMutation(internal.subscriptions.upsertFromWebhook, {
          lemonSqueezyId,
          lemonSqueezyCustomerId: customerId,
          userId: user._id,
          plan:
            billingProductId === "relay-pro"
              ? "relay-pro"
              : "cloud-workspace",
          status: "past_due",
          currentPeriodEnd: Date.now(),
        });
        break;
      }

      // Legacy one-time credit-pack purchase. Public credit-pack checkout is
      // gone, and old configured variants must not mint balance if a stale
      // LemonSqueezy order is delivered. Subscription allowance grants still go
      // through plans.applyPlanEntitlements → cloudLifecycle.topUpForOrder.
      case "order_created": {
        console.warn(`[billing] ${legacyCreditPackWebhookDisabledMessage()}`);
        break;
      }
    }

    return jsonResponse({ ok: true });
  }),
});

/** POST /billing/checkout — create authenticated Lemon Squeezy checkout for
 *  the two paid products: Relay Pro or Cloud Workspace. Free has no checkout. */
http.route({
  path: "/billing/checkout",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;

    let body: { productId?: string; region?: string } = {};
    try {
      body = await request.json();
    } catch {
      // allow empty body
    }
    const productId = normalizeBillingProduct(body.productId);
    if (!productId) {
      return errorResponse("productId must be 'relay-pro' or 'cloud-workspace'", 400);
    }
    const region = (body.region ?? "eu").trim() || "eu";
    const variant = variantForBillingProduct(productId);
    if (!variant.variantId) {
      return errorResponse(
        `${productId === "relay-pro" ? "Relay Pro" : "Cloud Workspace"} checkout is not configured (set LEMONSQUEEZY_${variant.envName})`,
        503,
      );
    }

    try {
      const url = await createLemonSqueezyCheckout({
        email: session.email,
        variantId: variant.variantId,
        variantEnvName: variant.envName,
        custom: {
          user_email: session.email,
          product_type: productId,
          plan_id: productId,
          tier: "byok",
          machine_type: "standard",
          region,
        },
      });
      return jsonResponse({
        url,
        productId,
        mode: parseBooleanEnv(lsEnv("SANDBOX"), true) ? "sandbox" : "live",
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return errorResponse(message, 500);
    }
  }),
});

/** POST /billing/yaver-cloud/checkout — legacy alias for Cloud Workspace checkout. */
http.route({
  path: "/billing/yaver-cloud/checkout",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;

    let body: { region?: string } = {};
    try {
      body = await request.json();
    } catch {
      // allow empty body
    }
    const region = (body.region ?? "eu").trim() || "eu";
    const variant = variantForBillingProduct("cloud-workspace");
    if (!variant.variantId) {
      return errorResponse(
        `Cloud Workspace checkout is not configured (set LEMONSQUEEZY_${variant.envName})`,
        503,
      );
    }

    try {
      const url = await createLemonSqueezyCheckout({
        email: session.email,
        variantId: variant.variantId,
        variantEnvName: variant.envName,
        custom: {
          user_email: session.email,
          product_type: "cloud-workspace",
          plan_id: "cloud-workspace",
          tier: "byok",
          machine_type: "standard",
          region,
        },
      });
      return jsonResponse({ url, productId: "cloud-workspace", mode: parseBooleanEnv(lsEnv("SANDBOX"), true) ? "sandbox" : "live" });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return errorResponse(message, 500);
    }
  }),
});

/** GET /billing/credits/packs — legacy credit-pack catalog.
 *  Disabled for the flat two-product model. Wallet/ledger internals remain for
 *  allowance accounting and legacy webhook idempotency, but Yaver no longer
 *  sells normal users prepaid top-ups. */
http.route({
  path: "/billing/credits/packs",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    return errorResponse(legacyCreditPacksDisabledMessage(), 410);
  }),
});

/** POST /billing/credits/checkout — legacy credit-pack checkout.
 *  Disabled for the flat two-product model. */
http.route({
  path: "/billing/credits/checkout",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    return errorResponse(legacyCreditPacksDisabledMessage(), 410);
  }),
});

/** POST /billing/yaver-cloud/dev-activate — bypass checkout and attach preview machine for testing. */
http.route({
  path: "/billing/yaver-cloud/dev-activate",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }

    let body: { region?: string } = {};
    try {
      body = await request.json();
    } catch {
      // allow empty body
    }
    const region = (body.region ?? "eu").trim() || "eu";

    try {
      const machineId = await ensurePreviewCloudMachine(ctx, session.userDocId, region);
      return jsonResponse({ ok: true, machineId, mode: "dev-bypass" });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return errorResponse(message, 500);
    }
  }),
});

/** POST /billing/yaver-cloud/dev-adopt — owner-only: register an
 *  EXISTING Hetzner box as a managed machine (imitates a managed
 *  purchase) without provisioning a new server or LemonSqueezy. */
http.route({
  path: "/billing/yaver-cloud/dev-adopt",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Owner-only (private preview) on this account", 403);
    }
    let body: { hetznerServerId?: string; region?: string; serverIp?: string; hostname?: string; deviceId?: string } = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Invalid JSON", 400);
    }
    const hetznerServerId = (body.hetznerServerId ?? "").toString().trim();
    if (!hetznerServerId) return errorResponse("hetznerServerId is required", 400);
    try {
      const adoptDeviceId = (body.deviceId ?? "").toString().trim();
      const machineId = await ctx.runMutation(internal.cloudMachines.adoptExisting, {
        userId: session.userDocId as any,
        hetznerServerId,
        region: (body.region ?? "eu").trim() || "eu",
        serverIp: body.serverIp,
        hostname: body.hostname,
        deviceId: adoptDeviceId || undefined,
      });
      // Mint a machine token so the box can report activity + self-park (auto-off).
      // Returned ONCE here; the caller writes it to /etc/yaver/machine.json on the
      // box. Only when a deviceId is supplied (setByoBootstrap requires it).
      let machineToken: string | undefined;
      if (adoptDeviceId) {
        machineToken = randomHex(24);
        await ctx.runMutation(internal.cloudMachines.setByoBootstrap, {
          machineId,
          machineTokenHash: await sha256Hex(machineToken),
          deviceId: adoptDeviceId,
        });
      }
      return jsonResponse({ ok: true, machineId, origin: "managed", mode: "dev-adopt", machineToken });
    } catch (error) {
      return errorResponse(error instanceof Error ? error.message : String(error), 500);
    }
  }),
});

/** POST /billing/yaver-cloud/dev-deprovision — tear down a managed machine the
 *  caller owns. This is explicit decommission, not Pause: it cancels linked
 *  billing and schedules a full provider purge (server + persistent volume +
 *  legacy snapshots) so the "cannot be undone" UI copy is honest. */
/** POST /billing/yaver-cloud/runners-authorized — flip a managed
 *  box's runnersAuthorized once its coding-agent OAuth is in place
 *  (via the existing yaver CLI/MCP runner-auth flow today; the web
 *  one-click flow is the tracked #9). Owner-gated, scoped to a
 *  machine the caller owns. Drives the UI Unauthorized→ready state.
 *  project_managed_cloud_onboarding_gap. */
http.route({
  path: "/billing/yaver-cloud/runners-authorized",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Owner-only (private preview) on this account", 403);
    }
    const body = await request.json().catch(() => ({}));
    const machineId = String(body.machineId ?? "").trim();
    if (!machineId) return errorResponse("machineId is required", 400);
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, {
      machineId: machineId as any,
    });
    if (!machine) return errorResponse("Machine not found", 404);
    if (machine.userId !== session.userDocId) {
      return errorResponse("Not your machine", 403);
    }
    const managedDenied = denyNonYaverManagedMachine(machine);
    if (managedDenied) return managedDenied;
    const authorized = body.authorized === false ? false : true;
    // Record the runner fact, but do NOT let it declare the box "ready" while a
    // wake is still climbing. resumeHealthCheck early-returns on
    // provisionPhase === "ready" (cloudMachines.ts:2257), so a "ready" written
    // mid-wake permanently disarms the watchdog: the row never gets promoted to
    // active, never gets abandoned, and keeps billing while stuck in "resuming".
    // Only a box the lifecycle has already settled may be moved to "ready" here.
    const settled = machine.status === "active";
    if (settled) {
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId: machineId as any,
        phase: authorized ? "ready" : "authorizing-runners",
        progress: authorized ? 100 : 90,
        runnersAuthorized: authorized,
      });
    } else {
      // Mid-wake: keep the fact, leave the ladder to resumeHealthCheck.
      await ctx.runMutation(internal.cloudMachines.setRunnersAuthorized, {
        machineId: machineId as any,
        runnersAuthorized: authorized,
      });
    }
    return jsonResponse({ ok: true, runnersAuthorized: authorized });
  }),
});

/** POST /billing/yaver-cloud/reconcile — self-heal: "I paid but have
 *  no resource". Relay Pro repairs a missing managed relay; Cloud Workspace
 *  repairs a missing managed box. Idempotent when a healthy/in-flight resource
 *  already exists. Provider actions still fail closed behind the active
 *  subscription/provisioning gates. project_managed_cloud_onboarding_gap. */
http.route({
  path: "/billing/yaver-cloud/reconcile",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;

    const sub = await ctx.runQuery(internal.subscriptions.getByUser, {
      userId: session.userDocId as any,
    });
    if (!sub || sub.status !== "active") {
      return errorResponse("No active subscription to reconcile", 400);
    }
    const productId = productForSubscriptionPlan(sub.plan);
    if (productId !== "relay-pro" && productId !== "cloud-workspace") {
      return errorResponse("Current subscription is not Relay Pro or Cloud Workspace", 400);
    }

    if (productId === "relay-pro") {
      const relays = await ctx.runQuery(internal.managedRelays.listBySubscription, {
        subscriptionId: sub._id,
      });
      const reusable = hasReusableManagedRelayForReconcile(relays);
      if (reusable) {
        return jsonResponse({ ok: true, checked: 1, repaired: 0, productId });
      }

      const region = "eu";
      const password = generateRelayPassword();
      const relayId = await ctx.runMutation(internal.managedRelays.create, {
        userId: session.userDocId as any,
        subscriptionId: sub._id,
        region,
        password,
      });
      await ctx.scheduler.runAfter(0, internal.provisionRelay.provision, {
        userId: session.userDocId as any,
        subscriptionId: sub._id,
        relayId,
        region,
        password,
      });
      return jsonResponse({ ok: true, checked: 1, repaired: 1, productId });
    }

    const r = await ctx.runAction(internal.cloudMachines.reconcileSubscriptions, {
      onlyUserId: session.userDocId as any,
    });
    return jsonResponse({ ok: true, productId, ...r });
  }),
});

http.route({
  path: "/billing/yaver-cloud/dev-deprovision",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    // Open to any cloud-access user — they can only ever decommission a
    // box they own (the per-machine ownership check below enforces it),
    // and a prepaid user must be able to tear down their own box.
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    let body: { machineId?: string } = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Invalid JSON", 400);
    }
    const machineId = (body.machineId ?? "").toString().trim();
    if (!machineId) return errorResponse("machineId is required", 400);
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId: machineId as any });
    if (!machine) return errorResponse("Machine not found", 404);
    if (String(machine.userId) !== String(session.userDocId)) {
      return errorResponse("Not your machine", 403);
    }
    const managedDenied = denyNonYaverManagedMachine(machine);
    if (managedDenied) return managedDenied;
    if (machine.subscriptionId) {
      await ctx.runMutation(internal.subscriptions.cancelById, {
        subscriptionId: machine.subscriptionId,
      });
    }
    await ctx.scheduler.runAfter(0, internal.plans.revokePlanEntitlements, {
      userId: session.userDocId as any,
    });
    await ctx.runMutation(internal.cloudMachines.setStatus, {
      machineId: machineId as any,
      status: "stopping",
    });
    await ctx.scheduler.runAfter(0, internal.cloudLifecycle.purgeMachineResources, {
      machineId: machineId as any,
      deleteSnapshots: true,
    });
    return jsonResponse({
      ok: true,
      machineId,
      mode: "decommission",
      note: "full cloud-resource purge scheduled",
    });
  }),
});

/** GET /billing/yaver-cloud/balance — prepaid wallet read. Open to any
 *  cloud-access user; reading a zero balance is harmless. */
http.route({
  path: "/billing/yaver-cloud/balance",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    const wallet = await ctx.runQuery(internal.cloudLifecycle.getWallet, {
      userId: session.userDocId as any,
    });
    // Legacy wallet fields remain for older clients. Current UI describes the
    // product as flat monthly Cloud Workspace plus included standard credits.
    const hourlyCents = estimatedHourlyCents("standard");
    const reservedCents = minimumReserveCents("standard");
    // Included-this-month fuel gauge: current Cloud Workspace uses the
    // standard allowance row as the shared weighted credit pool.
    const allowance = await ctx.runQuery(internal.cloudLifecycle.getAllowance, {
      userId: session.userDocId as any,
      machineType: "standard",
    });
    const allowanceIncludedCredits = Math.round(allowance.includedSeconds / 3600);
    const allowanceUsedCredits = Math.round((allowance.usedSeconds / 3600) * 10) / 10;
    const allowanceRemainingCredits = Math.max(0, Math.round((allowance.remainingSeconds / 3600) * 10) / 10);
    const pol = await ctx.runQuery(internal.gatewayPolicy.getAuthContext, {
      userId: session.userDocId as any,
    });
    const orKey = await ctx.runQuery(internal.openrouterKeys.getByUser, {
      userId: session.userDocId as any,
    });
    return jsonResponse({
      ok: true,
      ...wallet,
      prepaidBalanceCents: wallet.balanceCents,
      estimatedHourlyCents: hourlyCents,
      reservedCents,
      lowBalance: wallet.balanceCents <= reservedCents,
      allowance: {
        plan: allowance.plan,
        unit: "standard_credits",
        includedStandardCredits: allowanceIncludedCredits,
        usedStandardCredits: allowanceUsedCredits,
        remainingStandardCredits: allowanceRemainingCredits,
        includedSeconds: allowance.includedSeconds,
        usedSeconds: allowance.usedSeconds,
        remainingSeconds: allowance.remainingSeconds,
      },
      inference: {
        enabled: pol.enabled,
        dailyCapCents: pol.dailyCapCents,
        spentTodayCents: pol.spentTodayCents,
      },
      // Per-user OpenRouter credit (managed inference). Read from the
      // stored mirror — no live OpenRouter call on this hot path. `limit`
      // is our COGS budget (margin already removed), so it reads lower than
      // the retail AI wallet by design. null when the seat has no key (byok).
      openrouterCredit: orKey
        ? {
            limitCents: orKey.limitCents,
            usageCents: orKey.usageCents ?? 0,
            remainingCents: Math.max(0, orKey.limitCents - (orKey.usageCents ?? 0)),
            status: orKey.status,
          }
        : null,
    });
  }),
});

/** GET /billing/status — buyer-side product snapshot for web/MCP clients.
 *  Public catalog is Free, Relay Pro, Cloud Workspace; legacy subscription
 *  plan labels are normalized before returning. */
http.route({
  path: "/billing/status",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    const sub = await ctx.runQuery(internal.subscriptions.getByUser, {
      userId: session.userDocId as any,
    });
    const allowance = await ctx.runQuery(internal.cloudLifecycle.getAllowance, {
      userId: session.userDocId as any,
      machineType: "standard",
    });
    const pol = await ctx.runQuery(internal.gatewayPolicy.getAuthContext, {
      userId: session.userDocId as any,
    });
    const subscribed = !!sub && (sub.status === "active" || sub.status === "past_due");
    const productId = subscribed ? productForSubscriptionPlan(sub?.plan) : "free";
    const runnerMode = pol.enabled ? "managed" : "byok";
    return jsonResponse({
      ok: true,
      subscribed,
      productId,
      tier: productId === "cloud-workspace" ? "cloud-workspace" : productId === "relay-pro" ? "relay-pro" : null,
      runnerMode,
      plan: sub?.plan ?? null,
      subscriptionStatus: sub?.status ?? null,
      currentPeriodEnd: sub?.currentPeriodEnd ?? null,
      cancelledAt: sub?.cancelledAt ?? null,
      includedHoursLeft: Math.round((allowance.remainingSeconds / 3600) * 10) / 10,
      includedStandardCreditsLeft: Math.max(0, Math.round((allowance.remainingSeconds / 3600) * 10) / 10),
      includedStandardCredits: Math.round(allowance.includedSeconds / 3600),
      managedInference: pol.enabled === true,
    });
  }),
});

/** GET /billing/portal — the LemonSqueezy customer-portal URL for the user's
 *  active subscription (update payment / change plan / cancel). Fetched live
 *  from the LS API so it's always current; portalUrl is null when the user has
 *  no LS subscription yet, or a non-numeric (test) id, or LS isn't configured —
 *  callers fall back to the dashboard. */
http.route({
  path: "/billing/portal",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    const sub = await ctx.runQuery(internal.subscriptions.getByUser, {
      userId: session.userDocId as any,
    });
    const lsId = sub?.lemonSqueezyId;
    const apiKey = lsEnv("API_KEY");
    if (!lsId || !apiKey) {
      return jsonResponse({ ok: true, portalUrl: null, reason: lsId ? "billing not configured" : "no active subscription" });
    }
    try {
      const resp = await fetch(`https://api.lemonsqueezy.com/v1/subscriptions/${lsId}`, {
        headers: { Authorization: `Bearer ${apiKey}`, Accept: "application/vnd.api+json" },
      });
      if (!resp.ok) {
        return jsonResponse({ ok: true, portalUrl: null, reason: `lemonsqueezy ${resp.status}` });
      }
      const data = await resp.json();
      const urls = data?.data?.attributes?.urls || {};
      return jsonResponse({
        ok: true,
        portalUrl: urls.customer_portal ?? null,
        updatePaymentUrl: urls.update_payment_method ?? null,
      });
    } catch (e) {
      return jsonResponse({ ok: true, portalUrl: null, reason: String(e) });
    }
  }),
});

/** POST /billing/cancel — user-initiated unsubscribe for Relay Pro or Cloud
 *  Workspace. Cancels the caller's current LemonSqueezy subscription through
 *  the existing internal cancel path and immediately schedules teardown for
 *  linked Yaver-managed resources. */
http.route({
  path: "/billing/cancel",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;

    const body = await request.json().catch(() => ({}));
    if (body.confirm !== true && body.confirm !== "cancel") {
      return errorResponse("confirm=true is required", 400);
    }

    const sub = await ctx.runQuery(internal.subscriptions.getByUser, {
      userId: session.userDocId as any,
    });
    if (!sub || (sub.status !== "active" && sub.status !== "past_due")) {
      return errorResponse("No active subscription to cancel", 400);
    }
    if (String(sub.userId) !== String(session.userDocId)) {
      return errorResponse("Not your subscription", 403);
    }

    const productId = productForSubscriptionPlan(sub.plan);
    if (productId !== "relay-pro" && productId !== "cloud-workspace") {
      return errorResponse("Current subscription is not Relay Pro or Cloud Workspace", 400);
    }

    const [relays, machines] = await Promise.all([
      ctx.runQuery(internal.managedRelays.listBySubscription, { subscriptionId: sub._id }),
      ctx.runQuery(internal.cloudMachines.listBySubscription, { subscriptionId: sub._id }),
    ]);

    await ctx.runMutation(internal.subscriptions.cancelById, {
      subscriptionId: sub._id,
    });

    let relaysScheduled = 0;
    for (const relay of relays) {
      if (relay.hetznerServerId && relay.domain) {
        await ctx.scheduler.runAfter(0, internal.provisionRelay.deprovision, {
          relayId: relay._id,
          hetznerServerId: relay.hetznerServerId,
          domain: relay.domain,
        });
        relaysScheduled++;
      }
    }

    let machinesScheduled = 0;
    for (const machine of machines) {
      if (String(machine.userId) !== String(session.userDocId)) continue;
      const managedDenied = denyNonYaverManagedMachine(machine);
      if (managedDenied) continue;
      if (machine.status !== "stopped" && machine.status !== "stopping" && machine.status !== "removed") {
        await ctx.runMutation(internal.cloudMachines.deprovision, {
          machineId: machine._id,
        });
        machinesScheduled++;
      }
    }

    await ctx.scheduler.runAfter(0, internal.plans.revokePlanEntitlements, {
      userId: session.userDocId as any,
    });

    return jsonResponse({
      ok: true,
      productId,
      relaysScheduled,
      machinesScheduled,
    });
  }),
});

/** POST /billing/yaver-cloud/change-plan — upgrade to Cloud Workspace.
 *  There are only two paid products now: Relay Pro and Cloud Workspace. This
 *  route accepts Cloud Workspace only and requires LemonSqueezy variant sync
 *  before local entitlements change. */
http.route({
  path: "/billing/yaver-cloud/change-plan",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    let body: any;
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    const targetPlan = body?.plan;
    const region = String(body?.region || "eu").trim() || "eu";
    if (targetPlan !== "cloud-workspace") {
      return errorResponse("plan must be 'cloud-workspace'; Cloud Agent is retired", 400);
    }
    const sub = await ctx.runQuery(internal.subscriptions.getByUser, {
      userId: session.userDocId as any,
    });
    if (!sub || sub.status !== "active") {
      return errorResponse("No active subscription to change", 400);
    }
    const productId = productForSubscriptionPlan(sub.plan);
    if (productId !== "relay-pro" && productId !== "cloud-workspace") {
      return errorResponse("Current subscription is not Relay Pro or Cloud Workspace", 400);
    }
    if (sub.plan === targetPlan) {
      return jsonResponse({ ok: true, plan: targetPlan, unchanged: true });
    }
    const result = await ctx.runAction(internal.plans.changePlan, {
      userId: session.userDocId as any,
      lemonSqueezyId: sub.lemonSqueezyId ?? undefined,
      targetPlan,
    });
    if (!result.ok) {
      // Upgrade refused because billing isn't wired — surface honestly.
      return jsonResponse(
        { ok: false, reason: result.reason ?? "change-failed", plan: sub.plan },
        409,
      );
    }
    if (productId === "relay-pro") {
      await ctx.runMutation(internal.cloudMachines.ensureForSubscription, {
        userId: session.userDocId as any,
        machineType: "standard",
        region,
        subscriptionId: sub._id,
        tier: "byok",
      });
    }
    return jsonResponse({ ok: true, plan: targetPlan, tier: result.tier, billingSynced: result.billingSynced });
  }),
});

/** GET /billing/yaver-cloud/usage — legacy recent wallet activity.
 *  Current normie UI should not expose this as a prepaid cockpit. */
http.route({
  path: "/billing/yaver-cloud/usage",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    const recent = await ctx.runQuery(internal.cloudLifecycle.getRecentUsage, {
      userId: session.userDocId as any,
      limit: 20,
    });
    return jsonResponse({ ok: true, ...recent });
  }),
});

// ── Retired à-la-carte managed-service capability shelf ───────────────
// Flat billing has only Relay Pro and Cloud Workspace. The old managed
// service cockpit/toggles exposed wallet-style à-la-carte controls, so these
// routes now fail closed without reading wallet data or changing user settings.

/** GET /managed/services — the caller's capability opt-in set. */
http.route({
  path: "/managed/services",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    return errorResponse(managedServiceCapabilitiesRetiredMessage(), 410);
  }),
});

/** POST /managed/services — toggle ONE capability {service, enabled}. */
http.route({
  path: "/managed/services",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    return errorResponse(managedServiceCapabilitiesRetiredMessage(), 410);
  }),
});

/** GET /managed/cockpit?days=7 — wallet balance + enabled capabilities +
 *  burn-rate / days-left estimate. One fetch powers the shelf header. */
http.route({
  path: "/managed/cockpit",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    return errorResponse(managedServiceCapabilitiesRetiredMessage(), 410);
  }),
});

/** GET /managed/burn?days=7 — honest per-capability spend breakdown. */
http.route({
  path: "/managed/burn",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    return errorResponse(managedServiceCapabilitiesRetiredMessage(), 410);
  }),
});

/** GET /byo/machines — the user's bring-your-own cloud boxes' lifecycle
 *  state (alive/sleeping/deleted + timestamps). Available to ANY authed
 *  user (BYO is the free, run-on-your-own-account plane). Session-scoped:
 *  only ever returns the caller's own rows; holds no credentials. */
http.route({
  path: "/byo/machines",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const machines = await ctx.runQuery(internal.byoMachines.listForUserInternal, {
      userId: session.userDocId as any,
    });
    return jsonResponse({ ok: true, machines });
  }),
});

/** GET /machine/hosting?deviceId=<id> — the three-tier hosting provenance
 *  (managed | byo | self-hosted) of one of the caller's devices. The agent
 *  calls this to gate its own auto scale-to-zero (hosting_tier.go): it may
 *  power-manage managed/byo boxes it provisioned, never a self-hosted one.
 *  Session-scoped; returns self-hosted for an unknown/foreign deviceId (fail
 *  safe — never claim a box is manageable when it isn't the caller's). */
http.route({
  path: "/machine/hosting",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const deviceId = (new URL(request.url).searchParams.get("deviceId") || "").trim();
    const hosting = await ctx.runQuery(internal.cloudMachines.hostingForDevice, {
      userId: session.userDocId as any,
      deviceId,
    });
    return jsonResponse({ ok: true, ...hosting });
  }),
});

// POST /byo/provision-init — mint a BYO box bootstrap (device credential +
// self-bootstrapping cloud-init) so the PHONE can create the server on the
// user's own Hetzner account. No Hetzner token here: the phone holds it and
// calls api.hetzner.cloud directly. Returns the cloud-init user_data the
// phone bakes into the server. project: phone-direct hcloud provision-to-vibe.
http.route({
  path: "/byo/provision-init",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    let body: { machineType?: string; region?: string } = {};
    try {
      body = await request.json();
    } catch {
      /* defaults below */
    }
    const machineType = normalizeCloudMachineType(body.machineType);
    const region = (body.region ?? "eu").trim() === "us" ? "us" : "eu";
    // Anti-fan-out: cap BYO rows per user the same way managed does.
    const MAX = Number(process.env.YAVER_CLOUD_MAX_MACHINES_PER_USER) || 10;
    const existing = await ctx.runQuery(internal.cloudMachines.listForUser, { userId: session.userDocId as any });
    const live = Array.isArray(existing) ? existing.filter((m: any) => m.status && m.status !== "stopped").length : 0;
    if (live >= MAX) {
      return jsonResponse({ ok: false, error: `Machine limit reached (${MAX}).` }, 409);
    }
    try {
      const out = await ctx.runAction(internal.cloudMachines.mintByoBootstrap, {
        userId: session.userDocId as any,
        machineType,
        region,
      });
      return jsonResponse({ ok: true, ...out });
    } catch (e) {
      return jsonResponse({ ok: false, error: e instanceof Error ? e.message : String(e) }, 500);
    }
  }),
});

// POST /byo/provision-complete — the phone reports the Hetzner id/ip it just
// created so the row can manage it later. The box itself also self-registers
// as a device (via the baked session) and beacons phases over X-Machine-Token.
http.route({
  path: "/byo/provision-complete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    let body: { machineId?: string; hetznerServerId?: string; serverIp?: string } = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad body", 400);
    }
    if (!body.machineId || !body.hetznerServerId) {
      return errorResponse("machineId + hetznerServerId required", 400);
    }
    // Ownership check: the row must belong to the caller.
    const machine = await ctx.runQuery(internal.cloudMachines.get, { machineId: body.machineId as any }).catch(() => null);
    if (!machine || String(machine.userId) !== String(session.userDocId)) {
      return errorResponse("Not found", 404);
    }
    await ctx.runMutation(internal.cloudMachines.setProvisioned, {
      machineId: body.machineId as any,
      hetznerServerId: body.hetznerServerId,
      serverIp: body.serverIp ?? "",
      hostname: body.serverIp ?? "",
    });
    return jsonResponse({ ok: true });
  }),
});

/** POST /billing/yaver-cloud/provision — legacy wallet-funded spin-up.
 *  Disabled for the flat subscription model. New compute must be attached to
 *  Cloud Workspace subscription/reconcile flows so one user cannot create
 *  unmanaged prepaid machines that bypass margin controls. */
http.route({
  path: "/billing/yaver-cloud/provision",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    return errorResponse(legacyPrepaidProvisionDisabledMessage(), 410);
  }),
});

/** POST /billing/yaver-cloud/topup-dev — owner-dev prepaid credit stub.
 *  This is deliberately owner-gated and uses the P0 ledger primitive; no
 *  LemonSqueezy charge is created until the real prepaid checkout lands. */
http.route({
  path: "/billing/yaver-cloud/topup-dev",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    if (!isCloudPreviewUser(session.email, session.userDocId)) {
      return errorResponse("Owner-only (private preview) on this account", 403);
    }
    const body = await request.json().catch(() => ({}));
    const amountCents = Number(body.amountCents ?? 1000);
    if (!Number.isFinite(amountCents) || amountCents <= 0 || amountCents > 100_000) {
      return errorResponse("amountCents must be between 1 and 100000", 400);
    }
    const result = await ctx.runMutation(internal.cloudLifecycle.topUp, {
      userId: session.userDocId as any,
      amountCents: Math.round(amountCents),
    });
    return jsonResponse({ ok: true, mode: "owner-dev", ...result });
  }),
});

/** POST /billing/yaver-cloud/auto-park — configure auto-close.
 *
 *  Auto-park stays ON by default and cannot be disabled from customer-facing
 *  product APIs. A forgotten Cloud Workspace must stop its own meter; operators
 *  still have the explicit YAVER_CLOUD_IDLE_DISABLE emergency brake. */
http.route({
  path: "/billing/yaver-cloud/auto-park",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    const body = await request.json().catch(() => ({}));
    const parsed = validateCustomerAutoParkRequest(body);
    if (!parsed.ok) return errorResponse(parsed.error, 400);
    try {
      const r = await ctx.runMutation(internal.cloudMachines.setAutoPark, {
        userDocId: session.userDocId as any,
        machineId: parsed.machineId as any,
        enabled: parsed.enabled,
        idleMinutes: parsed.idleMinutes,
      });
      return jsonResponse({ ...r, machineId: parsed.machineId });
    } catch (e) {
      return errorResponse(e instanceof Error ? e.message : "Failed", 400);
    }
  }),
});

/** POST /billing/yaver-cloud/stop — owner-only PAUSE: snapshot the box,
 *  then DELETE the Hetzner server (a powered-off server still bills
 *  full price; only delete stops the charge). Status → "paused",
 *  snapshot id kept for resume. Real Hetzner calls when HCLOUD_TOKEN is
 *  set; fail-closed dry-run state transition otherwise. */
http.route({
  path: "/billing/yaver-cloud/stop",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    const body = await request.json().catch(() => ({}));
    const machineId = String(body.machineId ?? "").trim();
    if (!machineId) return errorResponse("machineId is required", 400);
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, {
      machineId: machineId as any,
    });
    if (!machine) return errorResponse("Machine not found", 404);
    if (String(machine.userId) !== String(session.userDocId)) {
      return errorResponse("Not your machine", 403);
    }
    const managedDenied = denyNonYaverManagedMachine(machine);
    if (managedDenied) return managedDenied;
    // Idempotent: already parked.
    if (machine.status === "paused" || machine.status === "stopped" || machine.status === "stopping") {
      return jsonResponse({ ok: true, machineId, status: machine.status, dryRun: true });
    }
    const wakeRunId = await ctx.runMutation(internal.wakeRuns.start, {
      userId: session.userDocId as any,
      machineId: machine._id,
      kind: "park",
      status: "queued",
      phase: "queued",
      progress: 1,
      machineType: String(machine.machineType || "standard"),
      targetDeviceId: machine.deviceId ?? managedDeviceIdFor(String(machine._id)),
      reason: "user requested Cloud Workspace park",
    });
    // P3: delegate to the real, Hetzner-integrated lifecycle (P2).
    // It is FAIL-CLOSED dry-run when HCLOUD_TOKEN is unset (prod
    // default — no real spend); real snapshot+delete only when an
    // owner deliberately sets the platform token.
    const r = await ctx.runAction(internal.cloudLifecycle.pauseMachine, {
      machineId: machineId as any,
    });
    if (r.ok === false) {
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId: machine._id,
        kind: "park",
        status: "failed",
        phase: "error",
        error: String(r.reason || "park failed"),
      }).catch(() => {});
    }
    return jsonResponse({ machineId, wakeRunId, ...r }, r.ok ? 200 : 409);
  }),
});

/** POST /billing/yaver-cloud/start — owner-only RESUME: recreate the
 *  Hetzner server from the pause snapshot, re-point DNS, status →
 *  "active". Balance-gated against the prepaid reserve (402 below it).
 *  Real Hetzner calls when HCLOUD_TOKEN is set; dry-run otherwise. */
http.route({
  path: "/billing/yaver-cloud/start",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    if (!cloudAccessAllowed(session.email, session.userDocId)) {
      return errorResponse("Yaver Cloud is private-preview only on this account", 403);
    }
    const body = await request.json().catch(() => ({}));
    const machineId = String(body.machineId ?? "").trim();
    if (!machineId) return errorResponse("machineId is required", 400);
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, {
      machineId: machineId as any,
    });
    if (!machine) return errorResponse("Machine not found", 404);
    if (String(machine.userId) !== String(session.userDocId)) {
      return errorResponse("Not your machine", 403);
    }
    const managedDenied = denyNonYaverManagedMachine(machine);
    if (managedDenied) return managedDenied;
    if (machine.status === "active") {
      return jsonResponse({ ok: true, machineId, status: "active", dryRun: true });
    }
    const wakeRunId = await ctx.runMutation(internal.wakeRuns.start, {
      userId: session.userDocId as any,
      machineId: machine._id,
      kind: "wake",
      status: "queued",
      phase: "queued",
      progress: 1,
      machineType: String(machine.machineType || "standard"),
      targetDeviceId: machine.deviceId ?? managedDeviceIdFor(String(machine._id)),
      reason: "user requested Cloud Workspace wake",
    });
    // Keep the explicit 402 billing contract mobile/web depend on, but present
    // it as flat-plan allowance instead of prepaid wallet UX.
    const gate = await ctx.runQuery(internal.cloudLifecycle.canStart, {
      userId: session.userDocId as any,
      machineType: String(machine.machineType || "cpu"),
    });
    if (!gate.ok) {
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId: machine._id,
        kind: "wake",
        status: "failed",
        phase: "billing-required",
        error: "Workspace allowance exhausted",
      }).catch(() => {});
      return jsonResponse({
        ok: false,
        error: "Workspace allowance exhausted. Cloud Workspace compute pauses until the next period or billing settings are updated.",
        balanceCents: gate.balanceCents,
        requiredCents: gate.requiredCents,
        wakeRunId,
      }, 402);
    }
    // P3: delegate to the real, Hetzner-integrated lifecycle (P2) —
    // recreate-from-snapshot, fail-closed dry-run when HCLOUD_TOKEN
    // is unset (prod default; no real spend).
    const r = await ctx.runAction(internal.cloudLifecycle.resumeMachine, {
      machineId: machineId as any,
    });
    if (r.ok === false) {
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId: machine._id,
        kind: "wake",
        status: r.retryable ? "retrying" : "failed",
        phase: r.retryable ? "provider-retry" : "error",
        error: String(r.reason || "wake failed"),
      }).catch(() => {});
    }
    // NOTE: resumeMachine owns the wake phase ladder now (booting →
    // registering, then resumeHealthCheck → ready once /health answers).
    // We deliberately do NOT pin phase="ready" here — doing so used to
    // paint 100% the instant the server record was created, long before
    // the box was reachable.
    return jsonResponse(
      { machineId, wakeRunId, balanceCents: gate.balanceCents, requiredCents: gate.requiredCents, ...r },
      r.ok ? 200 : 409,
    );
  }),
});

/** GET /subscription — Get subscription and managed relay status (authenticated). */
http.route({
  path: "/subscription",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    // Get user doc to get _id
    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const [subscription, relay, machines, wallet, allowance] = await Promise.all([
      ctx.runQuery(internal.subscriptions.getByUser, { userId: userDocId }),
      ctx.runQuery(internal.managedRelays.getByUser, { userId: userDocId }),
      ctx.runQuery(internal.cloudMachines.listForUser, { userId: userDocId }),
      ctx.runQuery(internal.cloudLifecycle.getWallet, { userId: userDocId }),
      ctx.runQuery(internal.cloudLifecycle.getAllowance, { userId: userDocId, machineType: "standard" }),
    ]);
    const allowanceBalance = {
      plan: allowance.plan,
      unit: "standard_credits",
      includedStandardCredits: Math.round(allowance.includedSeconds / 3600),
      usedStandardCredits: Math.round((allowance.usedSeconds / 3600) * 10) / 10,
      remainingStandardCredits: Math.max(0, Math.round((allowance.remainingSeconds / 3600) * 10) / 10),
      includedSeconds: allowance.includedSeconds,
      usedSeconds: allowance.usedSeconds,
      remainingSeconds: allowance.remainingSeconds,
    };

    return jsonResponse({
      // Owner-allowlist flag (server is the source of truth — the
      // CLOUD_PREVIEW_OWNER_EMAIL gate, never a hardcoded name). The
      // web hides the entire managed-cloud panel/billing for
      // non-owners; this is COSMETIC only — every money-spending
      // route is independently 403'd by isCloudPreviewUser and
      // provisioning is fail-closed behind canProvisionManaged, so a
      // non-owner reading this open-source code still cannot spend
      // Yaver's Hetzner. Private preview until LemonSqueezy is fully
      // integrated; owner-only purchases for now.
      cloudPreviewOwner: isCloudPreviewUser(session.email, session.userDocId),
      // True when this account may use the prepaid-cloud surfaces — the
      // owner allowlist OR the YAVER_CLOUD_PUBLIC launch flag. Mobile/web
      // render the wallet + machine controls when this is true; still
      // cosmetic (every money route is independently gated server-side).
      cloudAccess: cloudAccessAllowed(session.email, session.userDocId),
      subscription: subscription ? {
        plan: subscription.plan,
        status: subscription.status,
        currentPeriodEnd: subscription.currentPeriodEnd,
        cancelledAt: subscription.cancelledAt,
      } : null,
      prepaidBalanceCents: wallet.balanceCents,
      currency: wallet.currency,
      balance: { ...wallet, allowance: allowanceBalance },
      relay: relay ? {
        status: relay.status,
        domain: relay.domain,
        region: relay.region,
        quicPort: relay.quicPort,
        httpPort: relay.httpPort,
      } : null,
      machines: Array.isArray(machines)
        ? machines
            // Only surface machines the user can actually act on: a genuinely
            // wakeable parked box (paused/suspended WITH a recovery pointer) or
            // a live one. `removed` = decommissioned, `stopped` = not resumable
            // (waking it 409s) — both are dead rows that only clutter the picker
            // and cause failed wakes. Also collapse duplicate rows that share a
            // deviceId (the _id-truncation collision), keeping the first.
            .filter((machine) => {
              const s = String(machine.status || "");
              if (s === "removed" || s === "stopped") return false;
              // paused/suspended (wakeable) AND error (snapshot/park failed) are
              // only worth showing if they still have a recovery pointer to wake
              // from. A zombie — errored park with its server already deleted and
              // no snapshot — just offers a "Try wake again" that can never
              // succeed, so hide it (e.g. the mn71me24 leftover).
              if (s === "paused" || s === "suspended" || s === "error") {
                return !!(machine.lastSnapshotId || (machine.volumeId && machine.baseImageId));
              }
              return true;
            })
            .filter((machine, i, arr) => {
              // Collapse duplicate rows sharing a deviceId — keep the first.
              const dev = machine.deviceId;
              if (!dev) return true;
              return arr.findIndex((m) => m.deviceId === dev) === i;
            })
            .map((machine) => ({
            id: String(machine._id),
            machineType: machine.machineType,
            status: machine.status,
            hostname: machine.hostname,
            serverIp: machine.serverIp,
            region: machine.region,
            errorMessage: machine.errorMessage,
            subscriptionId: machine.subscriptionId ? String(machine.subscriptionId) : null,
            // Surfaced so the web Recycle/Remove dialog can resolve the
            // exact cloud resource of a managed box from stored state —
            // the user never has to recall it. Still an exact id (never
            // fuzzy-matched); the dialog matches on deviceId/ip. The UI
            // stays provider-neutral; `provider` tells the agent facade
            // which API to call. hetznerServerId kept for back-compat.
            provider: machine.provider ?? "hetzner",
            cloudResourceId: machine.cloudResourceId ?? machine.hetznerServerId ?? null,
            hetznerServerId: machine.hetznerServerId ?? null,
            // Fall back to the derived id rather than null. The column is only
            // written once a box registers, so a box whose session expired —
            // precisely the one the user needs to sign in — reported no id at
            // all, and the phone refused to recover it ("the cloud row does not
            // include the device id … wait a few seconds"), which could never
            // come true: it can't register until it's signed in, and we
            // wouldn't sign it in until it registered. The id was computable
            // from the machine id the whole time.
            deviceId: machine.deviceId ?? managedDeviceIdFor(machine._id.toString()),
            // Auto-close state, so mobile/web can render the toggle truthfully.
            // undefined === ON (the default), so we normalise to a real boolean.
            autoParkEnabled: machine.autoParkEnabled !== false,
            autoParkMinutes: machine.autoParkMinutes ?? 45,
            // Fast-wake: a persistent volume means park/wake no longer restore a
            // fat disk. Surfaced so the UI can say "wakes in ~1-2 min".
            hasVolume: Boolean(machine.volumeId),
            // Parked/wakeable machine surface: the mobile "Yaver-managed · Parked"
            // card renders specs + "Slept/Woke N ago" from these. Absent on older
            // rows → the card degrades to machineType/region.
            serverType: machine.serverType ?? null,
            specs: machine.specs ?? null,
            lastParkedAt: machine.lastParkedAt ?? null,
            lastWokeAt: machine.lastWokeAt ?? null,
            // First-class onboarding: web/mobile render an
            // initializing state + progress bar + "Authorize runners"
            // from these (project_managed_cloud_onboarding_gap).
            provisionPhase: machine.provisionPhase ?? null,
            provisionProgress:
              typeof machine.provisionProgress === "number"
                ? machine.provisionProgress
                : null,
            // When the CURRENT phase started. Without it a client can only
            // time a wake from lastWokeAt, so it cannot tell "booting for 20s"
            // from "booting for 9 minutes" — which is the whole difference
            // between a normal wake and a stuck one.
            provisionPhaseAt: machine.provisionPhaseAt ?? null,
            // Provider's own view of the server during a wake, so the UI can
            // say "Hetzner: initializing" instead of an unexplained spinner.
            providerStatus: machine.providerStatus ?? null,
            providerStatusAt: machine.providerStatusAt ?? null,
            // Wake/park run telemetry. lastWakeDurationMs lets a surface quote
            // an ETA measured on THIS box instead of a constant; lastWakeOutcome
            // lets a parked box explain why its last wake didn't stick.
            wakeStartedAt: machine.wakeStartedAt ?? null,
            wakeCompletedAt: machine.wakeCompletedAt ?? null,
            lastWakeDurationMs: machine.lastWakeDurationMs ?? null,
            lastWakeOutcome: machine.lastWakeOutcome ?? null,
            parkStartedAt: machine.parkStartedAt ?? null,
            parkCompletedAt: machine.parkCompletedAt ?? null,
            lastParkDurationMs: machine.lastParkDurationMs ?? null,
            snapshotSizeGb: machine.snapshotSizeGb ?? null,
            snapshotCreatedAt: machine.snapshotCreatedAt ?? null,
            // Short curated failure label the box itself beaconed
            // (phase="error"); drives the synthetic "Setting up" card's
            // failure state + recovery hint in web/mobile.
            provisionError: machine.provisionError ?? null,
            // "golden" ⇒ fast boot from a prebuilt snapshot; "vanilla" ⇒
            // ubuntu-24.04 with a 3–5 min first-boot build. Lets the card
            // show the right "setting up" expectation.
            bootImageSource: machine.bootImageSource ?? null,
            // Preserve UNSET as null — never coerce it to false. The readiness
            // gate everywhere else is strict `=== false` (cloudMachines.ts:2316
            // writes phase "ready"/100 when the field is undefined), so
            // flattening undefined→false told every client "runners are NOT
            // authorized" about a box the backend had just declared ready.
            // Mobile's ladders all test `runnersAuthorized !== false`, so they
            // pinned a fully-woken box at 92% "Finishing up…" forever and the
            // picker kept it filed under "Sleeping machines" — i.e. a
            // successful wake was unusable from the UI. null passes `!== false`
            // and matches the backend's own semantics.
            runnersAuthorized: machine.runnersAuthorized ?? null,
          }))
        : [],
    });
  }),
});

// --- Teams (shared machines, multi-user) ---

/** GET /teams — List teams for the authenticated user. */
http.route({
  path: "/teams",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const teams = await ctx.runQuery(internal.teams.getTeamsForUser, { userId: userDocId });
    return jsonResponse({ teams });
  }),
});

/** POST /teams — Create a team. */
http.route({
  path: "/teams",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const body = await request.json();
    const teamId = await ctx.runMutation(internal.teams.create, {
      name: body.name || "My Team",
      ownerId: userDocId,
      plan: body.plan || "cpu",
      maxMembers: body.maxMembers || 10,
    });

    return jsonResponse({ teamId });
  }),
});

/** POST /teams/members — Add a member to a team. */
http.route({
  path: "/teams/members",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const body = await request.json();
    if (!body.teamId || !body.email) return errorResponse("teamId and email required", 400);

    // Verify caller is team admin
    const team = await ctx.runQuery(internal.teams.getByTeamId, { teamId: body.teamId });
    if (!team) return errorResponse("Team not found", 404);

    const isMember = await ctx.runQuery(internal.teams.isMember, { teamId: body.teamId, userId: userDocId });
    if (!isMember) return errorResponse("Not a team member", 403);

    try {
      const result = await ctx.runMutation(internal.teams.addMember, {
        teamId: body.teamId,
        userEmail: body.email,
        role: body.role || "member",
        invitedBy: userDocId,
      });
      return jsonResponse(result);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      return errorResponse(msg, 400);
    }
  }),
});

/** GET /teams/members?teamId=xxx — List members of a team. */
http.route({
  path: "/teams/members",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const url = new URL(request.url);
    const teamId = url.searchParams.get("teamId");
    if (!teamId) return errorResponse("teamId required", 400);

    const members = await ctx.runQuery(internal.teams.listMembers, { teamId });
    return jsonResponse({ members });
  }),
});

/** GET /teams/validate?teamId=xxx — Check if authenticated user is a team member.
 *  Used by the multi-user agent to validate team access. */
http.route({
  path: "/teams/validate",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const url = new URL(request.url);
    const teamId = url.searchParams.get("teamId");
    if (!teamId) return errorResponse("teamId required", 400);

    const isMember = await ctx.runQuery(internal.teams.isMember, { teamId, userId: userDocId });
    return jsonResponse({ isMember, teamId, userId: session.userId });
  }),
});

// --- Company AI Options (tenant policy) ---

/** GET /company-ai/options?teamId=team_xxx — Read company AI policy.
 *  Returns safe defaults when the team has not configured AI yet. */
http.route({
  path: "/company-ai/options",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const url = new URL(request.url);
    const teamId = url.searchParams.get("teamId");
    if (!teamId) return errorResponse("teamId required", 400);

    const result = await ctx.runQuery(api.companyAIOptions.getByToken, { tokenHash, teamId });
    if (!result) return errorResponse("Team not found or access denied", 404);
    return jsonResponse({ ok: true, ...result });
  }),
});

/** POST /company-ai/options — Update company AI policy.
 *  Only team admins may write. Body: {teamId, options}. */
http.route({
  path: "/company-ai/options",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const body = await request.json();
    if (!body?.teamId || !body?.options) {
      return errorResponse("teamId and options are required", 400);
    }

    try {
      const id = await ctx.runMutation(api.companyAIOptions.setByToken, {
        tokenHash,
        teamId: body.teamId,
        options: body.options,
      });
      return jsonResponse({ ok: true, id });
    } catch (e: any) {
      return errorResponse(e?.message || "Failed to update company AI options", 403);
    }
  }),
});

/** POST /company-ai/resolve — Resolve company AI runtime for a work kind.
 *  Returns no secrets; clients use the selected Yaver device + existing agent endpoints. */
http.route({
  path: "/company-ai/resolve",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const body = await request.json();
    if (!body?.teamId || !body?.workKind) {
      return errorResponse("teamId and workKind are required", 400);
    }

    const result = await ctx.runQuery(api.companyAIOptions.resolveForToken, {
      tokenHash,
      teamId: body.teamId,
      workKind: body.workKind,
      requestedRunner: body.requestedRunner,
      requestedModel: body.requestedModel,
      requestedProvider: body.requestedProvider,
      requestedDeviceId: body.requestedDeviceId,
      source: body.source,
    });
    if (!result) return errorResponse("Team not found or access denied", 404);
    return jsonResponse(result);
  }),
});

// --- Cloud Machines ---

/** GET /machines — List cloud machines for the authenticated user. */
http.route({
  path: "/machines",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);

    const userDocId = await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
    if (!userDocId) return errorResponse("User not found", 404);

    const machines = await ctx.runQuery(internal.cloudMachines.listForUser, { userId: userDocId });
    return jsonResponse({ machines });
  }),
});

/** POST /machines — Create a cloud machine (called by webhook or admin). */
http.route({
  path: "/machines",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));

    const session = await ctx.runQuery(api.auth.validateSession, { tokenHash });
    if (!session) return errorResponse("Unauthorized", 401);
    const scopeDenied = requireFullScope(session);
    if (scopeDenied) return scopeDenied;
    return errorResponse(genericMachineCreateDisabledMessage(), 410);
  }),
});

// --- Chat assistant (landing page help bot) ---

http.route({
  path: "/chat",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const OPENROUTER_API_KEY = process.env.OPENROUTER_API_KEY;
    if (!OPENROUTER_API_KEY) {
      return jsonResponse({ error: "Chat not configured" }, 503);
    }

    let body;
    try {
      body = await request.json();
    } catch {
      return jsonResponse({ error: "Invalid JSON" }, 400);
    }

    const userMessage = body.message?.trim();
    if (!userMessage || userMessage.length > 500) {
      return jsonResponse({ error: "Message required (max 500 chars)" }, 400);
    }

    const systemPrompt = `You are Yaver's help assistant on the yaver.io website. Yaver is a free, open-source P2P tool that lets developers control AI coding agents (Claude Code, Codex, Aider, Ollama, etc.) from their phone, desktop, or any terminal.

Your ONLY purpose is to help users with Yaver-related questions:
- How to install and set up Yaver (CLI, mobile app, desktop GUI)
- How to connect devices, set up relay servers, use Tailscale
- How to use features: tasks, exec/RPC, session transfer, scheduling, notifications
- How to integrate: Telegram bot, Discord, Slack, CI/CD webhooks, MCP tools
- How to use SDKs (Go, Python, JS/TS, Flutter/Dart)
- How to self-host a relay server

Rules:
- Keep answers short (2-4 sentences). Link to docs when relevant.
- If the question is NOT about Yaver, politely say: "I can only help with Yaver-related questions. Check out yaver.io/docs for guides, or yaver.io/faq for common questions."
- Never make up features that don't exist.
- Key links: yaver.io/docs, yaver.io/manuals, yaver.io/download, yaver.io/manuals/integrations
- Yaver's open-source stack is free to self-host. Optional Yaver Cloud is web-billed managed infrastructure: saved cloud workspaces, private relay, and auto-stop. The mobile app controls already-owned machines and does not sell managed cloud inside the app stores.
- Privacy-first: code never leaves the developer's machines. Relay is pass-through, encrypted.`;

    try {
      const resp = await fetch("https://openrouter.ai/api/v1/chat/completions", {
        method: "POST",
        headers: {
          "Authorization": `Bearer ${OPENROUTER_API_KEY}`,
          "Content-Type": "application/json",
          "HTTP-Referer": "https://yaver.io",
          "X-Title": "Yaver Help",
        },
        body: JSON.stringify({
          model: "meta-llama/llama-4-scout",
          messages: [
            { role: "system", content: systemPrompt },
            ...(body.history || []).slice(-4),
            { role: "user", content: userMessage },
          ],
          max_tokens: 300,
          temperature: 0.3,
        }),
      });

      if (!resp.ok) {
        return jsonResponse({ error: "AI service unavailable" }, 502);
      }

      const data = await resp.json() as any;
      const reply = data.choices?.[0]?.message?.content || "Sorry, I couldn't generate a response.";

      return new Response(JSON.stringify({ ok: true, reply }), {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Access-Control-Allow-Origin": "*",
        },
      });
    } catch {
      return jsonResponse({ error: "Chat error" }, 500);
    }
  }),
});

// CORS preflight for /chat
http.route({
  path: "/chat",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "POST, OPTIONS",
        "Access-Control-Allow-Headers": "Content-Type",
      },
    });
  }),
});

// ── SDK Tokens ──────────────────────────────────────────────────────

/**
 * POST /sdk/token — Create an SDK token for the Feedback SDK.
 * Requires a valid CLI session token (Bearer auth).
 * Body: { label?, scopes?, allowedCIDRs?, expiresInMs? }
 * Returns: { token, expiresAt }
 */
http.route({
  path: "/sdk/token",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) return errorResponse("Unauthorized", 401);

    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const sdkToken = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const sdkTokenHash = await sha256Hex(sdkToken);

    const authHeader = request.headers.get("Authorization")!;
    const sessionTokenHash = await sha256Hex(authHeader.slice(7));

    let label: string | undefined;
    let scopes: string[] | undefined;
    let allowedCIDRs: string[] | undefined;
    let expiresInMs: number | undefined;
    try {
      const body = await request.json();
      label = body.label;
      scopes = body.scopes;
      allowedCIDRs = body.allowedCIDRs;
      expiresInMs = body.expiresInMs;
    } catch {
      // No body — use defaults
    }

    const result = await ctx.runMutation(api.auth.createSdkToken, {
      tokenHash: sdkTokenHash,
      sessionTokenHash,
      label,
      scopes,
      allowedCIDRs,
      expiresInMs,
    });

    return jsonResponse({ token: sdkToken, expiresAt: result.expiresAt });
  }),
});

http.route({
  path: "/sdk/token",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "POST, OPTIONS",
        "Access-Control-Allow-Headers": "Authorization, Content-Type",
      },
    });
  }),
});

/**
 * GET /sdk/token/validate — Validate an SDK token (used by agent auth middleware).
 * Returns: { user: { userId, email, fullName, provider, scopes, allowedCIDRs } }
 */
http.route({
  path: "/sdk/token/validate",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const result = await ctx.runQuery(api.auth.validateSdkToken, { tokenHash });
    if (!result) {
      return errorResponse("Invalid or expired SDK token", 401);
    }

    return jsonResponse({ user: result });
  }),
});

/**
 * POST /guests/sdk-token — Mint a delegated Feedback SDK token for a repo-scoped
 * guest grant. Bearer auth: guest's normal session token.
 * Body: { hostUserId, targetDeviceId }
 */
http.route({
  path: "/guests/sdk-token",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) return errorResponse("Unauthorized", 401);

    let body: { hostUserId?: string; targetDeviceId?: string };
    try {
      body = await request.json();
    } catch {
      return errorResponse("Invalid JSON", 400);
    }
    if (!body.hostUserId) return errorResponse("hostUserId is required");
    if (!body.targetDeviceId) return errorResponse("targetDeviceId is required");

    const authHeader = request.headers.get("Authorization")!;
    const guestTokenHash = await sha256Hex(authHeader.slice(7));

    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const sdkToken = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const sdkTokenHash = await sha256Hex(sdkToken);

    const result = await ctx.runMutation((api as any).guests.mintGuestFeedbackSdkToken, {
      guestTokenHash,
      sdkTokenHash,
      hostUserId: body.hostUserId,
      targetDeviceId: body.targetDeviceId,
    });

    return jsonResponse({
      token: sdkToken,
      expiresAt: result.expiresAt,
      allowedProjects: result.allowedProjects ?? [],
      sourceSurface: "feedback-sdk",
    });
  }),
});

http.route({
  path: "/guests/sdk-token",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "POST, OPTIONS",
        "Access-Control-Allow-Headers": "Authorization, Content-Type",
      },
    });
  }),
});

/**
 * POST /sdk/token/rotate — Rotate an SDK token.
 * Bearer auth: current SDK token.
 * Returns: { token, expiresAt } (new token; old valid for 5min grace).
 */
http.route({
  path: "/sdk/token/rotate",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const currentToken = authHeader.slice(7);
    const currentTokenHash = await sha256Hex(currentToken);

    // Generate new token
    const tokenBytes = new Uint8Array(32);
    crypto.getRandomValues(tokenBytes);
    const newToken = Array.from(tokenBytes)
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
    const newTokenHash = await sha256Hex(newToken);

    const result = await ctx.runMutation(api.auth.rotateSdkToken, {
      currentTokenHash,
      newTokenHash,
    });

    return jsonResponse({ token: newToken, expiresAt: result.expiresAt });
  }),
});

http.route({
  path: "/sdk/token/rotate",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "POST, OPTIONS",
        "Access-Control-Allow-Headers": "Authorization, Content-Type",
      },
    });
  }),
});

/**
 * POST /sdk/token/revoke — Revoke an SDK token.
 * Bearer auth: CLI session token. Body: { sdkToken }
 */
http.route({
  path: "/sdk/token/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) return errorResponse("Unauthorized", 401);

    const authHeader = request.headers.get("Authorization")!;
    const sessionTokenHash = await sha256Hex(authHeader.slice(7));

    let sdkToken: string;
    try {
      const body = await request.json();
      sdkToken = body.sdkToken;
    } catch {
      return errorResponse("Missing sdkToken in body", 400);
    }

    const sdkTokenHash = await sha256Hex(sdkToken);
    await ctx.runMutation(api.auth.revokeSdkToken, {
      sessionTokenHash,
      sdkTokenHash,
    });

    return jsonResponse({ ok: true });
  }),
});

// ── Security Events ─────────────────────────────────────────────────

/** POST /security/event — Report a security event from the agent. */
http.route({
  path: "/security/event",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    let body: { eventType: string; details: string };
    try {
      body = await request.json();
    } catch {
      return errorResponse("Invalid body", 400);
    }

    await ctx.runMutation(api.auth.reportSecurityEvent, {
      tokenHash,
      eventType: body.eventType,
      details: body.details,
    });

    return jsonResponse({ ok: true });
  }),
});

/** GET /security/events — List recent security events for the authenticated user. */
http.route({
  path: "/security/events",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const events = await ctx.runQuery(api.auth.listSecurityEvents, { tokenHash });
    return jsonResponse({ events });
  }),
});

// ── Guest Access ────────────────────────────────────────────────────

/** POST /guests/invite — Invite a guest by email. Host only. */
http.route({
  path: "/guests/invite",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    const email = typeof body.email === "string" ? body.email : undefined;
    const userId = typeof body.userId === "string" ? body.userId : undefined;
    const proposedDeviceIds = Array.isArray(body.deviceIds) ? body.deviceIds.map(String) : undefined;
    const scope =
      body.scope === "full" || body.scope === "feedback-only" || body.scope === "sdk-project"
        ? body.scope
        : undefined;
    const allowedProjects = Array.isArray(body.allowedProjects)
      ? body.allowedProjects.map(String)
      : undefined;
    const canVibe = body.canVibe === true ? true : undefined;
    if (!email && !userId) {
      return errorResponse("email or userId is required");
    }

    try {
      const result = await ctx.runMutation(api.guests.invite, {
        tokenHash,
        guestEmail: email,
        guestUserId: userId,
        proposedDeviceIds,
        scope,
        allowedProjects,
        canVibe,
      });
      return jsonResponse({
        ok: true,
        inviteCode: result.inviteCode,
        guestRegistered: result.guestRegistered,
        guestUserId: result.guestUserId,
        guestEmail: result.guestEmail,
      });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to invite guest", 400);
    }
  }),
});

/** POST /guests/accept — Accept a pending invitation by email match. Guest only. */
http.route({
  path: "/guests/accept",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    if (!body.hostUserId) return errorResponse("hostUserId is required");
    const approvedDeviceIds = Array.isArray(body.approvedDeviceIds)
      ? body.approvedDeviceIds.map(String)
      : undefined;

    try {
      await ctx.runMutation(api.guests.accept, {
        tokenHash,
        hostUserId: body.hostUserId,
        approvedDeviceIds,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to accept invitation", 400);
    }
  }),
});

/** POST /guests/accept-code — Accept invitation via 6-char code. Works with any OAuth email. */
http.route({
  path: "/guests/accept-code",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    if (!body.code) return errorResponse("code is required");
    const approvedDeviceIds = Array.isArray(body.approvedDeviceIds)
      ? body.approvedDeviceIds.map(String)
      : undefined;

    try {
      const result = await ctx.runMutation(api.guests.acceptByCode, {
        tokenHash,
        inviteCode: body.code,
        approvedDeviceIds,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to accept invitation", 400);
    }
  }),
});

/** GET /guests/find-by-code?code=XXXX — Preview invitation before accepting. */
http.route({
  path: "/guests/find-by-code",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const code = url.searchParams.get("code") ?? "";
    if (!code) return errorResponse("code is required");

    const info = await ctx.runQuery(api.guests.findByCode, { tokenHash, inviteCode: code });
    if (!info) return errorResponse("Invite not found or expired", 404);
    return jsonResponse(info);
  }),
});

/** GET /users/lookup?userId=XXXX — Resolve a public user id to a display profile. */
http.route({
  path: "/users/lookup",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const userId = url.searchParams.get("userId") ?? "";
    if (!userId) return errorResponse("userId is required");

    const user = await ctx.runQuery(api.guests.lookupPublicUser, { tokenHash, userId });
    if (!user) return errorResponse("User not found", 404);
    return jsonResponse(user);
  }),
});

/** POST /guests/revoke — Revoke guest access. Host only. */
http.route({
  path: "/guests/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    const email = typeof body.email === "string" ? body.email : undefined;
    const userId = typeof body.userId === "string" ? body.userId : undefined;
    if (!email && !userId) return errorResponse("email or userId is required");

    try {
      await ctx.runMutation(api.guests.revoke, {
        tokenHash,
        guestEmail: email,
        guestUserId: userId,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to revoke guest", 400);
    }
  }),
});

/**
 * POST /guests/leave — Guest drops their OWN access to a host's shared infra.
 * Guest only; the session user is always the guest, so this can never remove
 * anyone else's access. Re-invitable afterwards.
 */
http.route({
  path: "/guests/leave",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    const hostUserId = typeof body.hostUserId === "string" ? body.hostUserId : undefined;
    const hostEmail = typeof body.hostEmail === "string" ? body.hostEmail : undefined;
    if (!hostUserId && !hostEmail) {
      return errorResponse("hostUserId or hostEmail is required");
    }

    try {
      const result = await ctx.runMutation(api.guests.leave, {
        tokenHash,
        hostUserId,
        hostEmail,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to leave shared access", 400);
    }
  }),
});

/** GET /guests/list — List all guests (host perspective). */
http.route({
  path: "/guests/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const guests = await ctx.runQuery(api.guests.listGuests, { tokenHash });
    return jsonResponse({ guests });
  }),
});

/** GET /guests/hosts — List hosts who granted access (guest perspective). */
http.route({
  path: "/guests/hosts",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const hosts = await ctx.runQuery(api.guests.listHosts, { tokenHash });
    return jsonResponse(hosts);
  }),
});

/** GET /guests/conversion?role=guest|host — UI-surface funnel state. */
http.route({
  path: "/guests/conversion",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);
    const role = new URL(request.url).searchParams.get("role") === "host" ? "host" : "guest";

    const result = role === "host"
      ? await ctx.runQuery((api as any).guests.getHostConversionSummary, { tokenHash })
      : await ctx.runQuery((api as any).guests.getGuestConversionSurface, { tokenHash });
    return jsonResponse(result);
  }),
});

/** GET /guests/allowed — Get approved guest userIds (agent calls this). */
http.route({
  path: "/guests/allowed",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const deviceId = url.searchParams.get("deviceId") ?? undefined;

    const guestUserIds = await ctx.runQuery(api.guests.getGuestUserIds, { tokenHash, deviceId });
    return jsonResponse({ guestUserIds });
  }),
});

/** GET /guests/config — Get guest config(s). Query param: ?email=foo@bar.com (optional). */
http.route({
  path: "/guests/config",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const email = url.searchParams.get("email") || undefined;

    const configs = await ctx.runQuery(api.guests.getGuestConfig, { tokenHash, guestEmail: email });
    return jsonResponse({ configs });
  }),
});

/** POST /guests/config — Update guest config. Host only. */
http.route({
  path: "/guests/config",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    if (!body.email) return errorResponse("email is required");

    try {
      await ctx.runMutation(api.guests.updateGuestConfig, {
        tokenHash,
        guestEmail: body.email,
        scope:
          body.scope === "full" || body.scope === "feedback-only" || body.scope === "sdk-project"
            ? body.scope
            : undefined,
        dailyTokenLimit: body.dailyTokenLimit,
        allowedRunners: body.allowedRunners,
        usageMode: body.usageMode,
        shareAllDevices: body.shareAllDevices,
        deviceIds: body.deviceIds,
        shareAllMachines: body.shareAllMachines,
        machineIds: body.machineIds,
        resourcePreset: body.resourcePreset,
        useHostApiKeys: body.useHostApiKeys,
        allowGuestProvidedApiKeys: body.allowGuestProvidedApiKeys,
        allowDesktopControl: body.allowDesktopControl,
        allowBrowserControl: body.allowBrowserControl,
        allowTunnelForward: body.allowTunnelForward,
        requireIsolation: body.requireIsolation,
        cpuLimitPercent: body.cpuLimitPercent,
        ramLimitMb: body.ramLimitMb,
        priorityMode: body.priorityMode,
        schedule: body.schedule,
        allowedProjects: Array.isArray(body.allowedProjects) ? body.allowedProjects.map(String) : undefined,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to update guest config", 400);
    }
  }),
});

/** POST /guests/usage — Record guest usage (agent reports). */
http.route({
  path: "/guests/usage",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const body = await request.json();
    if (!body.guestUserId || !body.secondsUsed) {
      return errorResponse("guestUserId and secondsUsed are required");
    }

    try {
      await ctx.runMutation(api.guests.recordGuestUsage, {
        tokenHash,
        guestUserId: body.guestUserId,
        secondsUsed: body.secondsUsed,
        date: body.date || new Date().toISOString().slice(0, 10),
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to record usage", 400);
    }
  }),
});

/** GET /guests/usage — Get guest usage. Query param: ?date=2026-04-06 (optional). */
http.route({
  path: "/guests/usage",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) {
      return errorResponse("Unauthorized", 401);
    }
    const token = authHeader.slice(7);
    const tokenHash = await sha256Hex(token);

    const url = new URL(request.url);
    const date = url.searchParams.get("date") || undefined;

    const usage = await ctx.runQuery(api.guests.getGuestUsage, { tokenHash, date });
    return jsonResponse({ usage });
  }),
});

// ── Connections (social graph) ──────────────────────────────────────

const connectionsApi = (api as any).connections;

/** Helper: extract bearer → tokenHash, or null. */
async function bearerHash(request: Request): Promise<string | null> {
  const authHeader = request.headers.get("Authorization");
  if (!authHeader?.startsWith("Bearer ")) return null;
  return await sha256Hex(authHeader.slice(7));
}

/** POST /connections/request — send (or auto-accept) a connection request. */
http.route({
  path: "/connections/request",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    try {
      const result = await ctx.runMutation(connectionsApi.request, {
        tokenHash,
        peerUserId: typeof body.peerUserId === "string" ? body.peerUserId : undefined,
        peerEmail: typeof body.peerEmail === "string" ? body.peerEmail : undefined,
        nickname: typeof body.nickname === "string" ? body.nickname : undefined,
        source: typeof body.source === "string" ? body.source : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to send request", 400);
    }
  }),
});

/** POST /connections/accept — accept an incoming request. */
http.route({
  path: "/connections/accept",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.accept, { tokenHash, peerUserId: String(body.peerUserId) });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to accept", 400);
    }
  }),
});

/** POST /connections/remove — decline / cancel / unfriend. */
http.route({
  path: "/connections/remove",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.remove, { tokenHash, peerUserId: String(body.peerUserId) });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to remove", 400);
    }
  }),
});

/** POST /connections/block — block a peer. */
http.route({
  path: "/connections/block",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.block, { tokenHash, peerUserId: String(body.peerUserId) });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to block", 400);
    }
  }),
});

/** POST /connections/unblock — unblock a peer. */
http.route({
  path: "/connections/unblock",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.unblock, { tokenHash, peerUserId: String(body.peerUserId) });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to unblock", 400);
    }
  }),
});

/** POST /connections/nickname — set a private nickname. */
http.route({
  path: "/connections/nickname",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.peerUserId) return errorResponse("peerUserId is required");
    try {
      await ctx.runMutation(connectionsApi.setNickname, {
        tokenHash,
        peerUserId: String(body.peerUserId),
        nickname: typeof body.nickname === "string" ? body.nickname : "",
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to set nickname", 400);
    }
  }),
});

/** GET /connections/list?status= — list my connections. */
http.route({
  path: "/connections/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const url = new URL(request.url);
    const status = url.searchParams.get("status");
    const result = await ctx.runQuery(connectionsApi.list, {
      tokenHash,
      status: status === "accepted" || status === "pending" || status === "blocked" ? status : undefined,
    });
    return jsonResponse(result);
  }),
});

/** GET /connections/search?query= — find a user by userId or email. */
http.route({
  path: "/connections/search",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const url = new URL(request.url);
    const query = url.searchParams.get("query") ?? "";
    if (!query) return errorResponse("query is required");
    const result = await ctx.runQuery(connectionsApi.search, { tokenHash, query });
    if (!result) return errorResponse("User not found", 404);
    return jsonResponse(result);
  }),
});

/** GET /connections/suggested — people you already collaborate with. */
http.route({
  path: "/connections/suggested",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const suggestions = await ctx.runQuery(connectionsApi.suggested, { tokenHash });
    return jsonResponse({ suggestions });
  }),
});

// ── Project shares ──────────────────────────────────────────────────

const projectSharesApi = (api as any).projectShares;

/** POST /project-shares/create — create a shared project. Owner. */
http.route({
  path: "/project-shares/create",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    const hostKind = body.hostKind === "managed-cloud" ? "managed-cloud" : "owner-device";
    try {
      const result = await ctx.runMutation(projectSharesApi.create, {
        tokenHash,
        slug: String(body.slug ?? ""),
        repoUrl: String(body.repoUrl ?? ""),
        defaultBranch: typeof body.defaultBranch === "string" ? body.defaultBranch : undefined,
        hostKind,
        hostDeviceId: typeof body.hostDeviceId === "string" ? body.hostDeviceId : undefined,
        hostMachineId: typeof body.hostMachineId === "string" ? body.hostMachineId : undefined,
        payer: body.payer === "invitee" ? "invitee" : body.payer === "owner" ? "owner" : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to create project", 400);
    }
  }),
});

/** POST /project-shares/invite — invite a person to a project. Owner. */
http.route({
  path: "/project-shares/invite",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareId) return errorResponse("shareId is required");
    const role = body.role === "dev" || body.role === "normie" || body.role === "viewer" ? body.role : undefined;
    try {
      const result = await ctx.runMutation(projectSharesApi.invite, {
        tokenHash,
        shareId: body.shareId,
        peerUserId: typeof body.peerUserId === "string" ? body.peerUserId : undefined,
        peerEmail: typeof body.peerEmail === "string" ? body.peerEmail : undefined,
        role,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to invite", 400);
    }
  }),
});

/** POST /project-shares/accept — accept a project invite by code. */
http.route({
  path: "/project-shares/accept",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareCode) return errorResponse("shareCode is required");
    try {
      const result = await ctx.runMutation(projectSharesApi.accept, {
        tokenHash,
        shareCode: String(body.shareCode),
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to accept", 400);
    }
  }),
});

/** GET /project-shares/list — my owned + joined projects. */
http.route({
  path: "/project-shares/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const result = await ctx.runQuery(projectSharesApi.listMine, { tokenHash });
    return jsonResponse(result);
  }),
});

/** GET /project-shares/find-by-code?code= — preview before accepting. */
http.route({
  path: "/project-shares/find-by-code",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const url = new URL(request.url);
    const code = url.searchParams.get("code") ?? "";
    if (!code) return errorResponse("code is required");
    const info = await ctx.runQuery(projectSharesApi.findByCode, { tokenHash, shareCode: code });
    if (!info) return errorResponse("Project not found", 404);
    return jsonResponse(info);
  }),
});

/** POST /project-shares/set-role — change a member's role. Owner. */
http.route({
  path: "/project-shares/set-role",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareId || !body.memberUserId) return errorResponse("shareId and memberUserId are required");
    const role = body.role === "dev" || body.role === "normie" || body.role === "viewer" ? body.role : null;
    if (!role) return errorResponse("valid role is required");
    try {
      await ctx.runMutation(projectSharesApi.setRole, {
        tokenHash,
        shareId: body.shareId,
        memberUserId: String(body.memberUserId),
        role,
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to set role", 400);
    }
  }),
});

/** POST /project-shares/revoke-member — remove a member. Owner. */
http.route({
  path: "/project-shares/revoke-member",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareId || !body.memberUserId) return errorResponse("shareId and memberUserId are required");
    try {
      await ctx.runMutation(projectSharesApi.revokeMember, {
        tokenHash,
        shareId: body.shareId,
        memberUserId: String(body.memberUserId),
      });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to revoke member", 400);
    }
  }),
});

/** POST /project-shares/archive — archive a project. Owner. */
http.route({
  path: "/project-shares/archive",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json();
    if (!body.shareId) return errorResponse("shareId is required");
    try {
      await ctx.runMutation(projectSharesApi.archive, { tokenHash, shareId: body.shareId });
      return jsonResponse({ ok: true });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to archive", 400);
    }
  }),
});

// ── Project artifacts ───────────────────────────────────────────────

const projectArtifactsApi = (api as any).projectArtifacts;

/** POST /project-artifacts — create metadata for a shareable project output. */
http.route({
  path: "/project-artifacts",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => null);
    if (!body) return errorResponse("invalid json", 400);
    try {
      const result = await ctx.runMutation(projectArtifactsApi.create, {
        tokenHash,
        shareId: typeof body.shareId === "string" ? body.shareId : undefined,
        projectSlug: typeof body.projectSlug === "string" ? body.projectSlug : undefined,
        taskId: typeof body.taskId === "string" ? body.taskId : undefined,
        localTaskId: typeof body.localTaskId === "string" ? body.localTaskId : undefined,
        kind: typeof body.kind === "string" ? body.kind : undefined,
        title: String(body.title ?? ""),
        description: typeof body.description === "string" ? body.description : undefined,
        provider: typeof body.provider === "string" ? body.provider : undefined,
        storageId: typeof body.storageId === "string" ? body.storageId : undefined,
        uploadIntentId: typeof body.uploadIntentId === "string" ? body.uploadIntentId : undefined,
        objectKey: typeof body.objectKey === "string" ? body.objectKey : undefined,
        url: typeof body.url === "string" ? body.url : undefined,
        contentType: typeof body.contentType === "string" ? body.contentType : undefined,
        sizeBytes: typeof body.sizeBytes === "number" ? body.sizeBytes : undefined,
        checksum: typeof body.checksum === "string" ? body.checksum : undefined,
        visibility: body.visibility === "private" || body.visibility === "project" || body.visibility === "public-link"
          ? body.visibility
          : undefined,
        shareTtlMs: typeof body.shareTtlMs === "number" ? body.shareTtlMs : undefined,
        expiresAt: typeof body.expiresAt === "number" ? body.expiresAt : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to create artifact", 400);
    }
  }),
});

/** POST /project-artifacts/upload-url — mint a Convex file upload URL for a project artifact. */
http.route({
  path: "/project-artifacts/upload-url",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => ({}));
    try {
      const result = await ctx.runMutation(projectArtifactsApi.generateUploadUrl, {
        tokenHash,
        shareId: typeof body.shareId === "string" ? body.shareId : undefined,
        projectSlug: typeof body.projectSlug === "string" ? body.projectSlug : undefined,
        sizeBytes: typeof body.sizeBytes === "number" ? body.sizeBytes : 0,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to create upload URL", 400);
    }
  }),
});

/** GET /project-artifacts?projectSlug=&shareId=&kind= — list project artifacts. */
http.route({
  path: "/project-artifacts",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const url = new URL(request.url);
    try {
      const result = await ctx.runQuery(projectArtifactsApi.list, {
        tokenHash,
        shareId: url.searchParams.get("shareId") || undefined,
        projectSlug: url.searchParams.get("projectSlug") || undefined,
        kind: url.searchParams.get("kind") || undefined,
        limit: Number(url.searchParams.get("limit") || "") || undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to list artifacts", 400);
    }
  }),
});

/** GET /project-artifacts/usage?projectSlug=&shareId= — storage/count usage for a project and its owner. */
http.route({
  path: "/project-artifacts/usage",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const url = new URL(request.url);
    try {
      const result = await ctx.runQuery(projectArtifactsApi.usage, {
        tokenHash,
        shareId: url.searchParams.get("shareId") || undefined,
        projectSlug: url.searchParams.get("projectSlug") || undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to read artifact usage", 400);
    }
  }),
});

/** POST /project-artifacts/cleanup — owner-only expired artifact retention cleanup. */
http.route({
  path: "/project-artifacts/cleanup",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => ({}));
    try {
      const result = await ctx.runMutation(projectArtifactsApi.cleanupExpired, {
        tokenHash,
        shareId: typeof body.shareId === "string" ? body.shareId : undefined,
        projectSlug: typeof body.projectSlug === "string" ? body.projectSlug : undefined,
        limit: typeof body.limit === "number" ? body.limit : undefined,
        deleteStorage: typeof body.deleteStorage === "boolean" ? body.deleteStorage : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to clean up artifacts", 400);
    }
  }),
});

/** POST /project-artifacts/hide — hide an artifact from project/public views. */
http.route({
  path: "/project-artifacts/hide",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => null);
    if (!body?.artifactId) return errorResponse("artifactId is required", 400);
    try {
      const result = await ctx.runMutation(projectArtifactsApi.hide, {
        tokenHash,
        artifactId: body.artifactId,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to hide artifact", 400);
    }
  }),
});

/** GET /project-artifacts/public?token= — public artifact metadata by share token. */
http.route({
  path: "/project-artifacts/public",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const shareToken = url.searchParams.get("token") || "";
    if (!shareToken) return errorResponse("token is required", 400);
    const result = await ctx.runQuery(projectArtifactsApi.publicByToken, { shareToken });
    if (!result) return errorResponse("Artifact not found", 404);
    await ctx.runMutation(projectArtifactsApi.touchPublic, { shareToken }).catch(() => null);
    return jsonResponse(result);
  }),
});

// ── Feedback work queue ─────────────────────────────────────────────

const feedbackWorkItemsApi = (api as any).feedbackWorkItems;

/** POST /feedback-work-items — Feedback SDK creates owner-reviewed task/issue work. */
http.route({
  path: "/feedback-work-items",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const sdkTokenHash = await bearerHash(request);
    if (!sdkTokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => null);
    if (!body) return errorResponse("invalid json", 400);
    try {
      const result = await ctx.runMutation(feedbackWorkItemsApi.createFromSdk, {
        sdkTokenHash,
        shareId: typeof body.shareId === "string" ? body.shareId : undefined,
        projectSlug: typeof body.projectSlug === "string" ? body.projectSlug : undefined,
        title: String(body.title ?? ""),
        body: String(body.body ?? ""),
        kind: typeof body.kind === "string" ? body.kind : undefined,
        priority: typeof body.priority === "string" ? body.priority : undefined,
        component: typeof body.component === "string" ? body.component : undefined,
        appVersion: typeof body.appVersion === "string" ? body.appVersion : undefined,
        platform: typeof body.platform === "string" ? body.platform : undefined,
        artifactIds: Array.isArray(body.artifactIds) ? body.artifactIds.filter((id: unknown) => typeof id === "string") : undefined,
        attachmentUrls: Array.isArray(body.attachmentUrls) ? body.attachmentUrls.filter((url: unknown) => typeof url === "string") : undefined,
        target: typeof body.target === "string" ? body.target : undefined,
        ttlMs: typeof body.ttlMs === "number" ? body.ttlMs : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to queue feedback work", 400);
    }
  }),
});

/** GET /feedback-work-items — owner list, or requester "mine" list. */
http.route({
  path: "/feedback-work-items",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const url = new URL(request.url);
    try {
      const result = await ctx.runQuery(feedbackWorkItemsApi.list, {
        tokenHash,
        shareId: url.searchParams.get("shareId") || undefined,
        projectSlug: url.searchParams.get("projectSlug") || undefined,
        scope: url.searchParams.get("scope") === "mine" ? "mine" : "owned",
        status: url.searchParams.get("status") || undefined,
        limit: Number(url.searchParams.get("limit") || "") || undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to list feedback work", 400);
    }
  }),
});

/** POST /feedback-work-items/claim — owner worker claims next queued feedback item. */
http.route({
  path: "/feedback-work-items/claim",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => ({}));
    try {
      const result = await ctx.runMutation(feedbackWorkItemsApi.claimNext, {
        tokenHash,
        shareId: typeof body.shareId === "string" ? body.shareId : undefined,
        projectSlug: typeof body.projectSlug === "string" ? body.projectSlug : undefined,
        workerId: typeof body.workerId === "string" ? body.workerId : undefined,
      });
      return jsonResponse(result ?? { ok: true, item: null });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to claim feedback work", 400);
    }
  }),
});

/** POST /feedback-work-items/status — owner records task/issue/branch outcome. */
http.route({
  path: "/feedback-work-items/status",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => null);
    if (!body?.itemId) return errorResponse("itemId is required", 400);
    try {
      const result = await ctx.runMutation(feedbackWorkItemsApi.update, {
        tokenHash,
        itemId: body.itemId,
        status: body.status,
        taskId: typeof body.taskId === "string" ? body.taskId : undefined,
        issueUrl: typeof body.issueUrl === "string" ? body.issueUrl : undefined,
        branch: typeof body.branch === "string" ? body.branch : undefined,
        reason: typeof body.reason === "string" ? body.reason : undefined,
        lastError: typeof body.lastError === "string" ? body.lastError : undefined,
        workerId: typeof body.workerId === "string" ? body.workerId : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to update feedback work", 400);
    }
  }),
});

/** POST /feedback-work-items/route — owner changes queue target for local workers. */
http.route({
  path: "/feedback-work-items/route",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => null);
    if (!body?.itemId) return errorResponse("itemId is required", 400);
    try {
      const result = await ctx.runMutation(feedbackWorkItemsApi.route, {
        tokenHash,
        itemId: body.itemId,
        target: body.target,
        reason: typeof body.reason === "string" ? body.reason : undefined,
        workerId: typeof body.workerId === "string" ? body.workerId : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to route feedback work", 400);
    }
  }),
});

/** POST /feedback-work-items/queue-relay-source — owner queues feedback as relay branch work. */
http.route({
  path: "/feedback-work-items/queue-relay-source",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const tokenHash = await bearerHash(request);
    if (!tokenHash) return errorResponse("Unauthorized", 401);
    const body = await request.json().catch(() => null);
    if (!body?.itemId) return errorResponse("itemId is required", 400);
    try {
      const result = await ctx.runMutation(feedbackWorkItemsApi.queueRelaySource, {
        tokenHash,
        itemId: body.itemId,
        branch: typeof body.branch === "string" ? body.branch : undefined,
        workerId: typeof body.workerId === "string" ? body.workerId : undefined,
        ttlMs: typeof body.ttlMs === "number" ? body.ttlMs : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to queue relay source work", 400);
    }
  }),
});

// ── Host-Share Leases ───────────────────────────────────────────────

const hostShareApi = (api as any).hostShare;

/** POST /host-share/create — Create a host-backed coding invite. */
http.route({
  path: "/host-share/create",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();

    try {
      const result = await ctx.runMutation(hostShareApi.createInvite, {
        tokenHash,
        guestEmail: typeof body.guestEmail === "string" ? body.guestEmail : undefined,
        guestUserId: typeof body.guestUserId === "string" ? body.guestUserId : undefined,
        label: typeof body.label === "string" ? body.label : undefined,
        hostDeviceId: typeof body.hostDeviceId === "string" ? body.hostDeviceId : undefined,
        inviteTtlMinutes: typeof body.inviteTtlMinutes === "number" ? body.inviteTtlMinutes : undefined,
        sessionTtlMinutes: typeof body.sessionTtlMinutes === "number" ? body.sessionTtlMinutes : undefined,
        idleTimeoutMinutes: typeof body.idleTimeoutMinutes === "number" ? body.idleTimeoutMinutes : undefined,
        toolingPreset: typeof body.toolingPreset === "string" ? body.toolingPreset : undefined,
        resourcePreset: typeof body.resourcePreset === "string" ? body.resourcePreset : undefined,
        allowInfra: typeof body.allowInfra === "boolean" ? body.allowInfra : undefined,
        allowTerminal: typeof body.allowTerminal === "boolean" ? body.allowTerminal : undefined,
        allowTunnel: typeof body.allowTunnel === "boolean" ? body.allowTunnel : undefined,
        useHostAgentTools: typeof body.useHostAgentTools === "boolean" ? body.useHostAgentTools : undefined,
        useHostInfra: typeof body.useHostInfra === "boolean" ? body.useHostInfra : undefined,
        allowedRunners: Array.isArray(body.allowedRunners) ? body.allowedRunners.map(String) : undefined,
        allowedProjects: Array.isArray(body.allowedProjects) ? body.allowedProjects.map(String) : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to create host-share invite", 400);
    }
  }),
});

/** GET /host-share/invite?code=XXXX — Preview a host-share invite. */
http.route({
  path: "/host-share/invite",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    const inviteCode = url.searchParams.get("code") ?? "";
    if (!inviteCode) return errorResponse("code is required");
    try {
      const invite = await ctx.runQuery(hostShareApi.findInviteByCode, { tokenHash, inviteCode });
      if (!invite) return errorResponse("Invite not found or expired", 404);
      return jsonResponse(invite);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to load host-share invite", 400);
    }
  }),
});

/** POST /host-share/join — Redeem a host-share invite by code. */
http.route({
  path: "/host-share/join",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (typeof body.code !== "string" || !body.code.trim()) return errorResponse("code is required");
    try {
      const result = await ctx.runMutation(hostShareApi.joinByCode, {
        tokenHash,
        inviteCode: body.code,
        guestDeviceId: typeof body.guestDeviceId === "string" ? body.guestDeviceId : undefined,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to join host-share session", 400);
    }
  }),
});

/** POST /host-share/revoke — Revoke a host-share invite by code. */
http.route({
  path: "/host-share/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (typeof body.code !== "string" || !body.code.trim()) return errorResponse("code is required");
    try {
      const result = await ctx.runMutation(hostShareApi.revokeInvite, {
        tokenHash,
        inviteCode: body.code,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to revoke host-share invite", 400);
    }
  }),
});

/** GET /host-share/list?role=host|guest — List invites for the caller. */
http.route({
  path: "/host-share/list",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const role = (new URL(request.url).searchParams.get("role") ?? "host") as "host" | "guest";
    const invites = await ctx.runQuery(hostShareApi.listInvites, { tokenHash, role });
    return jsonResponse({ invites });
  }),
});

/** GET /host-share/sessions?role=host|guest — List active sessions for the caller. */
http.route({
  path: "/host-share/sessions",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const role = (new URL(request.url).searchParams.get("role") ?? "host") as "host" | "guest";
    const sessions = await ctx.runQuery(hostShareApi.listSessions, { tokenHash, role });
    return jsonResponse({ sessions });
  }),
});

/** POST /host-share/end — End an active host-share session by session id. */
http.route({
  path: "/host-share/end",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (typeof body.sessionId !== "string" || !body.sessionId.trim()) {
      return errorResponse("sessionId is required");
    }
    try {
      const result = await ctx.runMutation(hostShareApi.endSession, {
        tokenHash,
        sessionId: body.sessionId,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to end host-share session", 400);
    }
  }),
});

/** GET /host-share/access?guestUserId=...&deviceId=... — Resolve active host-share access on this host/device. */
http.route({
  path: "/host-share/access",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    const guestUserId = url.searchParams.get("guestUserId") ?? "";
    const deviceId = url.searchParams.get("deviceId") ?? undefined;
    if (!guestUserId) return errorResponse("guestUserId is required");
    try {
      const access = await ctx.runQuery(hostShareApi.getAccessForHostDevice, {
        tokenHash,
        guestUserId,
        deviceId,
      });
      if (!access) return jsonResponse({ access: null });
      return jsonResponse({ access });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to resolve host-share access", 400);
    }
  }),
});

/** POST /host-share/touch — Refresh last-activity timestamp for an active session. */
http.route({
  path: "/host-share/touch",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json();
    if (typeof body.sessionId !== "string" || !body.sessionId.trim()) return errorResponse("sessionId is required");
    try {
      const result = await ctx.runMutation(hostShareApi.touchSessionActivity, {
        tokenHash,
        sessionId: body.sessionId,
      });
      return jsonResponse(result);
    } catch (e: any) {
      return errorResponse(e.message || "Failed to touch host-share session", 400);
    }
  }),
});

/** GET /host-share/peer-access?hostUserId=...&deviceId=... — Resolve active host-share access on guest-owned agent for the host caller. */
http.route({
  path: "/host-share/peer-access",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const url = new URL(request.url);
    const hostUserId = url.searchParams.get("hostUserId") ?? "";
    const deviceId = url.searchParams.get("deviceId") ?? undefined;
    if (!hostUserId) return errorResponse("hostUserId is required");
    try {
      const access = await ctx.runQuery(hostShareApi.getAccessForGuestDevice, {
        tokenHash,
        hostUserId,
        deviceId,
      });
      if (!access) return jsonResponse({ access: null });
      return jsonResponse({ access });
    } catch (e: any) {
      return errorResponse(e.message || "Failed to resolve host-share peer access", 400);
    }
  }),
});

// ─── Machine-side TLS reconciler endpoints ─────────────────────────
//
// Called by /usr/local/bin/yaver-tls-reconciler (installed by the
// cloudMachines.provision cloud-init). Auth is a long-lived per-machine
// token whose SHA-256 hash lives on cloudMachines.machineTokenHash.

async function authenticateMachineRequest(
  ctx: { runQuery: (q: any, args: any) => Promise<any> },
  request: Request,
  machineIdRaw: string | null,
): Promise<{ ok: true; machine: any } | { ok: false; status: number; error: string }> {
  const token = request.headers.get("x-machine-token");
  if (!token) return { ok: false, status: 401, error: "Missing X-Machine-Token" };
  if (!machineIdRaw) return { ok: false, status: 400, error: "Missing machineId" };
  let machine: any;
  try {
    machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId: machineIdRaw as any });
  } catch {
    return { ok: false, status: 400, error: "Invalid machineId" };
  }
  if (!machine) return { ok: false, status: 404, error: "Unknown machine" };
  if (!machine.machineTokenHash) return { ok: false, status: 409, error: "Machine not yet provisioned" };
  const hash = await sha256Hex(token);
  if (hash !== machine.machineTokenHash) {
    return { ok: false, status: 403, error: "Bad machine token" };
  }
  return { ok: true, machine };
}

/** GET /machine/pending-tls?machineId=... — list verified userDomains routed to this machine. */
http.route({
  path: "/machine/pending-tls",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const auth = await authenticateMachineRequest(ctx, request, url.searchParams.get("machineId"));
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    const rows = await ctx.runQuery(internal.userDomains.listPendingTLSForMachine, {
      machineId: auth.machine._id,
    });
    return jsonResponse({
      domains: rows.map((r: any) => ({ domain: r.domain, domainId: r._id.toString() })),
    });
  }),
});

/** POST /machine/phase — box cloud-init reports its onboarding phase
 *  (installing-docker / pulling-image / registering) so web/mobile
 *  show a real progress bar. Machine-token authed; privacy-safe
 *  (label + percent only). project_managed_cloud_onboarding_gap. */
http.route({
  path: "/machine/phase",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    // Accept query params (cloud-init posts these — no JSON body, to
    // keep the runcmd YAML list-form quote-safe) OR a JSON body.
    const qs = new URL(request.url).searchParams;
    const body = await request.json().catch(() => ({} as any));
    const machineId = qs.get("machineId") ?? body.machineId ?? null;
    const auth = await authenticateMachineRequest(ctx, request, machineId);
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    const phase = String(qs.get("phase") ?? body.phase ?? "").trim();
    // Default progress per phase so the box only has to send a label.
    const PCT: Record<string, number> = {
      "installing-docker": 45,
      "pulling-image": 60,
      "starting-agent": 75,
      "registering": 85,
    };
    // "error" is the box's own failure beacon (end-of-cloud-init health
    // probe failed). It carries a short curated label, no progress.
    if (phase === "error") {
      const errLabel = String(qs.get("error") ?? body.error ?? "")
        .trim()
        .slice(0, 200);
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId: auth.machine._id,
        phase: "error",
        error: errLabel || "provisioning failed",
      });
      return jsonResponse({ ok: true });
    }
    if (!phase || !(phase in PCT)) {
      return errorResponse("Unknown or missing phase", 400);
    }
    const progress =
      typeof body.progress === "number" ? body.progress : PCT[phase];
    await ctx.runMutation(internal.cloudMachines.setPhase, {
      machineId: auth.machine._id,
      phase,
      progress,
    });
    return jsonResponse({ ok: true });
  }),
});

/** POST /machine/activity — the box agent pings this when it does real
 *  work (task run / exec / interactive session) so idle auto-shutdown
 *  (cloudLifecycle.idleSweep) doesn't pause a box that's actually in use.
 *  Machine-token authed (same as /machine/phase). Throttled server-side
 *  (touchActivity only writes when the prior stamp is >60s old). */
http.route({
  path: "/machine/activity",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const qs = new URL(request.url).searchParams;
    const body = await request.json().catch(() => ({} as any));
    const machineId = qs.get("machineId") ?? body.machineId ?? null;
    const auth = await authenticateMachineRequest(ctx, request, machineId);
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    await ctx.runMutation(internal.cloudMachines.touchActivity, {
      machineId: auth.machine._id,
    });
    return jsonResponse({ ok: true });
  }),
});

/** POST /machine/park-self — a MANAGED box asks to be scaled to zero because
 *  its OWN agent (machine_activity.go) decided it has been idle past the
 *  grace-confirmed threshold. Machine-token authed: the box parks itself.
 *
 *  This is the cost-free replacement for the removed idle-sweep Convex cron
 *  (crons.ts) — no perpetual server-side polling; the box that isn't running
 *  pays nothing to decide it should stop, and the server only does work at the
 *  instant a box parks. Auto-off is ON by default for managed boxes;
 *  YAVER_CLOUD_IDLE_DISABLE is the operator emergency brake. pauseMachine is
 *  itself HCLOUD_TOKEN-fail-closed and snapshots BEFORE deleting (a failed
 *  snapshot aborts the delete — never lose an unrecoverable box). */
http.route({
  path: "/machine/park-self",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const machineId = new URL(request.url).searchParams.get("machineId");
    const auth = await authenticateMachineRequest(ctx, request, machineId);
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    // Same default-on guard as the agent. YAVER_CLOUD_IDLE_DISABLE is the
    // operator emergency brake; HCLOUD_TOKEN/pauseMachine still fail-closed.
    if (!managedCloudIdleAutoOffEnabled()) {
      return jsonResponse({ ok: false, skipped: "idle auto-off disabled (YAVER_CLOUD_IDLE_DISABLE=true)" });
    }
    await ctx.scheduler.runAfter(0, internal.cloudLifecycle.pauseMachine, {
      machineId: auth.machine._id,
    });
    return jsonResponse({ ok: true, parking: true });
  }),
});

/** POST /machine/tls-issued — reconciler reports a successful cert issue. */
http.route({
  path: "/machine/tls-issued",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({}));
    const auth = await authenticateMachineRequest(ctx, request, body.machineId ?? null);
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    const domain = String(body.domain ?? "").trim().toLowerCase();
    if (!domain) return errorResponse("Missing domain", 400);
    const row = await ctx.runQuery(internal.userDomains.getByDomain, { domain });
    if (!row) return errorResponse("Unknown domain", 404);
    if (row.targetId !== auth.machine._id.toString()) {
      return errorResponse("Domain not routed to this machine", 403);
    }
    await ctx.runMutation(internal.userDomains.markTLSIssued, { domainId: row._id });
    return jsonResponse({ ok: true });
  }),
});

/** POST /machine/tls-error — reconciler reports a certbot failure. */
http.route({
  path: "/machine/tls-error",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => ({}));
    const auth = await authenticateMachineRequest(ctx, request, body.machineId ?? null);
    if (!auth.ok) return errorResponse(auth.error, auth.status);
    const domain = String(body.domain ?? "").trim().toLowerCase();
    if (!domain) return errorResponse("Missing domain", 400);
    const row = await ctx.runQuery(internal.userDomains.getByDomain, { domain });
    if (!row) return errorResponse("Unknown domain", 404);
    if (row.targetId !== auth.machine._id.toString()) {
      return errorResponse("Domain not routed to this machine", 403);
    }
    await ctx.runMutation(internal.userDomains.setStatus, {
      domainId: row._id,
      status: "error",
      errorMessage: String(body.error ?? "TLS issue failed"),
    });
    return jsonResponse({ ok: true });
  }),
});

// ── Yaver Gateway (inference arbitrage) trust boundary ──────────────
// The gateway (Cloudflare Worker / relay) holds upstream provider keys
// server-side and resells per-token inference into the prepaid wallet.
// Two routes: /gateway/authorize (user bearer → userId + balance, so the
// gateway never sees a key — "the wallet IS the key") and /gateway/meter
// (gateway-secret → debit). Both fail-closed pre-launch. See
// docs/yaver-gateway-spec.md and managedMeter.ts.

/** POST /gateway/authorize — resolve a user's bearer session token to
 *  {userId, balanceCents, allow} before the gateway streams inference.
 *  Inference floor = any positive balance; per-request + per-hour
 *  ceilings (gateway-side) bound a single burst. No snapshot reserve
 *  (that's a compute-box concern, not tokens). */
http.route({
  path: "/gateway/authorize",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    // Two ways to authenticate to the gateway:
    //   1. A scoped gateway token (operator-minted, inference-only) — the
    //      safe key path for free-tier tenants. Resolves to a userId but
    //      grants NOTHING outside the gateway.
    //   2. A normal Yaver session token (owner/phone) — existing behavior,
    //      still gated by cloudAccessAllowed.
    const authHeader = request.headers.get("Authorization") ?? "";
    const bearer = authHeader.startsWith("Bearer ") ? authHeader.slice(7) : "";
    if (!bearer) return errorResponse("Unauthorized", 401);

    let userId: string | null = null;
    let scoped = false;
    const tokHash = await sha256Hex(bearer);
    const gw = await ctx.runQuery(internal.gatewayTokens.resolveInternal, { tokenHash: tokHash });
    if (gw) {
      userId = gw.userId;
      scoped = true;
    } else {
      const session = await authenticateRequest(ctx, request);
      if (!session) return errorResponse("Unauthorized", 401);
      // Session path keeps the private-preview gate; the scoped-token path
      // does not (the operator minting the token IS the authorization).
      if (!cloudAccessAllowed(session.email, session.userDocId)) {
        return errorResponse("Yaver Premium is private-preview only on this account", 403);
      }
      userId = session.userDocId as any;
    }

    const wallet = await ctx.runQuery(internal.cloudLifecycle.getWallet, {
      userId: userId as any,
    });
    // Per-user limits — operator-set, user-immutable (gatewayPolicy).
    const pol = await ctx.runQuery(internal.gatewayPolicy.getAuthContext, {
      userId: userId as any,
    });

    const dailyExceeded =
      (pol.dailyCapCents ?? 0) > 0 && pol.spentTodayCents >= (pol.dailyCapCents ?? 0);
    const allow = pol.enabled && wallet.balanceCents > 0 && !dailyExceeded;

    // Inference is meaningful activity — keep the user's managed box warm
    // so idle auto-shutdown doesn't pause it mid-session. Fire-and-forget
    // (no added latency); throttled write inside touchActivityForUser.
    if (allow) {
      await ctx.scheduler.runAfter(0, internal.cloudMachines.touchActivityForUser, {
        userId: userId as any,
      });
    }

    return jsonResponse({
      ok: true,
      userId,
      scoped,
      balanceCents: wallet.balanceCents,
      allow,
      // Reason helps the Worker/log distinguish a deny cause (non-secret).
      reason: !pol.enabled
        ? "disabled"
        : wallet.balanceCents <= 0
          ? "insufficient_balance"
          : dailyExceeded
            ? "daily_cap"
            : "ok",
      // Per-user ceilings the Worker enforces (0 = fall back to env default).
      limits: {
        maxTokensPerRequest: pol.maxTokensPerRequest ?? 0,
        maxCentsPerRequest: pol.maxCentsPerRequest ?? 0,
        hourlyCapCents: pol.hourlyCapCents ?? 0,
        dailyCapCents: pol.dailyCapCents ?? 0,
        spentTodayCents: pol.spentTodayCents,
      },
    });
  }),
});

// ── Yaver Gateway: operator-only management (policy + scoped tokens) ──
// All gated by isOwner (ownerAllowlist env). A tenant has NO route here —
// they cannot read/raise their own caps or mint themselves a token.

/** POST /gateway/policy/set — operator sets a user's limits. Body:
 *  { targetUserId, enabled?, dailyCapCents?, hourlyCapCents?,
 *    maxTokensPerRequest?, maxCentsPerRequest?, freeGrantCents?, note? } */
http.route({
  path: "/gateway/policy/set",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    let body: any = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    if (!body.targetUserId) return errorResponse("targetUserId required", 400);
    const res = await ctx.runMutation(internal.gatewayPolicy.setPolicyInternal, {
      userId: body.targetUserId as any,
      enabled: typeof body.enabled === "boolean" ? body.enabled : undefined,
      dailyCapCents: typeof body.dailyCapCents === "number" ? body.dailyCapCents : undefined,
      hourlyCapCents: typeof body.hourlyCapCents === "number" ? body.hourlyCapCents : undefined,
      maxTokensPerRequest:
        typeof body.maxTokensPerRequest === "number" ? body.maxTokensPerRequest : undefined,
      maxCentsPerRequest:
        typeof body.maxCentsPerRequest === "number" ? body.maxCentsPerRequest : undefined,
      freeGrantCents: typeof body.freeGrantCents === "number" ? body.freeGrantCents : undefined,
      note: typeof body.note === "string" ? body.note : undefined,
      setBy: String(session.userDocId),
    });
    return jsonResponse(res);
  }),
});

/** GET /gateway/policy?userId= — operator reads a user's policy. */
http.route({
  path: "/gateway/policy",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    const target = new URL(request.url).searchParams.get("userId");
    if (!target) return errorResponse("userId required", 400);
    const policy = await ctx.runQuery(internal.gatewayPolicy.getPolicyInternal, {
      userId: target as any,
    });
    const tokens = await ctx.runQuery(internal.gatewayTokens.listForUserInternal, {
      userId: target as any,
    });
    return jsonResponse({ ok: true, policy, tokens });
  }),
});

/** POST /gateway/token/mint — operator mints a scoped inference token for a
 *  user. Returns the RAW token ONCE (only the hash is stored). Body:
 *  { targetUserId, label?, expiresAt? } */
http.route({
  path: "/gateway/token/mint",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    let body: any = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    if (!body.targetUserId) return errorResponse("targetUserId required", 400);
    // Raw token: 32 random bytes, prefixed so it's recognizable in logs/env.
    const buf = new Uint8Array(32);
    crypto.getRandomValues(buf);
    const raw =
      "ygw_" + Array.from(buf).map((b) => b.toString(16).padStart(2, "0")).join("");
    const tokenHash = await sha256Hex(raw);
    const res = await ctx.runMutation(internal.gatewayTokens.mintInternal, {
      userId: body.targetUserId as any,
      tokenHash,
      scope: "inference",
      label: typeof body.label === "string" ? body.label : undefined,
      createdBy: String(session.userDocId),
      expiresAt: typeof body.expiresAt === "number" ? body.expiresAt : undefined,
    });
    // raw is returned ONCE — the operator stores it (it's the OPENAI_API_KEY
    // they bake into the tenant's runner). It is never retrievable again.
    return jsonResponse({ ...res, token: raw });
  }),
});

/** POST /gateway/token/revoke — operator revokes one token by id. */
http.route({
  path: "/gateway/token/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    let body: any = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    if (!body.tokenId) return errorResponse("tokenId required", 400);
    const res = await ctx.runMutation(internal.gatewayTokens.revokeInternal, {
      tokenId: body.tokenId as any,
    });
    return jsonResponse(res);
  }),
});

/** POST /gateway/token/rotate — key rotation as a protection mechanism:
 *  revoke ALL of a user's existing tokens and mint a fresh one. Body:
 *  { targetUserId, label? } → returns the new raw token once. */
http.route({
  path: "/gateway/token/rotate",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const session = await authenticateRequest(ctx, request);
    if (!session) return errorResponse("Unauthorized", 401);
    if (!isOwner(session.email, session.userDocId)) return errorResponse("Operator only", 403);
    let body: any = {};
    try {
      body = await request.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    if (!body.targetUserId) return errorResponse("targetUserId required", 400);
    await ctx.runMutation(internal.gatewayTokens.revokeAllForUserInternal, {
      userId: body.targetUserId as any,
    });
    const buf = new Uint8Array(32);
    crypto.getRandomValues(buf);
    const raw =
      "ygw_" + Array.from(buf).map((b) => b.toString(16).padStart(2, "0")).join("");
    const tokenHash = await sha256Hex(raw);
    const res = await ctx.runMutation(internal.gatewayTokens.mintInternal, {
      userId: body.targetUserId as any,
      tokenHash,
      scope: "inference",
      label: typeof body.label === "string" ? body.label : "rotated",
      createdBy: String(session.userDocId),
    });
    return jsonResponse({ ...res, token: raw, rotated: true });
  }),
});

/** POST /gateway/meter — gateway-authenticated (GATEWAY_SHARED_SECRET).
 *  The gateway reports RAW upstream token cost (providerCostCents); Convex
 *  applies markup(kind) and debits the wallet via recordManagedUsage. The
 *  body asserts an arbitrary userId + cost, so it must be gateway-secret
 *  authed, never user-bearer. Body: { userId, kind, provider, model, unit,
 *  quantity, providerCostCents, ref? }. */
http.route({
  path: "/gateway/meter",
  method: "POST",
  handler: httpAction(async (ctx, req) => {
    const authHeader = req.headers.get("authorization") ?? "";
    const token = authHeader.startsWith("Bearer ") ? authHeader.slice(7) : "";
    const check = await ctx.runAction(internal.gatewaySecret.verify, { token });
    if (!check.secretConfigured) return errorResponse("GATEWAY_SHARED_SECRET not set", 500);
    if (!check.ok) return errorResponse("Unauthorized", 401);
    let body: any;
    try {
      body = await req.json();
    } catch {
      return errorResponse("Bad JSON", 400);
    }
    const { userId, kind, provider, unit, quantity, providerCostCents, model, ref, dryRun } =
      body ?? {};
    if (!userId || typeof providerCostCents !== "number") {
      return errorResponse("Missing userId or providerCostCents", 400);
    }
    const result = await ctx.runMutation(internal.managedMeter.recordManagedUsage, {
      userId: userId as any,
      kind: String(kind ?? "inference"),
      provider: String(provider ?? "unknown"),
      unit: String(unit ?? "token"),
      quantity: Number(quantity ?? 0),
      providerCostCents,
      model: model ? String(model) : undefined,
      ref: ref ? String(ref) : undefined,
      // Gateway may force dryRun; otherwise inherit the global launch flag.
      dryRun:
        typeof dryRun === "boolean"
          ? dryRun
          : !parseBooleanEnv(process.env.YAVER_MANAGED_METER_LIVE, false),
    });
    return jsonResponse({ ok: true, ...result });
  }),
});

// External cron runner — Hetzner systemd timers POST { name } here instead
// of using Convex built-in crons. Bearer auth via CRON_TRIGGER_SECRET env
// (verified through internal.cronSecret.verify for httpAction-runtime parity).
const runCron = httpAction(async (ctx, req) => {
  const authHeader = req.headers.get("authorization") ?? "";
  const token = authHeader.startsWith("Bearer ") ? authHeader.slice(7) : "";
  const check = await ctx.runAction(internal.cronSecret.verify, { token });
  if (!check.secretConfigured) {
    return new Response("CRON_TRIGGER_SECRET not set", { status: 500 });
  }
  if (!check.ok) {
    return new Response("Unauthorized", { status: 401 });
  }
  let body: { name?: string };
  try {
    body = await req.json();
  } catch {
    return new Response("Bad JSON", { status: 400 });
  }
  const name = body.name;
  switch (name) {
    case "pruneAuthLogs":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneAuthLogs, {});
      break;
    case "pruneMobileStreamLogs":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneMobileStreamLogs, {});
      break;
    case "pruneDeveloperLogs":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneDeveloperLogs, {});
      break;
    case "pruneDeviceEvents":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneDeviceEvents, {});
      break;
    case "pruneExpiredSessions":
      await ctx.scheduler.runAfter(0, internal.cleanup.pruneExpiredSessions, {});
      break;
    case "cloudMeter":
      // Managed-cloud prepaid meter (P2). Same external-Hetzner-timer
      // pattern as the prune jobs (no Convex crons.interval, per
      // crons.ts). dryRun:true while in private preview so it never
      // drains a real wallet pre-launch; set YAVER_CLOUD_METER_LIVE=true
      // (Convex env, owner decision per project_business_model) to flip
      // it live — a one-env-flip go-live, no code change. The real
      // Hetzner spend is independently gated by HCLOUD_TOKEN inside
      // pause/resume — this only meters the wallet ledger.
      await ctx.scheduler.runAfter(0, internal.cloudLifecycle.meterTick, {
        intervalSeconds: 3600,
        dryRun: !parseBooleanEnv(process.env.YAVER_CLOUD_METER_LIVE, false),
      });
      break;
    case "sweepStalePendingClaims":
      // Daily sweep so unclaimed bootstrap-pending rows don't pile up
      // when a fresh box joins the relay and the user never visits
      // their dashboard. 24h is enough for a real claim while still
      // bounding the table.
      await ctx.scheduler.runAfter(0, internal.pendingDeviceClaims.sweepStale, {});
      break;
    case "cloudIdleSweep":
      // Idle auto-shutdown (P1.4): pause active managed boxes with no
      // meaningful activity past the threshold so we never bill Hetzner
      // hours nobody is using. Auto-off is default-on for managed boxes;
      // YAVER_CLOUD_IDLE_DISABLE is the emergency brake. The pause is
      // HCLOUD_TOKEN fail-closed and intentionally NOT tied to the wallet
      // meter dry-run flag: private-preview ledger simulation must never keep
      // a forgotten real server running. Schedule this on the
      // Hetzner cron timers like the others (every 10–15 min is plenty).
      await ctx.scheduler.runAfter(0, internal.cloudLifecycle.idleSweep, {
        enabled: managedCloudIdleAutoOffEnabled(),
        idleMinutes: Number(process.env.YAVER_CLOUD_IDLE_MINUTES) || 45,
        dryRun: false,
      });
      break;
    case "reconcileManagedSubscriptions":
      // RECOVERY: re-provision any active managed subscription with no
      // live box (paid → no cloud resource). Idempotent. Schedule it
      // on the Hetzner cron timers like the others (hourly is plenty).
      await ctx.scheduler.runAfter(0, internal.cloudMachines.reconcileSubscriptions, {});
      break;
    default:
      return new Response(`Unknown cron: ${name}`, { status: 404 });
  }
  return new Response(JSON.stringify({ ok: true, scheduled: name }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
});

http.route({ path: "/crons/run", method: "POST", handler: runCron });

// ── Org admin console (/admin/*) ─────────────────────────────────────
//
// Gate: a caller is an admin if (a) the request carries a valid Bearer
// session, AND (b) the session user's email/userId is in the env-var
// owner allowlist (ownerAllowlist.ts). The platformRole DB flag is the
// future authoritative source — see admin.ts comment. Every admin route
// goes through requireAdminRequest below so the gate lives in one place.

async function requireAdminRequest(
  ctx: { runQuery: (query: any, args: any) => Promise<any> },
  request: Request,
): Promise<
  | { ok: true; user: NonNullable<Awaited<ReturnType<typeof authenticateRequest>>> }
  | { ok: false; response: Response }
> {
  const user = await authenticateRequest(ctx, request);
  if (!user) {
    return { ok: false, response: errorResponse("Unauthorized", 401) };
  }

  // Three-source gate, evaluated in order:
  //   1) env-var owner allowlist (solo-dev bootstrap; ALWAYS allowed).
  //   2) users.platformRole === "admin" (post-promotion path).
  // The env-var path is permanent — it stays as the escape hatch so
  // the solo dev can never lock themselves out by misconfiguring a
  // policy. Schema-promoted admins are additionally subject to the
  // MFA gate below if org policy turns it on.
  const isAllowlistAdmin = isOwner(user.email, user.userId);
  let isSchemaAdmin = false;
  if (!isAllowlistAdmin) {
    try {
      const userDoc = await ctx.runQuery(internal.auth.getUserByDocId, {
        userDocId: user.userDocId,
      });
      isSchemaAdmin = userDoc?.platformRole === "admin";
    } catch {
      // getUserByDocId may not exist on every deployment until the
      // generated API regenerates — be permissive on lookup failure
      // so the env-var allowlist still works.
      isSchemaAdmin = false;
    }
  }
  if (!isAllowlistAdmin && !isSchemaAdmin) {
    return {
      ok: false,
      response: errorResponse(
        "Forbidden — caller is not a platform admin",
        403,
      ),
    };
  }

  // MFA-for-admins enforcement. Reads org policy; absence ⇒ disabled.
  // Solo-dev safety: env-var bootstrap admins are exempt unconditionally
  // (the policy could otherwise lock everyone out). Only schema-promoted
  // admins fall through to this check.
  if (!isAllowlistAdmin) {
    try {
      const policy = await ctx.runQuery(internal.admin.getOrgPolicy, {});
      if (policy?.requireMfaForAdmins) {
        const userDoc = await ctx.runQuery(internal.auth.getUserByDocId, {
          userDocId: user.userDocId,
        });
        if (!userDoc?.totpEnabled) {
          return {
            ok: false,
            response: errorResponse(
              "MFA required for platform admins — enroll TOTP in your account settings first.",
              403,
            ),
          };
        }
      }
    } catch {
      // Policy lookup failure should not lock out a schema admin who
      // is otherwise valid.
    }
  }

  return { ok: true, user };
}

http.route({
  path: "/admin/overview",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const [counts, recent] = await Promise.all([
      ctx.runQuery(internal.admin.dashboardCounts, {}),
      ctx.runQuery(internal.admin.recentAuditEvents, { limit: 5 }),
    ]);
    return jsonResponse({ ...counts, recent });
  }),
});

http.route({
  path: "/admin/devices",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const rows = await ctx.runQuery(internal.admin.fleetDevices, {});
    return jsonResponse({ rows });
  }),
});

http.route({
  path: "/admin/sessions",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const rows = await ctx.runQuery(internal.admin.activeSessionsForAdmin, {});
    return jsonResponse({ rows });
  }),
});

http.route({
  path: "/admin/mesh",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const data = await ctx.runQuery(internal.admin.fleetMesh, {});
    return jsonResponse(data);
  }),
});

http.route({
  path: "/admin/users",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const rows = await ctx.runQuery(internal.admin.allUsersForAdmin, {});
    return jsonResponse({ rows });
  }),
});

http.route({
  path: "/admin/audit",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const url = new URL(request.url);
    const params = url.searchParams;
    const numOrUndef = (key: string): number | undefined => {
      const v = params.get(key);
      if (v === null || v === "") return undefined;
      const n = Number(v);
      return Number.isFinite(n) ? n : undefined;
    };
    const strOrUndef = (key: string): string | undefined => {
      const v = params.get(key);
      return v === null || v === "" ? undefined : v;
    };
    const result = await ctx.runQuery(internal.admin.mergedAuditFeed, {
      limit: numOrUndef("limit") ?? 50,
      cursor: numOrUndef("cursor"),
      actorEmail: strOrUndef("actor"),
      eventType: strOrUndef("event"),
      sinceMs: numOrUndef("since"),
      untilMs: numOrUndef("until"),
    });
    return jsonResponse(result);
  }),
});

// Identity check for the layout role guard. Both gates are consulted
// — env-var allowlist (solo-dev bootstrap) and users.platformRole
// (post-promotion). The layout uses isAdmin to decide whether to
// render the admin chrome or redirect to /dashboard.
http.route({
  path: "/admin/identity",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const user = await authenticateRequest(ctx, request);
    if (!user) return errorResponse("Unauthorized", 401);
    let schemaAdmin = false;
    try {
      const doc = await ctx.runQuery(internal.auth.getUserByDocId, {
        userDocId: user.userDocId as any,
      });
      schemaAdmin = doc?.platformRole === "admin";
    } catch {}
    const allowlistAdmin = isOwner(user.email, user.userId);
    return jsonResponse({
      isAdmin: allowlistAdmin || schemaAdmin,
      via: allowlistAdmin ? "allowlist" : schemaAdmin ? "platformRole" : null,
      email: user.email,
      userId: user.userId,
    });
  }),
});

// CORS preflights for every /admin/* path. The dashboard fetches from
// CONVEX_URL with Authorization: Bearer, so the preflight has to
// answer for that header. The user dashboard already advertises the
// permissive header set, so we mirror it here.
http.route({
  pathPrefix: "/admin/",
  method: "OPTIONS",
  handler: httpAction(async () => {
    return new Response(null, {
      status: 204,
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
        "Access-Control-Allow-Headers": "Authorization, Content-Type",
        "Access-Control-Max-Age": "86400",
      },
    });
  }),
});

// ── Admin action POSTs ───────────────────────────────────────────────
//
// Every action POST goes through requireAdminRequest. Body is JSON;
// the caller's userDocId is passed to mutations so audit rows record
// who-did-what. Soft-fails (target already gone, etc.) return ok=true
// with a flag rather than a 404, so the UI can stay congruent.

async function parseJsonBody(req: Request): Promise<any> {
  try {
    const text = await req.text();
    if (!text) return {};
    return JSON.parse(text);
  } catch {
    return null;
  }
}

async function getCallerDocId(
  ctx: { runQuery: (q: any, args: any) => Promise<any> },
  request: Request,
): Promise<any | null> {
  // Convex `Id<"users">` is a branded string at the type level. The
  // generated API accepts it as-is when returned from a query, so we
  // pass it straight through with no runtime work — the `any` here
  // sidesteps the brand mismatch without losing safety (the mutation
  // arg schemas re-validate at the convex boundary).
  const authHeader = request.headers.get("Authorization");
  if (!authHeader?.startsWith("Bearer ")) return null;
  const tokenHash = await sha256Hex(authHeader.slice(7));
  return await ctx.runQuery(api.auth.getUserDocId, { tokenHash });
}

// ── Devices ──────────────────────────────────────────────────────────

http.route({
  path: "/admin/devices/rescue",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.deviceDocId || !body?.command) {
      return errorResponse("deviceDocId and command required", 400);
    }
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(internal.admin.queueAgentRescue, {
        deviceDocId: body.deviceDocId,
        command: body.command,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/devices/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.deviceDocId) return errorResponse("deviceDocId required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(internal.admin.revokeDevice, {
        deviceDocId: body.deviceDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── Sessions ─────────────────────────────────────────────────────────

http.route({
  path: "/admin/sessions/revoke",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.sessionDocId) {
      return errorResponse("sessionDocId required", 400);
    }
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(internal.admin.revokeSession, {
        sessionDocId: body.sessionDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── Users ────────────────────────────────────────────────────────────

http.route({
  path: "/admin/users/promote",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetEmail) return errorResponse("targetEmail required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(internal.admin.promoteToAdmin, {
        targetEmail: body.targetEmail,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/users/demote",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetDocId) return errorResponse("targetDocId required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(internal.admin.demoteFromAdmin, {
        targetDocId: body.targetDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/users/sign-out",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetDocId) return errorResponse("targetDocId required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(internal.admin.signOutUserAllSessions, {
        targetDocId: body.targetDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/users/export",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetDocId) return errorResponse("targetDocId required", 400);
    try {
      const bundle = await ctx.runQuery(internal.admin.exportUserBundleById, {
        targetDocId: body.targetDocId,
      });
      if (!bundle) return errorResponse("User not found", 404);
      return new Response(JSON.stringify(bundle, null, 2), {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          "Content-Disposition": `attachment; filename=\"yaver-user-${body.targetDocId}.json\"`,
          "Access-Control-Allow-Origin": "*",
        },
      });
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/users/delete",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.targetDocId) return errorResponse("targetDocId required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(internal.admin.deleteUserCascade, {
        targetDocId: body.targetDocId,
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── Org policy ───────────────────────────────────────────────────────

http.route({
  path: "/admin/policy",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const policy = await ctx.runQuery(internal.admin.getOrgPolicy, {});
    return jsonResponse({ policy });
  }),
});

http.route({
  path: "/admin/policy",
  method: "PUT",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body) return errorResponse("JSON body required", 400);
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(internal.admin.setOrgPolicy, {
        callerDocId,
        enforceRelay: body.enforceRelay,
        allowedRunners: body.allowedRunners,
        allowedProviders: body.allowedProviders,
        idleTimeoutMin: body.idleTimeoutMin,
        auditRetentionDays: body.auditRetentionDays,
        requireMfaForAdmins: body.requireMfaForAdmins,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── OIDC config ──────────────────────────────────────────────────────

// Public, no auth — used by the /auth sign-in page to decide whether
// to render the "Sign in with company SSO" button. Returns the SAFE
// shape (no secret, no internal endpoints), only what the button needs:
// {enabled, issuerUrl, label}. Returns {enabled:false} for the
// solo-dev / default case where no OIDC has been configured.
http.route({
  path: "/auth/oidc/info",
  method: "GET",
  handler: httpAction(async (ctx) => {
    const cfg = await ctx.runQuery(internal.admin.getOidcConfig, {});
    if (!cfg || !cfg.enabled) {
      return jsonResponse({ enabled: false });
    }
    let label = cfg.issuerUrl;
    try {
      label = new URL(cfg.issuerUrl).host;
    } catch {}
    return jsonResponse({ enabled: true, label });
  }),
});

http.route({
  path: "/admin/sso/oidc",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const cfg = await ctx.runQuery(internal.admin.getOidcConfig, {});
    return jsonResponse({ config: cfg });
  }),
});

/** Resolve an OIDC issuer's .well-known/openid-configuration. Returns
 *  the four endpoints we need + a normalized status. Used by both
 *  Test-connection and Save. */
async function discoverOidc(issuerUrl: string): Promise<{
  ok: boolean;
  status: string;
  endpoints?: {
    authorizationEndpoint: string;
    tokenEndpoint: string;
    userinfoEndpoint: string;
    jwksUri: string;
  };
}> {
  try {
    const trimmed = issuerUrl.trim().replace(/\/$/, "");
    const wellKnownUrl = `${trimmed}/.well-known/openid-configuration`;
    const res = await fetch(wellKnownUrl, {
      headers: { Accept: "application/json" },
      signal: AbortSignal.timeout(10_000),
    });
    if (!res.ok) {
      return { ok: false, status: `${wellKnownUrl} returned ${res.status}` };
    }
    const json: any = await res.json();
    const required = ["authorization_endpoint", "token_endpoint", "userinfo_endpoint", "jwks_uri"];
    for (const key of required) {
      if (!json[key] || typeof json[key] !== "string") {
        return { ok: false, status: `${wellKnownUrl} response missing ${key}` };
      }
    }
    return {
      ok: true,
      status: `OK — discovered ${json.issuer ?? trimmed}`,
      endpoints: {
        authorizationEndpoint: json.authorization_endpoint,
        tokenEndpoint: json.token_endpoint,
        userinfoEndpoint: json.userinfo_endpoint,
        jwksUri: json.jwks_uri,
      },
    };
  } catch (err: any) {
    return { ok: false, status: String(err?.message || err) };
  }
}

http.route({
  path: "/admin/sso/oidc/test",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.issuerUrl) return errorResponse("issuerUrl required", 400);
    const result = await discoverOidc(body.issuerUrl);
    return jsonResponse(result);
  }),
});

http.route({
  path: "/admin/sso/oidc",
  method: "PUT",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const body = await parseJsonBody(request);
    if (!body?.issuerUrl || !body?.clientId) {
      return errorResponse("issuerUrl and clientId required", 400);
    }
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    // Discover endpoints inside Save so the saved config is always
    // current. If discovery fails AND we have no prior discovery,
    // refuse — better to surface the bad config than save a useless
    // row.
    const discovery = await discoverOidc(body.issuerUrl);
    if (!discovery.ok) {
      const existing = await ctx.runQuery(internal.admin.getOidcConfig, {});
      if (!existing || !existing.authorizationEndpoint) {
        return errorResponse(
          `Discovery failed: ${discovery.status}. Fix the issuer URL or test from the Test button first.`,
          400,
        );
      }
    }
    try {
      const result = await ctx.runMutation(internal.admin.setOidcConfig, {
        callerDocId,
        enabled: body.enabled === true,
        issuerUrl: body.issuerUrl,
        clientId: body.clientId,
        clientSecret: body.clientSecret ?? "",
        tenant: body.tenant ?? undefined,
        discovered: discovery.ok ? discovery.endpoints : undefined,
      });
      return jsonResponse({ ...result, discovered: discovery.ok });
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

http.route({
  path: "/admin/sso/oidc",
  method: "DELETE",
  handler: httpAction(async (ctx, request) => {
    const gate = await requireAdminRequest(ctx, request);
    if (!gate.ok) return gate.response;
    const callerDocId = await getCallerDocId(ctx, request);
    if (!callerDocId) return errorResponse("Unauthorized", 401);
    try {
      const result = await ctx.runMutation(internal.admin.clearOidcConfig, {
        callerDocId,
      });
      return jsonResponse(result);
    } catch (err: any) {
      return errorResponse(String(err?.message || err), 400);
    }
  }),
});

// ── OIDC sign-in flow ────────────────────────────────────────────────
//
// The "company SSO" button on /auth → /auth/oidc/start (this server)
// → 302 to IdP authorize endpoint with PKCE + state. After the user
// authenticates the IdP redirects to /auth/oidc/callback (this
// server) which verifies state, exchanges code for tokens, fetches
// userinfo, enforces tenant restriction, upserts the user, mints a
// Yaver session, and redirects to the web client with the session
// token in the URL (same shape as the existing OAuth callbacks).

function base64Url(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function randomBase64Url(byteLen: number): string {
  const buf = new Uint8Array(byteLen);
  crypto.getRandomValues(buf);
  return base64Url(buf);
}

async function pkceChallenge(verifier: string): Promise<string> {
  const hash = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(verifier),
  );
  return base64Url(new Uint8Array(hash));
}

/** The web base URL the OIDC callback redirects back to with the
 *  session token. Configured via WEB_BASE_URL env; falls back to
 *  the request origin so a self-host deployment Just Works without
 *  setting an env var. */
function resolveWebBaseUrl(request: Request): string {
  const env = (process.env.WEB_BASE_URL || "").trim().replace(/\/$/, "");
  if (env) return env;
  try {
    const u = new URL(request.url);
    return `${u.protocol}//${u.host}`;
  } catch {
    return "";
  }
}

http.route({
  path: "/auth/oidc/start",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const cfg = await ctx.runQuery(internal.admin.getOidcConfig, {});
    if (!cfg || !cfg.enabled || !cfg.authorizationEndpoint) {
      return errorResponse(
        "OIDC is not configured on this deployment.",
        404,
      );
    }
    const url = new URL(request.url);
    const returnTo = url.searchParams.get("return_to") || undefined;

    const state = randomBase64Url(24);
    const verifier = randomBase64Url(48);
    const challenge = await pkceChallenge(verifier);
    const nonce = randomBase64Url(16);

    await ctx.runMutation(internal.admin.startOidcAttempt, {
      state,
      codeVerifier: verifier,
      nonce,
      returnTo,
    });

    const webBase = resolveWebBaseUrl(request);
    const callbackUrl = `${webBase}/auth/oidc/callback`;
    const params = new URLSearchParams({
      response_type: "code",
      client_id: cfg.clientId,
      redirect_uri: callbackUrl,
      scope: "openid email profile",
      state,
      nonce,
      code_challenge: challenge,
      code_challenge_method: "S256",
    });
    const authorizeUrl = `${cfg.authorizationEndpoint}?${params.toString()}`;
    return new Response(null, {
      status: 302,
      headers: { Location: authorizeUrl },
    });
  }),
});

http.route({
  path: "/auth/oidc/callback",
  method: "GET",
  handler: httpAction(async (ctx, request) => {
    const url = new URL(request.url);
    const code = url.searchParams.get("code");
    const state = url.searchParams.get("state");
    const errParam = url.searchParams.get("error");
    const webBase = resolveWebBaseUrl(request);

    function redirectToAuth(query: Record<string, string>) {
      const sp = new URLSearchParams(query);
      return new Response(null, {
        status: 302,
        headers: { Location: `${webBase}/auth?${sp.toString()}` },
      });
    }

    if (errParam) return redirectToAuth({ oidc_error: errParam });
    if (!code || !state) return redirectToAuth({ oidc_error: "missing_params" });

    const cfg = await ctx.runQuery(internal.admin.getOidcConfigRaw, {});
    if (!cfg || !cfg.enabled || !cfg.tokenEndpoint || !cfg.userinfoEndpoint) {
      return redirectToAuth({ oidc_error: "config_missing" });
    }

    const attempt = await ctx.runMutation(internal.admin.consumeOidcAttempt, { state });
    if (!attempt) return redirectToAuth({ oidc_error: "invalid_state" });

    // Exchange code → tokens.
    let clientSecret = "";
    try {
      clientSecret = await decryptStoredOidcSecret(cfg.clientSecretEnc);
    } catch (err: any) {
      console.error("[oidc] secret decrypt failed:", err);
      return redirectToAuth({ oidc_error: "secret_decrypt_failed" });
    }

    const callbackUrl = `${webBase}/auth/oidc/callback`;
    const tokenForm = new URLSearchParams({
      grant_type: "authorization_code",
      code,
      redirect_uri: callbackUrl,
      client_id: cfg.clientId,
      client_secret: clientSecret,
      code_verifier: attempt.codeVerifier,
    });

    let tokens: any;
    try {
      const tokenRes = await fetch(cfg.tokenEndpoint, {
        method: "POST",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded",
          Accept: "application/json",
        },
        body: tokenForm.toString(),
        signal: AbortSignal.timeout(15_000),
      });
      if (!tokenRes.ok) {
        const detail = await tokenRes.text().catch(() => "");
        console.error("[oidc] token endpoint", tokenRes.status, detail);
        return redirectToAuth({ oidc_error: "token_exchange_failed" });
      }
      tokens = await tokenRes.json();
    } catch (err: any) {
      console.error("[oidc] token fetch:", err);
      return redirectToAuth({ oidc_error: "token_fetch_error" });
    }

    if (!tokens?.access_token) {
      return redirectToAuth({ oidc_error: "no_access_token" });
    }

    // Fetch userinfo. We rely on userinfo for identity (email, sub)
    // rather than parsing the ID token here — keeps the implementation
    // small and works against any compliant OIDC server. Tenant
    // restriction is enforced after userinfo so an unfederated user
    // never makes it to the upsert.
    let userinfo: any;
    try {
      const uiRes = await fetch(cfg.userinfoEndpoint, {
        headers: { Authorization: `Bearer ${tokens.access_token}` },
        signal: AbortSignal.timeout(10_000),
      });
      if (!uiRes.ok) {
        return redirectToAuth({ oidc_error: "userinfo_failed" });
      }
      userinfo = await uiRes.json();
    } catch (err) {
      return redirectToAuth({ oidc_error: "userinfo_error" });
    }

    const sub = String(userinfo?.sub || "").trim();
    const email = String(userinfo?.email || "").trim().toLowerCase();
    const name = String(userinfo?.name || userinfo?.preferred_username || email || "").trim();
    const picture = userinfo?.picture ? String(userinfo.picture) : undefined;
    if (!sub) return redirectToAuth({ oidc_error: "userinfo_no_sub" });

    // Tenant restriction. Empty tenant string ⇒ no restriction. Two
    // common shapes: email-domain match (eng.example.com matches
    // alice@eng.example.com) and exact tenant id match (Azure AD's
    // `tid` claim or hosted-domain `hd`).
    const tenant = (cfg.tenant ?? "").trim().toLowerCase();
    if (tenant) {
      const emailDomain = email.split("@")[1] || "";
      const hd = String(userinfo?.hd || "").toLowerCase();
      const tid = String(userinfo?.tid || "").toLowerCase();
      const matched = emailDomain === tenant || hd === tenant || tid === tenant;
      if (!matched) {
        return redirectToAuth({ oidc_error: "tenant_denied" });
      }
    }

    if (!email) return redirectToAuth({ oidc_error: "no_email" });

    // Upsert + session mint.
    const upserted = await ctx.runMutation(internal.admin.upsertOidcUser, {
      issuer: cfg.issuerUrl,
      sub,
      email,
      name: name || undefined,
      avatarUrl: picture,
    });

    // Mint a Yaver session via the existing helper. The raw token
    // returned here is what the web client persists, mirroring the
    // other OAuth callbacks.
    const rawToken = randomBase64Url(32);
    const tokenHash = await sha256Hex(rawToken);
    const expiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;
    await ctx.runMutation(internal.auth.createSession, {
      tokenHash,
      userId: upserted.userDocId,
      expiresAt,
    });

    // Hand the token back to /auth on the web client. The page already
    // knows how to consume `token=…` in the URL fragment for its
    // existing OAuth flows.
    const finalParams = new URLSearchParams({
      token: rawToken,
      provider: "oidc",
    });
    const returnTo = attempt.returnTo || "";
    if (returnTo) finalParams.set("return_to", returnTo);
    return new Response(null, {
      status: 302,
      headers: {
        Location: `${webBase}/auth#${finalParams.toString()}`,
      },
    });
  }),
});

// ── Task Packages — runner share/accept (docs/yaver-task-packages.md) ──

/** POST /packages/allocation — look up a shared Task Package by invite code,
 *  for the runner's consent screen. Public (the code is the secret); returns
 *  only consent-safe fields (package name, domains, schedule, willNot, dataShown). */
http.route({
  path: "/packages/allocation",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const body = await request.json().catch(() => null);
    const code = typeof body?.code === "string" ? body.code.trim() : "";
    if (!code) return errorResponse("need { code }", 400);
    const alloc = await ctx.runQuery(api.taskPackages.allocationByCode, { code });
    if (!alloc) return errorResponse("not found", 404);
    return jsonResponse(alloc);
  }),
});

/** POST /packages/accept — the runner accepts a shared package under consent.
 *  Materializes the scoped grant + activates the allocation. Auth: Bearer. */
http.route({
  path: "/packages/accept",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => null);
    if (!body?.code || !body?.deviceId) return errorResponse("need { code, deviceId }", 400);
    try {
      const out = await ctx.runMutation(api.taskPackages.acceptAllocation, {
        tokenHash,
        code: String(body.code),
        deviceId: String(body.deviceId),
        wifiOnly: typeof body.wifiOnly === "boolean" ? body.wifiOnly : undefined,
        chargingOnly: typeof body.chargingOnly === "boolean" ? body.chargingOnly : undefined,
      });
      return jsonResponse({ ...out, ok: true });
    } catch (e: any) {
      return errorResponse(e?.message || "accept failed", 400);
    }
  }),
});

/** POST /packages/shared — list the packages shared WITH the caller (runner). */
http.route({
  path: "/packages/shared",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    try {
      const rows = await ctx.runQuery(api.taskPackages.sharedWithMe, { tokenHash });
      return jsonResponse({ allocations: rows });
    } catch (e: any) {
      return errorResponse(e?.message || "list failed", 400);
    }
  }),
});

function classifyWhatsappCommand(text: string): string {
  const t = text.trim().toLowerCase();
  if (/^(status|durum|ping)\b/.test(t)) return "status";
  if (/\b(build|bundle|rebuild|native)\b/.test(t) && /\b(reload|push|yenile)\b/.test(t)) return "build_reload";
  if (/\b(reload|refresh|yenile|hot reload|hermes)\b/.test(t)) return "reload";
  return "task";
}

function extractWhatsappText(msg: any): string {
  return String(
    msg?.text?.body ||
    msg?.interactive?.button_reply?.title ||
    msg?.button?.text ||
    "",
  ).trim();
}

async function deliverWhatsappCommandToAgent(ctx: any, args: {
  receiptId: any;
  userId: any;
  targetDeviceId: string;
  projectSlug?: string;
  action: string;
  commandText: string;
}): Promise<boolean> {
  const secret = (process.env.WHATSAPP_AGENT_INGRESS_SECRET || "").trim();
  if (!secret) {
    await ctx.runMutation(internal.whatsapp.receiptFinish, {
      receiptId: args.receiptId,
      status: "failed",
      errorCode: "agent_ingress_secret_missing",
    });
    return false;
  }
  const endpoint: string | null = await ctx.runQuery(internal.whatsapp.deviceEndpoint, {
    userId: args.userId,
    deviceId: args.targetDeviceId,
  });
  if (!endpoint) {
    await ctx.runMutation(internal.whatsapp.receiptFinish, {
      receiptId: args.receiptId,
      status: "failed",
      errorCode: "no_public_endpoint",
    });
    return false;
  }
  try {
    const resp = await fetch(`${endpoint}/integrations/whatsapp/command`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Yaver-WhatsApp-Secret": secret,
      },
      body: JSON.stringify({
        source: "whatsapp",
        action: args.action,
        commandText: args.commandText,
        projectSlug: args.projectSlug,
      }),
    });
    let data: any = {};
    try { data = await resp.json(); } catch { /* ignore */ }
    await ctx.runMutation(internal.whatsapp.receiptFinish, {
      receiptId: args.receiptId,
      status: resp.ok ? "delivered" : "failed",
      taskId: typeof data?.taskId === "string" ? data.taskId : undefined,
      errorCode: resp.ok ? undefined : `agent_http_${resp.status}`,
    });
    return resp.ok;
  } catch {
    await ctx.runMutation(internal.whatsapp.receiptFinish, {
      receiptId: args.receiptId,
      status: "failed",
      errorCode: "agent_fetch_failed",
    });
    return false;
  }
}

// POST /whatsapp/invite — authenticated developer creates a Yaver-owned
// WhatsApp join code for one project/device. Returns a wa.me link when
// WHATSAPP_PUBLIC_NUMBER is configured.
http.route({
  path: "/whatsapp/invite",
  method: "OPTIONS",
  handler: httpAction(async () => corsPreflightResponse()),
});
http.route({
  path: "/whatsapp/invite",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const authHeader = request.headers.get("Authorization");
    if (!authHeader?.startsWith("Bearer ")) return errorResponse("Unauthorized", 401);
    const tokenHash = await sha256Hex(authHeader.slice(7));
    const body = await request.json().catch(() => ({}));
    if (!body?.targetDeviceId || typeof body.targetDeviceId !== "string") {
      return errorResponse("targetDeviceId required", 400);
    }
    const result = await ctx.runMutation(api.whatsapp.createInvite, {
      tokenHash,
      targetDeviceId: body.targetDeviceId,
      projectSlug: typeof body.projectSlug === "string" ? body.projectSlug : undefined,
      allowedActions: Array.isArray(body.allowedActions) ? body.allowedActions.map(String) : undefined,
      ttlHours: typeof body.ttlHours === "number" ? body.ttlHours : undefined,
    });
    return jsonResponse(result);
  }),
});

// GET /whatsapp/webhook — Meta verification handshake for the Yaver-owned
// WhatsApp Business Cloud API number.
http.route({
  path: "/whatsapp/webhook",
  method: "GET",
  handler: httpAction(async (_ctx, request) => {
    const url = new URL(request.url);
    const mode = url.searchParams.get("hub.mode");
    const token = url.searchParams.get("hub.verify_token");
    const challenge = url.searchParams.get("hub.challenge") || "";
    const expected = (process.env.WHATSAPP_VERIFY_TOKEN || "").trim();
    if (mode === "subscribe" && expected && token === expected) {
      return new Response(challenge, { status: 200, headers: { "Content-Type": "text/plain" } });
    }
    return new Response("forbidden", { status: 403 });
  }),
});

// POST /whatsapp/webhook — command intake. Raw message text is used only in
// this action while forwarding inline to the target agent; Convex stores only
// hashes, routing metadata, and delivery receipts.
http.route({
  path: "/whatsapp/webhook",
  method: "POST",
  handler: httpAction(async (ctx, request) => {
    const raw = await request.text();
    const appSecret = (process.env.WHATSAPP_APP_SECRET || "").trim();
    const sigHeader = (request.headers.get("x-hub-signature-256") || "").replace(/^sha256=/i, "").trim().toLowerCase();
    if (!appSecret) return new Response("not configured", { status: 503 });
    const expectedSig = await hmacSha256Hex(appSecret, raw);
    if (!sigHeader || !constantTimeHexEqual(expectedSig, sigHeader)) {
      return new Response("bad signature", { status: 401 });
    }

    let body: any = {};
    try { body = JSON.parse(raw); } catch { return new Response("ok", { status: 200 }); }
    for (const entry of body.entry || []) {
      for (const change of entry.changes || []) {
        const value = change.value || {};
        for (const msg of value.messages || []) {
          const from = String(msg.from || "");
          const waMessageId = String(msg.id || crypto.randomUUID());
          const text = extractWhatsappText(msg);
          if (!from || !text) continue;

          const join = text.match(/^join\s+([A-Za-z0-9-]{4,32})\b/i);
          if (join) {
            const bound = await ctx.runMutation(internal.whatsapp.bindJoinCode, {
              phone: from,
              code: join[1],
              displayName: value.contacts?.[0]?.profile?.name ? String(value.contacts[0].profile.name) : undefined,
            });
            await ctx.runAction(internal.whatsapp.sendText, {
              to: from,
              body: bound.ok
                ? "Yaver linked this WhatsApp chat to the developer project. Send feedback or a task request here."
                : "That Yaver join code is invalid or expired. Ask the developer for a new invite.",
            });
            continue;
          }

          const contact = await ctx.runQuery(internal.whatsapp.resolveContact, { phone: from });
          if (!contact) continue;
          const action = classifyWhatsappCommand(text);
          if (!contact.allowedActions.includes(action)) {
            await ctx.runAction(internal.whatsapp.sendText, {
              to: from,
              body: "This WhatsApp chat is not allowed to run that Yaver action for the project.",
            });
            continue;
          }
          const receipt = await ctx.runMutation(internal.whatsapp.receiptStart, {
            userId: contact.userId,
            phoneHash: contact.phoneHash,
            waMessageIdHash: await sha256Hex(waMessageId),
            targetDeviceId: contact.targetDeviceId,
            projectSlug: contact.projectSlug,
            action,
          });
          if (!receipt.ok || receipt.duplicate) continue;
          const delivered = await deliverWhatsappCommandToAgent(ctx, {
            receiptId: receipt.receiptId,
            userId: contact.userId,
            targetDeviceId: contact.targetDeviceId,
            projectSlug: contact.projectSlug,
            action,
            commandText: text,
          });
          await ctx.runAction(internal.whatsapp.sendText, {
            to: from,
            body: !delivered
              ? "Yaver received this, but the developer machine is not reachable yet."
              : action === "status"
              ? "Yaver is checking the developer machine."
              : action === "reload" || action === "build_reload"
                ? "Yaver sent the reload request to the developer machine."
                : "Yaver sent this as a scoped task request to the developer machine.",
          });
        }
      }
    }
    return new Response("ok", { status: 200 });
  }),
});

export default http;
