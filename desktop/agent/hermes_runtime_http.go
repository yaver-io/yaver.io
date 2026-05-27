package main

// hermes_runtime_http.go — HTTP routes for the Hermes runtime layer.
//
//   POST /hermes/validate   {bundlePath}                   → HermesValidation
//   POST /hermes/run        {bundlePath, timeoutSec, …}    → HermesResult
//   POST /hermes/smoke      {bundleDir}                    → HermesSmokeResult
//
// All routes require the agent-token or a paired token; SDK tokens are
// rejected because hermes invocation is a privileged op (can read
// arbitrary files via -emit-binary args).

import (
	"context"
	"encoding/json"
	"net/http"
)

type hermesValidateReq struct {
	BundlePath string `json:"bundlePath"`
}

type hermesRunReq struct {
	BundlePath       string   `json:"bundlePath"`
	TimeoutSec       int      `json:"timeoutSec"`
	EnableSourceMode bool     `json:"enableSourceMode"`
	ExtraArgs        []string `json:"extraArgs"`
}

type hermesSmokeReq struct {
	BundleDir string `json:"bundleDir"`
}

func (s *HTTPServer) handleHermesValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req hermesValidateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.BundlePath == "" {
		jsonError(w, http.StatusBadRequest, "bundlePath required")
		return
	}
	jsonReply(w, http.StatusOK, ValidateHermesBundle(req.BundlePath))
}

func (s *HTTPServer) handleHermesRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req hermesRunReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.BundlePath == "" {
		jsonError(w, http.StatusBadRequest, "bundlePath required")
		return
	}
	ctx := r.Context()
	if _, ok := ctx.Deadline(); !ok {
		// Belt + braces — TimeoutSec already caps inside HermesRun, but
		// we'd rather not leak a goroutine if the client disconnects.
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
	}
	res := HermesRun(ctx, HermesRunOpts{
		BundlePath:       req.BundlePath,
		TimeoutSec:       req.TimeoutSec,
		EnableSourceMode: req.EnableSourceMode,
		ExtraArgs:        req.ExtraArgs,
	})
	jsonReply(w, http.StatusOK, res)
}

func (s *HTTPServer) handleHermesSmoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req hermesSmokeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.BundleDir == "" {
		jsonError(w, http.StatusBadRequest, "bundleDir required")
		return
	}
	jsonReply(w, http.StatusOK, HermesSmokeTest(r.Context(), req.BundleDir))
}
