package main

// flightrecorder.go — the black box for remote boxes.
//
// WHY THIS EXISTS
//
// When a remote box goes silent, every layer we own goes silent with it:
// Tailscale, Yaver Mesh, the QUIC relay and the agent are all *processes on
// that box*. Their absence tells you the box stopped executing code; it never
// tells you WHY. On 2026-07-16 the Mac mini dropped mid-run and the only
// evidence available was negative — frozen Tailscale rx counters and a DERP
// ping timeout — which cannot separate "the OS slept it", "power was cut",
// "the network died" and "our agent crashed". That ambiguity is the bug this
// file fixes: it makes the *last breath* durable, so the next boot can say
// what happened instead of leaving you to infer it.
//
// The aviation analogy is load-bearing. A flight recorder is small, bounded,
// survives the event it records, and is read AFTER recovery — not streamed
// live. So: events are written locally as they happen (a box that is losing
// power cannot phone home), the buffer is capped, and the sync to Convex
// happens once the box is back up.
//
// THE CENTRAL INFERENCE
//
// A graceful stop always writes a `shutdown` record. So if a session's last
// record is anything else, that session did NOT stop gracefully — the box was
// powered off, panicked, or was killed. That single rule is what distinguishes
// "not our software" from "our bug", and it needs no OS cooperation at all.
// The OS shutdown cause (below) is corroboration, not the primary signal.
//
// WRITE BUDGET
//
// Lifecycle events are inherently rare (a boot, a sleep, a shutdown), so this
// costs a handful of Convex rows per box per day — not a log stream. The caps
// are enforced in three independent places so no future caller can turn this
// into one: the local buffer keeps flightRecorderMaxEvents, the sync ships only
// what is new, and the Convex mutation prunes to the same bound server-side.
// Never call recordFlightEvent from a loop, a poll, or a per-request path.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// flightRecorderMaxEvents bounds both the local buffer and the server-side
// table. 50 covers weeks of real lifecycle history for a box that boots daily,
// while keeping a full sync to one small request.
const flightRecorderMaxEvents = 50

// Event kinds. Deliberately a small closed set: this is a lifecycle recorder,
// not a log. Anything that would fire more than a few times a day does not
// belong here.
const (
	flightKindBoot        = "boot"         // agent started
	flightKindShutdown    = "shutdown"     // agent stopped gracefully (signal received)
	flightKindUncleanStop = "unclean_stop" // inferred at boot: a previous session never wrote `shutdown`
	flightKindSleep       = "sleep"        // OS reported a sleep, recovered post-hoc from the OS log
	flightKindWake        = "wake"         // OS reported a wake, recovered post-hoc from the OS log
)

// FlightEvent is one record. The field set is deliberately narrow so it stays
// inside the Convex privacy contract: it is an activity audit summary (action +
// outcome + timestamp) and nothing else.
//
// It MUST NOT grow a path, a LAN IP, a hostname, a token or any command output.
// Detail is a short, bounded, human-readable cause — never free-form logs.
type FlightEvent struct {
	// Session is a per-process random id. It is what lets a later boot
	// attribute an orphaned record to the exact run that died.
	Session string `json:"session"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail,omitempty"`
	At      string `json:"at"` // RFC3339 UTC
}

// flightDetailMaxLen keeps a pathological OS log line from becoming a data
// dump. Causes we care about are a few words ("Shutdown Cause: -128").
const flightDetailMaxLen = 200

type flightRecorder struct {
	mu      sync.Mutex
	path    string
	session string
}

var (
	flightRecorderOnce     sync.Once
	flightRecorderInstance *flightRecorder
)

// newFlightRecorder is exported to tests via a path override so the real
// ~/.yaver buffer is never touched by a test run.
func newFlightRecorder(path, session string) *flightRecorder {
	return &flightRecorder{path: path, session: session}
}

func flightRecorderPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".yaver", "flight-recorder.json")
}

// getFlightRecorder returns the process-wide recorder, or nil when no home
// directory is resolvable. A nil recorder makes every method a no-op: the black
// box is diagnostic, so it must never be able to take the agent down with it.
func getFlightRecorder() *flightRecorder {
	flightRecorderOnce.Do(func() {
		path := flightRecorderPath()
		if path == "" {
			return
		}
		flightRecorderInstance = newFlightRecorder(path, newFlightSessionID())
	})
	return flightRecorderInstance
}

func newFlightSessionID() string {
	// Not security-sensitive — it only needs to be distinct per process so a
	// later boot can tell sessions apart.
	return fmt.Sprintf("%d-%d", time.Now().UTC().UnixNano(), os.Getpid())
}

func (r *flightRecorder) read() ([]FlightEvent, error) {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var events []FlightEvent
	if err := json.Unmarshal(data, &events); err != nil {
		// A corrupt buffer must not wedge the agent or hide future events.
		// Losing old diagnostic history is strictly better than refusing to
		// record new history, so we start clean rather than propagate.
		return nil, nil
	}
	return events, nil
}

// write persists atomically: a box losing power mid-write is precisely the
// scenario this file exists to record, so a torn buffer is not acceptable.
func (r *flightRecorder) write(events []FlightEvent) error {
	if len(events) > flightRecorderMaxEvents {
		events = events[len(events)-flightRecorderMaxEvents:]
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(events)
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// record appends one event stamped now and trims to the cap. Errors are returned
// for tests but callers in the agent deliberately ignore them.
func (r *flightRecorder) record(kind, detail string) error {
	return r.recordAt(kind, detail, time.Now())
}

// recordAt appends one event stamped at an explicit instant.
//
// Most events happen when we notice them, but not all: a `sleep` is only ever
// observed retroactively, on resume, and stamping it "now" would place the
// suspend at the moment the machine woke — inverting the timeline the recorder
// exists to get right. The honest stamp is the last moment the machine was seen
// awake, which only the caller knows.
func (r *flightRecorder) recordAt(kind, detail string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	events, err := r.read()
	if err != nil {
		return err
	}
	if len(detail) > flightDetailMaxLen {
		detail = detail[:flightDetailMaxLen]
	}
	events = append(events, FlightEvent{
		Session: r.session,
		Kind:    kind,
		Detail:  strings.TrimSpace(detail),
		At:      at.UTC().Format(time.RFC3339),
	})
	return r.write(events)
}

// detectUncleanStop is the core inference, run once at boot BEFORE this session
// records anything.
//
// It looks at the most recent session in the buffer. If that session exists, is
// not us, and never wrote a `shutdown`, then it died without warning: power
// loss, panic, forced kill, or an OS that never gave us a signal. We record that
// verdict against the DEAD session's id so the timeline reads honestly.
//
// Returns the event recorded, or nil when the previous session ended cleanly (or
// there is no previous session).
func (r *flightRecorder) detectUncleanStop(priorCause string) *FlightEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	events, err := r.read()
	if err != nil || len(events) == 0 {
		return nil
	}
	last := events[len(events)-1]
	if last.Session == r.session || last.Kind == flightKindShutdown {
		return nil
	}
	detail := "previous session ended without a graceful shutdown record"
	if strings.TrimSpace(priorCause) != "" {
		detail = priorCause
	}
	if len(detail) > flightDetailMaxLen {
		detail = detail[:flightDetailMaxLen]
	}
	ev := FlightEvent{
		Session: last.Session, // attribute to the session that died, not to us
		Kind:    flightKindUncleanStop,
		Detail:  detail,
		At:      time.Now().UTC().Format(time.RFC3339),
	}
	events = append(events, ev)
	if err := r.write(events); err != nil {
		return nil
	}
	return &ev
}

// shutdownCauseRe matches the macOS pmset log's shutdown cause line. Negative
// codes are the interesting ones (-128 = power loss / hard reset, -60 =
// watchdog); 5 is a normal user-initiated shutdown.
var shutdownCauseRe = regexp.MustCompile(`Shutdown Cause:\s*(-?\d+)`)

// priorShutdownCause asks the OS why it last went down. Best-effort and
// non-fatal: it is corroboration for detectUncleanStop, never a dependency.
// A box where this returns "" still gets the unclean-stop verdict.
func priorShutdownCause(ctx context.Context) string {
	switch runtime.GOOS {
	case "darwin":
		// pmset's log carries the SMC's own shutdown cause, which survives a
		// power cut precisely because it is not our process writing it.
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "pmset", "-g", "log").Output()
		if err != nil {
			return ""
		}
		matches := shutdownCauseRe.FindAllStringSubmatch(string(out), -1)
		if len(matches) == 0 {
			return ""
		}
		code := matches[len(matches)-1][1]
		return "macOS shutdown cause " + code + ": " + describeShutdownCause(code)
	case "linux":
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// -1 is the previous boot; a non-zero exit just means no persistent
		// journal, which is common and not worth surfacing.
		out, err := exec.CommandContext(ctx, "journalctl", "-b", "-1", "-n", "1", "--no-pager", "-o", "cat").Output()
		if err != nil {
			return ""
		}
		line := strings.TrimSpace(string(out))
		if line == "" {
			return ""
		}
		return "last line of previous boot journal: " + line
	}
	return ""
}

// describeShutdownCause translates the codes worth knowing. Anything else is
// reported by number rather than guessed at.
func describeShutdownCause(code string) string {
	switch code {
	case "0":
		return "power loss or hard reset"
	case "-128":
		return "power loss or hard reset"
	case "-60":
		return "watchdog / kernel panic"
	case "-3":
		return "hard restart (power button held)"
	case "3":
		return "hard shutdown (power button held)"
	case "5":
		return "clean OS shutdown"
	default:
		return "see Apple's SMC shutdown cause table"
	}
}

// sortFlightEvents orders oldest-first by timestamp. The buffer is already
// append-ordered; this is belt-and-braces for a buffer merged across sessions.
func sortFlightEvents(events []FlightEvent) {
	sort.SliceStable(events, func(i, j int) bool { return events[i].At < events[j].At })
}

// RecordFlightBoot is called once, early in agent startup. It writes this
// session's `boot` and, if the previous session died without a shutdown record,
// the `unclean_stop` verdict for it.
//
// Call this BEFORE the agent does anything that could itself crash, so the boot
// record exists even for a run that dies immediately.
func RecordFlightBoot(ctx context.Context) {
	r := getFlightRecorder()
	if r == nil {
		return
	}
	// The OS probe runs first so its cause can be attached to the verdict, but
	// it must never delay or block the boot record if the OS is slow.
	cause := priorShutdownCause(ctx)
	r.detectUncleanStop(cause)
	_ = r.record(flightKindBoot, "agent started")
}

// RecordFlightShutdown is called from the agent's signal handler. Its presence
// in the buffer is what makes the NEXT boot able to say "this was clean" — so
// it must be on every graceful exit path, and on no other path.
func RecordFlightShutdown(signal string) {
	r := getFlightRecorder()
	if r == nil {
		return
	}
	_ = r.record(flightKindShutdown, "agent stopped on "+signal)
}

// FlightEvents returns the local buffer oldest-first, for the sync path and for
// `yaver flight`.
func FlightEvents() []FlightEvent {
	r := getFlightRecorder()
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	events, err := r.read()
	if err != nil {
		return nil
	}
	sortFlightEvents(events)
	return events
}

// --- sync watermark -------------------------------------------------------
//
// The recorder ships its buffer to Convex on the heartbeat. Without a watermark
// it would re-send the same 50 events on every beat forever — the "extreme data
// writing" this feature must not become. The watermark is the timestamp of the
// newest event Convex has acknowledged; only newer events are sent.
//
// It advances ONLY after a successful heartbeat. A failed sync therefore
// re-sends, which is correct — and the server dedups on (session, kind, at), so
// a re-send can never double-record.

// flightSyncMark is the persisted watermark. Kept in its own small file so the
// event buffer's on-disk format stays a plain array.
type flightSyncMark struct {
	At string `json:"at"` // RFC3339 UTC of the newest synced event
}

func (r *flightRecorder) markPath() string { return r.path + ".synced" }

func (r *flightRecorder) readMark() string {
	data, err := os.ReadFile(r.markPath())
	if err != nil {
		return ""
	}
	var mark flightSyncMark
	if err := json.Unmarshal(data, &mark); err != nil {
		return ""
	}
	return mark.At
}

func (r *flightRecorder) writeMark(at string) error {
	data, err := json.Marshal(flightSyncMark{At: at})
	if err != nil {
		return err
	}
	tmp := r.markPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.markPath())
}

// unsynced returns the events newer than the watermark, oldest-first.
func (r *flightRecorder) unsynced() []FlightEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	events, err := r.read()
	if err != nil || len(events) == 0 {
		return nil
	}
	sortFlightEvents(events)
	mark := r.readMark()
	if mark == "" {
		return events
	}
	var out []FlightEvent
	for _, ev := range events {
		// Strictly-after: an event exactly at the watermark is already synced.
		if ev.At > mark {
			out = append(out, ev)
		}
	}
	return out
}

// markSynced advances the watermark to the newest event in the batch that was
// just accepted.
func (r *flightRecorder) markSynced(events []FlightEvent) {
	if len(events) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	newest := events[0].At
	for _, ev := range events {
		if ev.At > newest {
			newest = ev.At
		}
	}
	_ = r.writeMark(newest)
}

// flightEventPayload is the heartbeat wire shape. Field names must match the
// `flightEvents` validator in backend/convex/devices.ts.
type flightEventPayload struct {
	Session string `json:"session"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail,omitempty"`
	AtMs    int64  `json:"atMs"`
}

// PendingFlightEvents returns the wire payload for the next heartbeat, plus the
// events it covers so the caller can confirm them on success. Returns nil when
// there is nothing new — the steady-state case, which must cost nothing.
func PendingFlightEvents() ([]flightEventPayload, []FlightEvent) {
	r := getFlightRecorder()
	if r == nil {
		return nil, nil
	}
	events := r.unsynced()
	if len(events) == 0 {
		return nil, nil
	}
	payload := make([]flightEventPayload, 0, len(events))
	for _, ev := range events {
		at, err := time.Parse(time.RFC3339, ev.At)
		if err != nil {
			continue
		}
		payload = append(payload, flightEventPayload{
			Session: ev.Session,
			Kind:    ev.Kind,
			Detail:  ev.Detail,
			AtMs:    at.UTC().UnixMilli(),
		})
	}
	if len(payload) == 0 {
		return nil, nil
	}
	return payload, events
}

// ConfirmFlightEventsSynced advances the watermark. Call ONLY after the
// heartbeat carrying these events returned success.
func ConfirmFlightEventsSynced(events []FlightEvent) {
	r := getFlightRecorder()
	if r == nil {
		return
	}
	r.markSynced(events)
}
