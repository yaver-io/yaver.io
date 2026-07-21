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
    const subnetId = this.stringOption(req.providerOptions, "subnetId") || this.stringEnv("AWS_SUBNET_ID");
    const securityGroupId = this.stringOption(req.providerOptions, "securityGroupId") || this.stringEnv("AWS_SECURITY_GROUP_ID");
    if (!subnetId || !securityGroupId) {
      throw this.error("createMachine", "missing_network", "AWS createMachine requires subnetId/securityGroupId or AWS_SUBNET_ID/AWS_SECURITY_GROUP_ID");
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
      UserData: this.base64Utf8(req.userData),
    };
    const keyName = req.sshKeyNames?.[0] || this.stringEnv("AWS_KEY_NAME");
    if (keyName) params.KeyName = keyName;
    this.applyTags(params, req.tags, ["instance", "volume"]);
    const xml = await this.ec2(params);
    const instanceId = this.xmlValue(xml, "instanceId");
    if (!instanceId) throw this.error("createMachine", "bad_response", "AWS RunInstances returned no instanceId");
    return {
      cloudResourceId: instanceId,
      providerStatus: this.xmlValue(xml, "name") || "pending",
      serverType: req.sku,
    };
  }

  async createMachineFromImageAndVolume(req: WakeFromVolumeRequest): Promise<CreateMachineResult> {
    return this.createMachine(req);
  }

  async createMachineFromSnapshot(_req: WakeFromSnapshotRequest): Promise<CreateMachineResult> {
    this.unsupported("createMachineFromSnapshot", "AWS snapshot wake needs EBS-volume-from-snapshot orchestration before placement eligibility");
  }

  async snapshotMachine(req: SnapshotMachineRequest): Promise<SnapshotResult> {
    this.requireConfig("snapshotMachine");
    throw this.error(
      "snapshotMachine",
      "not_wired",
      `AWS snapshotMachine is not wired yet for ${req.cloudResourceId}; use durable EBS path first`,
    );
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
    const raw = this.xmlValue(xml, "name") || "unknown";
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

  async listYaverTaggedResources(_req?: ListTaggedResourcesRequest): Promise<TaggedResource[]> {
    return [];
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
