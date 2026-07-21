import { AbstractCloudProvider, ProviderOperationError } from "./abstract";
import type {
  BudgetStatus,
  CostEstimate,
  CostEstimateRequest,
  CreateMachineRequest,
  CreateMachineResult,
  CreateVolumeRequest,
  CreateVolumeResult,
  DeleteMachineRequest,
  DeleteVolumeRequest,
  FirewallRequest,
  ListTaggedResourcesRequest,
  MachineProfile,
  MachineStatusRequest,
  ProviderCapabilities,
  ProviderMachineStatus,
  RegionOption,
  ResolveSkuRequest,
  SkuDecision,
  SnapshotMachineRequest,
  SnapshotResult,
  TaggedResource,
  WakeFromSnapshotRequest,
  WakeFromVolumeRequest,
} from "./types";

const AZURE_ARM = "https://management.azure.com";
const COMPUTE_API = "2026-03-01";
const NETWORK_API = "2025-05-01";

type AzureProviderOptions = {
  bearerToken?: string;
  subscriptionId?: string;
  resourceGroup?: string;
  location?: string;
  armBase?: string;
};

type AzureResource = {
  id?: string;
  name?: string;
  properties?: {
    provisioningState?: string;
    diskState?: string;
    statuses?: Array<{ code?: string; displayStatus?: string }>;
  };
};

// Azure REST sources checked 2026-07-21:
// - Microsoft.Compute/virtualMachines Create Or Update API 2026-03-01
// - Microsoft.Compute/disks Create Or Update API 2026-03-01
// - Microsoft.Network/networkSecurityGroups API 2025-05-01
export class AzureProvider extends AbstractCloudProvider {
  readonly id = "azure" as const;
  private readonly bearerToken?: string;
  private readonly subscriptionId?: string;
  private readonly resourceGroup?: string;
  private readonly location: string;
  private readonly armBase: string;

  constructor(options: AzureProviderOptions = {}) {
    super();
    this.bearerToken = options.bearerToken;
    this.subscriptionId = options.subscriptionId;
    this.resourceGroup = options.resourceGroup;
    this.location = options.location || "westeurope";
    this.armBase = options.armBase || AZURE_ARM;
  }

  describeCapabilities(): ProviderCapabilities {
    return {
      provider: this.id,
      profiles: [],
      capabilities: [
        "cloud-init",
        "docker",
        "systemd",
        "durable-volume",
        "snapshot-fallback",
        "image-boot",
        "provider-status",
        "outbound-relay",
        "stable-endpoint",
      ],
      regions: ["westeurope", "northeurope", "eastus", "westus3"],
      productionEligible: false,
      notes: [
        "Azure adapter is implemented but not placement-eligible until network bootstrap, budget telemetry, cleanup, and live Yaver probes pass.",
      ],
    };
  }

  async listRegions(_profile: MachineProfile): Promise<RegionOption[]> {
    return [
      { id: "westeurope", label: "West Europe" },
      { id: "northeurope", label: "North Europe" },
      { id: "eastus", label: "East US" },
      { id: "westus3", label: "West US 3" },
    ];
  }

  async resolveSku(req: ResolveSkuRequest): Promise<SkuDecision> {
    return {
      sku: req.profile === "linux-runner-gpu" ? "Standard_NC4as_T4_v3" : "Standard_D4s_v5",
      arch: "amd64",
      vcpu: req.profile === "linux-runner-gpu" ? 4 : 4,
      ramGb: req.profile === "linux-runner-gpu" ? 28 : 16,
      diskGb: Math.max(req.diskGb || 128, 128),
      notes: "Default Azure SKU decision; not placement-eligible until probes pass.",
    };
  }

  async estimateCost(_req: CostEstimateRequest): Promise<CostEstimate> {
    return {
      currency: "USD",
      confidence: "unknown",
      notes: "Azure retail pricing is not wired into the provider facade yet.",
    };
  }

  async readBudgetStatus(): Promise<BudgetStatus> {
    const configured = Boolean(this.bearerToken && this.subscriptionId && this.resourceGroup);
    return {
      provider: this.id,
      ok: configured,
      lastSyncedAt: Date.now(),
      reason: configured ? undefined : "AZURE_BEARER_TOKEN, AZURE_SUBSCRIPTION_ID, or AZURE_RESOURCE_GROUP missing",
    };
  }

  async createVolume(req: CreateVolumeRequest): Promise<CreateVolumeResult> {
    this.requireConfig("createVolume");
    const diskName = req.name;
    const disk = await this.request<AzureResource>(
      `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}/providers/Microsoft.Compute/disks/${encodeURIComponent(diskName)}?api-version=${COMPUTE_API}`,
      {
        method: "PUT",
        body: {
          location: req.region || this.location,
          tags: this.azureTags(req.tags),
          sku: { name: "StandardSSD_LRS" },
          properties: {
            creationData: { createOption: "Empty" },
            diskSizeGB: req.sizeGb,
          },
        },
      },
    );
    if (!disk.id) throw this.error("createVolume", "bad_response", "Azure disk API returned no id");
    return { volumeId: disk.id };
  }

  async deleteVolume(req: DeleteVolumeRequest): Promise<void> {
    this.requireConfig("deleteVolume");
    await this.request<void>(`${this.resourcePath(req.volumeId)}?api-version=${COMPUTE_API}`, {
      method: "DELETE",
      okStatuses: [200, 202, 204, 404],
    });
  }

  async createMachine(req: CreateMachineRequest): Promise<CreateMachineResult> {
    this.requireConfig("createMachine");
    const nicId = this.stringOption(req.providerOptions, "networkInterfaceId") || this.stringEnv("AZURE_NETWORK_INTERFACE_ID");
    if (!nicId) {
      throw this.error(
        "createMachine",
        "missing_network_interface",
        "Azure createMachine requires providerOptions.networkInterfaceId or AZURE_NETWORK_INTERFACE_ID until VNet/NIC bootstrap is implemented",
      );
    }
    const adminUsername = this.stringOption(req.providerOptions, "adminUsername") || "yaver";
    const sshPublicKey = this.stringOption(req.providerOptions, "sshPublicKey") || this.stringEnv("AZURE_SSH_PUBLIC_KEY");
    if (!sshPublicKey) {
      throw this.error("createMachine", "missing_ssh_key", "Azure createMachine requires sshPublicKey or AZURE_SSH_PUBLIC_KEY");
    }
    const vm = await this.request<AzureResource>(
      `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}/providers/Microsoft.Compute/virtualMachines/${encodeURIComponent(req.name)}?api-version=${COMPUTE_API}`,
      {
        method: "PUT",
        body: {
          location: req.region || this.location,
          tags: this.azureTags(req.tags),
          properties: {
            hardwareProfile: { vmSize: req.sku },
            osProfile: {
              computerName: req.name.slice(0, 64),
              adminUsername,
              customData: this.base64Utf8(req.userData),
              linuxConfiguration: {
                disablePasswordAuthentication: true,
                ssh: {
                  publicKeys: [
                    {
                      path: `/home/${adminUsername}/.ssh/authorized_keys`,
                      keyData: sshPublicKey,
                    },
                  ],
                },
              },
            },
            storageProfile: {
              imageReference: this.imageReference(req.image),
              osDisk: {
                createOption: "FromImage",
                managedDisk: { storageAccountType: "StandardSSD_LRS" },
              },
              dataDisks: (req.volumeIds || []).map((id, index) => ({
                lun: index,
                createOption: "Attach",
                managedDisk: { id },
              })),
            },
            networkProfile: {
              networkInterfaces: [{ id: nicId, properties: { primary: true } }],
            },
          },
        },
      },
    );
    if (!vm.id) throw this.error("createMachine", "bad_response", "Azure VM API returned no id");
    return {
      cloudResourceId: vm.id,
      providerStatus: vm.properties?.provisioningState,
      serverType: req.sku,
    };
  }

  async createMachineFromImageAndVolume(req: WakeFromVolumeRequest): Promise<CreateMachineResult> {
    return this.createMachine(req);
  }

  async createMachineFromSnapshot(_req: WakeFromSnapshotRequest): Promise<CreateMachineResult> {
    this.unsupported("createMachineFromSnapshot", "Azure snapshot wake needs disk-from-snapshot orchestration before placement eligibility");
  }

  async snapshotMachine(req: SnapshotMachineRequest): Promise<SnapshotResult> {
    this.requireConfig("snapshotMachine");
    throw this.error(
      "snapshotMachine",
      "not_wired",
      `Azure snapshotMachine is not wired yet for ${req.cloudResourceId}; use durable disk path first`,
    );
  }

  async deleteMachine(req: DeleteMachineRequest): Promise<void> {
    this.requireConfig("deleteMachine");
    await this.request<void>(`${this.resourcePath(req.cloudResourceId)}?api-version=${COMPUTE_API}`, {
      method: "DELETE",
      okStatuses: [200, 202, 204, 404],
    });
  }

  async getMachineStatus(req: MachineStatusRequest): Promise<ProviderMachineStatus> {
    this.requireConfig("getMachineStatus");
    const vm = await this.request<AzureResource>(`${this.resourcePath(req.cloudResourceId)}?api-version=${COMPUTE_API}`, {
      method: "GET",
    });
    const raw =
      vm.properties?.statuses?.find((s) => s.code?.startsWith("PowerState/"))?.displayStatus ||
      vm.properties?.provisioningState ||
      "unknown";
    return { status: raw, rawStatus: raw };
  }

  async openFirewall(req: FirewallRequest): Promise<void> {
    this.requireConfig("openFirewall");
    const nsgName = this.stringEnv("AZURE_NETWORK_SECURITY_GROUP");
    if (!nsgName) {
      throw this.error("openFirewall", "missing_nsg", "AZURE_NETWORK_SECURITY_GROUP missing");
    }
    let priority = 1200;
    for (const rule of req.ports) {
      await this.request<void>(
        `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}/providers/Microsoft.Network/networkSecurityGroups/${encodeURIComponent(nsgName)}/securityRules/${encodeURIComponent(`yaver-${rule.protocol}-${rule.port}`)}?api-version=${NETWORK_API}`,
        {
          method: "PUT",
          body: {
            properties: {
              priority: priority++,
              direction: "Inbound",
              access: "Allow",
              protocol: rule.protocol === "tcp" ? "Tcp" : "Udp",
              sourcePortRange: "*",
              destinationPortRange: String(rule.port),
              sourceAddressPrefix: rule.source || "*",
              destinationAddressPrefix: "*",
            },
          },
        },
      );
    }
  }

  async listYaverTaggedResources(_req?: ListTaggedResourcesRequest): Promise<TaggedResource[]> {
    // Must be implemented before production eligibility.
    return [];
  }

  private requireConfig(operation: string): void {
    if (!this.bearerToken || !this.subscriptionId || !this.resourceGroup) {
      throw this.error(operation, "missing_config", "Azure provider credentials/config are not configured");
    }
  }

  private async request<T>(
    path: string,
    options: { method: string; body?: unknown; okStatuses?: number[] },
  ): Promise<T> {
    const res = await fetch(`${this.armBase}${path}`, {
      method: options.method,
      headers: {
        Authorization: `Bearer ${this.bearerToken}`,
        ...(options.body ? { "Content-Type": "application/json" } : {}),
      },
      body: options.body ? JSON.stringify(options.body) : undefined,
    });
    const okStatuses = options.okStatuses || [200, 201, 202, 204];
    if (!okStatuses.includes(res.status)) {
      const text = await res.text();
      throw this.error("request", "provider_http_error", `Azure API ${res.status}: ${text.slice(0, 500)}`);
    }
    if (res.status === 204) return undefined as T;
    return (await res.json().catch(() => undefined)) as T;
  }

  private resourcePath(id: string): string {
    if (id.startsWith("/")) return id;
    if (id.startsWith("https://management.azure.com")) return id.slice("https://management.azure.com".length);
    return `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}/providers/Microsoft.Compute/virtualMachines/${encodeURIComponent(id)}`;
  }

  private imageReference(image: string | number): unknown {
    if (typeof image === "string" && image.startsWith("/subscriptions/")) {
      return { id: image };
    }
    // Portable default. Callers can pass a custom image id for golden images.
    return {
      publisher: "Canonical",
      offer: "0001-com-ubuntu-server-jammy",
      sku: "22_04-lts-gen2",
      version: "latest",
    };
  }

  private azureTags(tags: Record<string, string>): Record<string, string> {
    const out: Record<string, string> = {};
    for (const [key, value] of Object.entries(tags)) {
      out[key.slice(0, 512)] = value.slice(0, 256);
    }
    return out;
  }

  private stringOption(options: Record<string, unknown> | undefined, key: string): string | undefined {
    const value = options?.[key];
    return typeof value === "string" && value.trim() ? value.trim() : undefined;
  }

  private stringEnv(key: string): string | undefined {
    const value = process.env[key];
    return value && value.trim() ? value.trim() : undefined;
  }

  private base64Utf8(value: string): string {
    const bytes = new TextEncoder().encode(value);
    let binary = "";
    for (const byte of bytes) binary += String.fromCharCode(byte);
    return btoa(binary);
  }

  private error(operation: string, code: string, message: string): ProviderOperationError {
    return new ProviderOperationError({ provider: this.id, operation, code, message });
  }
}

export function createAzureProviderFromEnv(): AzureProvider {
  return new AzureProvider({
    bearerToken: process.env.AZURE_BEARER_TOKEN,
    subscriptionId: process.env.AZURE_SUBSCRIPTION_ID,
    resourceGroup: process.env.AZURE_RESOURCE_GROUP,
    location: process.env.AZURE_LOCATION,
  });
}
