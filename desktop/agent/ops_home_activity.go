package main

// ops_home_activity.go — the ACTIVITY engine (docs/yaver-single-kumanda.md §7):
// named multi-device sequences ("Watch Apple TV", "Good night") that the user
// runs by voice, a phone/TV tile, or a car/watch one-tap. Each step routes a
// logical key to a device through sendHomeKey (ops_home.go); on_error controls
// whether a failed step aborts or the activity continues.
//
// Closed-loop verify (read the live /capture frame to confirm a step landed,
// retry/fall back if not) is the next layer — the executor is structured so a
// per-step verify hook drops in without changing callers.

import (
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "home_activity_create",
		Description: "Create or replace an activity. Payload {name, steps:[{device, key, app?, onError?}]}. onError: continue | abort (default abort).",
		Schema: map[string]interface{}{"type": "object", "required": []string{"name", "steps"}, "properties": map[string]interface{}{
			"name":  map[string]interface{}{"type": "string"},
			"steps": map[string]interface{}{"type": "array"},
		}},
		Handler: homeActivityCreateHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "home_activity_list",
		Description: "List defined activities.",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Handler:     homeActivityListHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "home_activity_remove",
		Description: "Delete an activity. Payload {name}.",
		Schema:      map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}}},
		Handler:     homeActivityRemoveHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "home_activity_run",
		Description: "Run an activity by name. Payload {name}. Walks its steps in order; a failed step aborts unless its onError is \"continue\".",
		Schema:      map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}}},
		Handler:     homeActivityRunHandler,
	})
}

func homeActivityCreateHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p homeActivity
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Steps) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "name and at least one step are required"}
	}
	for i, st := range p.Steps {
		hasKey := strings.TrimSpace(st.Device) != "" && strings.TrimSpace(st.Key) != ""
		hasVerb := strings.TrimSpace(st.Verb) != ""
		if !hasKey && !hasVerb {
			return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("step %d needs device+key or a verb", i+1)}
		}
	}
	homeStoreMu.Lock()
	defer homeStoreMu.Unlock()
	s, err := loadHomeStore()
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	replaced := false
	for i := range s.Activities {
		if strings.EqualFold(s.Activities[i].Name, p.Name) {
			s.Activities[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		s.Activities = append(s.Activities, p)
	}
	if err := saveHomeStore(s); err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"activity": p.Name, "steps": len(p.Steps), "updated": replaced}}
}

func homeActivityListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	s, err := loadHomeStore()
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"activities": s.Activities}}
}

func homeActivityRemoveHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	homeStoreMu.Lock()
	defer homeStoreMu.Unlock()
	s, err := loadHomeStore()
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	kept := s.Activities[:0]
	removed := false
	for _, a := range s.Activities {
		if strings.EqualFold(a.Name, p.Name) {
			removed = true
			continue
		}
		kept = append(kept, a)
	}
	s.Activities = kept
	if err := saveHomeStore(s); err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"removed": removed}}
}

func homeActivityRunHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	s, err := loadHomeStore()
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	act, ok := s.activity(p.Name)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: fmt.Sprintf("activity %q not found", p.Name)}
	}
	results, completed := runActivitySteps(act.Steps,
		func(st homeStep) error { return runHomeStep(c, st) },
		func(st homeStep) error { return verifyHomeStep(c, st) },
	)
	return OpsResult{OK: completed, Initial: map[string]interface{}{
		"activity":  act.Name,
		"completed": completed,
		"steps":     results,
	}}
}

// runHomeStep executes one step: a generic ops verb (st.Verb) dispatched
// in-process, or the logical-key router (sendHomeKey). This is what lets
// activities mix AV keys with ac_set / camera_snapshot / ir_blast / switches.
func runHomeStep(c OpsContext, st homeStep) error {
	if strings.TrimSpace(st.Verb) != "" {
		payload, _ := json.Marshal(st.Payload)
		res := dispatchOps(c, OpsRequest{Machine: "local", Verb: strings.TrimSpace(st.Verb), Payload: payload})
		if !res.OK {
			if res.Error != "" {
				return fmt.Errorf("%s", res.Error)
			}
			return fmt.Errorf("verb %s failed", st.Verb)
		}
		return nil
	}
	_, e := sendHomeKey(c, st.Device, st.Key, st.App)
	return e
}

// homeStepResult records the outcome of one step for the activity report.
type homeStepResult struct {
	Device string `json:"device,omitempty"`
	Key    string `json:"key,omitempty"`
	Verb   string `json:"verb,omitempty"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// runActivitySteps walks steps in order. For each step it calls run, then (if
// the step has a Verify) the injected verify — the CLOSED LOOP. A step that
// fails either is retried up to Retry extra times; if it still fails it aborts
// the activity unless OnError == "continue". Both run and verify are injected,
// so this is unit-testable without hardware.
func runActivitySteps(steps []homeStep, run func(homeStep) error, verify func(homeStep) error) (results []homeStepResult, completed bool) {
	for _, st := range steps {
		attempts := st.Retry + 1
		if attempts < 1 {
			attempts = 1
		}
		var err error
		for a := 0; a < attempts; a++ {
			err = run(st)
			if err == nil && verify != nil && strings.TrimSpace(st.Verify) != "" {
				err = verify(st)
			}
			if err == nil {
				break
			}
		}
		r := homeStepResult{Device: st.Device, Key: st.Key, Verb: st.Verb, OK: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
		if err != nil && !strings.EqualFold(strings.TrimSpace(st.OnError), "continue") {
			return results, false
		}
	}
	return results, true
}

// verifyHomeStep runs a step's closed-loop check. Supported forms:
//
//	"camera:<id>"  — that camera must yield a non-black frame (the step's
//	                 effect, e.g. "TV is on", is visible on the feed).
//
// Unknown / empty verify is a no-op (passes). This is the seam where richer
// checks (input==hdmi2, OCR channel, motion) drop in.
func verifyHomeStep(c OpsContext, st homeStep) error {
	v := strings.TrimSpace(st.Verify)
	if v == "" {
		return nil
	}
	if strings.HasPrefix(v, "camera:") {
		id := strings.TrimPrefix(v, "camera:")
		rec, ok := cameraGet(strings.TrimSpace(id))
		if !ok {
			return fmt.Errorf("verify: camera %q not found", id)
		}
		jpeg, err := cameraGrabFrame(c.Ctx, rec.URL)
		if err != nil {
			return fmt.Errorf("verify: %v", err)
		}
		if frameIsBlack(jpeg) {
			return fmt.Errorf("verify: camera %q frame is dark — step did not visibly land", id)
		}
		return nil
	}
	return nil
}
