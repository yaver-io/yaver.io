import { createBedrockInferenceProviderFromEnv } from "./cloudProviders/bedrockInference";
import { createByoInferenceProvider, OpenAICompatibleInferenceProvider } from "./cloudProviders/openaiCompatibleInference";
import type { InferenceBackendDescriptor, ModelCapability } from "./cloudProviders/types";

export type InferenceBackendRegistry = {
  descriptors: InferenceBackendDescriptor[];
};

export async function createInferenceBackendRegistry(env: Record<string, string | undefined> = process.env): Promise<InferenceBackendRegistry> {
  const bedrock = createBedrockInferenceProviderFromEnv();
  const bedrockModels = await bedrock.describeModels();
  const descriptors: InferenceBackendDescriptor[] = [
    {
      id: "bedrock",
      provider: "aws",
      kind: "bedrock",
      label: "Amazon Bedrock",
      productionEligible: bedrock.configured(),
      keyPolicy: "managed-secret",
      models: bedrockModels,
    },
  ];
  if (env.YAVER_EXTERNAL_OPENAI_BASE_URL) {
    const external = new OpenAICompatibleInferenceProvider({
      id: "external",
      label: "External OpenAI-compatible gateway",
      baseUrl: env.YAVER_EXTERNAL_OPENAI_BASE_URL,
      apiKey: env.YAVER_EXTERNAL_OPENAI_API_KEY,
      keyPolicy: env.YAVER_EXTERNAL_OPENAI_API_KEY ? "managed-secret" : "none",
      models: parseModels(env.YAVER_EXTERNAL_OPENAI_MODELS),
      productionEligible: true,
    });
    descriptors.push(external.descriptor());
  }
  const byo = createByoInferenceProvider({
    models: parseModels(env.YAVER_BYO_INFERENCE_MODELS) || [
      { id: "custom", label: "Custom model" },
    ],
  });
  descriptors.push(byo.descriptor());
  return { descriptors };
}

function parseModels(raw: string | undefined): ModelCapability[] | undefined {
  const models = String(raw || "")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean)
    .map((id) => ({ id, label: id }));
  return models.length ? models : undefined;
}
