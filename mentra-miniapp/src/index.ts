/**
 * Yaver MentraOS miniapp — hands-free agent loop on smart glasses.
 *
 * Hardware coverage (single binary):
 *   - Mentra Live (audio-only, $349) — voice in + voice out via speakers
 *   - Even Realities G1 / G2 — voice in + glanceable HUD via showTextWall
 *   - Vuzix Z100 — HUD only, voice via paired Bluetooth mic
 *
 * Flow per turn:
 *   1. User wakes glasses ("Yaver" wake word in MentraOS settings — TODO)
 *   2. MentraOS streams ASR events; we wait for a settled final transcript
 *   3. POST transcript to local Yaver agent /voice/stream (websocket bridge)
 *   4. Stream transcript + task result + (TTS frames) back to glasses
 *   5. Display result on HUD (G1/G2/Z100), speak it (Live), or both
 *
 * Why this exists: kivanc's desk workflow is 3 parallel Claude Code panes
 * in tmux. Glasses replicate that ambient awareness on the body — see
 * project_voice_glasses_revival_2026_05_27.md.
 *
 * Run:
 *   cp .env.example .env  &&  fill in MENTRAOS_API_KEY + YAVER_SDK_TOKEN
 *   bun install
 *   bun run dev
 */

import { AppServer, type AppSession } from "@mentra/sdk";

const PACKAGE_NAME = required("MENTRAOS_PACKAGE_NAME");
const MENTRA_API_KEY = required("MENTRAOS_API_KEY");
const YAVER_AGENT_URL = process.env.YAVER_AGENT_URL ?? "http://127.0.0.1:18080";
const YAVER_SDK_TOKEN = required("YAVER_SDK_TOKEN");
const YAVER_PROJECT = process.env.YAVER_PROJECT ?? "";
const PORT = Number(process.env.PORT ?? 8080);

function required(name: string): string {
  const v = process.env[name];
  if (!v) {
    console.error(`[mentra] missing required env: ${name}`);
    process.exit(2);
  }
  return v;
}

class YaverMentraApp extends AppServer {
  protected async onSession(session: AppSession, sessionId: string, userId: string): Promise<void> {
    console.log(`[mentra] session opened — user=${userId} sid=${sessionId}`);

    // Welcome glance on the HUD (no-op on audio-only Live).
    try {
      session.layouts.showTextWall("Yaver ready · say a task");
    } catch (e) {
      // Mentra Live throws on HUD calls — ignore.
    }

    let lastFinalAt = 0;
    let inFlight: AbortController | null = null;

    session.events.onTranscription(async (data: any) => {
      // Mentra emits both partials + finals. We only act on finals.
      if (!data?.isFinal) return;
      const text = String(data.text ?? "").trim();
      if (!text) return;

      // Debounce: ignore finals within 600ms of the previous one
      // (Mentra sometimes double-fires on EOT).
      const now = Date.now();
      if (now - lastFinalAt < 600) return;
      lastFinalAt = now;

      // Cancel any in-flight task — newest utterance wins.
      inFlight?.abort();
      inFlight = new AbortController();

      try {
        session.layouts.showTextWall(`heard · ${truncate(text, 80)}`);
      } catch {}

      try {
        const result = await dispatchToYaver(text, inFlight.signal);
        try {
          session.layouts.showTextWall(`done · ${truncate(result.text, 200)}`);
        } catch {}
        // TTS readback via MentraOS AudioManager (SDK 2.10+). Auto-routes
        // to glasses speakers (Mentra Live) or paired phone (G1/G2 which
        // have no speakers) via session.capabilities.hasSpeaker. Don't
        // pre-render PCM in the Yaver agent — Mentra owns the TTS path
        // end-to-end and has ElevenLabs wired in.
        // See: project_spatial_constraints_2026.md fact #3.
        try {
          await (session as any).audio?.speak?.(truncate(result.text, 280));
        } catch (audioErr) {
          // Older SDK or unsupported — silently fall back to display only.
        }
      } catch (e: any) {
        if (e?.name === "AbortError") return;
        console.error("[mentra] dispatch error:", e);
        try {
          session.layouts.showTextWall(`error · ${truncate(String(e?.message ?? e), 120)}`);
        } catch {}
      }
    });

    // Subscribe to the agent's blackbox command stream so glass-wearer
    // hears notifications about runner-auth state + Hermes-reload
    // results without having to ask. Same SSE endpoint mobile uses.
    const cmdStream = subscribeToAgentCommands(session, sessionId);

    session.events.onDisconnected(() => {
      console.log(`[mentra] session closed — sid=${sessionId}`);
      inFlight?.abort();
      try { cmdStream?.close(); } catch {}
    });
  }
}

/**
 * POST transcript to the Yaver agent and return the final task result.
 * Uses a one-shot synchronous flow against /voice/dispatch (a thin
 * REST shim we'll add to the agent if missing — for v1 we use the
 * existing /tasks endpoint with source="voice-input").
 *
 * The full WS streaming flow lives on the mobile/glasses-mic side.
 * Here we already HAVE the final transcript from Mentra's onboard
 * ASR — no need to re-do STT on the agent.
 */
async function dispatchToYaver(transcript: string, signal: AbortSignal): Promise<{ taskId: string; text: string; status: string }> {
  const create = await fetch(`${YAVER_AGENT_URL}/tasks`, {
    method: "POST",
    signal,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${YAVER_SDK_TOKEN}`,
    },
    body: JSON.stringify({
      title: truncate(transcript, 60),
      description: transcript,
      source: "voice-input",
      speechContext: { inputFromSpeech: true, sttProvider: "mentra" },
      workDir: YAVER_PROJECT ? `~/Workspace/${YAVER_PROJECT}` : undefined,
      // Tell the agent's prompt wrapper: this is a glasses HUD with
      // voice readback. Claude should keep the response terse + skip
      // markdown / code blocks (display can't render them legibly).
      viewport: {
        surface: "glasses-mentra-display",
        paneCount: 1,
        voice: true,
        ttsBudget: 200,
      },
    }),
  });
  if (!create.ok) {
    throw new Error(`tasks POST ${create.status}: ${await create.text().catch(() => "")}`);
  }
  const created = (await create.json()) as { id: string };
  const taskId = created.id;

  // Poll until terminal status. We deliberately don't await TTS here —
  // glasses get the text glance; audio readback is a Phase 2 concern.
  const deadline = Date.now() + 10 * 60 * 1000; // 10min hard cap
  while (Date.now() < deadline) {
    if (signal.aborted) throw new DOMException("aborted", "AbortError");
    await new Promise((r) => setTimeout(r, 1500));
    const poll = await fetch(`${YAVER_AGENT_URL}/tasks/${taskId}`, {
      signal,
      headers: { Authorization: `Bearer ${YAVER_SDK_TOKEN}` },
    });
    if (!poll.ok) continue;
    const t = (await poll.json()) as { status: string; resultText?: string; output?: string[] };
    if (isTerminal(t.status)) {
      const text = (t.resultText ?? lastNonEmpty(t.output) ?? "").trim() || "(no output)";
      return { taskId, text, status: t.status };
    }
  }
  throw new Error("task polling timed out after 10 minutes");
}

function isTerminal(s: string): boolean {
  return s === "completed" || s === "failed" || s === "stopped" || s === "review";
}

function lastNonEmpty(out?: string[]): string | undefined {
  if (!Array.isArray(out)) return undefined;
  for (let i = out.length - 1; i >= 0; i--) {
    const line = (out[i] ?? "").trim();
    if (line) return line;
  }
  return undefined;
}

function truncate(s: string, n: number): string {
  if (s.length <= n) return s;
  return s.slice(0, n - 1) + "…";
}

/**
 * Subscribe to the agent's /blackbox/command-stream SSE endpoint so
 * the glass-wearer gets typed notifications without polling:
 *
 *   runner_auth_required  → "Claude reauth needed on box. Check phone."
 *   runner_auth_completed → "Claude ready."
 *   app_reloaded          → "sfmg reloaded." (with caption if present)
 *
 * Bun's native fetch supports ReadableStream so we parse SSE
 * line-by-line without importing a third-party EventSource shim.
 */
function subscribeToAgentCommands(session: AppSession, sessionId: string): { close: () => void } {
  const url = `${YAVER_AGENT_URL}/blackbox/command-stream?device=glass-${encodeURIComponent(sessionId)}`;
  const controller = new AbortController();
  (async () => {
    try {
      const resp = await fetch(url, {
        method: "GET",
        signal: controller.signal,
        headers: {
          Accept: "text/event-stream",
          Authorization: `Bearer ${YAVER_SDK_TOKEN}`,
        },
      });
      if (!resp.ok || !resp.body) {
        console.warn(`[mentra] command-stream subscribe failed: ${resp.status}`);
        return;
      }
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      while (!controller.signal.aborted) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        // SSE frames are delimited by blank lines; iterate full frames
        let idx: number;
        while ((idx = buffer.indexOf("\n\n")) !== -1) {
          const frame = buffer.slice(0, idx);
          buffer = buffer.slice(idx + 2);
          const dataLine = frame.split("\n").find((l) => l.startsWith("data:"));
          if (!dataLine) continue;
          try {
            const msg = JSON.parse(dataLine.slice(5).trim());
            handleAgentCommand(session, msg);
          } catch {
            // ignore malformed frames
          }
        }
      }
    } catch (e: any) {
      if (e?.name !== "AbortError") {
        console.warn(`[mentra] command-stream loop ended: ${e?.message ?? e}`);
      }
    }
  })();
  return { close: () => controller.abort() };
}

function handleAgentCommand(session: AppSession, frame: any): void {
  const cmd = frame?.command;
  const data = frame?.data ?? {};
  if (!cmd) return;
  try {
    switch (cmd) {
      case "runner_auth_required": {
        const runner = String(data.runner ?? "the runner");
        try { session.layouts.showTextWall(`${runner} reauth needed — check phone`); } catch {}
        (session as any).audio?.speak?.(`${runner} reauth needed. Check your phone.`);
        break;
      }
      case "runner_auth_completed": {
        const runner = String(data.runner ?? "runner");
        try { session.layouts.showTextWall(`${runner} ready`); } catch {}
        (session as any).audio?.speak?.(`${runner} ready.`);
        break;
      }
      case "app_reloaded": {
        const slug = String(data.slug ?? "app");
        const caption = String(data.caption ?? "").trim();
        const text = caption ? `${slug} · ${truncate(caption, 60)}` : `${slug} reloaded`;
        try { session.layouts.showTextWall(text); } catch {}
        (session as any).audio?.speak?.(text);
        break;
      }
    }
  } catch (err) {
    console.warn(`[mentra] handler error for ${cmd}:`, err);
  }
}

const server = new YaverMentraApp({
  packageName: PACKAGE_NAME,
  apiKey: MENTRA_API_KEY,
  port: PORT,
});

server.start();
console.log(`[mentra] yaver miniapp listening on :${PORT}`);
console.log(`[mentra] forwards transcriptions to ${YAVER_AGENT_URL}`);
