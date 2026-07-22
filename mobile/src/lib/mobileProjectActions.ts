export type MobileProjectAction = {
  label: string;
  target: string;
  type: string;
  framework?: string;
  platform?: string;
  command?: string;
  icon?: string;
  supported?: boolean;
  reason?: string;
};

export function isHermesMobileFramework(framework?: string): boolean {
  return framework === "expo" || framework === "react-native";
}

export function isYaverSelfDevelopmentProject(project?: string, path?: string, repoURL?: string): boolean {
  const haystack = `${project || ""} ${path || ""} ${repoURL || ""}`.toLowerCase();
  return haystack.includes("yaver.io") ||
    haystack.includes("yaver-io/yaver") ||
    haystack.includes("io.yaver.mobile");
}

export const YAVER_SELF_DEV_HERMES_BLOCK_REASON =
  "Yaver developing Yaver must use WebRTC/browser preview. Loading Yaver into Yaver via Hermes puts two shake/exit owners in one process, so the preview can trap the host app.";

export function guardYaverSelfDevelopmentActions(
  actions: MobileProjectAction[],
  project?: string,
  path?: string,
  repoURL?: string,
): MobileProjectAction[] {
  if (!isYaverSelfDevelopmentProject(project, path, repoURL)) {
    return actions;
  }

  const guarded = actions.map((action) => {
    if ((action.type === "open-native" || action.type === "compile-hermes") && isHermesMobileFramework(action.framework)) {
      return {
        ...action,
        supported: false,
        reason: YAVER_SELF_DEV_HERMES_BLOCK_REASON,
      };
    }
    return action;
  });

  const firstRemoteRuntime = guarded.findIndex((action) => action.type === "remote-runtime" && isHermesMobileFramework(action.framework));
  if (firstRemoteRuntime <= 0) {
    return guarded;
  }

  const [remoteRuntime] = guarded.splice(firstRemoteRuntime, 1);
  return [remoteRuntime, ...guarded];
}
