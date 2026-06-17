package main

// home_store.go — local-first persistence for the "single kumanda" home-control
// surface (docs/yaver-single-kumanda.md). Devices + activities live in
// ~/.yaver/home/store.json, NEVER in Convex (privacy contract): a home layout
// and the rooms in it are the user's, and stay on the box.
//
// This is the data spine the home_* ops verbs route over. It is deliberately a
// separate namespace from the coding-agent surfaces so the universal-remote /
// camera / appliance features never pollute the main Yaver dev UI.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// homeDevice is one controllable thing in the home. Kind selects the transport
// the router uses; Address is the per-kind locator (an Apple TV identifier, a
// Mi Box adb serial / "ip:port", later an IR-blaster ref / camera RTSP URL).
type homeDevice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // apple_tv | mibox | (future: satellite_ir, ac, camera, switch)
	Address string `json:"address,omitempty"`
}

// homeStep is one action inside an activity: send a logical key (or "launch")
// to a device. OnError controls sequencing: "continue" keeps going on failure,
// anything else (default) aborts the activity.
type homeStep struct {
	// A step is EITHER a logical-key form (device + key) OR a generic-verb form
	// (verb + payload). The generic form lets an activity call ANY ops verb —
	// ac_set, camera_snapshot, ir_blast, a switch, even a non-home verb — so
	// "Cool the room + close the blinds" composes cross-connector.
	Device  string                 `json:"device,omitempty"` // homeDevice.ID (key form)
	Key     string                 `json:"key,omitempty"`    // logical key, or "launch"
	App     string                 `json:"app,omitempty"`
	Verb    string                 `json:"verb,omitempty"`    // generic-verb form
	Payload map[string]interface{} `json:"payload,omitempty"` // payload for Verb
	OnError string                 `json:"onError,omitempty"` // continue | abort (default abort)
	// Verify is an optional closed-loop check run AFTER the step lands: e.g.
	// "camera:<id>" (that camera must produce a non-black frame). Empty = no
	// verify. Retry is how many extra attempts to make if the step or its
	// verify fails (0 = try once).
	Verify string `json:"verify,omitempty"`
	Retry  int    `json:"retry,omitempty"`
}

// homeActivity is a named multi-device sequence ("Watch Apple TV", "Good night").
type homeActivity struct {
	Name  string     `json:"name"`
	Steps []homeStep `json:"steps"`
}

type homeStore struct {
	Devices    []homeDevice   `json:"devices"`
	Activities []homeActivity `json:"activities"`
}

var homeStoreMu sync.Mutex

func homeStorePath() (string, error) {
	dir, err := yaverDir()
	if err != nil {
		return "", err
	}
	hdir := filepath.Join(dir, "home")
	if err := os.MkdirAll(hdir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(hdir, "store.json"), nil
}

// loadHomeStore reads the store, returning an empty (non-nil) store when the
// file does not exist yet.
func loadHomeStore() (*homeStore, error) {
	path, err := homeStorePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &homeStore{}, nil
		}
		return nil, err
	}
	var s homeStore
	if len(data) > 0 {
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

func saveHomeStore(s *homeStore) error {
	path, err := homeStorePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *homeStore) device(id string) (homeDevice, bool) {
	for _, d := range s.Devices {
		if strings.EqualFold(d.ID, id) {
			return d, true
		}
	}
	return homeDevice{}, false
}

func (s *homeStore) activity(name string) (homeActivity, bool) {
	for _, a := range s.Activities {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return homeActivity{}, false
}
