package main

// flight_cmd.go — `yaver flight` reads a device's black box.
//
// The recorder (flightrecorder.go) is useless without this: it faithfully
// records WHY a box died, and without a readback you would have to open the
// Convex dashboard by hand to see it.
//
// Two sources, deliberately:
//
//   yaver flight                 the LOCAL buffer — works with no network, no
//                                Convex, and no auth. This matters: the box you
//                                are standing in front of after a power cut is
//                                exactly the one that could not phone home.
//   yaver flight --device <x>    a REMOTE box's history from Convex, which is
//                                the only view available while that box is still
//                                down.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// flightRemoteEvent is the wire shape of GET /devices/flight. `At`/`CreatedAt`
// are ms epoch (Convex numbers), unlike the local buffer's RFC3339 strings.
type flightRemoteEvent struct {
	Session   string `json:"session"`
	Kind      string `json:"kind"`
	Detail    string `json:"detail"`
	At        int64  `json:"at"`
	CreatedAt int64  `json:"createdAt"`
}

func runFlight(args []string) {
	fs := flag.NewFlagSet("flight", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	device := fs.String("device", "", "read a remote device's black box from Convex (alias, name, or deviceId); default = this machine's local buffer")
	limit := fs.Int("limit", flightRecorderMaxEvents, "max events to show, newest first")
	asJSON := fs.Bool("json", false, "emit raw JSON")
	if err := fs.Parse(args); err != nil {
		return
	}
	if *limit <= 0 {
		fmt.Fprintln(os.Stderr, "flight: --limit must be positive")
		os.Exit(1)
	}

	if strings.TrimSpace(*device) == "" {
		printLocalFlight(*limit, *asJSON)
		return
	}
	printRemoteFlight(strings.TrimSpace(*device), *limit, *asJSON)
}

func printLocalFlight(limit int, asJSON bool) {
	events := FlightEvents()
	// Newest-first is the useful order: the last record before silence is the
	// whole point.
	reversed := make([]FlightEvent, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		reversed = append(reversed, events[i])
	}
	if len(reversed) > limit {
		reversed = reversed[:limit]
	}
	if asJSON {
		emitJSON(reversed)
		return
	}
	if len(reversed) == 0 {
		fmt.Println("No flight events recorded on this machine yet.")
		fmt.Println("The agent writes one on every start and graceful stop; run 'yaver serve' at least once.")
		return
	}
	fmt.Printf("Black box for this machine (%d event(s), newest first)\n\n", len(reversed))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "WHEN\tKIND\tDETAIL")
	for _, ev := range reversed {
		fmt.Fprintf(w, "%s\t%s\t%s\n", formatFlightTime(ev.At), ev.Kind, ev.Detail)
	}
	w.Flush()
	fmt.Println()
	fmt.Println(flightVerdict(reversed))
}

func printRemoteFlight(device string, limit int, asJSON bool) {
	cfg := mustLoadAuthConfig()
	deviceID, err := resolveFlightDeviceID(cfg, device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flight: %v\n", err)
		os.Exit(1)
	}
	events, err := fetchRemoteFlight(cfg, deviceID, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flight: %v\n", err)
		os.Exit(1)
	}
	if asJSON {
		emitJSON(events)
		return
	}
	if len(events) == 0 {
		fmt.Printf("No flight events synced for %s yet.\n", device)
		fmt.Println("A box ships its buffered events on the first heartbeat after it comes back up,")
		fmt.Println("so a box that is still down will have nothing newer than its last boot.")
		return
	}
	fmt.Printf("Black box for %s (%d event(s), newest first)\n\n", device, len(events))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "WHEN\tKIND\tDETAIL")
	for _, ev := range events {
		fmt.Fprintf(w, "%s\t%s\t%s\n", formatFlightTimeMs(ev.At), ev.Kind, ev.Detail)
	}
	w.Flush()
	fmt.Println()
	local := make([]FlightEvent, 0, len(events))
	for _, ev := range events {
		local = append(local, FlightEvent{Session: ev.Session, Kind: ev.Kind, Detail: ev.Detail, At: time.UnixMilli(ev.At).UTC().Format(time.RFC3339)})
	}
	fmt.Println(flightVerdict(local))
}

// flightVerdict turns the newest-first history into the one line the operator
// actually wants: did this box stop gracefully, or did it die?
//
// This mirrors detectUncleanStop's rule rather than re-deriving it: a graceful
// stop always writes `shutdown`, so the newest record tells the story.
func flightVerdict(newestFirst []FlightEvent) string {
	if len(newestFirst) == 0 {
		return "No verdict: nothing recorded."
	}
	switch newestFirst[0].Kind {
	case flightKindShutdown:
		return "Verdict: last stop was GRACEFUL (the agent recorded its own shutdown)."
	case flightKindUncleanStop:
		return "Verdict: the previous run DIED HARD — power loss, panic, or a forced kill. " +
			"Not a Yaver crash unless a `boot` with no `shutdown` repeats.\n" +
			"          Cause: " + newestFirst[0].Detail
	case flightKindBoot:
		return "Verdict: the agent is RUNNING (booted, no stop recorded yet). " +
			"If this box has gone silent since, it stopped without warning."
	default:
		return "Verdict: last event was " + newestFirst[0].Kind + "."
	}
}

func resolveFlightDeviceID(cfg *Config, device string) (string, error) {
	// An explicit deviceId should work even when the device row is gone or the
	// list call fails — a box whose registration is broken is one you may most
	// need the history for.
	devices, err := listDevicesEnsuringAuth(cfg)
	if err != nil {
		return device, nil
	}
	lower := strings.ToLower(device)
	var matches []string
	for _, d := range devices {
		if strings.EqualFold(d.DeviceID, device) {
			return d.DeviceID, nil
		}
		if strings.ToLower(d.Name) == lower || strings.HasPrefix(strings.ToLower(d.DeviceID), lower) {
			matches = append(matches, d.DeviceID)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return device, nil // let the backend decide; it may still be a raw id
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%q matches %d devices (%s); use a full deviceId", device, len(matches), strings.Join(matches, ", "))
	}
}

func fetchRemoteFlight(cfg *Config, deviceID string, limit int) ([]flightRemoteEvent, error) {
	url := fmt.Sprintf("%s/devices/flight?deviceId=%s&limit=%d", cfg.ConvexSiteURL, deviceID, limit)
	req, err := newBearerRequest("GET", url, cfg.AuthToken, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("unauthorized — run `yaver auth`")
	case http.StatusNotFound:
		return nil, fmt.Errorf("device %s is not registered to this account", deviceID)
	default:
		return nil, fmt.Errorf("read flight events failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		Events []flightRemoteEvent `json:"events"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode flight events: %w", err)
	}
	return result.Events, nil
}

func emitJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func formatFlightTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return formatFlightInstant(t)
}

func formatFlightTimeMs(ms int64) string {
	return formatFlightInstant(time.UnixMilli(ms))
}

// formatFlightInstant shows local time plus an age, because "when did it die"
// is always the question — an absolute UTC stamp alone makes you do arithmetic.
func formatFlightInstant(t time.Time) string {
	age := time.Since(t)
	return fmt.Sprintf("%s (%s ago)", t.Local().Format("2006-01-02 15:04:05"), roundFlightAge(age))
}

func roundFlightAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
