#!/usr/bin/env node
import fs from "node:fs";
import path from "node:path";

const repoRoot = process.cwd();
const sourcePath = path.join(repoRoot, "desktop/agent/httpserver.go");
const source = fs.readFileSync(sourcePath, "utf8");

const ROUTE_RE = /mux\.HandleFunc\("([^"]+)",\s*([^\n]+?)\)/g;

const PUBLIC_ALLOWLIST = new Set([
  "/health",
  "/integrations/whatsapp/command",
  "/blobs/public",
  "/changelog.html",
  "/changelog.atom",
  "/auth/pair/info",
  "/auth/pair/session",
  "/auth/pair/submit",
  "/auth/pair/encrypted",
  "/support/info",
  "/support/redeem",
  "/auth/recover",
  "/auth/recover/session",
  "/auth/reload-from-disk",
  "/auth/factory-reset",
  "/auth/status",
  "/newsletter/subscribe",
  "/newsletter/confirm",
  "/newsletter/unsubscribe",
  "/oauth/.well-known/openid-configuration",
  "/oauth/authorize",
  "/oauth/login",
  "/oauth/token",
  "/oauth/userinfo",
  "/oauth/jwks",
  "/.well-known/oauth-protected-resource",
  "/.well-known/oauth-protected-resource/mcp",
  "/.well-known/oauth-authorization-server",
  "/mail/onboard/callback",
  "/s/",
  "/waitlist/join",
  "/waitlist/leaderboard",
  "/docs",
  "/docs/",
  "/meet/",
  "/ab/assign",
  "/ab/events",
  "/clips/",
  "/webhooks/stripe",
  "/webhooks/lemonsqueezy",
  "/asciinema/",
  "/chat/messages",
  "/chat/stream",
  "/chat/widget.js",
  "/analytics/views",
  "/webhooks/trigger",
  "/dev/native-bundle",
  "/dev/native-assets",
  "/dev/web-bundle/",
  "/dev/hermes-wasm-runtime",
  "/dev/",
  "/dev-web/",
  "/deploy/webhook",
  "/forms/",
]);

const OWNER_ONLY_PREFIXES = [
  "/tasks",
  "/finalize",
  "/deploy",
  "/stores",
  "/listing",
  "/publish",
  "/info",
  "/hardware",
  "/self-check",
  "/bus",
  "/agent/status",
  "/agent/capabilities",
  "/agent/self-heal",
  "/runner-auth/set",
  "/auth/ssh",
  "/runner/opencode",
  "/machine",
  "/agent/env-profile",
  "/agent/toolchain-sync",
  "/agent/dev-configs",
  "/dev-environments",
  "/code",
  "/agent/runner/restart",
  "/agent/update",
  "/agent/shutdown",
  "/infra",
  "/agent/clean",
  "/agent/doctor",
  "/agent/tools",
  "/schedules",
  "/streams",
  "/netcapture",
  "/autoideas",
  "/autoinit",
  "/releases",
  "/incidents",
  "/operations",
  "/capabilities/snapshot",
  "/errors",
  "/monitors",
  "/analytics/events",
  "/flags/override",
  "/flags/delete",
  "/logs",
  "/sourcemaps",
  "/env",
  "/sync",
  "/statuspage",
  "/email",
  "/apikeys",
  "/pubsub/topics",
  "/search",
  "/feedback-board",
  "/remoteview",
  "/ghost",
  "/capture",
  "/appletv",
  "/stream",
  "/tunnel",
  "/files",
  "/host-share/fs",
  "/shared-storage",
  "/project",
  "/imports",
  "/forms",
  "/newsletter/subscribers",
  "/newsletter/campaigns",
  "/newsletter/compose",
  "/jobs",
  "/img",
  "/pdf",
  "/yaver-agent/audit",
  "/mcp/servers",
];

const SDK_OR_GUEST_PREFIXES = [
  "/agent/runners",
  "/agent/runner/switch",
  "/ops",
  "/dev/status",
  "/dev/target",
  "/dev/reload",
  "/dev/reload-app",
  "/dev/native-fingerprint",
  "/dev/events",
  "/dev/compatibility",
  "/dev/build-native",
  "/unity",
  "/vibing",
];

const SDK_PREFIXES = [
  "/runner-auth/setup",
  "/runner-auth/browser",
  "/runner-auth/credentials/import",
  "/runner-provider/preflight",
  "/company-ai/resolve-local",
  "/analytics/ingest",
  "/flags/eval",
  "/env/get",
  "/pubsub/publish",
  "/pubsub/subscribe",
  "/feedback-board/public",
  "/voice",
  "/feedback",
  "/shots",
  "/design-references",
  "/test-app",
  "/blackbox",
  "/mobile-workers",
  "/todolist",
  "/errors/ingest",
];

const SENSITIVE_HANDLER_NAMES = [
  "Credentials",
  "ToolchainGitCredentials",
  "OpenCodeConfig",
  "SSHAuthorizedKeys",
  "Shutdown",
  "EnvGet",
  "EnvList",
  "APIKeys",
  "Vault",
  "FilesRead",
  "FilesRaw",
  "SharedStorageRead",
  "SharedStorageRaw",
  "MCPServers",
  "YaverAgentDeviceAudit",
  "Netcapture",
  "Ghost",
  "Capture",
  "RemoteView",
  "Ops",
];

function classifyWrapper(handlerExpr) {
  if (handlerExpr.includes("s.authMCP(")) return "authMCP";
  if (handlerExpr.includes("s.authSDKOrGuest(")) return "authSDKOrGuest";
  if (handlerExpr.includes("s.authSDK(")) return "authSDK";
  if (handlerExpr.includes("s.auth(")) return "auth";
  if (handlerExpr.includes("s.authBuildLocal(")) return "authBuildLocal";
  if (handlerExpr.includes("s.rateLimit(s.auth(")) return "auth";
  if (handlerExpr.includes("s.rateLimit(s.authSDK(")) return "authSDK";
  if (handlerExpr.includes("s.rateLimit(s.authSDKOrGuest(")) return "authSDKOrGuest";
  return "public";
}

function startsWithAny(value, prefixes) {
  return prefixes.some((prefix) => value === prefix || value.startsWith(`${prefix}/`) || value.startsWith(prefix));
}

function routeExpected(pathname) {
  if (PUBLIC_ALLOWLIST.has(pathname)) return "public";
  if (startsWithAny(pathname, SDK_OR_GUEST_PREFIXES)) return "authSDKOrGuest";
  if (startsWithAny(pathname, SDK_PREFIXES)) return "authSDK";
  if (startsWithAny(pathname, OWNER_ONLY_PREFIXES)) return "auth";
  return null;
}

function weakerThan(actual, expected) {
  if (expected === "public") return false;
  if (expected === "auth") return actual !== "auth" && actual !== "authMCP";
  if (expected === "authSDK") return actual === "public" || actual === "authSDKOrGuest";
  if (expected === "authSDKOrGuest") return actual === "public";
  return false;
}

const routes = [];
let match;
while ((match = ROUTE_RE.exec(source)) !== null) {
  routes.push({ path: match[1], expr: match[2].trim(), actual: classifyWrapper(match[2]) });
}

const findings = [];

for (const route of routes) {
  const expected = routeExpected(route.path);
  if (expected && weakerThan(route.actual, expected)) {
    findings.push({
      severity: expected === "auth" ? "high" : "medium",
      check: "weak-route-wrapper",
      path: route.path,
      expected,
      actual: route.actual,
      handler: route.expr,
    });
  }

  if (
    route.actual === "public" &&
    !PUBLIC_ALLOWLIST.has(route.path) &&
    SENSITIVE_HANDLER_NAMES.some((name) => route.expr.includes(name))
  ) {
    findings.push({
      severity: "high",
      check: "sensitive-public-handler",
      path: route.path,
      actual: route.actual,
      handler: route.expr,
    });
  }
}

const routeCount = routes.length;
const publicRoutes = routes.filter((r) => r.actual === "public").map((r) => r.path);
const unlistedPublic = publicRoutes.filter((p) => !PUBLIC_ALLOWLIST.has(p));
for (const pathname of unlistedPublic) {
  findings.push({
    severity: "low",
    check: "public-route-not-reviewed",
    path: pathname,
    actual: "public",
    detail: "public route is not in scripts/security-route-audit.mjs allowlist; review whether this is intentional",
  });
}

console.log(`Audited ${routeCount} desktop agent routes from ${sourcePath}`);
console.log(`Public routes: ${publicRoutes.length}`);
if (findings.length === 0) {
  console.log("No route wrapper regressions found.");
} else {
  console.log(`Findings: ${findings.length}`);
  for (const f of findings) {
    console.log(`[${f.severity}] ${f.check} ${f.path} expected=${f.expected || "review"} actual=${f.actual}`);
    if (f.handler) console.log(`  ${f.handler}`);
    if (f.detail) console.log(`  ${f.detail}`);
  }
}

if (findings.some((f) => ["high", "medium"].includes(f.severity))) {
  process.exit(1);
}
