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

const HETZNER_API = "https://api.hetzner.cloud/v1";

type HetznerProviderOptions = {
  token: string;
  apiBase?: string;
};

type HetznerServerResponse = {
  server?: {
    id?: number;
    status?: string;
    public_net?: { ipv4?: { ip?: string } };
  };
};

export class HetznerProvider extends AbstractCloudProvider {
  readonly id = "hetzner" as const;
  private readonly token: string;
  private readonly apiBase: string;

  constructor(options: HetznerProviderOptions) {
    super();
    this.token = options.token;
    this.apiBase = options.apiBase || HETZNER_API;
  }

  describeCapabilities(): ProviderCapabilities {
    return {
      provider: this.id,
      profiles: ["linux-runner"],
      capabilities: [
        "cloud-init",
        "docker",
        "systemd",
        "durable-volume",
        "snapshot-fallback",
        "image-boot",
        "delete-stops-compute-spend",
        "provider-status",
        "tagged-cleanup",
        "outbound-relay",
        "stable-endpoint",
      ],
      regions: ["eu", "us"],
      productionEligible: true,
      notes: [
        "Baseline Yaver managed compute provider.",
        "WebRTC, Redroid, and Yaver Serverless host profiles require explicit probes before eligibility.",
      ],
    };
  }

  async listRegions(_profile: MachineProfile): Promise<RegionOption[]> {
    return [
      { id: "eu", label: "Europe" },
      { id: "us", label: "United States" },
    ];
  }

  async resolveSku(req: ResolveSkuRequest): Promise<SkuDecision> {
    const sku = req.profile === "linux-runner-gpu" ? "gex44" : "cx32";
    return {
      sku,
      arch: "amd64",
      vcpu: req.profile === "linux-runner-gpu" ? 16 : 4,
      ramGb: req.profile === "linux-runner-gpu" ? 64 : 8,
      diskGb: req.profile === "linux-runner-gpu" ? 320 : 80,
      notes: "Fallback SKU decision; production provisioning still passes the concrete SKU selected by cloudMachines.",
    };
  }

  async estimateCost(_req: CostEstimateRequest): Promise<CostEstimate> {
    return {
      currency: "EUR",
      confidence: "unknown",
      notes: "Hetzner live pricing is not wired into the provider facade yet.",
    };
  }

  async readBudgetStatus(): Promise<BudgetStatus> {
    return {
      provider: this.id,
      ok: Boolean(this.token),
      lastSyncedAt: Date.now(),
      reason: this.token ? undefined : "HCLOUD_TOKEN missing",
    };
  }

  async createVolume(req: CreateVolumeRequest): Promise<CreateVolumeResult> {
    const r = await this.request("/volumes", {
      method: "POST",
      body: {
        name: req.name,
        size: req.sizeGb,
        location: this.locationForRegion(req.region),
        format: "ext4",
        labels: this.hetznerLabels(req.tags),
      },
    });
    const j = (await r.json()) as { volume?: { id?: number } };
    if (!j.volume?.id) {
      throw this.error("createVolume", "bad_response", "Hetzner volume API returned no id");
    }
    return { volumeId: String(j.volume.id) };
  }

  async deleteVolume(req: DeleteVolumeRequest): Promise<void> {
    await this.request(`/volumes/${encodeURIComponent(req.volumeId)}`, {
      method: "DELETE",
      okStatuses: [200, 202, 204, 404],
    });
  }

  async createMachine(req: CreateMachineRequest): Promise<CreateMachineResult> {
    const payload: Record<string, unknown> = {
      name: req.name,
      server_type: req.sku,
      image: req.image,
      location: this.locationForRegion(req.region),
      labels: this.hetznerLabels(req.tags),
      user_data: req.userData,
    };
    if (req.volumeIds?.length) {
      payload.volumes = req.volumeIds.map((id) => Number(id));
      payload.automount = false;
    }
    if (req.sshKeyNames?.length) {
      payload.ssh_keys = req.sshKeyNames;
    }
    const r = await this.request("/servers", { method: "POST", body: payload });
    const j = (await r.json()) as HetznerServerResponse;
    const id = j.server?.id;
    if (!id) throw this.error("createMachine", "bad_response", "Hetzner server API returned no id");
    return {
      cloudResourceId: String(id),
      serverIp: j.server?.public_net?.ipv4?.ip,
      providerStatus: j.server?.status,
      serverType: req.sku,
    };
  }

  async createMachineFromImageAndVolume(req: WakeFromVolumeRequest): Promise<CreateMachineResult> {
    return this.createMachine(req);
  }

  async createMachineFromSnapshot(req: WakeFromSnapshotRequest): Promise<CreateMachineResult> {
    return this.createMachine({ ...req, image: /^\d+$/.test(req.snapshotId) ? Number(req.snapshotId) : req.snapshotId });
  }

  async snapshotMachine(req: SnapshotMachineRequest): Promise<SnapshotResult> {
    const r = await this.request(`/servers/${encodeURIComponent(req.cloudResourceId)}/actions/create_image`, {
      method: "POST",
      body: { type: "snapshot", description: req.label },
    });
    const j = (await r.json()) as { image?: { id?: number } };
    if (!j.image?.id) throw this.error("snapshotMachine", "bad_response", "Hetzner snapshot API returned no image id");
    return { snapshotId: String(j.image.id) };
  }

  async deleteMachine(req: DeleteMachineRequest): Promise<void> {
    await this.request(`/servers/${encodeURIComponent(req.cloudResourceId)}`, {
      method: "DELETE",
      okStatuses: [200, 202, 204, 404],
    });
  }

  async getMachineStatus(req: MachineStatusRequest): Promise<ProviderMachineStatus> {
    const r = await this.request(`/servers/${encodeURIComponent(req.cloudResourceId)}`, { method: "GET" });
    const j = (await r.json()) as HetznerServerResponse;
    const raw = j.server?.status || "unknown";
    return { status: raw, rawStatus: raw };
  }

  async openFirewall(_req: FirewallRequest): Promise<void> {
    this.unsupported("openFirewall", "Hetzner firewall management is not wired into the provider facade yet");
  }

  async listYaverTaggedResources(_req?: ListTaggedResourcesRequest): Promise<TaggedResource[]> {
    // Intentionally conservative for the first facade extraction. Cleanup will
    // grow this into servers + volumes + snapshots before non-Hetzner providers
    // are placement-eligible.
    return [];
  }

  private locationForRegion(region: string): string {
    return region.startsWith("us") ? "ash" : "fsn1";
  }

  private hetznerLabels(tags: Record<string, string>): Record<string, string> {
    const labels: Record<string, string> = {};
    for (const [rawKey, rawValue] of Object.entries(tags)) {
      const key = rawKey.replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 63);
      const value = rawValue.replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 63);
      if (key && value) labels[key] = value;
    }
    return labels;
  }

  private async request(
    path: string,
    options: {
      method: string;
      body?: unknown;
      okStatuses?: number[];
    },
  ): Promise<Response> {
    const res = await fetch(`${this.apiBase}${path}`, {
      method: options.method,
      headers: {
        Authorization: `Bearer ${this.token}`,
        ...(options.body ? { "Content-Type": "application/json" } : {}),
      },
      body: options.body ? JSON.stringify(options.body) : undefined,
    });
    const okStatuses = options.okStatuses || [200, 201, 202, 204];
    if (!okStatuses.includes(res.status)) {
      const text = await res.text();
      throw this.error("request", "provider_http_error", `Hetzner API ${res.status}: ${text.slice(0, 500)}`);
    }
    return res;
  }

  private error(operation: string, code: string, message: string): ProviderOperationError {
    return new ProviderOperationError({ provider: this.id, operation, code, message });
  }
}

export function createHetznerProvider(token: string): HetznerProvider {
  return new HetznerProvider({ token });
}
