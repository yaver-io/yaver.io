// llmClaudeSubscription.ts — LlmProvider backed by Claude on the user's
// SUBSCRIPTION (Max/Pro) via the mirrored OAuth token, NOT a metered API key.
//
// This is the iOS default once the desktop has mirrored the plan token to the
// phone. It produces the SAME EditPlan as the BYO-key Anthropic provider — it
// reuses that provider's tool definition, prompt builders, and response parser
// (the wire shape is identical Messages API) and only swaps the transport:
//   BYO key  → llmAnthropic.ts        → x-api-key  (bills the metered API)
//   plan     → subscriptionStore.ts   → Bearer OAuth (draws from the plan, $0)
//
// RN-coupled (subscriptionStore imports expo-secure-store); not tsx-tested. The
// transport's request/parse correctness is pinned in claudeSubscription.test.mts.

import {
  APPLY_EDITS_TOOL,
  buildSystemPrompt,
  buildUserMessage,
  parseAnthropicResponse,
  type AnthropicResponse,
} from "./llmAnthropic";
import { assertRequestSize, type EditFilesRequest, type EditPlan, type LlmProvider } from "./llmClient";
import { sendClaudeSubscriptionMessage } from "./subscriptionStore";

export interface ClaudeSubscriptionProviderOptions {
  /** Override the model. Default: claude-opus-4-7 (matches the BYO provider). */
  model?: string;
  maxTokens?: number;
}

/** Build an LlmProvider that edits the phone-local sandbox using the user's
 *  Claude subscription. Throws at call time (not construction) if no plan token
 *  has been mirrored — surfaced to the UI as "mirror your plan from desktop". */
export function createClaudeSubscriptionProvider(
  opts: ClaudeSubscriptionProviderOptions = {},
): LlmProvider {
  const model = opts.model ?? "claude-opus-4-7";
  const maxTokens = opts.maxTokens ?? 4096;

  return {
    id: "subscription",
    model,

    async editFiles(req: EditFilesRequest): Promise<EditPlan> {
      assertRequestSize(req);

      const ctrl = new AbortController();
      const timer = setTimeout(() => ctrl.abort(), req.timeoutMs ?? 60_000);
      try {
        const body = (await sendClaudeSubscriptionMessage({
          model,
          maxTokens,
          system: buildSystemPrompt(req),
          messages: [{ role: "user", content: buildUserMessage(req) }],
          tools: [APPLY_EDITS_TOOL],
          toolChoice: { type: "tool", name: "apply_edits" },
          signal: ctrl.signal,
        })) as AnthropicResponse;
        return parseAnthropicResponse(body);
      } finally {
        clearTimeout(timer);
      }
    },
  };
}
