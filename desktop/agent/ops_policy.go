package main

// ops_policy.go — run a LEARNED policy on the arm, and record the demonstrations
// that train it. This is the agent-facing surface of the video→policy→arm
// pipeline (docs/yaver-video-to-policy-harness-cell.md):
//
//   demonstrate → train (off-box, rented GPU) → serve (rented GPU/Jetson) → run
//
//   arm_demo_start/stop/list   dense {frame,state} capture → a training dataset
//   policy_bind                point the arm at a served policy endpoint (vault)
//   policy_status              show the binding + probe the server health
//   policy_step                one observe→infer→execute chunk (safe; for testing)
//   policy_run                 the bounded, safety-gated policy loop
//   policy_stop                interrupt a running policy_run
//
// Every motion goes through the LOCAL safety gate in arm/policy.go — a remote
// model never moves the arm unchecked.

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yaver-io/agent/arm"
)

const armPolicyVaultName = "arm-policy"

var (
	armDemoRec     = arm.DefaultDemoRecorder()
	policyStopFlag atomic.Bool
)

// --- policy binding (vault project "robot", name "arm-policy") ---

func policyBindingGet() arm.PolicyConfig {
	var cfg arm.PolicyConfig
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(armVaultProject, armPolicyVaultName); gerr == nil && e != nil && e.Value != "" {
			_ = json.Unmarshal([]byte(e.Value), &cfg)
		}
	}
	return cfg
}

func policyBindingSave(cfg arm.PolicyConfig) error {
	b, _ := json.Marshal(cfg)
	if vs, err := openVaultOptional(); err == nil {
		return vs.Set(VaultEntry{Project: armVaultProject, Name: armPolicyVaultName, Category: "custom",
			Value: string(b), Notes: "Yaver arm policy endpoint (served imitation/VLA model)"})
	} else {
		return err
	}
}

// policyForOps resolves the arm controller + a PolicyClient from the saved
// binding, merging per-call overrides.
func policyForOps(over arm.PolicyConfig) (*arm.Controller, *arm.PolicyClient, arm.PolicyConfig, *OpsResult) {
	ctrl, deny := armForOps()
	if deny != nil {
		return nil, nil, arm.PolicyConfig{}, deny
	}
	cfg := policyBindingGet()
	// merge overrides
	if strings.TrimSpace(over.Endpoint) != "" {
		cfg.Endpoint = over.Endpoint
	}
	if strings.TrimSpace(over.APIKey) != "" {
		cfg.APIKey = over.APIKey
	}
	if strings.TrimSpace(over.Prompt) != "" {
		cfg.Prompt = over.Prompt
	}
	if over.MaxStepDeg > 0 {
		cfg.MaxStepDeg = over.MaxStepDeg
	}
	if over.MaxChunks > 0 {
		cfg.MaxChunks = over.MaxChunks
	}
	if over.MaxSeconds > 0 {
		cfg.MaxSeconds = over.MaxSeconds
	}
	if over.VelPct > 0 {
		cfg.VelPct = over.VelPct
	}
	if strings.TrimSpace(over.Verify) != "" {
		cfg.Verify = over.Verify
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nil, cfg, &OpsResult{OK: false, Code: "no_policy", Error: "no policy endpoint — policy_bind {endpoint} first (a served ACT/SmolVLA/π0 model)"}
	}
	// http timeout doubles as the stale-server watchdog
	client := arm.NewPolicyClient(cfg.Endpoint, cfg.APIKey, 12*time.Second)
	return ctrl, client, cfg, nil
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	// --- demonstrations (training data) ---
	reg("arm_demo_start", "Start DENSE demo recording {name, prompt?, fps?} — captures synchronized camera frames + joint state while you hand-guide (free-drive) or teleop the arm. The episodes train an imitation policy (ACT/SmolVLA/π0) on a rented GPU.", func(c OpsContext, payload json.RawMessage) OpsResult {
		ctrl, deny := armForOps()
		if deny != nil {
			return *deny
		}
		var p struct {
			Name   string `json:"name"`
			Prompt string `json:"prompt"`
			FPS    int    `json:"fps"`
		}
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &p)
		}
		if strings.TrimSpace(p.Name) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "name required (e.g. \"seat-wire-cavity3\")"}
		}
		if err := armDemoRec.Start(c.Ctx, ctrl, p.Name, p.Prompt, p.FPS); err != nil {
			return OpsResult{OK: false, Code: "demo_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"recording": p.Name, "fps": p.FPS, "hint": "hand-guide the arm now (arm_freedrive on); arm_demo_stop when done"}}
	})
	reg("arm_demo_stop", "Stop the active demo recording and finalize the episode dataset (returns frame count + location)", func(c OpsContext, _ json.RawMessage) OpsResult {
		meta, err := armDemoRec.Stop()
		if err != nil {
			return OpsResult{OK: false, Code: "demo_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"episode": meta}}
	})
	reg("arm_demo_list", "List recorded demo episodes (name, frames, prompt) — the dataset you ship to a rented GPU to train", func(c OpsContext, _ json.RawMessage) OpsResult {
		list, err := armDemoRec.List()
		if err != nil {
			return OpsResult{OK: false, Code: "demo_failed", Error: err.Error()}
		}
		active, name, frames := armDemoRec.Active()
		return OpsResult{OK: true, Initial: map[string]any{"episodes": list, "recording": active, "current": name, "currentFrames": frames}}
	})

	// --- policy binding + run ---
	reg("policy_bind", "Bind the arm to a served policy endpoint {endpoint, apiKey?, prompt?, model?, maxStepDeg?, velPct?} — a model (ACT/SmolVLA/π0) served on a rented GPU (gpu_bind/Salad) or a local Jetson that maps camera+state→action chunk. Saved encrypted in the vault.", func(c OpsContext, payload json.RawMessage) OpsResult {
		var cfg arm.PolicyConfig
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &cfg); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
		}
		if strings.TrimSpace(cfg.Endpoint) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "endpoint required (the served policy URL)"}
		}
		if err := policyBindingSave(cfg); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"endpoint": cfg.Endpoint, "model": cfg.Model, "hasKey": cfg.APIKey != ""}}
	})
	reg("policy_status", "Show the policy binding (endpoint/model, never the key) and probe the served model's health", func(c OpsContext, _ json.RawMessage) OpsResult {
		cfg := policyBindingGet()
		out := map[string]any{"endpoint": cfg.Endpoint, "model": cfg.Model, "prompt": cfg.Prompt, "bound": strings.TrimSpace(cfg.Endpoint) != ""}
		if strings.TrimSpace(cfg.Endpoint) != "" {
			client := arm.NewPolicyClient(cfg.Endpoint, cfg.APIKey, 5*time.Second)
			if err := client.Health(c.Ctx); err != nil {
				out["healthy"] = false
				out["healthError"] = err.Error()
			} else {
				out["healthy"] = true
			}
		}
		return OpsResult{OK: true, Initial: out}
	})
	reg("policy_step", "Run ONE observe→infer→execute chunk through the safety gate (for testing a policy before a full run)", func(c OpsContext, payload json.RawMessage) OpsResult {
		var over arm.PolicyConfig
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &over)
		}
		ctrl, client, cfg, deny := policyForOps(over)
		if deny != nil {
			return *deny
		}
		cfg.MaxChunks = 1
		res := ctrl.RunPolicy(c.Ctx, client, cfg, nil)
		return OpsResult{OK: res.OK, Code: res.Code, Error: res.Error, Initial: res}
	})
	reg("policy_run", "Run the served policy on the arm: bounded, safety-gated observe→infer→execute loop {prompt?, maxChunks?, maxSeconds?, verify?, velPct?, maxStepDeg?}. Stops on done/budget/policy_stop, and HARD-stops (e-stop) on any out-of-range or oversized step. The model never moves the arm unchecked.", func(c OpsContext, payload json.RawMessage) OpsResult {
		var over arm.PolicyConfig
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &over)
		}
		ctrl, client, cfg, deny := policyForOps(over)
		if deny != nil {
			return *deny
		}
		if cfg.MaxSeconds <= 0 || cfg.MaxSeconds > 300 {
			cfg.MaxSeconds = 120 // hard ceiling on a synchronous run
		}
		policyStopFlag.Store(false)
		res := ctrl.RunPolicy(c.Ctx, client, cfg, func() bool { return policyStopFlag.Load() })
		return OpsResult{OK: res.OK, Code: res.Code, Error: res.Error, Initial: res}
	})
	reg("policy_stop", "Interrupt a running policy_run (the loop halts after the current chunk)", func(c OpsContext, _ json.RawMessage) OpsResult {
		policyStopFlag.Store(true)
		return OpsResult{OK: true, Initial: map[string]any{"stopping": true}}
	})
}
