export function nativeBuildFailureMessage(buildResult: any): string {
  return [
    buildResult?.phase ? `phase: ${buildResult.phase}` : null,
    buildResult?.error || "Build failed",
    buildResult?.helpHint || null,
    buildResult?.output ? `\n${String(buildResult.output).trim()}` : null,
  ].filter(Boolean).join("\n");
}

export function nativeBuildFailureTitle(buildResult: any): string {
  if (buildResult?.code === "NATIVE_MODULE_INCOMPATIBLE") return "Compatibility Blocked";
  if (buildResult?.code === "BC_VERSION_MISMATCH") return "Hermes Version Mismatch";
  return "Load Failed";
}
