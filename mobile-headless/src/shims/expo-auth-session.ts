// Shim for expo-auth-session. Used for Google/Microsoft OAuth in the
// mobile app. Headless tests use direct-token sign-in instead.
export function useAuthRequest(): any[] { return [null, null, async () => null]; }
export function makeRedirectUri(..._args: any[]) { return "yaver://oauth-callback"; }
export const AuthRequest = class { constructor() {} };
export const ResponseType = { Code: "code", Token: "token", IdToken: "id_token" };
export const Prompt = { None: "none", Login: "login" };
export async function exchangeCodeAsync(..._args: any[]) { throw new Error("unavailable"); }
export default { useAuthRequest, makeRedirectUri, AuthRequest, ResponseType, Prompt, exchangeCodeAsync };
