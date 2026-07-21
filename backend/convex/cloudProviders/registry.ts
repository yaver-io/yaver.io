import { AzureProvider } from "./azure";
import { AwsProvider } from "./aws";
import { GcpProvider } from "./gcp";
import { HetznerProvider, createHetznerProvider } from "./hetzner";
import { gcpProjectIdFromEnv } from "./credentials";
import type { ProviderCapabilities } from "./types";

export type ManagedCloudProviderRegistry = {
  computeProviders: Array<HetznerProvider | GcpProvider | AwsProvider | AzureProvider>;
  capabilities: ProviderCapabilities[];
};

export function createManagedCloudProviderRegistry(env: Record<string, string | undefined> = process.env): ManagedCloudProviderRegistry {
  const computeProviders: Array<HetznerProvider | GcpProvider | AwsProvider | AzureProvider> = [];
  if (env.HCLOUD_TOKEN) {
    computeProviders.push(createHetznerProvider(env.HCLOUD_TOKEN));
  }
  computeProviders.push(new GcpProvider({
    // accessToken is the manual-probing override only; production authenticates
    // through GCP_SERVICE_ACCOUNT_JSON inside the provider (see credentials.ts).
    accessToken: env.GCP_ACCESS_TOKEN,
    projectId: env.GCP_PROJECT_ID || gcpProjectIdFromEnv(env),
    zone: env.GCP_ZONE,
  }));
  computeProviders.push(new AwsProvider({
    accessKeyId: env.AWS_ACCESS_KEY_ID,
    secretAccessKey: env.AWS_SECRET_ACCESS_KEY,
    sessionToken: env.AWS_SESSION_TOKEN,
    region: env.AWS_REGION,
  }));
  computeProviders.push(new AzureProvider({
    bearerToken: env.AZURE_BEARER_TOKEN,
    subscriptionId: env.AZURE_SUBSCRIPTION_ID,
    resourceGroup: env.AZURE_RESOURCE_GROUP,
    location: env.AZURE_LOCATION,
  }));
  return {
    computeProviders,
    capabilities: computeProviders.map((provider) => provider.describeCapabilities()),
  };
}
