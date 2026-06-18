# Contributing to Yaver

Thanks for wanting to contribute! Yaver is open source and we welcome
bug reports, feature requests, documentation fixes, and code
contributions.

## How to contribute

1. **File an issue first** for anything non-trivial. It lets us align
   on scope and prevents wasted work.
2. **Fork the repo** and create a topic branch off `main`.
3. **Make your change.** Keep the diff scoped — one concern per PR.
   Include tests where it makes sense.
4. **Run the local test suite**: `./scripts/test-suite.sh --unit --lan --relay`.
5. **Run the full GitHub CI locally**: `./scripts/run-ci-local.sh`.
   If that passes, GitHub Actions will almost certainly pass.
6. **Open a pull request** against `main`. Reference the issue it
   fixes.

## Licensing of contributions

By submitting a pull request, you agree that:

1. Your contribution is licensed under the same license as the file or
   package you are modifying. See [`LICENSING.md`](../planning/LICENSING.md) for
   the mapping — briefly:
   - Client SDKs and CLIs (`cli/`, `sdk/*`) → **Apache-2.0**.
   - Core server / agent / relay / control-plane / Yaver&nbsp;UI apps →
     **FSL-1.1-Apache-2.0** (Functional Source License, auto-converts
     to Apache-2.0 two years after each release).
2. You grant **SIMKAB ELEKTRIK** a perpetual, irrevocable, worldwide,
   royalty-free license to use, modify, sublicense, and relicense your
   contribution, including under the commercial licenses offered as
   part of the Yaver dual-licensing program.
3. You have the right to grant these licenses — the work is yours, or
   your employer has authorized the contribution.

## Developer Certificate of Origin (DCO)

We use the [Developer Certificate of Origin](https://developercertificate.org/)
instead of a heavyweight CLA. Every commit must carry a `Signed-off-by:`
trailer, which certifies:

> The contribution was created in whole or in part by me and I have the
> right to submit it under the open source license indicated in the file;
> or the contribution is based upon previous work that, to the best of my
> knowledge, is covered under an appropriate open source license and I
> have the right under that license to submit that work with
> modifications.

To add the sign-off to every commit, use:

```bash
git commit -s -m "your commit message"
```

That appends:

```
Signed-off-by: Your Name <you@example.com>
```

If you forget, fix it with:

```bash
git commit --amend -s           # last commit
git rebase --signoff HEAD~N     # last N commits
```

CI will eventually enforce DCO. In the meantime, PRs without a
sign-off will get a gentle reminder from a reviewer.

## Coding conventions

See [`CLAUDE.md`](../../CLAUDE.md) (yes, the AI-agent guide doubles as the
human style guide) for the house rules. The short version:

- **Go**: standard layout, `gofmt`, run `go test ./...` before opening.
- **TypeScript / React**: functional components, hooks, `tsc --noEmit`
  must pass. Use the existing design tokens (`text-surface-*`, etc.) —
  don't introduce new color systems.
- **Convex**: mutations for writes, queries for reads, HTTP actions
  for OAuth callbacks.
- **Mobile**: native builds only (`xcodebuild` / `gradle`) — do not
  use Expo CLI.
- **No commit-auto-bumps-a-user's-version** changes in shared PRs —
  keep version bumps in their own commit.

## Security issues

Do **not** open a public issue for a security vulnerability. Email
[security@yaver.io](mailto:security@yaver.io) (alias for
`kivanc.cakmak@simkab.com`). We will acknowledge within 48 hours.

## Commercial license / relicensing

If you want to use the AGPL-licensed core of Yaver without the AGPL
obligations (for example, bundling a modified agent into a closed-source
product), a commercial license is available. Contact
[kivanc.cakmak@simkab.com](mailto:kivanc.cakmak@simkab.com).

## Questions

For general questions, open a GitHub Discussion, or reach out via the
links on [yaver.io](https://yaver.io).

Thanks for contributing to Yaver!
