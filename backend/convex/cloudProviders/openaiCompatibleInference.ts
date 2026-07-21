import { AbstractInferenceProvider, ProviderOperationError } from "./abstract";
import type {
  InferenceBackendDescriptor,
  InferenceCostEstimate,
  InferenceEstimateRequest,
  ManagedInferenceRequest,
  ManagedInferenceResult,
  ModelCapability,
} from "./types";

type OpenAICompatibleInferenceOptions = {
  id: "byo" | "external";
  label: string;
  baseUrl?: string;
  apiKey?: string;
  models?: ModelCapability[];
  keyPolicy: "managed-secret" | "user-vault" | "none";
  productionEligible?: boolean;
};

export class OpenAICompatibleInferenceProvider extends AbstractInferenceProvider {
  readonly id: "byo" | "external";
  private readonly label: string;
  private readonly baseUrl?: string;
  private readonly apiKey?: string;
  private readonly models: ModelCapability[];
  private readonly keyPolicy: "managed-secret" | "user-vault" | "none";
  private readonly productionEligible: boolean;

  constructor(options: OpenAICompatibleInferenceOptions) {
    super();
    this.id = options.id;
    this.label = options.label;
    this.baseUrl = options.baseUrl;
    this.apiKey = options.apiKey;
    this.models = options.models || [];
    this.keyPolicy = options.keyPolicy;
    this.productionEligible = options.productionEligible ?? true;
  }

  descriptor(): InferenceBackendDescriptor {
    return {
      id: this.id,
      provider: this.id,
      kind: this.id === "byo" ? "byo" : "openai-compatible",
      label: this.label,
      baseUrl: this.baseUrl,
      productionEligible: this.productionEligible,
      keyPolicy: this.keyPolicy,
      models: this.models,
    };
  }

  async describeModels(): Promise<ModelCapability[]> {
    return this.models;
  }

  async estimateInferenceCost(_req: InferenceEstimateRequest): Promise<InferenceCostEstimate> {
    return { currency: "USD", confidence: "unknown" };
  }

  async invoke(req: ManagedInferenceRequest): Promise<ManagedInferenceResult> {
    if (!this.baseUrl) throw this.error("invoke", "missing_base_url", `${this.label} base URL is not configured`);
    if (this.keyPolicy !== "none" && !this.apiKey) {
      throw this.error("invoke", "missing_api_key", `${this.label} API key is not configured`);
    }
    throw this.error(
      "invoke",
      "not_wired",
      "OpenAI-compatible invoke is intentionally not wired until gateway quota/redaction policy is connected",
    );
  }

  private error(operation: string, code: string, message: string): ProviderOperationError {
    return new ProviderOperationError({ provider: this.id, operation, code, message });
  }
}

export function createByoInferenceProvider(args: {
  baseUrl?: string;
  apiKey?: string;
  models?: ModelCapability[];
}): OpenAICompatibleInferenceProvider {
  return new OpenAICompatibleInferenceProvider({
    id: "byo",
    label: "User BYO inference",
    baseUrl: args.baseUrl,
    apiKey: args.apiKey,
    models: args.models,
    keyPolicy: "user-vault",
  });
}
