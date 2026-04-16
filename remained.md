# Remained

- [ ] Bring the home Linux/Hetzner machine online and confirm it appears as `online` in `/console/machines`.
- [ ] Deploy the updated Yaver agent binary on the home machine and restart `yaver serve`.
- [ ] Run `yaver agent mesh-smoke --device <home-device>` from the MacBook and verify remote task execution returns `MESH_SMOKE_OK`.
- [ ] Run one agent graph pinned to the home Linux box and verify node placement, stop behavior, and status polling.
- [ ] Run one `Auto` graph with an iOS/TestFlight-shaped prompt and confirm planning lands on the Mac.
- [ ] Run one `Auto` graph with an Android/Play Store-shaped prompt and confirm build/test nodes land on Linux.
- [ ] Verify mobile Agent Mode shows machine selection and per-node placement reasons.
- [ ] Verify web Console `agent` tab shows machine selection, node placements, and machine capability badges.
- [ ] Check remote workdir assumptions on each machine; make sure the same repo path or synced checkout exists where graphs are dispatched.
- [ ] Expand mesh smoke coverage to autodev/autotest remote loops once the home machine is reachable reliably.
- [ ] Add a stronger provider-cost scoring layer if you want per-runner token economics to influence placement more aggressively.
