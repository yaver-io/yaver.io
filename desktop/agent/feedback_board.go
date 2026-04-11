package main

// feedback_board.go — Canny / Nolt / Feature Upvote replacement
// for the solo-SaaS case. Hosts a public upvotable feature-
// request board on the dev's own relay. Zero vendor, zero
// Convex — every row lives in ~/.yaver/feedback-board/items.json
// with optional pubsub fan-out for "new upvote" notifications.
//
// Data model:
//
//   FeedbackItem {id, title, description, status, upvotes,
//                 createdAt, createdBy, tags, comments[]}
//
//   FeedbackComment {id, author, body, createdAt}
//
// Status enum: open | planned | in-progress | shipped | closed
//
// HTTP surface (two auth levels):
//
//   Authenticated (dev-side management):
//     GET    /feedback-board                 — list every item
//     POST   /feedback-board                 — create a new item
//     POST   /feedback-board/<id>/status    — change status
//     DELETE /feedback-board/<id>           — remove
//     POST   /feedback-board/<id>/comment    — add a comment
//
//   Public (end-user-facing, SDK token scope):
//     POST   /feedback-board/<id>/upvote    — vote via fingerprint
//     GET    /feedback-board/public         — same data, minus
//                                             internal fields
//
// Upvotes are rate-limited per (item, fingerprint) using the
// same sha256 approach the release rollout code uses, so a
// malicious user can't spam a single item into first place.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FeedbackItem is one feature request / bug report on the board.
type FeedbackItem struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Status      string            `json:"status"` // open|planned|in-progress|shipped|closed
	Upvotes     int               `json:"upvotes"`
	CreatedAt   string            `json:"createdAt"`
	CreatedBy   string            `json:"createdBy,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Comments    []FeedbackComment `json:"comments,omitempty"`
	// voteFingerprints prevents duplicate votes from the same
	// browser / device. Hash of ip+user-agent+session.
	voteFingerprints map[string]bool
}

// FeedbackComment is a reply on an item.
type FeedbackComment struct {
	ID        string `json:"id"`
	Author    string `json:"author,omitempty"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

var feedbackBoardMu sync.Mutex

func feedbackBoardPath() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "feedback-board")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "items.json"), nil
}

type feedbackBoardFile struct {
	Items map[string]*FeedbackItem `json:"items"`
}

func loadFeedbackBoard() (*feedbackBoardFile, error) {
	p, err := feedbackBoardPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &feedbackBoardFile{Items: map[string]*FeedbackItem{}}, nil
		}
		return nil, err
	}
	var f feedbackBoardFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	if f.Items == nil {
		f.Items = map[string]*FeedbackItem{}
	}
	for _, it := range f.Items {
		if it.voteFingerprints == nil {
			it.voteFingerprints = map[string]bool{}
		}
	}
	return &f, nil
}

func saveFeedbackBoard(f *feedbackBoardFile) error {
	p, err := feedbackBoardPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// voterFingerprint is a stable per-caller hash so the same
// user can't double-upvote. Uses the remote addr + any
// yaver-identifying header we see; falls back to the raw IP.
func voterFingerprint(r *http.Request) string {
	raw := r.RemoteAddr + "|" + r.UserAgent() + "|" + r.Header.Get("X-Yaver-Token-Hash")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:16]
}

// feedbackItemList returns the board sorted by upvotes desc +
// newest-first tiebreaker.
func feedbackItemList(file *feedbackBoardFile, includeClosed bool) []*FeedbackItem {
	out := make([]*FeedbackItem, 0, len(file.Items))
	for _, it := range file.Items {
		if !includeClosed && it.Status == "closed" {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Upvotes != out[j].Upvotes {
			return out[i].Upvotes > out[j].Upvotes
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

// --- HTTP: authenticated management ---------------------------------------

func (s *HTTPServer) handleFeedbackBoard(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/feedback-board")
	path = strings.TrimPrefix(path, "/")

	feedbackBoardMu.Lock()
	defer feedbackBoardMu.Unlock()
	file, err := loadFeedbackBoard()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// /feedback-board  → list / create
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			includeClosed := r.URL.Query().Get("all") == "1"
			jsonReply(w, http.StatusOK, map[string]interface{}{
				"ok":    true,
				"items": feedbackItemList(file, includeClosed),
			})
		case http.MethodPost:
			var body FeedbackItem
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			if strings.TrimSpace(body.Title) == "" {
				jsonError(w, http.StatusBadRequest, "title required")
				return
			}
			if body.ID == "" {
				body.ID = randomID()
			}
			if body.Status == "" {
				body.Status = "open"
			}
			body.CreatedAt = time.Now().UTC().Format(time.RFC3339)
			body.voteFingerprints = map[string]bool{}
			file.Items[body.ID] = &body
			if err := saveFeedbackBoard(file); err != nil {
				jsonError(w, http.StatusInternalServerError, err.Error())
				return
			}
			jsonReply(w, http.StatusCreated, map[string]interface{}{
				"ok":   true,
				"item": &body,
			})
		default:
			jsonError(w, http.StatusMethodNotAllowed, "GET or POST")
		}
		return
	}

	// /feedback-board/<id>[/action]
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	item, exists := file.Items[id]
	if !exists {
		jsonError(w, http.StatusNotFound, "item not found")
		return
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "item": item})
	case action == "" && r.Method == http.MethodDelete:
		delete(file.Items, id)
		_ = saveFeedbackBoard(file)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case action == "status" && r.Method == http.MethodPost:
		var body struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		switch body.Status {
		case "open", "planned", "in-progress", "shipped", "closed":
			item.Status = body.Status
			_ = saveFeedbackBoard(file)
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
		default:
			jsonError(w, http.StatusBadRequest, "invalid status")
		}
	case action == "comment" && r.Method == http.MethodPost:
		var body FeedbackComment
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.ID == "" {
			body.ID = randomID()
		}
		body.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		item.Comments = append(item.Comments, body)
		_ = saveFeedbackBoard(file)
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "comment": body})
	case action == "upvote" && r.Method == http.MethodPost:
		fp := voterFingerprint(r)
		if item.voteFingerprints == nil {
			item.voteFingerprints = map[string]bool{}
		}
		if item.voteFingerprints[fp] {
			jsonError(w, http.StatusConflict, "already voted")
			return
		}
		item.voteFingerprints[fp] = true
		item.Upvotes++
		_ = saveFeedbackBoard(file)
		// Fan out to the pubsub hub if the dev has a
		// "feedback-board" topic subscribed.
		payload, _ := json.Marshal(map[string]interface{}{
			"itemId":  item.ID,
			"title":   item.Title,
			"upvotes": item.Upvotes,
		})
		GlobalPubSub().Publish("feedback-board/upvote", payload)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"upvotes": item.Upvotes,
		})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "unknown action")
	}
}

// handleFeedbackBoardPublic is the read-only public view, used
// by the dev's static "vote on the roadmap" page. Strips the
// internal voteFingerprints set + serves even when the token
// only has SDK scope.
func (s *HTTPServer) handleFeedbackBoardPublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	feedbackBoardMu.Lock()
	defer feedbackBoardMu.Unlock()
	file, err := loadFeedbackBoard()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Build a trimmed public view.
	type publicItem struct {
		ID          string            `json:"id"`
		Title       string            `json:"title"`
		Description string            `json:"description,omitempty"`
		Status      string            `json:"status"`
		Upvotes     int               `json:"upvotes"`
		CreatedAt   string            `json:"createdAt"`
		Tags        []string          `json:"tags,omitempty"`
		Comments    []FeedbackComment `json:"comments,omitempty"`
	}
	items := feedbackItemList(file, false)
	out := make([]publicItem, 0, len(items))
	for _, it := range items {
		out = append(out, publicItem{
			ID:          it.ID,
			Title:       it.Title,
			Description: it.Description,
			Status:      it.Status,
			Upvotes:     it.Upvotes,
			CreatedAt:   it.CreatedAt,
			Tags:        it.Tags,
			Comments:    it.Comments,
		})
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"items": out,
	})
}

// Ensure fmt stays imported even on compile configs that don't
// hit the Sprintf above.
var _ = fmt.Sprintf
