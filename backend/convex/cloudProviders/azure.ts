import { AbstractCloudProvider, ProviderOperationError } from "./abstract";
import { getAzureAccessToken, hasRefreshableCredentials } from "./credentials";
import type {
  AttachEgressIpRequest,
  BudgetStatus,
  EgressIpReservation,
  ReleaseEgressIpRequest,
  ReserveEgressIpRequest,
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
        "tagged-cleanup",
        "stable-egress-ip",
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
    const configured = Boolean(
      (this.bearerToken || hasRefreshableCredentials("azure")) && this.subscriptionId && this.resourceGroup,
    );
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
    let nicId = this.stringOption(req.providerOptions, "networkInterfaceId") || this.stringEnv("AZURE_NETWORK_INTERFACE_ID");
    if (!nicId) {
      // Bootstrap a NIC (with its own public IP) in the configured subnet.
      // Requiring a pre-made NIC made this adapter unusable for real placement,
      // and a NIC is per-VM anyway — sharing one across VMs is not valid.
      nicId = await this.ensureNetworkInterface(req);
    }
    if (!nicId) {
      throw this.error(
        "createMachine",
        "missing_network_interface",
        "Azure createMachine could not resolve a network interface. Set AZURE_SUBNET_ID (or providerOptions.subnetId) so a NIC can be created, or pin AZURE_NETWORK_INTERFACE_ID.",
      );
    }
    const osDiskId = this.stringOption(req.providerOptions, "osDiskId");
    const adminUsername = this.stringOption(req.providerOptions, "adminUsername") || "yaver";
    const sshPublicKey = this.stringOption(req.providerOptions, "sshPublicKey") || this.stringEnv("AZURE_SSH_PUBLIC_KEY");
    if (!sshPublicKey && !osDiskId) {
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
            // An attached OS disk already carries its users/keys; Azure rejects
            // an osProfile in that case.
            ...(osDiskId ? {} : { osProfile: {
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
            } }),
            storageProfile: {
              // Attaching a restored OS disk and provisioning from an image are
              // mutually exclusive: Azure rejects an imageReference alongside an
              // Attach osDisk.
              ...(osDiskId ? {} : { imageReference: this.imageReference(req.image) }),
              osDisk: osDiskId
                ? { createOption: "Attach", osType: "Linux", managedDisk: { id: osDiskId } }
                : {
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
    // The VM PUT never carries an address, and the caller hard-throws on a
    // missing IP *after* the VM exists ⇒ guaranteed billing orphan. Resolve it
    // by walking NIC → ipConfigurations → publicIPAddress, bounded.
    const serverIp = await this.resolvePublicIp(nicId);
    return {
      cloudResourceId: vm.id,
      serverIp,
      providerStatus: vm.properties?.provisioningState,
      serverType: req.sku,
    };
  }

  async createMachineFromImageAndVolume(req: WakeFromVolumeRequest): Promise<CreateMachineResult> {
    return this.createMachine(req);
  }

  /**
   * Azure cannot boot a VM straight from a snapshot: a snapshot is not an
   * image. The real sequence is snapshot → managed disk → VM with that disk
   * ATTACHED as its OS disk.
   *
   * That intermediate disk is a second failure window — if VM creation throws
   * after the disk exists, the disk keeps billing with nothing referencing it.
   * So the disk is reclaimed here on failure rather than left for the sweep.
   */
  async createMachineFromSnapshot(req: WakeFromSnapshotRequest): Promise<CreateMachineResult> {
    this.requireConfig("createMachineFromSnapshot");
    const location = req.region || this.location;
    const diskName = `${req.name}-osdisk`.slice(0, 78);
    const diskId =
      `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}` +
      `/providers/Microsoft.Compute/disks/${diskName}`;

    await this.request<AzureResource>(`${diskId}?api-version=${COMPUTE_API}`, {
      method: "PUT",
      body: {
        location,
        tags: this.azureTags({ managed: "true", service: "yaver-cloud-machine" }),
        properties: {
          creationData: { createOption: "Copy", sourceResourceId: req.snapshotId },
          osType: "Linux",
        },
      },
    });

    try {
      // `providerOptions.osDiskId` tells createMachine to ATTACH this disk
      // instead of provisioning a fresh one FromImage.
      return await this.createMachine({
        ...req,
        providerOptions: { ...(req.providerOptions ?? {}), osDiskId: diskId },
      });
    } catch (e) {
      // Do not strand the disk we just paid to create.
      try {
        await this.request<void>(`${diskId}?api-version=${COMPUTE_API}`, {
          method: "DELETE",
          okStatuses: [200, 202, 204, 404],
        });
      } catch { /* reported by the orphan sweep if this also fails */ }
      throw e;
    }
  }

  /**
   * Capture the VM's OS disk as a managed snapshot.
   *
   * ⚠️ COST: an Azure snapshot bills for stored bytes until deleted, and it
   * outlives the VM by design. It is therefore a satellite that decommission
   * must reclaim — the same shape as the Hetzner volume that leaked.
   */
  async snapshotMachine(req: SnapshotMachineRequest): Promise<SnapshotResult> {
    this.requireConfig("snapshotMachine");
    const vm = await this.request<{ location?: string; properties?: { storageProfile?: { osDisk?: { managedDisk?: { id?: string } } } } }>(
      `${req.cloudResourceId}?api-version=${COMPUTE_API}`, { method: "GET" },
    );
    const sourceDiskId = vm.properties?.storageProfile?.osDisk?.managedDisk?.id;
    if (!sourceDiskId) {
      throw this.error("snapshotMachine", "bad_response", "Azure VM reported no managed OS disk to snapshot");
    }
    const name = `yaver-${req.label}-${Date.now()}`.replace(/[^A-Za-z0-9._-]/g, "-").slice(0, 78);
    const snapshotId =
      `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}` +
      `/providers/Microsoft.Compute/snapshots/${name}`;
    await this.request<AzureResource>(`${snapshotId}?api-version=${COMPUTE_API}`, {
      method: "PUT",
      body: {
        location: vm.location || this.location,
        tags: this.azureTags({ managed: "true", service: "yaver-snapshot" }),
        properties: { creationData: { createOption: "Copy", sourceResourceId: sourceDiskId } },
      },
    });
    return { snapshotId };
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
    // Priority must be DETERMINISTIC per rule. Resetting a counter to 1200 on
    // every call meant a second call with different ports reassigned the same
    // priorities to different rules, so the effective firewall depended on
    // call order. Derive it from the port instead: same rule ⇒ same priority,
    // and a repeat call is a genuine idempotent overwrite.
    for (const rule of req.ports) {
      const priority = 1200 + ((rule.port + (rule.protocol === "udp" ? 1 : 0)) % 2000);
      await this.request<void>(
        `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}/providers/Microsoft.Network/networkSecurityGroups/${encodeURIComponent(nsgName)}/securityRules/${encodeURIComponent(`yaver-${rule.protocol}-${rule.port}`)}?api-version=${NETWORK_API}`,
        {
          method: "PUT",
          body: {
            properties: {
              priority,
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

  /**
   * Every Yaver-tagged resource in the resource group. Azure Resource Graph
   * would be richer, but the plain resource-group listing needs no extra
   * permission and is enough to answer the only question that matters: does
   * the provider hold something Convex does not know about?
   */
  async listYaverTaggedResources(req?: ListTaggedResourcesRequest): Promise<TaggedResource[]> {
    this.requireConfig("listYaverTaggedResources");
    const wanted = this.azureTags(req?.tags ?? { yaver: "managed" });
    const [tagKey, tagValue] = Object.entries(wanted)[0] ?? ["yaver", "managed"];
    try {
      const j = await this.request<{ value?: Array<Record<string, unknown>> }>(
        `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}/resources` +
          `?api-version=2021-04-01&$filter=${encodeURIComponent(`tagName eq '${tagKey}' and tagValue eq '${tagValue}'`)}`,
        { method: "GET" },
      );
      return (j.value ?? []).flatMap((row) => {
        const id = typeof row.id === "string" ? row.id : "";
        if (!id) return [];
        const t = String(row.type ?? "").toLowerCase();
        const type: TaggedResource["type"] =
          t.includes("virtualmachines") ? "machine"
          : t.includes("disks") ? "volume"
          : t.includes("snapshots") ? "snapshot"
          : t.includes("publicipaddresses") ? "ip"
          : t.includes("networksecuritygroups") ? "firewall"
          : "unknown";
        return [{
          id,
          type,
          provider: this.id,
          tags: (row.tags as Record<string, string> | undefined) ?? {},
        }];
      });
    } catch {
      return [];
    }
  }

  // ─── Stable egress identity (Standard static Public IP) ─────────────────
  // Standard SKU is required: with a Standard public IP on the NIC, outbound
  // traffic is SOURCED from that address. A Basic SKU (or an LB frontend) does
  // not give the same guarantee, so it would not satisfy the contract.

  async reserveEgressIp(req: ReserveEgressIpRequest): Promise<EgressIpReservation> {
    this.requireConfig("reserveEgressIp");
    const location = req.scope || req.region || this.location;
    const created = await this.request<AzureResource & { properties?: { ipAddress?: string } }>(
      `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}/providers/Microsoft.Network/publicIPAddresses/${encodeURIComponent(req.name)}?api-version=${NETWORK_API}`,
      {
        method: "PUT",
        body: {
          location,
          sku: { name: "Standard" },
          tags: this.azureTags({ ...req.tags, "yaver-role": "egress" }),
          properties: { publicIPAllocationMethod: "Static", publicIPAddressVersion: "IPv4" },
        },
      },
    );
    const id = created.id
      || `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}/providers/Microsoft.Network/publicIPAddresses/${req.name}`;
    let address = String(created.properties?.ipAddress ?? "");
    for (let i = 0; i < 10 && !address; i++) {
      await new Promise((r) => setTimeout(r, 2000));
      try {
        const live = await this.request<{ properties?: { ipAddress?: string } }>(
          `${id}?api-version=${NETWORK_API}`, { method: "GET" },
        );
        address = String(live.properties?.ipAddress ?? "");
      } catch { /* not allocated yet */ }
    }
    if (!address) {
      throw this.error("reserveEgressIp", "bad_response", "Azure public IP did not receive an address");
    }
    return { egressIpId: id, address, scope: location, idleCostUsdPerMonth: 3.6 };
  }

  async attachEgressIp(req: AttachEgressIpRequest): Promise<void> {
    this.requireConfig("attachEgressIp");
    // Azure binds a public IP through the NIC's ipConfiguration, not the VM.
    const nicId = this.stringOption(req.providerOptions, "networkInterfaceId")
      || this.stringEnv("AZURE_NETWORK_INTERFACE_ID");
    if (!nicId) {
      throw this.error("attachEgressIp", "missing_network_interface", "Azure attachEgressIp requires networkInterfaceId");
    }
    const nic = await this.request<{ location?: string; properties?: { ipConfigurations?: Array<Record<string, any>> } }>(
      `${nicId}?api-version=${NETWORK_API}`, { method: "GET" },
    );
    const configs = nic.properties?.ipConfigurations ?? [];
    if (!configs.length) {
      throw this.error("attachEgressIp", "bad_response", "Azure NIC has no ipConfigurations");
    }
    configs[0].properties = { ...(configs[0].properties ?? {}), publicIPAddress: { id: req.egressIpId } };
    await this.request<void>(`${nicId}?api-version=${NETWORK_API}`, {
      method: "PUT",
      body: { location: nic.location, properties: { ipConfigurations: configs } },
    });
  }

  async releaseEgressIp(req: ReleaseEgressIpRequest): Promise<void> {
    this.requireConfig("releaseEgressIp");
    await this.request<void>(`${req.egressIpId}?api-version=${NETWORK_API}`, {
      method: "DELETE",
      okStatuses: [200, 202, 204, 404],
    });
  }

  /** NIC → ipConfigurations[0] → publicIPAddress → ipAddress, bounded. */
  private async resolvePublicIp(nicId: string): Promise<string | undefined> {
    for (let i = 0; i < 10; i++) {
      try {
        const nic = await this.request<{ properties?: { ipConfigurations?: Array<{ properties?: { publicIPAddress?: { id?: string } } }> } }>(
          `${nicId}?api-version=${NETWORK_API}`, { method: "GET" },
        );
        const pipId = nic.properties?.ipConfigurations?.[0]?.properties?.publicIPAddress?.id;
        if (pipId) {
          const pip = await this.request<{ properties?: { ipAddress?: string } }>(
            `${pipId}?api-version=${NETWORK_API}`, { method: "GET" },
          );
          const addr = pip.properties?.ipAddress;
          if (addr) return addr;
        }
      } catch { /* keep polling within the bound */ }
      await new Promise((r) => setTimeout(r, 2000));
    }
    return undefined;
  }

  /**
   * Fresh ARM token per request — see the GCP note. A provider instance can
   * outlive the token, and a 401 mid-provision can strand a created VM.
   */
  private async token(): Promise<string> {
    if (this.bearerToken) return this.bearerToken; // manual probing override
    return getAzureAccessToken();
  }

  /**
   * Create a per-VM NIC with its own Standard public IP, inside a subnet the
   * operator has designated.
   *
   * Deliberately does NOT create the VNet/subnet: that is shared, account-wide
   * infrastructure which may not belong to Yaver (resource-boundary rule), and
   * debris from a half-failed VNet creation is far harder to reason about than
   * a missing-config error. The subnet is config; the NIC is per-workspace and
   * therefore ours to manage.
   *
   * ⚠️ The NIC and its public IP are SATELLITES: they outlive the VM unless
   * reclaimed. They carry the standard tags so listYaverTaggedResources sees
   * them and the orphan sweep can report them.
   */
  private async ensureNetworkInterface(req: CreateMachineRequest): Promise<string | undefined> {
    const subnetId = this.stringOption(req.providerOptions, "subnetId") || this.stringEnv("AZURE_SUBNET_ID");
    if (!subnetId) return undefined;
    const location = req.region || this.location;
    const base = `/subscriptions/${this.subscriptionId}/resourceGroups/${this.resourceGroup}/providers/Microsoft.Network`;
    const pipName = `${req.name}-pip`.slice(0, 78);
    const nicName = `${req.name}-nic`.slice(0, 78);
    const pipId = `${base}/publicIPAddresses/${pipName}`;
    const nicId = `${base}/networkInterfaces/${nicName}`;

    await this.request<AzureResource>(`${pipId}?api-version=${NETWORK_API}`, {
      method: "PUT",
      body: {
        location,
        sku: { name: "Standard" },
        tags: this.azureTags({ managed: "true", service: "yaver-cloud-machine" }),
        properties: { publicIPAllocationMethod: "Static", publicIPAddressVersion: "IPv4" },
      },
    });
    await this.request<AzureResource>(`${nicId}?api-version=${NETWORK_API}`, {
      method: "PUT",
      body: {
        location,
        tags: this.azureTags({ managed: "true", service: "yaver-cloud-machine" }),
        properties: {
          ipConfigurations: [{
            name: "ipconfig1",
            properties: {
              subnet: { id: subnetId },
              privateIPAllocationMethod: "Dynamic",
              publicIPAddress: { id: pipId },
            },
          }],
        },
      },
    });
    return nicId;
  }

  private requireConfig(operation: string): void {
    // A refreshable client-credentials app counts as configured; a pasted
    // bearer token also works but only for manual probing (it expires).
    const canAuth = this.bearerToken || hasRefreshableCredentials("azure");
    if (!canAuth || !this.subscriptionId || !this.resourceGroup) {
      throw this.error(
        operation,
        "missing_config",
        "Azure provider is not configured: set AZURE_TENANT_ID, AZURE_CLIENT_ID, AZURE_CLIENT_SECRET, AZURE_SUBSCRIPTION_ID and AZURE_RESOURCE_GROUP in Convex env",
      );
    }
  }

  private async request<T>(
    path: string,
    options: { method: string; body?: unknown; okStatuses?: number[] },
  ): Promise<T> {
    const res = await fetch(`${this.armBase}${path}`, {
      method: options.method,
      headers: {
        Authorization: `Bearer ${await this.token()}`,
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
