// deviceLabels.ts — pure helpers for smart device auto-labels + auto-seeded
// alias slugs at registration.
//
// Why: a freshly-registered device used to get `name = os.Hostname()` and no
// alias. That hurt two things: (1) device pickers showed cryptic hostnames,
// and (2) the on-device voice helper had nothing memorable to resolve
// ("switch my hetzner box" → which deviceId?). These helpers derive a
// friendly label ("Hetzner box", "MacBook", "Linux box", "Raspberry Pi",
// "Windows PC") and a short alias slug ("hetzner", "mac", "linux", "pi",
// "windows") from signals we ALREADY have — platform, the raw hostname,
// hardwareProfile (incl. isWsl), and the cloud provider/region when the box
// is a managed cloud machine.
//
// PURE + dependency-free on purpose: no ctx, no db, no Convex imports — so
// it's trivially unit-testable and safe to call from registerDevice and the
// CLI. Privacy: every output is derived from platform / provider / region /
// generic-hostname-keywords only — NEVER an absolute path, username, IP, or
// secret (see backend privacy contract / convex_privacy_test.go). The raw
// hostname is only INSPECTED for keywords, never stored by these helpers.

export type DevicePlatform = "macos" | "windows" | "linux" | "android" | "ios";

export interface LabelSignals {
  platform: DevicePlatform;
  /** Raw hostname the agent registered (inspected for keywords only). */
  hostname?: string;
  /** Cloud provider when this is a managed cloud box, e.g. "hetzner". */
  cloudProvider?: string;
  /** Datacenter region for a cloud box, e.g. "hel1" / "eu". */
  cloudRegion?: string;
  /** True when the linux box is actually WSL on Windows. */
  isWsl?: boolean;
  /** Lowercased cpu/gpu/model string, if the agent reported one (raspberry pi detect). */
  hardwareModel?: string;
}

function titleCaseProvider(p: string): string {
  const known: Record<string, string> = {
    hetzner: "Hetzner",
    aws: "AWS",
    gcp: "GCP",
    azure: "Azure",
    digitalocean: "DigitalOcean",
    linode: "Linode",
    vultr: "Vultr",
    ovh: "OVH",
    fly: "Fly",
  };
  const key = p.trim().toLowerCase();
  if (known[key]) return known[key];
  return key.charAt(0).toUpperCase() + key.slice(1);
}

function looksLikeRaspberryPi(s: LabelSignals): boolean {
  const h = (s.hostname || "").toLowerCase();
  const m = (s.hardwareModel || "").toLowerCase();
  return (
    h.includes("raspberry") ||
    h === "raspberrypi" ||
    h.startsWith("rpi") ||
    m.includes("raspberry")
  );
}

/**
 * Derive a friendly display name from the signals. Returns null when we have
 * nothing better than the hostname (caller should keep the existing name).
 */
export function smartDeviceLabel(s: LabelSignals): string | null {
  // Cloud boxes win — provider (+ region) is the most useful label.
  if (s.cloudProvider) {
    const prov = titleCaseProvider(s.cloudProvider);
    const region = s.cloudRegion?.trim();
    return region ? `${prov} box (${region})` : `${prov} box`;
  }

  switch (s.platform) {
    case "macos": {
      const h = (s.hostname || "").toLowerCase();
      if (h.includes("macbook")) return "MacBook";
      if (h.includes("mac-mini") || h.includes("macmini") || h.includes("mini"))
        return "Mac mini";
      if (h.includes("imac")) return "iMac";
      if (h.includes("studio")) return "Mac Studio";
      return "Mac";
    }
    case "linux": {
      if (looksLikeRaspberryPi(s)) return "Raspberry Pi";
      if (s.isWsl) return "WSL box";
      return "Linux box";
    }
    case "windows":
      return "Windows PC";
    case "android":
      return "Android device";
    case "ios":
      return "iPhone"; // refined elsewhere if iPad detected
    default:
      return null;
  }
}

/**
 * Derive a short, memorable alias slug (lowercase, [a-z0-9-]) used to
 * auto-seed `devices.alias` so voice/CLI resolution works day-one. Matches
 * the alias validation `^[a-z0-9._-]{1,48}$`. Returns null when no good slug.
 */
export function smartAliasSlug(s: LabelSignals): string | null {
  if (s.cloudProvider) return s.cloudProvider.trim().toLowerCase().replace(/[^a-z0-9-]/g, "") || null;
  switch (s.platform) {
    case "macos":
      return "mac";
    case "linux":
      if (looksLikeRaspberryPi(s)) return "pi";
      if (s.isWsl) return "wsl";
      return "linux";
    case "windows":
      return "windows";
    case "android":
      return "android";
    case "ios":
      return "iphone";
    default:
      return null;
  }
}

/**
 * Given a desired base slug and the set of slugs already taken by this user,
 * return a free slug ("mac", then "mac-2", "mac-3", …) or null if the base
 * is empty. Keeps within the 48-char alias limit.
 */
export function uniqueAliasSlug(base: string | null, taken: Set<string>): string | null {
  if (!base) return null;
  const clean = base.toLowerCase().replace(/[^a-z0-9-]/g, "").slice(0, 40);
  if (!clean) return null;
  if (!taken.has(clean)) return clean;
  for (let i = 2; i < 100; i++) {
    const candidate = `${clean}-${i}`;
    if (!taken.has(candidate)) return candidate;
  }
  return null;
}

/**
 * A hostname is "raw"/uninformative if it looks machine-generated or is just
 * the bare platform — i.e. worth replacing with a smart label. We only ever
 * REPLACE such names; a user-meaningful hostname is left alone.
 */
export function isRawHostname(name: string | undefined, platform: DevicePlatform): boolean {
  if (!name) return true;
  const n = name.trim().toLowerCase();
  if (!n) return true;
  // bare platform words
  if (["linux", "ubuntu", "debian", "localhost", "macos", "windows"].includes(n)) return true;
  // generic cloud-image hostnames: "ubuntu-4gb-hel1-1", uuid-ish, "ip-10-0-0-1"
  if (/^ubuntu-/.test(n)) return true;
  if (/^ip-\d+/.test(n)) return true;
  if (/^[0-9a-f]{8,}$/.test(n)) return true; // hex blob
  if (/^(srv|vps|host|server|debian|fedora|arch)[-_]?\d*$/.test(n)) return true;
  return false;
}
