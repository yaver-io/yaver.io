# Persistent mobile test coordinator

This coordinator keeps a mobile test running when the initiating terminal,
SSH connection, or coding-agent session disappears. Jobs and checkpoints are
owner-only files under `~/.yaver/mobile-test`; test output is append-only under
the job's artifact directory.

```bash
cd e2e
npm run mobile-test -- doctor
npm run mobile-test -- enqueue --scenario contract-smoke
npm run mobile-test -- status
npm run mobile-test -- follow <job-id>
```

Install the macOS coordinator with
`./scripts/install-mobile-test-coordinator.sh`. The launch agent owns execution;
the CLI only adds or observes jobs. A killed coordinator adopts a still-running
step after restart. It never launches a second copy of that step.

Product manifests contain argv arrays, never shell programs or secret values.
Live scenarios name the environment variables they require. Missing values end
with a `NAMED / MISSING_ENV` verdict and a configuration route rather than a
spinner or silent skip. Add another product by providing its own manifest with
the same contract and passing `--manifest /path/to/product.json`.

Yaver's initial lanes are:

- `contract-smoke`: deterministic Studio/mobile contracts;
- `fresh-onboarding`: real RN-web onboarding in a mobile device context;
- `rn-web-overview`: route-by-route mobile overview;
- `ios-native-stream`: native Simulator frame loop;
- `android-native-vibe`: native emulator and pixel verdict.

The coordinator does not put test credentials in its journal. Configure live
credentials in the launch-agent environment or an owner-only wrapper, then
reinstall/restart the service. Artifacts and manifests must remain free of
tokens, customer names, private hostnames, and machine addresses.
