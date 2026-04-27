package main

import (
	"encoding/json"
	"net/http"
)

func (s *HTTPServer) handleCodeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	payload, err := buildCodeStatusPayload()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "code status: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, payload)
}

func (s *HTTPServer) handleCodeAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Target   string `json:"target"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	out, err := applyCodeAttach(body.Target, body.Username)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "code attach: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "result": out})
}

func (s *HTTPServer) handleCodeDetach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	out, err := applyCodeDetach()
	if err != nil {
		jsonError(w, http.StatusBadRequest, "code detach: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "code": out})
}

func (s *HTTPServer) handleCodeRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	repos, err := listCodeReposStructured()
	if err != nil {
		jsonError(w, http.StatusBadRequest, "code repos: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "repos": repos})
}

func (s *HTTPServer) handleCodeRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	out, err := setCodeRepoStructured(body.Query)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "code repo: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, out)
}

func (s *HTTPServer) handleCodeDev(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	out, err := runCodeDevActionStructured(body.Action)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "code dev: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, out)
}

func (s *HTTPServer) handleCodeDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body CodeDeployRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	out, err := runCodeDeployRequestStructured(body)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "code deploy: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{"ok": true, "result": out})
}
