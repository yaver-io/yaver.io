// auth_recover_pair.go — sign a bootstrap-mode box back in by driving its
// own pair window, over whatever transport still reaches it.
//
// A box that lost its token is not sealed: while it sits in bootstrap mode
// it serves an UNAUTHENTICATED `/info` carrying a rotating passkey, and an
// unauthenticated `/auth/pair/submit?code=<passkey>` that accepts a
// session. That is the same pair the phone uses on the LAN, and the same
// one `yaver auth send <passkey> <url>` drives by hand. Nothing about it
// requires a human — the passkey is readable by anyone who can reach the
// box, and this machine's own session is the thing worth pushing.
//
// So recovery is: read the passkey, post our session back. Two hops, no
// browser, no typing.
//
// Why not `/auth/recover`? Because for a bootstrap box its recommended
// mode is "pair", and pair only OPENS a window — it hands back a session id
// and waits for someone else to submit a token. Calling it and then waiting
// for the box to go healthy waits forever: the caller IS the someone else.
// `/auth/recover` earns its keep for the auth-EXPIRED case, where the box
// still holds a device identity and can verify a host token.
//
// Transport-agnostic on purpose: this drives the pair window over the same
// candidate ladder every other remote call uses (LAN → mesh → public →
// relay). auth_recover_ssh.go layers SSH underneath for when that ladder is
// empty — which is the common case off-LAN, since an unauthenticated agent
// gets its relay registration rejected.

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// fetchRemoteBootstrapPasskeyHTTP reads the pair code from a box that is
// waiting to be adopted. Unauthenticated by design on the target's side; we
// still send our bearer so the same call is harmless against a healthy box.
func fetchRemoteBootstrapPasskeyHTTP(ctx context.Context, cfg *Config, target *DeviceInfo) (string, RemoteAgentCandidate, error) {
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		return "", RemoteAgentCandidate{}, err
	}
	if len(candidates) == 0 {
		return "", RemoteAgentCandidate{}, fmt.Errorf("device has no transport candidates")
	}
	chosen, status, raw, reqErr := doRemoteAgentRequest(ctx, candidates, cfg.AuthToken, http.MethodGet, "/info", nil, 8*time.Second)
	if reqErr != nil {
		return "", RemoteAgentCandidate{}, reqErr
	}
	if status >= 300 {
		return "", chosen, fmt.Errorf("%s", extractRemoteError(status, raw))
	}
	code, err := parseBootstrapPasskey(raw)
	if err != nil {
		return "", chosen, err
	}
	return code, chosen, nil
}

// pushTokenToRemotePairWindowHTTP submits this machine's session to an open
// pair window.
func pushTokenToRemotePairWindowHTTP(ctx context.Context, cfg *Config, target *DeviceInfo, code string, payload []byte) error {
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return fmt.Errorf("device has no transport candidates")
	}
	path := "/auth/pair/submit?code=" + urlQueryEscape(strings.ToUpper(strings.TrimSpace(code)))
	_, status, raw, reqErr := doRemoteAgentRequest(ctx, candidates, cfg.AuthToken, http.MethodPost, path, payload, 15*time.Second)
	if reqErr != nil {
		return reqErr
	}
	if status >= 300 {
		return fmt.Errorf("%s", extractRemoteError(status, raw))
	}
	return nil
}

// recoverDeviceAuthViaPairWindow signs a bootstrap-mode box back in over
// HTTP. Returns the transport label that carried it.
func recoverDeviceAuthViaPairWindow(ctx context.Context, cfg *Config, target *DeviceInfo) (string, error) {
	payload, err := pairSubmitPayload(cfg)
	if err != nil {
		return "", err
	}
	code, chosen, err := fetchRemoteBootstrapPasskeyHTTP(ctx, cfg, target)
	if err != nil {
		return "", err
	}
	if err := pushTokenToRemotePairWindowHTTP(ctx, cfg, target, code, payload); err != nil {
		return "", err
	}
	return firstNonEmpty(strings.TrimSpace(chosen.Label), chosen.Kind, "direct"), nil
}
