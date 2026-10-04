# Zero-custody remote workers and cloud provisioning

Provider control is a privileged endpoint tool. Yaver-operated infrastructure
must never receive a user's Hetzner, AWS, Google Cloud, Alibaba Cloud, Git, SSH
or AI-provider credential.

Supported custody modes:

1. User-controlled local provisioner (preferred).
2. Native mobile direct-to-provider client.
3. User-owned always-on `yaver-provisionerd` on a server/NAS/VPS.

Cloudflare/Relay Free may transport only an E2EE signed command to that trusted
endpoint. A provider API token stored as a Worker secret, KV value or Convex
field is prohibited. AWS/GCP/Alibaba integrations should prefer short-lived
federation rather than long-lived access keys.

Infrastructure operations default to `ask`. The endpoint enforces an allowlist
of regions/types, concurrent-resource and estimated-spend ceilings, TTL and
idle shutdown. Destructive operations require exact resource confirmation and
idempotency/replay protection.

Cloud-init contains only the agent, public/one-time bootstrap material and a
relay rendezvous address. It must not contain model/Git/provider credentials or
an account master key. Each worker gets fresh identity material, establishes
E2EE, receives secrets only inside that channel (preferably memory/pipe), and is
revoked on destruction.

Normal VPS execution is not confidential computing. Yaver's control plane can
be blind while the VM/OS/hypervisor can still observe plaintext processed there.

