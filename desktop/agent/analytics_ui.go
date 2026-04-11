package main

// analytics_ui.go — PostHog / Plausible-lite views on top of
// the existing TrackEvent ledger. Adds:
//
//   - GET /analytics/views         JS snippet for page-view + session tracking
//   - GET /analytics/top?since=    top pages / referrers / sources rollup
//   - GET /analytics/funnel?steps= funnel analysis over a comma-separated
//                                  event-name sequence
//   - GET /analytics/retention?    weekly retention cohort grid
//   - GET /analytics/summary       total unique visitors + sessions over N days
//
// The dev pastes the snippet into their landing page <head> and
// the agent starts tallying page views. Everything else runs on
// top of the same events.jsonl file analytics_events.go already
// maintains — no new storage, no migrations.
//
// Scope limit: we don't try to match PostHog's full feature set.
// Solo devs need top pages + funnels + retention + cohort view.
// That's 90% of the data value, 10% of the code.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// handleAnalyticsViewsJS serves the drop-in JavaScript the dev
// pastes into their landing page. Zero external deps.
func (s *HTTPServer) handleAnalyticsViewsJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	base := publicOauthBase(r)
	fmt.Fprintf(w, `(function(){
  var VID = localStorage.getItem("yaver_analytics_vid");
  if (!VID) { VID = "v-" + Math.random().toString(36).slice(2); localStorage.setItem("yaver_analytics_vid", VID); }
  var API = %q;
  function track(name, props) {
    var body = { name: name, props: Object.assign({}, props || {}), route: location.pathname + location.search };
    body.props.vid = VID;
    body.props.referrer = document.referrer || "";
    body.props.origin = location.hostname;
    navigator.sendBeacon ?
      navigator.sendBeacon(API + "/analytics/ingest", new Blob([JSON.stringify(body)], { type: "application/json" })) :
      fetch(API + "/analytics/ingest", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), keepalive: true });
  }
  // Initial pageview
  track("pageview");
  // SPA route changes
  var push = history.pushState;
  history.pushState = function() { push.apply(this, arguments); track("pageview"); };
  window.addEventListener("popstate", function() { track("pageview"); });
  window.yaver = window.yaver || {};
  window.yaver.track = track;
})();
`, base)
}

// --- shared reader ---------------------------------------------------------

// readAllEvents walks events.jsonl (both current + .old rotation)
// and returns every row. Fine at solo-dev scale; we can memoise
// or stream later if it matters.
func readAllEvents() []TrackEvent {
	p, err := analyticsPath()
	if err != nil {
		return nil
	}
	out := []TrackEvent{}
	for _, candidate := range []string{p + ".old", p} {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if line == "" {
				continue
			}
			var ev TrackEvent
			if err := json.Unmarshal([]byte(line), &ev); err == nil {
				out = append(out, ev)
			}
		}
	}
	return out
}

// parseSince returns the unix-ms lower bound from `?since=…`
// (accepts ms, "7d", "24h").
func parseSince(s string) int64 {
	if s == "" {
		return 0
	}
	if v, err := strconv.ParseInt(s, 10, 64); err == nil {
		return v
	}
	if strings.HasSuffix(s, "d") {
		n, _ := strconv.Atoi(strings.TrimSuffix(s, "d"))
		return time.Now().Add(-time.Duration(n) * 24 * time.Hour).UnixMilli()
	}
	if strings.HasSuffix(s, "h") {
		n, _ := strconv.Atoi(strings.TrimSuffix(s, "h"))
		return time.Now().Add(-time.Duration(n) * time.Hour).UnixMilli()
	}
	return 0
}

// --- top pages / referrers / sources ---------------------------------------

func (s *HTTPServer) handleAnalyticsTop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	since := parseSince(r.URL.Query().Get("since"))
	events := readAllEvents()
	routes := map[string]int{}
	referrers := map[string]int{}
	origins := map[string]int{}
	totalViews := 0
	uniqueVisitors := map[string]bool{}

	for _, ev := range events {
		if ev.Timestamp < since {
			continue
		}
		if ev.Name != "pageview" {
			continue
		}
		totalViews++
		if ev.Route != "" {
			routes[ev.Route]++
		}
		if v := ev.Props["referrer"]; v != "" {
			referrers[v]++
		}
		if v := ev.Props["origin"]; v != "" {
			origins[v]++
		}
		if v := ev.Props["vid"]; v != "" {
			uniqueVisitors[v] = true
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":             true,
		"views":          totalViews,
		"uniqueVisitors": len(uniqueVisitors),
		"topRoutes":      topN(routes, 20),
		"topReferrers":   topN(referrers, 20),
		"topOrigins":     topN(origins, 20),
	})
}

// topN reduces a frequency map to the top N sorted pairs. The
// output shape is a slice of { key, count } objects so JSON
// consumers get a stable ordering.
func topN(m map[string]int, n int) []map[string]interface{} {
	type pair struct {
		Key   string
		Count int
	}
	arr := make([]pair, 0, len(m))
	for k, v := range m {
		arr = append(arr, pair{k, v})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].Count > arr[j].Count })
	if len(arr) > n {
		arr = arr[:n]
	}
	out := make([]map[string]interface{}, 0, len(arr))
	for _, p := range arr {
		out = append(out, map[string]interface{}{"key": p.Key, "count": p.Count})
	}
	return out
}

// --- funnel ----------------------------------------------------------------

// GET /analytics/funnel?steps=signup_started,signup_completed,purchase
//
// Computes the classic sequential funnel: how many unique visitors
// made it to each step, in order. A visitor advances to step N+1
// only if they already hit step N. Works on the vid prop from the
// JS snippet.
func (s *HTTPServer) handleAnalyticsFunnel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	stepsQ := r.URL.Query().Get("steps")
	if stepsQ == "" {
		jsonError(w, http.StatusBadRequest, "steps required (comma separated)")
		return
	}
	steps := strings.Split(stepsQ, ",")
	since := parseSince(r.URL.Query().Get("since"))

	events := readAllEvents()
	// Group by visitor, preserving timestamp ordering.
	perVisitor := map[string][]TrackEvent{}
	for _, ev := range events {
		if ev.Timestamp < since {
			continue
		}
		vid := ev.Props["vid"]
		if vid == "" {
			continue
		}
		perVisitor[vid] = append(perVisitor[vid], ev)
	}
	for vid := range perVisitor {
		sort.Slice(perVisitor[vid], func(i, j int) bool { return perVisitor[vid][i].Timestamp < perVisitor[vid][j].Timestamp })
	}

	stepCounts := make([]int, len(steps))
	for _, evs := range perVisitor {
		stepIdx := 0
		for _, ev := range evs {
			if stepIdx >= len(steps) {
				break
			}
			if ev.Name == steps[stepIdx] {
				stepCounts[stepIdx]++
				stepIdx++
			}
		}
	}
	out := make([]map[string]interface{}, len(steps))
	for i, step := range steps {
		rate := 0.0
		if i == 0 {
			rate = 1.0
		} else if stepCounts[0] > 0 {
			rate = float64(stepCounts[i]) / float64(stepCounts[0])
		}
		out[i] = map[string]interface{}{
			"step":      step,
			"count":     stepCounts[i],
			"rateToTop": rate,
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "funnel": out})
}

// --- retention cohort ------------------------------------------------------

// GET /analytics/retention?weeks=8
//
// Bucket visitors into weekly cohorts by their first-seen date,
// then for each cohort measure how many came back in subsequent
// weeks. The resulting grid is the raw data behind Amplitude-
// style retention charts.
func (s *HTTPServer) handleAnalyticsRetention(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	weeks := 8
	if w2, err := strconv.Atoi(r.URL.Query().Get("weeks")); err == nil && w2 > 0 {
		weeks = w2
	}
	events := readAllEvents()
	type vinfo struct {
		first int64
		weeks map[int]bool
	}
	visitors := map[string]*vinfo{}
	firstByCohort := map[int]map[string]bool{}

	now := time.Now()
	weekStart := func(ms int64) int {
		d := time.UnixMilli(ms)
		// Weeks ago from now, rounded down.
		return int(now.Sub(d) / (7 * 24 * time.Hour))
	}

	for _, ev := range events {
		vid := ev.Props["vid"]
		if vid == "" {
			continue
		}
		v, ok := visitors[vid]
		if !ok {
			v = &vinfo{first: ev.Timestamp, weeks: map[int]bool{}}
			visitors[vid] = v
		}
		if ev.Timestamp < v.first {
			v.first = ev.Timestamp
		}
		v.weeks[weekStart(ev.Timestamp)] = true
	}
	// Cohort 0 = this week (newest), cohort N = N weeks ago.
	for vid, v := range visitors {
		cohort := weekStart(v.first)
		if cohort >= weeks {
			continue
		}
		if firstByCohort[cohort] == nil {
			firstByCohort[cohort] = map[string]bool{}
		}
		firstByCohort[cohort][vid] = true
	}
	grid := make([]map[string]interface{}, 0, weeks)
	for c := 0; c < weeks; c++ {
		cohort := firstByCohort[c]
		row := map[string]interface{}{"cohortWeeksAgo": c, "size": len(cohort), "retained": []int{}}
		retained := []int{}
		for offset := 0; offset+c < weeks; offset++ {
			back := 0
			for vid := range cohort {
				if visitors[vid].weeks[c-offset] {
					back++
				}
			}
			retained = append(retained, back)
		}
		row["retained"] = retained
		grid = append(grid, row)
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "weeks": weeks, "grid": grid})
}

// --- summary ---------------------------------------------------------------

func (s *HTTPServer) handleAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	since := parseSince(r.URL.Query().Get("since"))
	events := readAllEvents()
	views := 0
	unique := map[string]bool{}
	byDay := map[string]int{}
	for _, ev := range events {
		if ev.Timestamp < since {
			continue
		}
		if ev.Name != "pageview" {
			continue
		}
		views++
		if v := ev.Props["vid"]; v != "" {
			unique[v] = true
		}
		day := time.UnixMilli(ev.Timestamp).Format("2006-01-02")
		byDay[day]++
	}
	series := []map[string]interface{}{}
	days := make([]string, 0, len(byDay))
	for k := range byDay {
		days = append(days, k)
	}
	sort.Strings(days)
	for _, d := range days {
		series = append(series, map[string]interface{}{"day": d, "views": byDay[d]})
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":             true,
		"views":          views,
		"uniqueVisitors": len(unique),
		"series":         series,
	})
}
