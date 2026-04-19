// Shim for expo-apple-authentication. The mobile app uses this only
// on iOS; headless never calls these. Provide sentinels so imports
// don't throw.
export const AppleAuthenticationButtonType = { SIGN_IN: 0, CONTINUE: 1, SIGN_UP: 2 };
export const AppleAuthenticationButtonStyle = { WHITE: 0, WHITE_OUTLINE: 1, BLACK: 2 };
export const AppleAuthenticationScope = { FULL_NAME: 0, EMAIL: 1 };
export async function isAvailableAsync() { return false; }
export async function signInAsync(..._args: any[]): Promise<any> {
  throw new Error("Apple sign-in is unavailable in mobile-headless; use signIn({token}) or email+password instead.");
}
export async function refreshAsync(..._args: any[]): Promise<any> { throw new Error("unavailable"); }
export default { AppleAuthenticationButtonType, AppleAuthenticationButtonStyle, AppleAuthenticationScope, isAvailableAsync, signInAsync, refreshAsync };
