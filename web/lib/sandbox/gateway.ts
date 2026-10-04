"use client";

// Legacy compatibility surface. Hosted inference is deliberately unavailable:
// sending prompts to a Yaver-operated Worker would violate the zero-knowledge
// boundary. Browser features must use a local/direct provider integration or
// an E2EE-authorized remote agent.
export function getGatewayUrl(): string {
  return "";
}

export function gatewayConfigured(): boolean {
  return false;
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
  void opts;
  throw new GatewayError(
    "Hosted AI drafting is unavailable in zero-knowledge mode; use a direct provider or paired agent.",
    410,
  );
}
