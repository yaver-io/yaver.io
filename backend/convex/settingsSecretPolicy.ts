export const RAW_SETTINGS_SECRET_FIELDS = [
  "speechApiKey",
  "openAiApiKey",
  "glmApiKey",
  "anthropicApiKey",
  "deepseekApiKey",
  "githubToken",
  "gitlabToken",
  "bitbucketToken",
] as const;

const PUBLISHER_PROFILE_FORBIDDEN_FIELDS = [
  "password", "apiKey", "privateKey", "signingKey", "taxId", "bankAccount", "cardNumber",
] as const;

/** Settings is a preference/metadata API, never a credential transport. */
export function rawSecretFieldsInSettings(value: unknown): string[] {
  if (!value || typeof value !== "object" || Array.isArray(value)) return [];
  const record = value as Record<string, unknown>;
  const found: string[] = RAW_SETTINGS_SECRET_FIELDS.filter((field) => {
    const candidate = record[field];
    return typeof candidate === "string" && candidate.trim().length > 0;
  });
  const publisher = record.publisherProfile;
  if (publisher && typeof publisher === "object" && !Array.isArray(publisher)) {
    const profile = publisher as Record<string, unknown>;
    for (const field of PUBLISHER_PROFILE_FORBIDDEN_FIELDS) {
      const candidate = profile[field];
      if (typeof candidate === "string" && candidate.trim()) found.push(`publisherProfile.${field}`);
    }
  }
  return found;
}

export function settingsWithoutSecrets<T extends Record<string, unknown>>(settings: T): T {
  const safe = { ...settings };
  for (const field of RAW_SETTINGS_SECRET_FIELDS) delete safe[field];
  return safe;
}
