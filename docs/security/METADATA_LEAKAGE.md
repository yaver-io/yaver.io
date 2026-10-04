# Metadata leakage

Application E2EE does not hide all traffic metadata. Yaver/Cloudflare may see
source IP, opaque account/device/session identifiers, public keys, connection
times, packet sizes, duration, reconnect frequency, participant count, traffic
volume, subscription and billing information.

Do not expose semantic message type, model, command, filename, repository,
working directory, tool, MIME type or human-readable notification text outside
ciphertext. APNs/FCM payloads carry only an opaque wake/session hint; the app
decrypts and renders locally.

Future optional mitigations include coarse padding buckets, batching, cover
traffic and relay chaining. They increase cost and latency and are not part of
the initial guarantee. Product wording must say “Yaver cannot decrypt session
content,” not “Yaver learns nothing.”

