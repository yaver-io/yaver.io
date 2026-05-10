// Bundled host manifest. Loaded via require so Metro inlines it at
// build time — guarantees the JS side reports exactly what the iOS /
// Android binary linked, without a native bridge hop. Source of truth
// is mobile/sdk-manifest.json (TestSDKManifestInSync gates drift).
import sdkManifestJSON from "../../sdk-manifest.json";

export type NativeBuildConsumerContract = {
  consumerVersion?: string;
  consumerBuild?: string;
  consumerSdkVersion?: string;
  consumerHermesBCVersion?: number;
  consumerCurrentRuntimeFamilyId?: string;
  consumerDefaultRuntimeFamilyId?: string;
  consumerRuntimeFamilies?: Array<Record<string, unknown>>;
};

// hostNativeModulesFromBundledManifest extracts the {name: version}
// map from the bundled sdk-manifest.json. Used as the dynamic
// handshake payload (consumerNativeModules) so the agent's compat
// check sees what THIS host actually links — not a stale agent copy.
export function hostNativeModulesFromBundledManifest(): Record<string, string> {
  const m = (sdkManifestJSON as { nativeModules?: Record<string, string> })?.nativeModules;
  return m && typeof m === "object" ? m : {};
}

export function buildNativeBuildRequest(
  platform: "ios" | "android",
  contract?: NativeBuildConsumerContract,
  // `project` pins the request to a specific guest project so the agent
  // never falls back to whatever dev server happens to be running. The
  // agent (≥ 1.99.187) returns 400 PROJECT_REQUIRED when none of these
  // are set; older agents continue to honour the legacy fallback.
  project?: { projectPath?: string; projectName?: string; bundleId?: string },
) {
  const nativeModules = hostNativeModulesFromBundledManifest();
  return {
    platform,
    ...(project?.projectPath ? { projectPath: project.projectPath } : {}),
    ...(project?.projectName ? { projectName: project.projectName } : {}),
    ...(project?.bundleId ? { bundleId: project.bundleId } : {}),
    ...(contract?.consumerVersion ? { consumerVersion: contract.consumerVersion } : {}),
    ...(contract?.consumerBuild ? { consumerBuild: contract.consumerBuild } : {}),
    ...(contract?.consumerSdkVersion ? { consumerSdkVersion: contract.consumerSdkVersion } : {}),
    ...(typeof contract?.consumerHermesBCVersion === "number" && contract.consumerHermesBCVersion > 0
      ? { consumerHermesBCVersion: contract.consumerHermesBCVersion }
      : {}),
    ...(contract?.consumerCurrentRuntimeFamilyId ? { consumerCurrentRuntimeFamilyId: contract.consumerCurrentRuntimeFamilyId } : {}),
    ...(contract?.consumerDefaultRuntimeFamilyId ? { consumerDefaultRuntimeFamilyId: contract.consumerDefaultRuntimeFamilyId } : {}),
    ...(Array.isArray(contract?.consumerRuntimeFamilies) && contract.consumerRuntimeFamilies.length > 0
      ? { consumerRuntimeFamilies: contract.consumerRuntimeFamilies }
      : {}),
    ...(Object.keys(nativeModules).length > 0
      ? { consumerNativeModules: nativeModules }
      : {}),
  };
}

export function nativeBuildFailureMessage(buildResult: any): string {
  const lines = [
    buildResult?.phase ? `phase: ${buildResult.phase}` : null,
    compatibilitySummary(buildResult) || buildResult?.error || "Build failed",
    runtimeFamilySummary(buildResult),
    compatibilityDetails(buildResult),
    buildResult?.helpHint || null,
  ].filter(Boolean);
  // /dev/build-native returns the last 120 lines of subprocess stderr+stdout
  // in `output` on HTTP-error responses (devserver_http.go:2789). Surface a
  // tail so the user sees the actual failure (npm error, missing dep, expo
  // CLI quirk, etc.) instead of just "Build failed".
  if (typeof buildResult?.output === "string" && buildResult.output.trim()) {
    const tail = buildResult.output.split("\n").filter((l: string) => l.trim()).slice(-25).join("\n");
    if (tail) {
      lines.push("---");
      lines.push(tail);
    }
  }
  return lines.join("\n");
}

export function nativeBuildFailureTitle(buildResult: any): string {
  if (buildResult?.code === "NATIVE_MODULE_INCOMPATIBLE") return "Compatibility Blocked";
  if (buildResult?.code === "NATIVE_MODULE_VERSION_MISMATCH") return "Compatibility Blocked";
  if (buildResult?.code === "REACT_VERSION_MISMATCH") return "Compatibility Blocked";
  if (buildResult?.code === "FRAMEWORK_VERSION_MISMATCH") return "Compatibility Blocked";
  if (buildResult?.code === "RUNTIME_FAMILY_MISMATCH") return "Compatibility Blocked";
  if (buildResult?.code === "BC_VERSION_MISMATCH") return "Hermes Version Mismatch";
  return "Load Failed";
}

function compatibilitySummary(buildResult: any): string | null {
  if (buildResult?.code === "NATIVE_MODULE_INCOMPATIBLE") {
    return "Yaver blocked restart because the project uses native modules the mobile host does not include.";
  }
  if (buildResult?.code === "NATIVE_MODULE_VERSION_MISMATCH") {
    return "Yaver blocked restart because the project's native runtime contract does not match the mobile host.";
  }
  if (buildResult?.code === "REACT_VERSION_MISMATCH") {
    return "Yaver blocked restart because the project's React runtime does not match the mobile host.";
  }
  if (buildResult?.code === "FRAMEWORK_VERSION_MISMATCH") {
    return "Yaver blocked restart because the guest app does not match the selected mobile host runtime family.";
  }
  if (buildResult?.code === "RUNTIME_FAMILY_MISMATCH") {
    return "Yaver blocked restart because the guest app does not match the selected mobile host runtime family.";
  }
  if (buildResult?.code === "BC_VERSION_MISMATCH") {
    return buildResult?.error || "Hermes bytecode version mismatch.";
  }
  return null;
}

function compatibilityDetails(buildResult: any): string | null {
  if (Array.isArray(buildResult?.incompatibleNativeModules) && buildResult.incompatibleNativeModules.length > 0) {
    return `Missing in Yaver: ${buildResult.incompatibleNativeModules.join(", ")}`;
  }
  if (Array.isArray(buildResult?.nativeModuleVersionMismatches) && buildResult.nativeModuleVersionMismatches.length > 0) {
    return buildResult.nativeModuleVersionMismatches
      .map((item: any) => `${item.name}: project ${item.projectVersion} vs host ${item.hostVersion}`)
      .join("\n");
  }
  if (buildResult?.reactVersionMismatch) {
    return `React: project ${buildResult.reactVersionMismatch.projectVersion} vs host ${buildResult.reactVersionMismatch.hostVersion}`;
  }
  if (buildResult?.reactNativeVersionMismatch || buildResult?.expoVersionMismatch) {
    return [
      buildResult?.reactNativeVersionMismatch
        ? `React Native: project ${buildResult.reactNativeVersionMismatch.projectVersion} vs host ${buildResult.reactNativeVersionMismatch.hostVersion}`
        : null,
      buildResult?.reactVersionMismatch
        ? `React: project ${buildResult.reactVersionMismatch.projectVersion} vs host ${buildResult.reactVersionMismatch.hostVersion}`
        : null,
      buildResult?.expoVersionMismatch
        ? `Expo: project ${buildResult.expoVersionMismatch.projectVersion} vs host ${buildResult.expoVersionMismatch.hostVersion}`
        : null,
    ].filter(Boolean).join("\n");
  }
  return null;
}

function runtimeFamilySummary(buildResult: any): string | null {
  const selection = buildResult?.runtimeFamilySelection;
  if (!selection?.selected) return null;
  const selected = selection.selected;
  const selectedLabel = selected.label || selected.id || "unknown host family";
  const guest = selection.guest || buildResult?.guestRuntime || {};
  const guestLabel = [
    guest.expoVersion ? `Expo ${guest.expoVersion}` : null,
    guest.reactNativeVersion ? `RN ${guest.reactNativeVersion}` : null,
    guest.reactVersion ? `React ${guest.reactVersion}` : null,
  ].filter(Boolean).join(" / ");
  const supported = Array.isArray(selection.supported) && selection.supported.length > 0
    ? selection.supported.map((family: any) => family.label || family.id).join("; ")
    : "";
  if (selection.exactMatch) {
    return `Runtime family matched: ${selectedLabel}${guestLabel ? ` ← ${guestLabel}` : ""}`;
  }
  return [
    `Closest host family: ${selectedLabel}${guestLabel ? ` ← ${guestLabel}` : ""}`,
    selection.reason ? `Why: ${selection.reason}` : null,
    supported ? `Host supports: ${supported}` : null,
  ].filter(Boolean).join("\n");
}
