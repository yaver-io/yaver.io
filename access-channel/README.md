# Yaver Access Channel

A payload-minimal control plane for headless Yaver devices. It uses Cloudflare
Durable Object WebSocket hibernation and carries only the typed `accessSignal`
grammar (wake, authorization request, access request, presence, result).

It never carries OAuth tokens, source, terminal data, keystrokes, screenshots,
audio, video, or files. Those remain on Yaver's existing authenticated data
paths. JWTs are five-minute capabilities minted by Convex and passed in the
WebSocket subprotocol header, never a URL.

Required production wiring:

1. Set `JWKS_URL` to the Convex `/access-channel/jwks` endpoint.
2. Deploy through the repository release wrapper after its `access-channel`
   target has been explicitly approved.
3. Set Convex `YAVER_ACCESS_CHANNEL_URL` to the resulting `wss://.../v1/connect`.

The Worker retains no messages. Durable Object storage holds only an expiry for
an active OAuth-request coalescing key, preventing repeated prompts while a
request is already pending.
