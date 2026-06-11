package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yaver-io/agent/studio"
)

// qa_testaccount.go — ephemeral throwaway-account injection for agentic redroid
// flows. A flow YAML can reference {{email}} / {{password}} / {{fullName}} in its
// goal/expectations; when qa_run is invoked with {"testAccount":"ephemeral"} the
// runner mints a randomized account against Convex, substitutes those values
// into every flow (so the brain — never the tracked YAML — sees the secret), and
// deletes the account when the run finishes.
//
// This is what lets 03-signup-onboarding.flow.yaml run fully unattended without
// hardcoding credentials in the repo.

// qaTestAccount is a throwaway account created for one qa_run.
type qaTestAccount struct {
	Email    string
	Password string
	FullName string
	Token    string
	UserID   string
}

// resolveQAConvexURL picks the Convex site URL: explicit override, else the
// signed-in config, else the build default.
func resolveQAConvexURL(override string) string {
	if u := strings.TrimSpace(override); u != "" {
		return strings.TrimRight(u, "/")
	}
	if cfg, err := LoadConfig(); err == nil && cfg != nil {
		if u := strings.TrimSpace(cfg.ConvexSiteURL); u != "" {
			return strings.TrimRight(u, "/")
		}
	}
	return defaultConvexSiteURL
}

// createEphemeralQAAccount registers a randomized e2e-redroid-*@yaver.test user
// and returns its credentials + session token.
func createEphemeralQAAccount(ctx context.Context, convexURL string) (*qaTestAccount, error) {
	acct := &qaTestAccount{
		Email:    fmt.Sprintf("e2e-redroid-%s@yaver.test", qaRandHex(8)),
		Password: fmt.Sprintf("pw-%sA1", qaRandHex(12)),
		FullName: "Redroid QA " + qaRandHex(4),
	}
	payload, _ := json.Marshal(map[string]string{
		"email":    acct.Email,
		"password": acct.Password,
		"fullName": acct.FullName,
	})
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "POST", convexURL+"/auth/signup", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("signup request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("signup failed: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Token  string `json:"token"`
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode signup: %w", err)
	}
	acct.Token = out.Token
	acct.UserID = out.UserID
	return acct, nil
}

// delete removes the throwaway account. Best-effort; errors are returned for
// logging but never fatal to a run.
func (a *qaTestAccount) delete(ctx context.Context, convexURL string) error {
	if a == nil || strings.TrimSpace(a.Token) == "" {
		return nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "POST", convexURL+"/auth/delete-account", bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete-account: HTTP %d", resp.StatusCode)
	}
	return nil
}

// applyTestAccountTemplate substitutes the throwaway credentials into every
// flow's goal and expectations. Templating happens in-memory at run time, so the
// secret only ever reaches the LLM brain — never the tracked flow YAML.
func applyTestAccountTemplate(flows []studio.Scenario, a *qaTestAccount) {
	if a == nil {
		return
	}
	repl := strings.NewReplacer(
		"{{email}}", a.Email,
		"{{password}}", a.Password,
		"{{fullName}}", a.FullName,
		"{{fullname}}", a.FullName,
	)
	for i := range flows {
		flows[i].Goal = repl.Replace(flows[i].Goal)
		for j := range flows[i].Expectations {
			flows[i].Expectations[j] = repl.Replace(flows[i].Expectations[j])
		}
	}
}

// flowsReferenceTestAccount reports whether any flow uses a credential
// placeholder — used to warn when ephemeral injection wasn't requested.
func flowsReferenceTestAccount(flows []studio.Scenario) bool {
	for _, f := range flows {
		if strings.Contains(f.Goal, "{{email}}") || strings.Contains(f.Goal, "{{password}}") {
			return true
		}
		for _, e := range f.Expectations {
			if strings.Contains(e, "{{email}}") || strings.Contains(e, "{{password}}") {
				return true
			}
		}
	}
	return false
}

func qaRandHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "rnd"
	}
	return hex.EncodeToString(b)[:n]
}
