export interface TerminalSSHProfile {
  shell?: string;
  tmux?: boolean;
  tmuxSession?: string;
}

const SAFE_SHELLS = new Set(["default", "bash", "zsh", "fish"]);
const SAFE_TMUX_SESSION = /^[a-zA-Z0-9_.-]{1,48}$/;

/** Add the same structured shell profile understood by the agent's
 * /ws/terminal route. No arbitrary command or unchecked session text reaches
 * the URL; the server validates the values again before launching anything. */
export function addTerminalSSHProfile(
  params: URLSearchParams,
  profile?: TerminalSSHProfile | null,
): URLSearchParams {
  if (!profile) return params;
  const shell = SAFE_SHELLS.has(String(profile.shell)) ? String(profile.shell) : "default";
  params.set("profile_shell", shell);
  if (profile.tmux) {
    const session = String(profile.tmuxSession || "").trim();
    params.set("profile_tmux", SAFE_TMUX_SESSION.test(session) ? session : "yaver");
  }
  return params;
}
