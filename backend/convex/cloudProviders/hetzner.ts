import { AbstractCloudProvider, ProviderOperationError } from "./abstract";
import type {
  AttachEgressIpRequest,
  BudgetStatus,
  CostEstimate,
  CostEstimateRequest,
  EgressIpReservation,
  ReleaseEgressIpRequest,
  ReserveEgressIpRequest,
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
        // Really implemented as of 2026-07-21 (servers + volumes + snapshots +
        // primary IPs). Before that this was declared while
        // listYaverTaggedResources returned [] — a false green on the ONE
        // capability the placement gate checks.
        "tagged-cleanup",
        "stable-egress-ip",
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
    // cx32 does not exist in the Hetzner catalog (verified live 2026-07-21);
    // the shared Intel line is cx23/cx33/cx43. Keep this in step with
    // cloudLifecycle.hetznerServerType — two ladders that disagree is how a
    // resize lands on a type the snapshot cannot restore onto.
    // Keep in step with cloudLifecycle.hetznerServerType: the DEFAULT class is
    // 2c/4GB (cpx22 — cx23 is cheaper but sold out EU-wide as of 2026-07-21).
    // Heavier profiles opt up; they do not change the default.
    const sku =
      req.profile === "linux-runner-gpu" ? "gex44"
      : req.profile === "linux-runner-redroid" ? "cpx42"
      : req.profile === "linux-runner-webrtc" ? "cpx32"
      : "cpx22";
    return {
      sku,
      arch: "amd64",
      vcpu: req.profile === "linux-runner-gpu" ? 16 : req.profile === "linux-runner" ? 2 : 4,
      ramGb: req.profile === "linux-runner-gpu" ? 64 : req.profile === "linux-runner" ? 4 : 8,
      diskGb: req.profile === "linux-runner-gpu" ? 320 : 80,
      notes: "Fallback SKU decision; production provisioning still passes the concrete SKU selected by cloudMachines.",
    };
  }

  /**
   * Real hourly/monthly price for the SKU, straight from the provider.
   *
   * Cost-awareness is a product requirement, not telemetry: a workspace should
   * be able to tell its owner what it costs. Falls back to confidence:"unknown"
   * rather than guessing — a made-up number is worse than an absent one.
   */
  async estimateCost(req: CostEstimateRequest): Promise<CostEstimate> {
    try {
      const r = await this.request(`/server_types?name=${encodeURIComponent(req.sku)}`, {
        method: "GET",
      });
      const j = (await r.json()) as {
        server_types?: Array<{
          prices?: Array<{
            location?: string;
            price_hourly?: { gross?: string };
            price_monthly?: { gross?: string };
          }>;
        }>;
      };
      const wanted = this.locationForRegion(req.region);
      const prices = j.server_types?.[0]?.prices ?? [];
      const price = prices.find((p) => p.location === wanted) ?? prices[0];
      const hourly = Number(price?.price_hourly?.gross);
      const monthly = Number(price?.price_monthly?.gross);
      if (!Number.isFinite(hourly) && !Number.isFinite(monthly)) {
        return {
          currency: "EUR",
          confidence: "unknown",
          notes: `Hetzner returned no price for server type "${req.sku}" — does it exist in this account's catalog?`,
        };
      }
      return {
        currency: "EUR",
        estimatedHourlyCompute: Number.isFinite(hourly) ? hourly : undefined,
        estimatedMonthlyCompute: Number.isFinite(monthly) ? monthly : undefined,
        // Hetzner Volumes bill ~€0.044/GB/month; the parked-state cost driver.
        estimatedMonthlyStorage: req.diskGb ? Math.round(req.diskGb * 0.044 * 100) / 100 : undefined,
        confidence: "known",
      };
    } catch (e) {
      return {
        currency: "EUR",
        confidence: "unknown",
        notes: `Hetzner pricing lookup failed: ${e instanceof Error ? e.message : String(e)}`,
      };
    }
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

  /**
   * Every Yaver-labelled resource this token can see: servers, volumes,
   * snapshot images, and reserved primary IPs.
   *
   * This is the ONLY way a leak becomes visible. Convex knows what it *thinks*
   * it created; only the provider knows what actually exists. The 2026-07-21
   * audit found every customer decommission leaving its volume behind — and
   * nothing in the codebase could have detected it, because this method
   * returned [] on all four providers. Reconciliation reads this.
   *
   * Deliberately NOT filtered to the caller's expectations: it returns what is
   * there, and the sweep decides what is unexpected.
   */
  async listYaverTaggedResources(req?: ListTaggedResourcesRequest): Promise<TaggedResource[]> {
    const wanted = this.hetznerLabels(req?.tags ?? {});
    const selector = Object.entries(wanted).map(([k, v]) => `${k}=${v}`).join(",");
    const q = selector ? `?label_selector=${encodeURIComponent(selector)}` : "";
    const out: TaggedResource[] = [];

    const collect = async (
      path: string,
      key: string,
      type: TaggedResource["type"],
    ): Promise<void> => {
      try {
        const r = await this.request(`${path}${q}`, { method: "GET" });
        const j = (await r.json()) as Record<string, unknown>;
        const rows = (j[key] as Array<Record<string, unknown>> | undefined) ?? [];
        for (const row of rows) {
          if (!row?.id) continue;
          out.push({
            id: String(row.id),
            type,
            provider: this.id,
            tags: (row.labels as Record<string, string> | undefined) ?? {},
            status: typeof row.status === "string" ? row.status : undefined,
          });
        }
      } catch {
        // One resource type failing must not blind the sweep to the others —
        // a partial inventory still finds leaks.
      }
    };

    await collect("/servers", "servers", "machine");
    await collect("/volumes", "volumes", "volume");
    // Snapshots only: base/system images are Hetzner's, not ours to reap.
    await collect("/images?type=snapshot", "images", "snapshot");
    await collect("/primary_ips", "primary_ips", "ip");
    return out;
  }

  // ─── Stable egress identity ─────────────────────────────────────────────
  //
  // PRIMARY IP, not Floating IP. A Floating IP changes what reaches the box
  // INBOUND; the box still SOURCES outbound connections from its primary
  // address, and the vendor heuristic we are defending against sees the
  // source. A Floating IP here would test green — reserved, attached — while
  // the vendor kept seeing a new address on every wake.
  //
  // auto_delete:false is what makes it survive the server delete that park
  // performs. Without that flag the whole feature is a no-op.

  async reserveEgressIp(req: ReserveEgressIpRequest): Promise<EgressIpReservation> {
    const datacenter = req.scope || (await this.pickDatacenter(this.locationForRegion(req.region)));
    if (!datacenter) {
      throw this.error("reserveEgressIp", "no_datacenter", `No Hetzner datacenter found for region ${req.region}`);
    }
    const r = await this.request("/primary_ips", {
      method: "POST",
      body: {
        name: req.name,
        type: "ipv4",
        datacenter,
        assignee_type: "server",
        auto_delete: false,
        labels: this.hetznerLabels({ ...req.tags, service: "yaver-egress-ip", managed: "true" }),
      },
    });
    const j = (await r.json()) as {
      primary_ip?: { id?: number; ip?: string; datacenter?: { name?: string } };
    };
    if (!j.primary_ip?.id || !j.primary_ip.ip) {
      throw this.error("reserveEgressIp", "bad_response", "Hetzner primary IP API returned no id/ip");
    }
    return {
      egressIpId: String(j.primary_ip.id),
      address: j.primary_ip.ip,
      scope: String(j.primary_ip.datacenter?.name ?? datacenter),
      // Hetzner bills an unassigned primary IPv4 at roughly €0.50-1.20/mo.
      // Surfaced so the cost model can reason about parked workspaces.
      idleCostUsdPerMonth: 1.2,
    };
  }

  async attachEgressIp(req: AttachEgressIpRequest): Promise<void> {
    await this.request(`/primary_ips/${encodeURIComponent(req.egressIpId)}/actions/assign`, {
      method: "POST",
      body: { assignee_id: Number(req.cloudResourceId), assignee_type: "server" },
    });
  }

  async releaseEgressIp(req: ReleaseEgressIpRequest): Promise<void> {
    // Hetzner refuses to delete an ASSIGNED primary IP. The server is normally
    // gone by now; unassign first so a stale assignment cannot strand a
    // billing address forever.
    try {
      await this.request(`/primary_ips/${encodeURIComponent(req.egressIpId)}/actions/unassign`, {
        method: "POST",
        okStatuses: [200, 201, 202, 204, 404, 409],
      });
    } catch { /* usually already unassigned by the server delete */ }
    await this.request(`/primary_ips/${encodeURIComponent(req.egressIpId)}`, {
      method: "DELETE",
      okStatuses: [200, 202, 204, 404],
    });
  }

  /** Resolve a location (fsn1) to a concrete datacenter (fsn1-dc14). */
  private async pickDatacenter(location: string): Promise<string | null> {
    try {
      const r = await this.request("/datacenters", { method: "GET" });
      const j = (await r.json()) as {
        datacenters?: Array<{ name?: string; location?: { name?: string } }>;
      };
      return (j.datacenters ?? []).find((d) => d.location?.name === location && d.name)?.name ?? null;
    } catch {
      return null;
    }
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
