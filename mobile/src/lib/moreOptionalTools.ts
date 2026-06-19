export const OPTIONAL_MORE_TOOLS = [
  {
    id: "robot-cell",
    label: "Robot Cell",
    description: "Ender-3 screwdriver robot controls.",
  },
  {
    id: "printer",
    label: "3D Printer",
    description: "Bambu Lab chamber watch, control, and CAD.",
  },
  {
    id: "circuit",
    label: "Circuit Simulator",
    description: "SPICE/KiCad/EPLAN simulation and waveforms.",
  },
  {
    id: "ev-stations",
    label: "EV Stations",
    description: "Nearby charger discovery and filters.",
  },
  {
    id: "car-voice",
    label: "Car Voice Coding",
    description: "Hands-free coding commands over car audio.",
  },
  {
    id: "data-collection",
    label: "Data Collection",
    description: "Multi-vantage collection and source health.",
  },
  {
    id: "twin-mode",
    label: "Twin Mode",
    description: "Run and record on remote dev surfaces.",
  },
  {
    id: "task-packages",
    label: "Task Packages",
    description: "Portable collection/operation task bundles.",
  },
  {
    id: "package-accept",
    label: "Accept Shared Tasks",
    description: "Enter an invite code to run shared packages.",
  },
  {
    id: "screw-cell",
    label: "Screw Cell",
    description: "Shop-floor screw-cell analytics.",
  },
] as const;

export type OptionalMoreToolId = (typeof OPTIONAL_MORE_TOOLS)[number]["id"];

export function normalizeOptionalMoreTools(value: unknown): OptionalMoreToolId[] {
  if (!Array.isArray(value)) return [];
  const allowed = new Set<string>(OPTIONAL_MORE_TOOLS.map((tool) => tool.id));
  const seen = new Set<string>();
  const out: OptionalMoreToolId[] = [];
  for (const item of value) {
    if (typeof item !== "string" || !allowed.has(item) || seen.has(item)) continue;
    seen.add(item);
    out.push(item as OptionalMoreToolId);
  }
  return out;
}

export function isOptionalMoreToolEnabled(value: unknown, id: OptionalMoreToolId): boolean {
  return normalizeOptionalMoreTools(value).includes(id);
}
