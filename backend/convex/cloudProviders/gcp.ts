import { AbstractCloudProvider, ProviderOperationError } from "./abstract";
import { getGcpAccessToken, gcpProjectIdFromEnv, hasRefreshableCredentials } from "./credentials";
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
  /** Static token: manual probing only. Production uses the SA flow. */
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
        "tagged-cleanup",
        "stable-egress-ip",
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
    const configured = Boolean((this.accessToken || hasRefreshableCredentials("gcp")) && this.projectId);
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
    // `disks.insert` returns an OPERATION, not a Disk. Reading `selfLink` off
    // it yields the OPERATION's link, so every later toGcpPath() delete would
    // target a resource that does not exist — a silent orphan generator. The
    // deterministic resource path is the only trustworthy id here.
    void disk;
    return { volumeId: `projects/${this.projectId}/zones/${this.zone}/disks/${req.name}` };
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
    // ⚠️ `instances.insert` returns an OPERATION, never an Instance.
    //   - `selfLink` is the operation's link, not the instance's.
    //   - `status` is the OPERATION's status (PENDING/RUNNING/DONE), which
    //     reads exactly like an instance status and is not one.
    //   - `natIP` is ALWAYS undefined, and the caller hard-throws on a missing
    //     IP *after* the instance exists ⇒ guaranteed billing orphan.
    // Use the deterministic resource path and then GET the real instance.
    void instance;
    const resourcePath = `projects/${this.projectId}/zones/${this.zone}/instances/${req.name}`;
    let serverIp: string | undefined;
    let providerStatus: string | undefined;
    for (let i = 0; i < 15; i++) {
      try {
        const live = await this.request<GcpResource>(
          `/projects/${this.projectId}/zones/${this.zone}/instances/${req.name}`,
          { method: "GET" },
        );
        providerStatus = live.status;
        serverIp = live.networkInterfaces?.[0]?.accessConfigs?.[0]?.natIP;
        if (serverIp) break;
      } catch { /* instance not visible yet — keep polling within the bound */ }
      await new Promise((r) => setTimeout(r, 2000));
    }
    return {
      cloudResourceId: resourcePath,
      serverIp,
      providerStatus: providerStatus || "PROVISIONING",
      serverType: req.sku,
    };
  }

  async createMachineFromImageAndVolume(req: WakeFromVolumeRequest): Promise<CreateMachineResult> {
    return this.createMachine(req);
  }

  /**
   * Wake from a snapshot == create with that custom IMAGE as the boot source.
   * `snapshotMachine` deliberately produces an image (not a raw disk snapshot)
   * so the wake is an ordinary create — a disk-snapshot would force a
   * create-disk-then-attach dance and a second failure window in which a disk
   * exists that nothing references.
   */
  async createMachineFromSnapshot(req: WakeFromSnapshotRequest): Promise<CreateMachineResult> {
    return this.createMachine({ ...req, image: req.snapshotId });
  }

  /**
   * Capture the instance's BOOT disk as a custom image.
   *
   * `forceCreate` is required because the source disk is still attached to a
   * running instance; without it GCP refuses. As with AWS this yields a
   * crash-consistent image rather than a quiesced one, which is the right
   * tradeoff for a dev box.
   *
   * ⚠️ COST: custom images bill for stored bytes until deleted. The image id is
   * returned so the caller records it — an unrecorded image is simultaneously
   * permanently billed and unusable for restore, which is precisely the bug the
   * Relay Pro grace snapshot had.
   */
  async snapshotMachine(req: SnapshotMachineRequest): Promise<SnapshotResult> {
    this.requireConfig("snapshotMachine");
    const instance = await this.request<GcpResource & {
      disks?: Array<{ boot?: boolean; source?: string }>;
    }>(this.toGcpPath(req.cloudResourceId, "instances"), { method: "GET" });
    const bootDisk = (instance.disks ?? []).find((d) => d.boot)?.source
      ?? (instance.disks ?? [])[0]?.source;
    if (!bootDisk) {
      throw this.error("snapshotMachine", "bad_response", "GCP instance reported no boot disk to capture");
    }
    const name = `yaver-${req.label}-${Date.now()}`
      .toLowerCase().replace(/[^a-z0-9-]/g, "-").slice(0, 62);
    await this.request<GcpResource>(`/projects/${this.projectId}/global/images`, {
      method: "POST",
      okStatuses: [200, 201, 202],
      body: {
        name,
        sourceDisk: bootDisk,
        forceCreate: true,
        labels: this.gcpLabels({ managed: "true", service: "yaver-snapshot" }),
      },
    });
    // images.insert returns an Operation — the deterministic resource path is
    // the only id we can trust here (same trap as instances.insert).
    return { snapshotId: `projects/${this.projectId}/global/images/${name}` };
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

  /**
   * Instances, persistent disks, snapshots and reserved static addresses
   * carrying our label. Reconciliation reads this; without it a leak can never
   * be detected. Partial results are deliberate — one resource type failing
   * must not blind the sweep to the rest.
   */
  async listYaverTaggedResources(req?: ListTaggedResourcesRequest): Promise<TaggedResource[]> {
    this.requireConfig("listYaverTaggedResources");
    const tags = this.gcpLabels(req?.tags ?? { yaver: "managed" });
    const filter = Object.entries(tags).map(([k, v]) => `labels.${k}=${v}`).join(" AND ");
    const q = filter ? `?filter=${encodeURIComponent(filter)}` : "";
    const out: TaggedResource[] = [];

    const collect = async (path: string, type: TaggedResource["type"]): Promise<void> => {
      try {
        const j = await this.request<{ items?: Array<Record<string, unknown>> }>(`${path}${q}`, {
          method: "GET",
        });
        for (const row of j.items ?? []) {
          const name = typeof row.name === "string" ? row.name : undefined;
          if (!name) continue;
          out.push({
            id: typeof row.selfLink === "string" ? row.selfLink : name,
            type,
            provider: this.id,
            tags: (row.labels as Record<string, string> | undefined) ?? {},
            status: typeof row.status === "string" ? row.status : undefined,
          });
        }
      } catch { /* partial inventory still finds leaks */ }
    };

    await collect(`/projects/${this.projectId}/zones/${this.zone}/instances`, "machine");
    await collect(`/projects/${this.projectId}/zones/${this.zone}/disks`, "volume");
    await collect(`/projects/${this.projectId}/global/snapshots`, "snapshot");
    await collect(`/projects/${this.projectId}/regions/${this.regionOfZone()}/addresses`, "ip");
    return out;
  }

  // ─── Stable egress identity (static external IP) ────────────────────────
  // A regional static address attached via accessConfigs is what outbound
  // traffic is sourced from, and it survives instance deletion. It bills while
  // reserved-and-unused, so releasing it on decommission is a real cost stop.

  async reserveEgressIp(req: ReserveEgressIpRequest): Promise<EgressIpReservation> {
    this.requireConfig("reserveEgressIp");
    const region = this.regionOfZone();
    await this.request<GcpResource>(`/projects/${this.projectId}/regions/${region}/addresses`, {
      method: "POST",
      okStatuses: [200, 201, 202, 409],
      body: {
        name: req.name,
        addressType: "EXTERNAL",
        networkTier: "PREMIUM",
        description: "Yaver stable egress address",
        labels: this.gcpLabels({ ...req.tags, "yaver-role": "egress" }),
      },
    });
    // addresses.insert also returns an Operation — read the real address back.
    let address = "";
    for (let i = 0; i < 10 && !address; i++) {
      try {
        const live = await this.request<{ address?: string }>(
          `/projects/${this.projectId}/regions/${region}/addresses/${req.name}`,
          { method: "GET" },
        );
        address = String(live.address ?? "");
      } catch { /* not visible yet */ }
      if (!address) await new Promise((r) => setTimeout(r, 2000));
    }
    if (!address) {
      throw this.error("reserveEgressIp", "bad_response", "GCP address did not become readable");
    }
    return {
      egressIpId: `projects/${this.projectId}/regions/${region}/addresses/${req.name}`,
      address,
      scope: region,
      // GCP bills external IPv4 hourly whether attached or idle.
      idleCostUsdPerMonth: 7.2,
    };
  }

  async attachEgressIp(req: AttachEgressIpRequest): Promise<void> {
    this.requireConfig("attachEgressIp");
    // GCP has no "associate address" verb: the address is set in the
    // instance's accessConfig, so re-pointing means delete + add.
    const instancePath = this.toGcpPath(req.cloudResourceId, "instances");
    const address = this.stringOption(req.providerOptions, "address");
    if (!address) {
      throw this.error("attachEgressIp", "missing_address", "GCP attachEgressIp requires providerOptions.address");
    }
    await this.request<void>(
      `${instancePath}/deleteAccessConfig?accessConfig=External%20NAT&networkInterface=nic0`,
      { method: "POST", okStatuses: [200, 202, 400, 404] },
    );
    await this.request<void>(`${instancePath}/addAccessConfig?networkInterface=nic0`, {
      method: "POST",
      okStatuses: [200, 201, 202],
      body: { name: "External NAT", type: "ONE_TO_ONE_NAT", natIP: address },
    });
  }

  async releaseEgressIp(req: ReleaseEgressIpRequest): Promise<void> {
    this.requireConfig("releaseEgressIp");
    await this.request<void>(`/${req.egressIpId.replace(/^\/+/, "")}`, {
      method: "DELETE",
      okStatuses: [200, 202, 204, 404],
    });
  }

  /** us-central1-a → us-central1 */
  private regionOfZone(): string {
    return this.zone.replace(/-[a-z]$/, "");
  }

  /**
   * A FRESH access token for every request.
   *
   * Deliberately not cached on the instance: a provider object can outlive the
   * ~1h token lifetime, and a stale token turns into a 401 in the middle of a
   * provision — the worst possible moment, because the VM may already exist.
   * The credentials module owns the (short) cache.
   */
  private async token(): Promise<string> {
    if (this.accessToken) return this.accessToken; // manual probing override
    return getGcpAccessToken();
  }

  private requireConfig(operation: string): void {
    const canAuth = this.accessToken || hasRefreshableCredentials("gcp");
    if (!canAuth || !this.projectId) {
      throw this.error(
        operation,
        "missing_config",
        "GCP provider is not configured: set GCP_SERVICE_ACCOUNT_JSON and GCP_PROJECT_ID in Convex env",
      );
    }
  }

  private async request<T>(
    path: string,
    options: { method: string; body?: unknown; okStatuses?: number[] },
  ): Promise<T> {
    const res = await fetch(`${this.computeBase}${path}`, {
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
    // Falls back to the project embedded in the service-account key.
    projectId: gcpProjectIdFromEnv(),
    zone: process.env.GCP_ZONE,
  });
}
