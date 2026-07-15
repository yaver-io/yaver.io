# visionOS UI tests

These drive the **real app** in the visionOS simulator with real taps, against a
stub agent that speaks the agent's `/ops` wire protocol.

They exist because everything interesting about this surface is a *reaction to
something awkward the backend said* — "nothing was listening", "no dev server is
running" — and none of that is reachable from a Go test or a compile. The bug
class this surface actually had was **buttons that looked dead**: the agent
returns a refusal as HTTP 200 with `{"ok":false,"error":"…"}`, and a client that
only checks the status code shows nothing at all. That is a UI fact, and it needs
a UI test.

## Running them

```sh
# 1. start the stub agent (leave it running)
cd visionos/YaverVisionUITests/stubagent && go run . 18099

# 2. run the tests against a booted Vision Pro simulator
cd visionos && xcodegen generate
xcodebuild test -project visionos/YaverVision.xcodeproj -scheme YaverVision \
  -destination 'platform=visionOS Simulator,name=Apple Vision Pro' \
  CODE_SIGNING_ALLOWED=NO
```

If the stub isn't running the tests **skip** with instructions rather than
failing — a red suite should mean the app is wrong, not that you forgot a
terminal.

## How the app gets signed in

Purely through UserDefaults' *argument domain* (`-key value` launch arguments
outrank the standard domain), so there is **no test hook in production code** and
nothing touches the keychain.

One trap, paid for once: UserDefaults **property-list-parses** each `-key value`
pair. A bare JSON array (`[{...}]`) is not a valid plist, so the pair is silently
dropped — and the app then quietly falls back to whatever box the simulator still
has persisted. That is not a clean slate: simulator prefs live in the shared
`cfprefsd` store and **survive `simctl uninstall`**. The first run of these tests
sat on a leftover machine from a months-old session and timed out against an IP
that no longer existed. The fix is to wrap the JSON as a *quoted plist string*
(see `launchApp()`), which the parser hands back verbatim.

## Scenarios

The stub is driven over `POST /__scenario`, which is how a single run walks the
app through states a healthy machine never produces:

| scenario | `reload` returns | what it proves |
|---|---|---|
| `delivered` | `deliveredTo: 2` | the happy path reports success |
| `nobody` | `deliveredTo: 0` | a reload/push nothing received **warns** instead of claiming success |
| `refused` | HTTP 200 `{ok:false,error}` | the agent's refusal reaches a pixel — no dead button |

The session tests lean on the stub the same way: `/runner/session/turn` **refuses
a turn that arrives without a session name**, exactly as the agent does when more
than one runner is live. So a reply appearing in the pane is itself the proof that
the surface named the session instead of letting the backend guess.
