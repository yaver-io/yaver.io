/**
 * Decode operation-level proof that an HTTP endpoint is a Yaver agent.
 *
 * A 2xx alone is not proof: an SPA/dev server also returns 200 HTML for
 * unknown `/health` paths. Treating that as connected routed later `/tasks`
 * calls into Metro and produced `Unexpected token '<'` in RN-web.
 */
export type AgentHealth = {
  ok: true;
  lifecycleState: string;
  authExpired: boolean;
  hostname?: string;
  version?: string;
};

export async function readAgentHealth(response: Response): Promise<AgentHealth | null> {
  if (!response.ok) return null;
  try {
    const data = await response.clone().json() as Record<string, unknown>;
    if (data.ok !== true || typeof data.lifecycleState !== "string" || !data.lifecycleState.trim()) return null;
    return {
      ok: true,
      lifecycleState: data.lifecycleState,
      authExpired: data.authExpired === true,
      hostname: typeof data.hostname === "string" ? data.hostname : undefined,
      version: typeof data.version === "string" ? data.version : undefined,
    };
  } catch {
    return null;
  }
}
