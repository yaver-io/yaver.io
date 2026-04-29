export type GuestCrashReport = {
  timestamp?: string;
  phase?: string;
  message?: string;
  moduleName?: string | null;
  sourceURL?: string | null;
  bundlePath?: string | null;
  appVersion?: string | null;
  appBuild?: string | null;
};

export function shouldShowGuestCrashReport(report: GuestCrashReport | null | undefined): boolean {
  if (!report) return false;
  return Boolean(report.message || report.phase || report.moduleName);
}

export function formatGuestCrashReport(report: GuestCrashReport | null | undefined): string[] {
  if (!report) return [];
  const lines: string[] = [];
  if (report.phase) {
    lines.push(`phase: ${report.phase}`);
  }
  if (report.message) {
    lines.push(report.message);
  }
  if (report.moduleName) {
    lines.push(`module: ${report.moduleName}`);
  }
  if (report.appVersion || report.appBuild) {
    const version = [report.appVersion, report.appBuild ? `(${report.appBuild})` : null]
      .filter(Boolean)
      .join(" ");
    if (version) lines.push(`yaver: ${version}`);
  }
  return lines;
}
