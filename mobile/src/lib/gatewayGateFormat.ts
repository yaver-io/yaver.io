// gatewayGateFormat — pure helpers for the gateway human-gate approval screen.
// A "human gate" is a point where the gateway's automation paused for YOU: an
// OAuth re-consent, a 2FA/OTP, a captcha, a KYC upload, a payment confirm, a
// region confirm, or a generic "tap to relay". The screen lists pending gates
// and lets you resolve them; for interactive challenges it embeds a live
// remote view so you can solve them on your phone (you have no physical access
// to the box). These helpers classify the gate, pick a risk tier, and decide
// whether a gate needs the embedded remote view. No network / React here —
// unit-tested in gatewayGateFormat.test.mts.

import type { GatewayGate } from "./gatewayGateClient";

/** GateStep mirrors the agent's F3 handoff step taxonomy + the broker's
 *  human-gate kinds. Unknown strings degrade to "other". */
export type GateStep =
  | "login"
  | "two_factor"
  | "captcha"
  | "kyc_upload"
  | "payment_confirm"
  | "region_confirm"
  | "tap_relay"
  | "push_approval"
  | "other";

const KNOWN_STEPS: GateStep[] = [
  "login",
  "two_factor",
  "captcha",
  "kyc_upload",
  "payment_confirm",
  "region_confirm",
  "tap_relay",
  "push_approval",
];

/** normalizeStep coerces a gate's step/kind field to a known GateStep. */
export function normalizeStep(step?: string): GateStep {
  const s = (step || "").trim().toLowerCase().replace(/[-\s]+/g, "_");
  const hit = KNOWN_STEPS.find((k) => k === s);
  if (hit) return hit;
  // common aliases
  if (s === "2fa" || s === "otp" || s === "totp") return "two_factor";
  if (s === "oauth" || s === "consent" || s === "signin") return "login";
  if (s === "robot" || s === "challenge") return "captcha";
  if (s === "kyc" || s === "id_upload" || s === "verify_identity") return "kyc_upload";
  if (s === "payment" || s === "pay" || s === "charge_confirm") return "payment_confirm";
  if (s === "region" || s === "geo") return "region_confirm";
  if (s === "push" || s === "approve") return "push_approval";
  return "other";
}

/** A short human label for a step. */
export function stepLabel(step: GateStep): string {
  switch (step) {
    case "login":
      return "Sign in";
    case "two_factor":
      return "2FA code";
    case "captcha":
      return "Captcha";
    case "kyc_upload":
      return "ID upload";
    case "payment_confirm":
      return "Confirm payment";
    case "region_confirm":
      return "Confirm region";
    case "tap_relay":
      return "Approve";
    case "push_approval":
      return "Push approval";
    default:
      return "Action needed";
  }
}

/** Risk tier drives the confirm posture and badge colour. payment / kyc are
 *  high (irreversible / sensitive); captcha / login / 2FA are medium (auth
 *  surface); region / tap-relay / push are low. */
export type GateRisk = "low" | "medium" | "high";

export function gateRisk(step: GateStep): GateRisk {
  switch (step) {
    case "payment_confirm":
    case "kyc_upload":
      return "high";
    case "login":
    case "two_factor":
    case "captcha":
      return "medium";
    default:
      return "low";
  }
}

/** A colour for a risk tier (hex; the screen reads these directly). */
export function gateRiskColor(risk: GateRisk): string {
  switch (risk) {
    case "high":
      return "#ef4444";
    case "medium":
      return "#f59e0b";
    default:
      return "#22c55e";
  }
}

/** needsRemoteView reports whether resolving this gate requires the embedded
 *  live remote view: the user must SOLVE a challenge on the box (captcha,
 *  login, KYC upload), not just approve/deny a yes/no. 2FA can be either —
 *  if the gate carries an explicit prompt asking for a code we keep it as a
 *  plain approve; otherwise we surface the view so they can type into the box. */
export function needsRemoteView(gate: Pick<GatewayGate, "step" | "interactive">): boolean {
  if (gate.interactive === true) return true;
  if (gate.interactive === false) return false;
  const step = normalizeStep(gate.step);
  return step === "captcha" || step === "login" || step === "kyc_upload";
}

/** A one-line description for a gate, preferring the agent's own prompt,
 *  falling back to a synthesised "<connector> needs <label>". */
export function gateSummary(gate: GatewayGate): string {
  if (gate.prompt && gate.prompt.trim()) return gate.prompt.trim();
  const label = stepLabel(normalizeStep(gate.step));
  const who = (gate.connector || gate.service || gate.title || "A task").trim();
  return `${who} needs ${label.toLowerCase()}`;
}

/** ageLabel renders a relative age from an epoch-ms createdAt. */
export function ageLabel(createdAt?: number, now: number = Date.now()): string {
  if (!createdAt || createdAt <= 0) return "";
  const diff = now - createdAt;
  if (diff < 0) return "just now";
  if (diff < 60_000) return "just now";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return `${Math.floor(diff / 86_400_000)}d ago`;
}
