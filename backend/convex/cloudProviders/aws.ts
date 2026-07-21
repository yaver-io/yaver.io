import { AbstractCloudProvider, ProviderOperationError } from "./abstract";
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

type AwsProviderOptions = {
  accessKeyId?: string;
  secretAccessKey?: string;
  sessionToken?: string;
  region?: string;
  ec2Endpoint?: string;
};

type AwsEc2Xml = string;

// AWS EC2 Query API sources checked 2026-07-21:
// - RunInstances
// - DescribeInstances
// - TerminateInstances
// - CreateVolume/DeleteVolume
export class AwsProvider extends AbstractCloudProvider {
  readonly id = "aws" as const;
  private readonly accessKeyId?: string;
  private readonly secretAccessKey?: string;
  private readonly sessionToken?: string;
  private readonly region: string;
  private readonly ec2Endpoint: string;

  constructor(options: AwsProviderOptions = {}) {
    super();
    this.accessKeyId = options.accessKeyId;
    this.secretAccessKey = options.secretAccessKey;
    this.sessionToken = options.sessionToken;
    this.region = options.region || "eu-north-1";
    this.ec2Endpoint = options.ec2Endpoint || `https://ec2.${this.region}.amazonaws.com`;
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
      regions: ["eu-north-1", "eu-central-1", "us-east-1", "us-west-2"],
      productionEligible: false,
      notes: [
        "AWS adapter is implemented but not placement-eligible until VPC/security-group bootstrap, cleanup, budget telemetry, and live Yaver probes pass.",
      ],
    };
  }

  async listRegions(_profile: MachineProfile): Promise<RegionOption[]> {
    return [
      { id: "eu-north-1", label: "Stockholm" },
      { id: "eu-central-1", label: "Frankfurt" },
      { id: "us-east-1", label: "N. Virginia" },
      { id: "us-west-2", label: "Oregon" },
    ];
  }

  async resolveSku(req: ResolveSkuRequest): Promise<SkuDecision> {
    return {
      sku: req.profile === "linux-runner-gpu" ? "g5.xlarge" : "m7i.xlarge",
      arch: "amd64",
      vcpu: 4,
      ramGb: req.profile === "linux-runner-gpu" ? 16 : 16,
      diskGb: Math.max(req.diskGb || 128, 128),
      notes: "Default AWS SKU decision; not placement-eligible until probes pass.",
    };
  }

  async estimateCost(_req: CostEstimateRequest): Promise<CostEstimate> {
    return {
      currency: "USD",
      confidence: "unknown",
      notes: "AWS pricing is not wired into the provider facade yet.",
    };
  }

  async readBudgetStatus(): Promise<BudgetStatus> {
    const configured = Boolean(this.accessKeyId && this.secretAccessKey);
    return {
      provider: this.id,
      ok: configured,
      lastSyncedAt: Date.now(),
      reason: configured ? undefined : "AWS_ACCESS_KEY_ID or AWS_SECRET_ACCESS_KEY missing",
    };
  }

  async createVolume(req: CreateVolumeRequest): Promise<CreateVolumeResult> {
    this.requireConfig("createVolume");
    const params: Record<string, string> = {
      Action: "CreateVolume",
      Version: "2016-11-15",
      AvailabilityZone: this.availabilityZone(),
      Size: String(req.sizeGb),
      VolumeType: "gp3",
    };
    this.applyTags(params, req.tags);
    const xml = await this.ec2(params);
    const volumeId = this.xmlValue(xml, "volumeId");
    if (!volumeId) throw this.error("createVolume", "bad_response", "AWS CreateVolume returned no volumeId");
    return { volumeId };
  }

  async deleteVolume(req: DeleteVolumeRequest): Promise<void> {
    this.requireConfig("deleteVolume");
    await this.ec2({
      Action: "DeleteVolume",
      Version: "2016-11-15",
      VolumeId: req.volumeId,
    }, [200, 400]);
  }

  async createMachine(req: CreateMachineRequest): Promise<CreateMachineResult> {
    this.requireConfig("createMachine");
    const imageId = String(req.image || this.stringEnv("AWS_DEFAULT_AMI_ID") || "");
    if (!imageId) throw this.error("createMachine", "missing_image", "AWS createMachine requires AMI id");
    // Network bootstrap: use what the operator pinned, else discover the
    // account's default VPC. Requiring pre-created infrastructure made this
    // adapter unusable for real placement — and a half-configured network is
    // how a create fails AFTER the volume exists.
    let subnetId = this.stringOption(req.providerOptions, "subnetId") || this.stringEnv("AWS_SUBNET_ID");
    let securityGroupId = this.stringOption(req.providerOptions, "securityGroupId") || this.stringEnv("AWS_SECURITY_GROUP_ID");
    if (!subnetId || !securityGroupId) {
      const net = await this.discoverNetwork();
      subnetId = subnetId || net.subnetId;
      securityGroupId = securityGroupId || net.securityGroupId;
    }
    if (!subnetId || !securityGroupId) {
      throw this.error(
        "createMachine",
        "missing_network",
        "AWS createMachine found no usable subnet/security group. Set AWS_SUBNET_ID and AWS_SECURITY_GROUP_ID, or ensure the account has a default VPC in this region.",
      );
    }
    const params: Record<string, string> = {
      Action: "RunInstances",
      Version: "2016-11-15",
      ImageId: imageId,
      InstanceType: req.sku,
      MinCount: "1",
      MaxCount: "1",
      "NetworkInterface.1.DeviceIndex": "0",
      "NetworkInterface.1.SubnetId": subnetId,
      "NetworkInterface.1.Groups.1": securityGroupId,
      // Without this the instance gets NO public IPv4 and RunInstances returns
      // no address — which the caller treats as a hard failure AFTER the
      // instance already exists, i.e. a guaranteed billing orphan. A workspace
      // must be reachable, so a public address is part of the contract.
      "NetworkInterface.1.AssociatePublicIpAddress": "true",
      UserData: this.base64Utf8(req.userData),
    };
    const keyName = req.sshKeyNames?.[0] || this.stringEnv("AWS_KEY_NAME");
    if (keyName) params.KeyName = keyName;
    this.applyTags(params, req.tags, ["instance", "volume"]);
    const xml = await this.ec2(params);
    const instanceId = this.xmlValue(xml, "instanceId");
    if (!instanceId) throw this.error("createMachine", "bad_response", "AWS RunInstances returned no instanceId");

    // RunInstances answers before the public address is assigned, so the
    // create response frequently carries no ipAddress at all. Resolve it with
    // a bounded DescribeInstances poll instead of returning undefined — the
    // caller hard-throws on a missing IP and would strand this instance.
    let serverIp = this.xmlValue(xml, "ipAddress") || undefined;
    for (let i = 0; !serverIp && i < 10; i++) {
      await new Promise((r) => setTimeout(r, 2000));
      try {
        const desc = await this.ec2({
          Action: "DescribeInstances",
          Version: "2016-11-15",
          "InstanceId.1": instanceId,
        });
        serverIp = this.xmlValue(desc, "ipAddress") || undefined;
      } catch { /* transient — keep polling within the bound */ }
    }
    return {
      cloudResourceId: instanceId,
      serverIp,
      providerStatus: this.instanceStateName(xml) || "pending",
      serverType: req.sku,
    };
  }

  async createMachineFromImageAndVolume(req: WakeFromVolumeRequest): Promise<CreateMachineResult> {
    return this.createMachine(req);
  }

  /**
   * Wake from a snapshot == launch from the AMI that `snapshotMachine` created.
   * AWS models "a whole machine's disk state" as an AMI, so the snapshot id we
   * hand back IS an image id and the wake is just a create with that image.
   */
  async createMachineFromSnapshot(req: WakeFromSnapshotRequest): Promise<CreateMachineResult> {
    return this.createMachine({ ...req, image: req.snapshotId });
  }

  /**
   * CreateImage produces an AMI (plus backing EBS snapshots) from an instance.
   * `NoReboot=true` keeps the workspace usable while it is captured; the
   * tradeoff is a crash-consistent rather than quiesced image, which is the
   * right call for a dev box — a forced reboot mid-park is far more disruptive
   * than a journal replay on restore.
   *
   * ⚠️ COST: an AMI keeps its EBS snapshots alive and billing until the image is
   * deregistered AND those snapshots are deleted. Deregistering alone leaves the
   * snapshots behind — the classic AWS orphan, and exactly the satellite-outlives-
   * its-parent shape that leaked volumes on Hetzner. listYaverTaggedResources
   * reports snapshots separately so reclamation can see both.
   */
  async snapshotMachine(req: SnapshotMachineRequest): Promise<SnapshotResult> {
    this.requireConfig("snapshotMachine");
    const params: Record<string, string> = {
      Action: "CreateImage",
      Version: "2016-11-15",
      InstanceId: req.cloudResourceId,
      Name: `yaver-${req.label}-${Date.now()}`.slice(0, 127),
      NoReboot: "true",
    };
    this.applyTags(params, { managed: "true", service: "yaver-snapshot" }, ["image", "snapshot"]);
    const xml = await this.ec2(params);
    const imageId = this.xmlValue(xml, "imageId");
    if (!imageId) throw this.error("snapshotMachine", "bad_response", "AWS CreateImage returned no imageId");
    return { snapshotId: imageId };
  }

  async deleteMachine(req: DeleteMachineRequest): Promise<void> {
    this.requireConfig("deleteMachine");
    await this.ec2({
      Action: "TerminateInstances",
      Version: "2016-11-15",
      "InstanceId.1": req.cloudResourceId,
    }, [200, 400]);
  }

  async getMachineStatus(req: MachineStatusRequest): Promise<ProviderMachineStatus> {
    this.requireConfig("getMachineStatus");
    const xml = await this.ec2({
      Action: "DescribeInstances",
      Version: "2016-11-15",
      "InstanceId.1": req.cloudResourceId,
    });
    const raw = this.instanceStateName(xml) || "unknown";
    return { status: raw, rawStatus: raw };
  }

  async openFirewall(req: FirewallRequest): Promise<void> {
    this.requireConfig("openFirewall");
    const groupId = this.stringEnv("AWS_SECURITY_GROUP_ID");
    if (!groupId) throw this.error("openFirewall", "missing_security_group", "AWS_SECURITY_GROUP_ID missing");
    for (const rule of req.ports) {
      await this.ec2({
        Action: "AuthorizeSecurityGroupIngress",
        Version: "2016-11-15",
        GroupId: groupId,
        IpProtocol: rule.protocol,
        FromPort: String(rule.port),
        ToPort: String(rule.port),
        CidrIp: rule.source || "0.0.0.0/0",
      }, [200, 400]);
    }
  }

  /**
   * Instances, EBS volumes, snapshots and Elastic IPs carrying our tag.
   * Reconciliation reads this; without it a leak is undetectable forever.
   * Terminated instances are skipped — they no longer bill and would otherwise
   * drown the sweep in noise.
   */
  async listYaverTaggedResources(req?: ListTaggedResourcesRequest): Promise<TaggedResource[]> {
    this.requireConfig("listYaverTaggedResources");
    const tags = req?.tags ?? { yaver: "managed" };
    const filters: Record<string, string> = {};
    let n = 1;
    for (const [k, val] of Object.entries(tags)) {
      filters[`Filter.${n}.Name`] = `tag:${k}`;
      filters[`Filter.${n}.Value.1`] = val;
      n++;
    }
    const out: TaggedResource[] = [];

    const scan = async (
      action: string,
      idTag: string,
      type: TaggedResource["type"],
      statusTag?: string,
    ): Promise<void> => {
      try {
        const xml = await this.ec2({ Action: action, Version: "2016-11-15", ...filters });
        for (const id of this.xmlValues(xml, idTag)) {
          const status = type === "machine"
            ? this.instanceStateName(xml)
            : (statusTag ? this.xmlValues(xml, statusTag)[0] : undefined);
          if (type === "machine" && status === "terminated") continue;
          out.push({ id, type, provider: this.id, tags, status });
        }
      } catch { /* partial inventory still finds leaks */ }
    };

    await scan("DescribeInstances", "instanceId", "machine", "name");
    await scan("DescribeVolumes", "volumeId", "volume", "status");
    await scan("DescribeSnapshots", "snapshotId", "snapshot", "status");
    await scan("DescribeAddresses", "allocationId", "ip");
    return out;
  }

  // ─── Stable egress identity (Elastic IP) ────────────────────────────────
  // An EIP is the address outbound traffic is sourced from once associated,
  // and it survives instance termination (it merely disassociates) — which is
  // exactly what park needs. It also bills while unassociated, so releasing it
  // on decommission is a real cost stop, not tidy-up.

  async reserveEgressIp(req: ReserveEgressIpRequest): Promise<EgressIpReservation> {
    this.requireConfig("reserveEgressIp");
    const params: Record<string, string> = {
      Action: "AllocateAddress",
      Version: "2016-11-15",
      Domain: "vpc",
    };
    this.applyTags(params, { ...req.tags, "yaver-role": "egress" }, ["elastic-ip"]);
    const xml = await this.ec2(params);
    const allocationId = this.xmlValue(xml, "allocationId");
    const address = this.xmlValue(xml, "publicIp");
    if (!allocationId || !address) {
      throw this.error("reserveEgressIp", "bad_response", "AWS AllocateAddress returned no allocationId/publicIp");
    }
    return {
      egressIpId: allocationId,
      address,
      scope: this.region,
      // AWS bills all public IPv4 hourly (~$0.005/h) whether or not it is in use.
      idleCostUsdPerMonth: 3.6,
    };
  }

  async attachEgressIp(req: AttachEgressIpRequest): Promise<void> {
    this.requireConfig("attachEgressIp");
    await this.ec2({
      Action: "AssociateAddress",
      Version: "2016-11-15",
      AllocationId: req.egressIpId,
      InstanceId: req.cloudResourceId,
      AllowReassociation: "true",
    });
  }

  async releaseEgressIp(req: ReleaseEgressIpRequest): Promise<void> {
    this.requireConfig("releaseEgressIp");
    await this.ec2({
      Action: "ReleaseAddress",
      Version: "2016-11-15",
      AllocationId: req.egressIpId,
    }, [200, 400]);
  }

  /**
   * Find a usable subnet + security group without requiring the operator to
   * pre-create anything.
   *
   * Deliberately DISCOVERS rather than CREATES a VPC. Creating VPCs, gateways
   * and route tables from a provisioning path means a failure halfway through
   * leaves networking debris that the orphan sweep cannot reason about, and it
   * mutates account-wide infrastructure that may not belong to Yaver — the
   * resource-boundary rule. Discovery is reversible; creation is not.
   */
  private async discoverNetwork(): Promise<{ subnetId?: string; securityGroupId?: string }> {
    try {
      const vpcXml = await this.ec2({
        Action: "DescribeVpcs",
        Version: "2016-11-15",
        "Filter.1.Name": "isDefault",
        "Filter.1.Value.1": "true",
      });
      const vpcId = this.xmlValue(vpcXml, "vpcId");
      if (!vpcId) return {};

      const subnetXml = await this.ec2({
        Action: "DescribeSubnets",
        Version: "2016-11-15",
        "Filter.1.Name": "vpc-id",
        "Filter.1.Value.1": vpcId,
      });
      const subnetId = this.xmlValues(subnetXml, "subnetId")[0];

      const sgXml = await this.ec2({
        Action: "DescribeSecurityGroups",
        Version: "2016-11-15",
        "Filter.1.Name": "vpc-id",
        "Filter.1.Value.1": vpcId,
        "Filter.2.Name": "group-name",
        "Filter.2.Value.1": "default",
      });
      const securityGroupId = this.xmlValues(sgXml, "groupId")[0];
      return { subnetId, securityGroupId };
    } catch {
      return {};
    }
  }

  private requireConfig(operation: string): void {
    if (!this.accessKeyId || !this.secretAccessKey) {
      throw this.error(operation, "missing_config", "AWS provider credentials/config are not configured");
    }
  }

  private async ec2(params: Record<string, string>, okStatuses = [200]): Promise<AwsEc2Xml> {
    const body = new URLSearchParams(params).toString();
    const headers = await this.signAwsQuery(body);
    const res = await fetch(this.ec2Endpoint, {
      method: "POST",
      headers,
      body,
    });
    const text = await res.text();
    if (!okStatuses.includes(res.status)) {
      throw this.error("request", "provider_http_error", `AWS EC2 API ${res.status}: ${text.slice(0, 500)}`);
    }
    return text;
  }

  private async signAwsQuery(body: string): Promise<Record<string, string>> {
    const now = new Date();
    const amzDate = now.toISOString().replace(/[:-]|\.\d{3}/g, "");
    const dateStamp = amzDate.slice(0, 8);
    const host = new URL(this.ec2Endpoint).host;
    const canonicalHeaders = `content-type:application/x-www-form-urlencoded\nhost:${host}\nx-amz-date:${amzDate}\n`;
    const signedHeaders = "content-type;host;x-amz-date";
    const payloadHash = await this.sha256Hex(body);
    const canonicalRequest = [
      "POST",
      "/",
      "",
      canonicalHeaders,
      signedHeaders,
      payloadHash,
    ].join("\n");
    const credentialScope = `${dateStamp}/${this.region}/ec2/aws4_request`;
    const stringToSign = [
      "AWS4-HMAC-SHA256",
      amzDate,
      credentialScope,
      await this.sha256Hex(canonicalRequest),
    ].join("\n");
    const signingKey = await this.awsSigningKey(dateStamp);
    const signature = await this.hmacHex(signingKey, stringToSign);
    const headers: Record<string, string> = {
      "Content-Type": "application/x-www-form-urlencoded",
      "X-Amz-Date": amzDate,
      Authorization: `AWS4-HMAC-SHA256 Credential=${this.accessKeyId}/${credentialScope}, SignedHeaders=${signedHeaders}, Signature=${signature}`,
    };
    if (this.sessionToken) headers["X-Amz-Security-Token"] = this.sessionToken;
    return headers;
  }

  private async awsSigningKey(dateStamp: string): Promise<ArrayBuffer> {
    const kDate = await this.hmacBytes(this.utf8(`AWS4${this.secretAccessKey}`), dateStamp);
    const kRegion = await this.hmacBytes(kDate, this.region);
    const kService = await this.hmacBytes(kRegion, "ec2");
    return this.hmacBytes(kService, "aws4_request");
  }

  private async sha256Hex(value: string): Promise<string> {
    const digest = await crypto.subtle.digest("SHA-256", this.utf8Buffer(value));
    return this.hex(digest);
  }

  private async hmacBytes(key: ArrayBuffer | Uint8Array, value: string): Promise<ArrayBuffer> {
    const cryptoKey = await crypto.subtle.importKey("raw", this.toArrayBuffer(key), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
    return crypto.subtle.sign("HMAC", cryptoKey, this.utf8Buffer(value));
  }

  private async hmacHex(key: ArrayBuffer | Uint8Array, value: string): Promise<string> {
    return this.hex(await this.hmacBytes(key, value));
  }

  private utf8(value: string): Uint8Array<ArrayBuffer> {
    return new Uint8Array(this.utf8Buffer(value));
  }

  private utf8Buffer(value: string): ArrayBuffer {
    const bytes = new TextEncoder().encode(value);
    return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer;
  }

  private toArrayBuffer(value: ArrayBuffer | Uint8Array): ArrayBuffer {
    if (value instanceof ArrayBuffer) return value;
    return value.buffer.slice(value.byteOffset, value.byteOffset + value.byteLength) as ArrayBuffer;
  }

  private hex(bytes: ArrayBuffer): string {
    return Array.from(new Uint8Array(bytes)).map((b) => b.toString(16).padStart(2, "0")).join("");
  }

  private applyTags(params: Record<string, string>, tags: Record<string, string>, resourceTypes = ["volume"]): void {
    let tagSet = 1;
    for (const resourceType of resourceTypes) {
      params[`TagSpecification.${tagSet}.ResourceType`] = resourceType;
      let tagIndex = 1;
      for (const [key, value] of Object.entries(tags)) {
        params[`TagSpecification.${tagSet}.Tag.${tagIndex}.Key`] = key;
        params[`TagSpecification.${tagSet}.Tag.${tagIndex}.Value`] = value;
        tagIndex++;
      }
      tagSet++;
    }
  }

  private availabilityZone(): string {
    return this.stringEnv("AWS_AVAILABILITY_ZONE") || `${this.region}a`;
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

  private xmlValue(xml: string, tag: string): string | undefined {
    const match = xml.match(new RegExp(`<${tag}>([^<]+)</${tag}>`));
    return match?.[1];
  }

  /** Every occurrence of a tag, in document order. */
  private xmlValues(xml: string, tag: string): string[] {
    const out: string[] = [];
    const re = new RegExp(`<${tag}>([^<]+)</${tag}>`, "g");
    for (const m of xml.matchAll(re)) if (m[1]) out.push(m[1]);
    return out;
  }

  /**
   * The EC2 instance STATE, not merely the first `<name>` in the document.
   *
   * DescribeInstances XML contains many `<name>` elements (group names,
   * placement, tenancy, instance state, …) so a first-match regex reads
   * whichever happens to come first — a status that is wrong in a way that
   * looks plausible. `<instanceState>` wraps the one we want.
   */
  private instanceStateName(xml: string): string | undefined {
    const block = xml.match(/<instanceState>([\s\S]*?)<\/instanceState>/);
    if (!block?.[1]) return undefined;
    return this.xmlValue(block[1], "name");
  }

  private error(operation: string, code: string, message: string): ProviderOperationError {
    return new ProviderOperationError({ provider: this.id, operation, code, message });
  }
}

export function createAwsProviderFromEnv(): AwsProvider {
  return new AwsProvider({
    accessKeyId: process.env.AWS_ACCESS_KEY_ID,
    secretAccessKey: process.env.AWS_SECRET_ACCESS_KEY,
    sessionToken: process.env.AWS_SESSION_TOKEN,
    region: process.env.AWS_REGION,
  });
}
