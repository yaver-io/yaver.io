import { AbstractInferenceProvider, ProviderOperationError } from "./abstract";
import type {
  InferenceCostEstimate,
  InferenceEstimateRequest,
  ManagedInferenceRequest,
  ManagedInferenceResult,
  ModelCapability,
} from "./types";

type BedrockInferenceOptions = {
  region?: string;
  accessKeyId?: string;
  secretAccessKey?: string;
  sessionToken?: string;
};

// Bedrock/DeepSeek sources checked 2026-07-21:
// - AWS Bedrock DeepSeek model parameters docs list DeepSeek R1 and V3.1 for
//   Invoke/Converse APIs.
// - AWS Bedrock model overview lists DeepSeek among supported providers.
export class BedrockInferenceProvider extends AbstractInferenceProvider {
  readonly id = "aws" as const;
  private readonly region: string;
  private readonly accessKeyId?: string;
  private readonly secretAccessKey?: string;
  private readonly sessionToken?: string;

  constructor(options: BedrockInferenceOptions = {}) {
    super();
    this.region = options.region || "us-east-1";
    this.accessKeyId = options.accessKeyId;
    this.secretAccessKey = options.secretAccessKey;
    this.sessionToken = options.sessionToken;
  }

  async describeModels(): Promise<ModelCapability[]> {
    return [
      { id: "deepseek.r1-v1:0", label: "DeepSeek R1", contextTokens: 128_000 },
      { id: "deepseek.v3-1-v1:0", label: "DeepSeek V3.1", contextTokens: 128_000 },
    ];
  }

  async estimateInferenceCost(_req: InferenceEstimateRequest): Promise<InferenceCostEstimate> {
    return {
      currency: "USD",
      confidence: "unknown",
    };
  }

  async invoke(_req: ManagedInferenceRequest): Promise<ManagedInferenceResult> {
    if (!this.accessKeyId || !this.secretAccessKey) {
      throw this.error("invoke", "missing_config", "Bedrock credentials are not configured");
    }
    throw this.error(
      "invoke",
      "not_wired",
      "Bedrock invoke is intentionally not wired until gateway quota/redaction policy is connected",
    );
  }

  configured(): boolean {
    return Boolean(this.accessKeyId && this.secretAccessKey && this.region);
  }

  private error(operation: string, code: string, message: string): ProviderOperationError {
    return new ProviderOperationError({ provider: this.id, operation, code, message });
  }
}

export function createBedrockInferenceProviderFromEnv(): BedrockInferenceProvider {
  return new BedrockInferenceProvider({
    region: process.env.AWS_BEDROCK_REGION || process.env.AWS_REGION,
    accessKeyId: process.env.AWS_ACCESS_KEY_ID,
    secretAccessKey: process.env.AWS_SECRET_ACCESS_KEY,
    sessionToken: process.env.AWS_SESSION_TOKEN,
  });
}
