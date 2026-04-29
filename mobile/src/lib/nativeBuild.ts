export function nativeBuildFailureMessage(buildResult: any): string {
  const lines = [
    buildResult?.phase ? `phase: ${buildResult.phase}` : null,
    compatibilitySummary(buildResult) || buildResult?.error || "Build failed",
    compatibilityDetails(buildResult),
    buildResult?.helpHint || null,
  ].filter(Boolean);
  return lines.join("\n");
}

export function nativeBuildFailureTitle(buildResult: any): string {
  if (buildResult?.code === "NATIVE_MODULE_INCOMPATIBLE") return "Compatibility Blocked";
  if (buildResult?.code === "NATIVE_MODULE_VERSION_MISMATCH") return "Compatibility Blocked";
  if (buildResult?.code === "REACT_VERSION_MISMATCH") return "Compatibility Blocked";
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
  return null;
}
