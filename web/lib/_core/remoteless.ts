// AUTO-SYNCED from shared/client-core/src/remoteless.ts.
// DO NOT EDIT IN PLACE. Edit the source and re-run
// scripts/sync-client-core.sh. CI checks drift via `--check`.

// One pure decision for every client surface that can explicitly select, or
// fall back to, a local or hosted DeepSeek lane. Remoteless is bounded capacity
// and never becomes the default ahead of configured devices and runners.

export type RemotelessCapability =
  | "analysis-chat" | "code-edit" | "git-read" | "git-commit" | "git-push"
  | "static-preflight" | "existing-web-artifact"
  | "dev-server" | "web-build" | "flutter-render" | "native-build"
  | "simulator" | "shell" | "test" | "deploy" | "container";

export type RemotelessSupport = "supported" | "bounded" | "unavailable";

export type RemotelessCapabilityResult = {
  capability: RemotelessCapability;
  support: RemotelessSupport;
  code: string;
  summary: string;
  detail: string;
  route: { label: string; path: "/devices" };
  alternateRoute?: { label: string; path: "/devices" };
};

export type ExecutionCandidate = {
  id: string;
  name: string;
  role: "explicit" | "primary" | "secondary" | "focused";
  connected: boolean;
};

export type RemotelessPlacement =
  | { lane: "remote"; target: ExecutionCandidate; degraded: boolean; banner: string | null }
  | { lane: "remoteless"; capability: RemotelessCapabilityResult; banner: string }
  | { lane: "blocked"; capability: RemotelessCapabilityResult; banner: string };

const LOCAL_SUPPORT: Record<RemotelessCapability, RemotelessSupport> = {
  "analysis-chat": "supported", "code-edit": "bounded", "git-read": "supported", "git-commit": "bounded",
  "git-push": "bounded", "static-preflight": "supported",
  "existing-web-artifact": "supported", "dev-server": "unavailable",
  "web-build": "unavailable", "flutter-render": "unavailable",
  "native-build": "unavailable", simulator: "unavailable", shell: "unavailable",
  test: "unavailable", deploy: "unavailable", container: "unavailable",
};

const LABELS: Record<RemotelessCapability, string> = {
  "analysis-chat": "analysis and chat", "code-edit": "editing", "git-read": "Git inspection", "git-commit": "Git commit",
  "git-push": "Git push", "static-preflight": "static preflight",
  "existing-web-artifact": "an existing web artifact", "dev-server": "a dev server",
  "web-build": "a web build", "flutter-render": "Flutter rendering",
  "native-build": "a native build", simulator: "a simulator", shell: "shell commands",
  test: "tests", deploy: "deployment", container: "containers",
};

export function remotelessCapability(capability: RemotelessCapability, surface: "ios" | "android" | "web" | "companion"): RemotelessCapabilityResult {
  const support = capability === "analysis-chat" ? "supported" : surface === "ios" || surface === "android"
    ? LOCAL_SUPPORT[capability]
    : capability === "existing-web-artifact" ? "supported" : "unavailable";
  const label = LABELS[capability];
  const host = surface === "ios" ? "iPhone/iPad" : surface === "android" ? "Android device" : surface === "web" ? "browser" : "companion device";
  if (support === "supported") return {
    capability, support, code: `remoteless.${capability}.supported`,
    summary: `${label} can run on this device`,
    detail: `${label} does not require a remote shell or build toolchain.`,
    route: { label: "Choose a device", path: "/devices" },
  };
  if (support === "bounded") return {
    capability, support, code: `remoteless.${capability}.bounded`,
    summary: `${label} is available on this device with limits`,
    detail: `${label} can run locally, but background execution is bounded and interrupted work returns to Review.`,
    route: { label: "Choose a device", path: "/devices" },
  };
  return {
    capability, support, code: `remoteless.${capability}.unavailable`,
    summary: `${label} needs another execution target`,
    detail: capability === "flutter-render"
      ? `The ${host} can display an already-built Flutter web artifact, but Yaver's remoteless runtime has no Flutter SDK, shell, dev server, or simulator. Build and serve it on your own capable machine or VPS.`
      : `Yaver's remoteless runtime on this ${host} cannot provide ${label}: it has no general shell, package manager, persistent process host, simulator, native SDK, or container runtime. Use your own capable machine or VPS.`,
    route: { label: "Choose a capable device", path: "/devices" },
    alternateRoute: { label: "Choose a machine", path: "/devices" },
  };
}

export function resolveRemotelessPlacement(input: {
  capability: RemotelessCapability;
  surface: "ios" | "android" | "web" | "companion";
  candidates: ExecutionCandidate[];
  forceLocal?: boolean;
}): RemotelessPlacement {
  const capability = remotelessCapability(input.capability, input.surface);
  if (!input.forceLocal) {
    // Enforce precedence here instead of trusting every surface to construct
    // its array correctly. Within a role, caller order remains significant
    // (for example project-assigned primary before account-wide primary).
    const target = (["explicit", "primary", "secondary", "focused"] as const)
      .map((role) => input.candidates.find((candidate) => candidate.role === role && candidate.connected))
      .find((candidate): candidate is ExecutionCandidate => !!candidate);
    if (target) {
      const degraded = target.role === "secondary";
      return { lane: "remote", target, degraded, banner: degraded ? `Using secondary · ${target.name} · primary unavailable` : null };
    }
  }
  const configured = input.candidates.filter((candidate) => candidate.role !== "focused");
  const reason = configured.length ? "Primary and secondary devices are unavailable." : "No eligible remote device is configured.";
  if (capability.support === "unavailable") return { lane: "blocked", capability, banner: `${reason} ${capability.summary}.` };
  if (input.forceLocal) return { lane: "remoteless", capability, banner: `No remote box selected · ${capability.summary}.` };
  return { lane: "remoteless", capability, banner: `Remoteless fallback · ${reason} ${capability.summary}.` };
}
