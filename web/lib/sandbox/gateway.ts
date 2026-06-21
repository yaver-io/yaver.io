"use client";

// gateway.ts — browser client for the Yaver Gateway (gateway/src/index.ts), an
// OpenAI-compatible /v1/chat/completions endpoint. Beta users get the owner's
// GLM/z.ai capacity WITHOUT ever holding the key: the request authenticates
// with the user's Yaver SESSION TOKEN (the wallet is the key); the Worker holds
// ZAI_API_KEY as a secret and enforces per-user beta caps via Convex
// /gateway/authorize. The key is never sent to, stored in, or reachable from
// the browser. 401 = not signed in / not entitled; 402 = wallet/cap exhausted.

const DEFAULT_MODEL = "glm-5.2";

/** Public gateway URL. Set NEXT_PUBLIC_YAVER_GATEWAY_URL at build time. */
export function getGatewayUrl(): string {
  return (process.env.NEXT_PUBLIC_YAVER_GATEWAY_URL || "").replace(/\/$/, "");
}

export function gatewayConfigured(): boolean {
  return getGatewayUrl().length > 0;
}

export interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

export interface ChatOptions {
  token: string;
  messages: ChatMessage[];
  model?: string;
  maxTokens?: number;
  signal?: AbortSignal;
}

export class GatewayError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
    this.name = "GatewayError";
  }
}

/** Non-streaming chat completion. Returns the assistant message content. */
export async function chatComplete(opts: ChatOptions): Promise<string> {
  const base = getGatewayUrl();
  if (!base) {
    throw new GatewayError("AI gateway not configured (NEXT_PUBLIC_YAVER_GATEWAY_URL)", 0);
  }
  if (!opts.token) {
    throw new GatewayError("sign in to use AI drafting", 401);
  }
  const res = await fetch(`${base}/v1/chat/completions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${opts.token}`,
    },
    body: JSON.stringify({
      model: opts.model ?? DEFAULT_MODEL,
      messages: opts.messages,
      max_tokens: opts.maxTokens ?? 2048,
      stream: false,
    }),
    signal: opts.signal,
  });

  if (res.status === 401) throw new GatewayError("not authorized for AI drafting", 401);
  if (res.status === 402) {
    throw new GatewayError("AI usage limit reached — try again later or add credit", 402);
  }
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new GatewayError(text || `gateway error (${res.status})`, res.status);
  }

  const body = (await res.json()) as {
    choices?: Array<{ message?: { content?: string } }>;
  };
  const content = body.choices?.[0]?.message?.content;
  if (!content) throw new GatewayError("empty completion", res.status);
  return content;
}
