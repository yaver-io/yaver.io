export const DOGFOOD_MODE_EMAIL = "kivanc.cakmak@icloud.com";

export function isDogfoodModeUser(email?: string | null): boolean {
  return (email ?? "").trim().toLowerCase() === DOGFOOD_MODE_EMAIL;
}

export function dogfoodModeLabel(email?: string | null): string {
  return isDogfoodModeUser(email) ? "Dogfood" : "Vibe";
}
