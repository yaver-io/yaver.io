package main

// doctor_build_signing.go — two more false-green classes for the build
// doctor, both taken from a real incident (2026-07-19) in which a Mac mini
// could not ship to TestFlight and an entire session was spent on a wrong
// diagnosis. Same wedge as doctor_build_deep.go: the toolchain reports
// green and the deploy dies twenty minutes later.
//
//   • Codesign capability. `security find-identity -v -p codesigning`
//     cheerfully lists the Distribution cert on a machine that cannot sign
//     at all — reading the CERTIFICATE is public, only the PRIVATE KEY is
//     gated. The archive then fails with `errSecInternalComponent`, an
//     error that names none of its three actual causes:
//       1. the identity sits in a LOCKED keychain — and often NOT
//          login.keychain, so the user's login password doesn't help (in
//          the incident the Distribution cert was in a separate keychain
//          whose password nobody had, while login.keychain held only a
//          Development cert);
//       2. a keychain unlock does NOT survive across SSH invocations, so
//          unlocking in one command and signing in the next still fails —
//          the unlock has to happen in the same process as the build;
//       3. even unlocked, the private key's ACL blocks non-GUI callers
//          until `security set-key-partition-list` has granted `codesign:`.
//     The GUI workarounds people reach for first don't apply headlessly:
//     `launchctl asuser` needs root.
//     Because every cheaper signal lies, the only honest probe is to
//     actually sign something — we sign a throwaway copy of a small system
//     binary in a temp dir and throw it away.
//
//   • Disk headroom. An iOS archive needs on the order of 20 GB. In the
//     incident the doctor was green on a box with 162 MB free and the
//     deploy died on "No space left on device" after a long build. The
//     check costs a statfs; discovering it late costs a build.
//
// Both are additive on BuildDoctorReport, so existing clients that only
// read `ok` + `notes` keep working and every surface that renders the
// report (MCP ops verb, HTTP endpoint, mobile/web deploy panes) gets the
// new diagnosis for free.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// BuildSigningStatus reports whether this machine can actually produce a
// signed binary right now — not whether a certificate is merely present.
type BuildSigningStatus struct {
	Checked bool `json:"checked"`
	// CanSign is the result of a real codesign invocation, not an
	// inventory check. It is the only field that should gate a deploy.
	CanSign bool `json:"canSign"`
	// Identity is the certificate common name (e.g. "Apple Distribution:
	// ACME (TEAMID)"). Not a secret — it's embedded in every shipped
	// binary. The SHA-1 hash is deliberately NOT returned: it adds no
	// diagnostic value over the name and needlessly widens the payload.
	Identity string `json:"identity,omitempty"`
	// Keychain is the BASENAME of the keychain holding the identity.
	// Basename only: the absolute path leaks the macOS short username,
	// which the Convex privacy contract forbids in synced payloads.
	Keychain string `json:"keychain,omitempty"`
	// Locked reflects the keychain's lock state at probe time. Advisory:
	// a keychain can be unlocked and still fail to sign (cause #3).
	Locked bool `json:"locked,omitempty"`
	// Repaired is true when the agent self-healed during this probe by
	// unlocking the configured signing keychain (see repairSigningKeychain).
	Repaired bool   `json:"repaired,omitempty"`
	Error    string `json:"error,omitempty"`
	// Remedy is operator-facing next steps, chosen from the failure mode.
	Remedy string `json:"remedy,omitempty"`
}

// BuildDiskStatus reports free space on the volume the build will use.
type BuildDiskStatus struct {
	Checked    bool    `json:"checked"`
	OK         bool    `json:"ok"`
	FreeGB     float64 `json:"freeGB"`
	RequiredGB int     `json:"requiredGB"`
	// Mount is the mount point checked ("/" etc.) — a mount point is not
	// user-identifying, unlike a home-relative path.
	Mount string `json:"mount,omitempty"`
}

// identityLine matches a `security find-identity` row:
//
//  1. A1B2C3… "Apple Distribution: ACME (TEAMID)"
var identityLine = regexp.MustCompile(`^\s*\d+\)\s+([0-9A-Fa-f]{40})\s+"(.+)"\s*$`)

type signingIdentity struct {
	Hash string
	Name string
}

// listSigningIdentities returns the valid codesigning identities in the
// given keychain, or the whole search list when keychain is "".
func listSigningIdentities(ctx context.Context, keychain string) []signingIdentity {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"find-identity", "-v", "-p", "codesigning"}
	if keychain != "" {
		args = append(args, keychain)
	}
	out, err := exec.CommandContext(c, "security", args...).CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil
	}
	var ids []signingIdentity
	for _, line := range strings.Split(string(out), "\n") {
		if m := identityLine.FindStringSubmatch(line); m != nil {
			ids = append(ids, signingIdentity{Hash: m[1], Name: m[2]})
		}
	}
	return ids
}

// preferredSigningIdentity picks the identity a store build needs.
// Distribution outranks Development: an app-store-connect export cannot
// use a Development cert, so a machine with only the latter must not be
// reported as deploy-ready.
func preferredSigningIdentity(ids []signingIdentity) (signingIdentity, bool) {
	for _, want := range []string{"Apple Distribution", "iPhone Distribution", "Developer ID Application"} {
		for _, id := range ids {
			if strings.HasPrefix(id.Name, want) {
				return id, true
			}
		}
	}
	if len(ids) > 0 {
		return ids[0], true
	}
	return signingIdentity{}, false
}

// listKeychains returns the user's keychain search list.
func listKeychains(ctx context.Context) []string {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, "security", "list-keychains").CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil
	}
	var kcs []string
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.Trim(strings.TrimSpace(line), `"`); s != "" {
			kcs = append(kcs, s)
		}
	}
	return kcs
}

// locateIdentityKeychain finds which keychain in the search list holds the
// given identity. Returns the basename (privacy — see BuildSigningStatus)
// and the full path for follow-up commands.
func locateIdentityKeychain(ctx context.Context, hash string) (base, full string) {
	for _, kc := range listKeychains(ctx) {
		for _, id := range listSigningIdentities(ctx, kc) {
			if strings.EqualFold(id.Hash, hash) {
				return filepath.Base(kc), kc
			}
		}
	}
	return "", ""
}

// keychainLocked reports whether a keychain is locked. `security
// show-keychain-info` prints settings when unlocked and fails with
// "User interaction is not allowed." when locked in a non-GUI session.
func keychainLocked(ctx context.Context, keychain string) bool {
	if keychain == "" {
		return false
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := exec.CommandContext(c, "security", "show-keychain-info", keychain).Run()
	return err != nil
}

// codesignProbeBinary is the throwaway we sign. A small, always-present
// Mach-O. We copy it first — never sign anything in place.
const codesignProbeBinary = "/bin/ls"

// attemptCodesign copies a system binary to a temp dir and signs the copy.
// Returns the combined output on failure. This is the ONLY trustworthy
// signal: every cheaper check (cert present, keychain unlocked) can be
// green while signing still fails.
func attemptCodesign(ctx context.Context, hash string) error {
	dir, err := os.MkdirTemp("", "yaver-codesign-probe-")
	if err != nil {
		return fmt.Errorf("could not create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	src, err := os.ReadFile(codesignProbeBinary)
	if err != nil {
		return fmt.Errorf("could not read probe binary: %w", err)
	}
	target := filepath.Join(dir, "probe")
	if err := os.WriteFile(target, src, 0o755); err != nil {
		return fmt.Errorf("could not stage probe binary: %w", err)
	}

	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, "codesign", "--force", "--sign", hash, target).CombinedOutput()
	msg := strings.TrimSpace(string(out))
	// codesign is not reliably non-zero on failure when its output is
	// piped, so treat any errSec* in the output as a failure too.
	if err != nil || strings.Contains(msg, "errSec") {
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return fmt.Errorf("%s", firstLineRaw(msg))
	}
	return nil
}

// repairSigningKeychain is the self-heal path. When the operator has
// configured YAVER_SIGNING_KEYCHAIN + _PASSWORD (vault or the gitignored
// env file), an unlock is safe to attempt automatically: it is idempotent,
// affects only the named keychain, and is exactly what the deploy script
// does before building. Returns true when an unlock was performed.
//
// Deliberately NOT attempted without an explicitly configured keychain —
// guessing at keychains or passwords is not something an agent should do.
func repairSigningKeychain(ctx context.Context) bool {
	kc := strings.TrimSpace(os.Getenv("YAVER_SIGNING_KEYCHAIN"))
	pw := os.Getenv("YAVER_SIGNING_KEYCHAIN_PASSWORD")
	if kc == "" || pw == "" {
		return false
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(c, "security", "unlock-keychain", "-p", pw, kc).Run(); err != nil {
		return false
	}
	return true
}

// signingRemedy maps a codesign failure to the operator's next step. The
// generic "check your certificates" advice is what cost a session in the
// incident, so each branch names the specific fix.
func signingRemedy(errMsg, keychain string, locked bool) string {
	kcName := keychain
	if kcName == "" {
		kcName = "<keychain holding the identity>"
	}
	if strings.Contains(errMsg, "errSecInternalComponent") {
		return fmt.Sprintf(
			"codesign cannot reach the PRIVATE KEY in %s (the certificate is visible, which is why `security find-identity` looks healthy). "+
				"Headless fix: (1) `security unlock-keychain -p <pw> %s`; "+
				"(2) `security set-key-partition-list -S apple-tool-:,apple:,codesign: -s -k <pw> %s` to allow non-GUI signing; "+
				"(3) set YAVER_SIGNING_KEYCHAIN/_PASSWORD so the deploy unlocks it in-process — an unlock does not survive across SSH invocations. "+
				"Note: if the identity is not in login.keychain, your login password will NOT unlock it.",
			kcName, kcName, kcName)
	}
	if locked {
		return fmt.Sprintf("%s is locked — unlock it (`security unlock-keychain %s`) or set YAVER_SIGNING_KEYCHAIN/_PASSWORD for headless deploys.", kcName, kcName)
	}
	if strings.Contains(errMsg, "no identity found") || strings.Contains(errMsg, "ambiguous") {
		return "No usable signing identity — import your Apple Distribution certificate (.p12) into a keychain on this machine."
	}
	return "Run the codesign command manually on this machine to see the full error."
}

// probeSigningCapability answers "can this machine sign right now?" It is
// darwin-only; on other platforms it returns an unchecked status so the
// caller can skip it without special-casing.
func probeSigningCapability(ctx context.Context) BuildSigningStatus {
	st := BuildSigningStatus{}
	if runtime.GOOS != "darwin" {
		return st
	}
	st.Checked = true

	ids := listSigningIdentities(ctx, "")
	id, ok := preferredSigningIdentity(ids)
	if !ok {
		st.Error = "no codesigning identities on this machine"
		st.Remedy = "Import your Apple Distribution certificate (.p12) into a keychain, then re-run the doctor."
		return st
	}
	st.Identity = id.Name

	base, full := locateIdentityKeychain(ctx, id.Hash)
	st.Keychain = base
	st.Locked = keychainLocked(ctx, full)

	err := attemptCodesign(ctx, id.Hash)
	if err == nil {
		st.CanSign = true
		return st
	}

	// Self-heal once, then re-probe. This turns the single most common
	// headless failure into a non-event for the operator.
	if repairSigningKeychain(ctx) {
		st.Repaired = true
		st.Locked = keychainLocked(ctx, full)
		if err2 := attemptCodesign(ctx, id.Hash); err2 == nil {
			st.CanSign = true
			return st
		} else {
			err = err2
		}
	}

	st.Error = sanitisePathInError(err.Error())
	st.Remedy = signingRemedy(st.Error, st.Keychain, st.Locked)
	return st
}

// checkDiskHeadroom verifies the build volume has room. We check the
// volume holding the system temp dir: xcodebuild archives and derived data
// land there (/tmp/Yaver.xcarchive, /tmp/YaverBuild), and on macOS that is
// normally the same data volume as the checkout.
func checkDiskHeadroom(requiredGB int) BuildDiskStatus {
	st := BuildDiskStatus{RequiredGB: requiredGB}
	if requiredGB <= 0 {
		return st
	}
	mount := os.TempDir()
	_, free, ok := statfsGB(mount)
	if !ok {
		return st
	}
	st.Checked = true
	st.Mount = mount
	st.FreeGB = round1(free)
	st.OK = free >= float64(requiredGB)
	return st
}
