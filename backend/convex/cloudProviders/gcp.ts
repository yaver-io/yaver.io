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

const GCP_COMPUTE = "https://compute.googleapis.com/compute/v1";

type GcpProviderOptions = {
  accessToken?: string;
  projectId?: string;
  zone?: string;
  computeBase?: string;
};

type GcpResource = {
  id?: string | number;
  name?: string;
  selfLink?: string;
  status?: string;
  networkInterfaces?: Array<{ accessConfigs?: Array<{ natIP?: string }> }>;
};

// GCP REST sources checked 2026-07-21:
// - Compute Engine instances.insert/delete/get
// - Compute Engine disks.insert/delete
export class GcpProvider extends AbstractCloudProvider {
  readonly id = "gcp" as const;
  private readonly accessToken?: string;
  private readonly projectId?: string;
  private readonly zone: string;
  private readonly computeBase: string;

  constructor(options: GcpProviderOptions = {}) {
    super();
    this.accessToken = options.accessToken;
    this.projectId = options.projectId;
    this.zone = options.zone || "europe-west4-a";
    this.computeBase = options.computeBase || GCP_COMPUTE;
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
        "budget-telemetry",
        "first-party-inference",
      ],
      regions: ["europe-west4", "europe-west1", "us-central1"],
      productionEligible: false,
      notes: [
        "GCP adapter is implemented but not placement-eligible until firewall/bootstrap, cleanup, budget telemetry, and live Yaver probes pass.",
      ],
    };
  }

  async listRegions(_profile: MachineProfile): Promise<RegionOption[]> {
    return [
      { id: "europe-west4", label: "Netherlands" },
      { id: "europe-west1", label: "Belgium" },
      { id: "us-central1", label: "Iowa" },
    ];
  }

  async resolveSku(req: ResolveSkuRequest): Promise<SkuDecision> {
    return {
      sku: req.profile === "linux-runner-gpu" ? "n1-standard-4" : "e2-standard-4",
      arch: "amd64",
      vcpu: 4,
      ramGb: req.profile === "linux-runner-gpu" ? 15 : 16,
      diskGb: Math.max(req.diskGb || 128, 128),
      notes: "Default GCP SKU decision; not placement-eligible until probes pass.",
    };
  }

  async estimateCost(_req: CostEstimateRequest): Promise<CostEstimate> {
    return {
      currency: "USD",
      confidence: "unknown",
      notes: "GCP pricing is not wired into the provider facade yet.",
    };
  }

  async readBudgetStatus(): Promise<BudgetStatus> {
    const configured = Boolean(this.accessToken && this.projectId);
    return {
      provider: this.id,
      ok: configured,
      lastSyncedAt: Date.now(),
      reason: configured ? undefined : "GCP_ACCESS_TOKEN or GCP_PROJECT_ID missing",
    };
  }

  async createVolume(req: CreateVolumeRequest): Promise<CreateVolumeResult> {
    this.requireConfig("createVolume");
    const disk = await this.request<GcpResource>(
      `/projects/${this.projectId}/zones/${this.zone}/disks`,
      {
        method: "POST",
        body: {
          name: req.name,
          sizeGb: String(req.sizeGb),
          type: `zones/${this.zone}/diskTypes/pd-balanced`,
          labels: this.gcpLabels(req.tags),
        },
      },
    );
    return { volumeId: disk.selfLink || `projects/${this.projectId}/zones/${this.zone}/disks/${req.name}` };
  }

  async deleteVolume(req: DeleteVolumeRequest): Promise<void> {
    this.requireConfig("deleteVolume");
    await this.request<void>(this.toGcpPath(req.volumeId, "disks"), {
      method: "DELETE",
      okStatuses: [200, 202, 204, 404],
    });
  }

  async createMachine(req: CreateMachineRequest): Promise<CreateMachineResult> {
    this.requireConfig("createMachine");
    const diskAttachments = (req.volumeIds || []).map((id, index) => ({
      autoDelete: false,
      boot: false,
      deviceName: `yaver-data-${index}`,
      mode: "READ_WRITE",
      source: id,
      type: "PERSISTENT",
    }));
    const instance = await this.request<GcpResource>(
      `/projects/${this.projectId}/zones/${this.zone}/instances`,
      {
        method: "POST",
        body: {
          name: req.name,
          machineType: `zones/${this.zone}/machineTypes/${req.sku}`,
          labels: this.gcpLabels(req.tags),
          metadata: {
            items: [
              { key: "user-data", value: req.userData },
              { key: "startup-script", value: req.userData },
            ],
          },
          disks: [
            {
              autoDelete: true,
              boot: true,
              initializeParams: {
                sourceImage: this.imageReference(req.image),
                diskSizeGb: "64",
                diskType: `zones/${this.zone}/diskTypes/pd-balanced`,
              },
              type: "PERSISTENT",
            },
            ...diskAttachments,
          ],
          networkInterfaces: [
            {
              network: this.stringOption(req.providerOptions, "network") || "global/networks/default",
              accessConfigs: [{ name: "External NAT", type: "ONE_TO_ONE_NAT" }],
            },
          ],
        },
      },
    );
    return {
      cloudResourceId: instance.selfLink || `projects/${this.projectId}/zones/${this.zone}/instances/${req.name}`,
      serverIp: instance.networkInterfaces?.[0]?.accessConfigs?.[0]?.natIP,
      providerStatus: instance.status,
      serverType: req.sku,
    };
  }

  async createMachineFromImageAndVolume(req: WakeFromVolumeRequest): Promise<CreateMachineResult> {
    return this.createMachine(req);
  }

  async createMachineFromSnapshot(_req: WakeFromSnapshotRequest): Promise<CreateMachineResult> {
    this.unsupported("createMachineFromSnapshot", "GCP snapshot wake needs disk-from-snapshot orchestration before placement eligibility");
  }

  async snapshotMachine(req: SnapshotMachineRequest): Promise<SnapshotResult> {
    this.requireConfig("snapshotMachine");
    throw this.error(
      "snapshotMachine",
      "not_wired",
      `GCP snapshotMachine is not wired yet for ${req.cloudResourceId}; use durable disk path first`,
    );
  }

  async deleteMachine(req: DeleteMachineRequest): Promise<void> {
    this.requireConfig("deleteMachine");
    await this.request<void>(this.toGcpPath(req.cloudResourceId, "instances"), {
      method: "DELETE",
      okStatuses: [200, 202, 204, 404],
    });
  }

  async getMachineStatus(req: MachineStatusRequest): Promise<ProviderMachineStatus> {
    this.requireConfig("getMachineStatus");
    const instance = await this.request<GcpResource>(this.toGcpPath(req.cloudResourceId, "instances"), {
      method: "GET",
    });
    const raw = instance.status || "unknown";
    return { status: raw, rawStatus: raw };
  }

  async openFirewall(req: FirewallRequest): Promise<void> {
    this.requireConfig("openFirewall");
    for (const port of req.ports) {
      await this.request<void>(
        `/projects/${this.projectId}/global/firewalls`,
        {
          method: "POST",
          okStatuses: [200, 201, 202, 409],
          body: {
            name: `yaver-${port.protocol}-${port.port}`,
            network: "global/networks/default",
            direction: "INGRESS",
            allowed: [{ IPProtocol: port.protocol, ports: [String(port.port)] }],
            sourceRanges: [port.source || "0.0.0.0/0"],
            targetTags: ["yaver-managed"],
            description: "Managed by Yaver provider adapter",
          },
        },
      );
    }
  }

  async listYaverTaggedResources(_req?: ListTaggedResourcesRequest): Promise<TaggedResource[]> {
    return [];
  }

  private requireConfig(operation: string): void {
    if (!this.accessToken || !this.projectId) {
      throw this.error(operation, "missing_config", "GCP provider credentials/config are not configured");
    }
  }

  private async request<T>(
    path: string,
    options: { method: string; body?: unknown; okStatuses?: number[] },
  ): Promise<T> {
    const res = await fetch(`${this.computeBase}${path}`, {
      method: options.method,
      headers: {
        Authorization: `Bearer ${this.accessToken}`,
        ...(options.body ? { "Content-Type": "application/json" } : {}),
      },
      body: options.body ? JSON.stringify(options.body) : undefined,
    });
    const okStatuses = options.okStatuses || [200, 201, 202, 204];
    if (!okStatuses.includes(res.status)) {
      const text = await res.text();
      throw this.error("request", "provider_http_error", `GCP API ${res.status}: ${text.slice(0, 500)}`);
    }
    if (res.status === 204) return undefined as T;
    return (await res.json().catch(() => undefined)) as T;
  }

  private imageReference(image: string | number): string {
    if (typeof image === "string" && image.startsWith("projects/")) return image;
    if (typeof image === "string" && image.startsWith("https://")) return image;
    return "projects/ubuntu-os-cloud/global/images/family/ubuntu-2204-lts";
  }

  private toGcpPath(resourceId: string, kind: "instances" | "disks"): string {
    if (resourceId.startsWith("https://www.googleapis.com/compute/v1")) {
      return resourceId.slice("https://www.googleapis.com/compute/v1".length);
    }
    if (resourceId.startsWith("projects/")) return `/${resourceId}`;
    return `/projects/${this.projectId}/zones/${this.zone}/${kind}/${encodeURIComponent(resourceId)}`;
  }

  private gcpLabels(tags: Record<string, string>): Record<string, string> {
    const labels: Record<string, string> = {};
    for (const [rawKey, rawValue] of Object.entries(tags)) {
      const key = rawKey.toLowerCase().replace(/[^a-z0-9_-]/g, "-").slice(0, 63);
      const value = rawValue.toLowerCase().replace(/[^a-z0-9_-]/g, "-").slice(0, 63);
      if (key && value) labels[key] = value;
    }
    return labels;
  }

  private stringOption(options: Record<string, unknown> | undefined, key: string): string | undefined {
    const value = options?.[key];
    return typeof value === "string" && value.trim() ? value.trim() : undefined;
  }

  private error(operation: string, code: string, message: string): ProviderOperationError {
    return new ProviderOperationError({ provider: this.id, operation, code, message });
  }
}

export function createGcpProviderFromEnv(): GcpProvider {
  return new GcpProvider({
    accessToken: process.env.GCP_ACCESS_TOKEN,
    projectId: process.env.GCP_PROJECT_ID,
    zone: process.env.GCP_ZONE,
  });
}
