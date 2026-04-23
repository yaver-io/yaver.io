package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if strings.TrimSpace(port) == "" {
		port = "4010"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/google/token", func(w http.ResponseWriter, r *http.Request) {
		suffix := requestSuffix(r, "google")
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": providerToken("google", suffix),
			"token_type":   "Bearer",
		})
	})
	mux.HandleFunc("/google/userinfo", func(w http.ResponseWriter, r *http.Request) {
		identity := providerIdentity("google", bearerSuffix(r, "google"))
		writeJSON(w, http.StatusOK, map[string]any{
			"id":             identity.ID,
			"sub":            identity.ID,
			"email":          identity.Email,
			"email_verified": true,
			"name":           identity.Name,
			"picture":        "https://example.test/google.png",
		})
	})

	mux.HandleFunc("/microsoft/token", func(w http.ResponseWriter, r *http.Request) {
		suffix := requestSuffix(r, "microsoft")
		identity := providerIdentity("microsoft", suffix)
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": providerToken("microsoft", suffix),
			"token_type":   "Bearer",
			"id_token": jwt(map[string]any{
				"sub":                identity.ID,
				"oid":                identity.ID,
				"email":              identity.Email,
				"preferred_username": identity.Email,
				"name":               identity.Name,
			}),
		})
	})
	mux.HandleFunc("/microsoft/userinfo", func(w http.ResponseWriter, r *http.Request) {
		identity := providerIdentity("microsoft", bearerSuffix(r, "microsoft"))
		writeJSON(w, http.StatusOK, map[string]any{
			"id":                identity.ID,
			"mail":              identity.Email,
			"userPrincipalName": identity.Email,
			"displayName":       identity.Name,
		})
	})

	mux.HandleFunc("/apple/token", func(w http.ResponseWriter, r *http.Request) {
		suffix := requestSuffix(r, "apple")
		identity := providerIdentity("apple", suffix)
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": providerToken("apple", suffix),
			"token_type":   "Bearer",
			"id_token": jwt(map[string]any{
				"iss":              "https://appleid.apple.com",
				"aud":              "com.yaver.web",
				"sub":              identity.ID,
				"email":            identity.Email,
				"email_verified":   true,
				"is_private_email": false,
			}),
		})
	})

	mux.HandleFunc("/github/token", func(w http.ResponseWriter, r *http.Request) {
		suffix := requestSuffix(r, "github")
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": providerToken("github", suffix),
			"token_type":   "bearer",
		})
	})
	mux.HandleFunc("/github/user", func(w http.ResponseWriter, r *http.Request) {
		identity := providerIdentity("github", bearerSuffix(r, "github"))
		numericID := githubNumericID(identity.Suffix)
		writeJSON(w, http.StatusOK, map[string]any{
			"id":         numericID,
			"login":      fmt.Sprintf("github-ci-%s", identity.Suffix),
			"name":       identity.Name,
			"email":      "",
			"avatar_url": "https://example.test/github.png",
		})
	})
	mux.HandleFunc("/github/user/emails", func(w http.ResponseWriter, r *http.Request) {
		identity := providerIdentity("github", bearerSuffix(r, "github"))
		writeJSON(w, http.StatusOK, []map[string]any{
			{"email": identity.Email, "primary": true, "verified": true},
		})
	})

	mux.HandleFunc("/gitlab/token", func(w http.ResponseWriter, r *http.Request) {
		suffix := requestSuffix(r, "gitlab")
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": providerToken("gitlab", suffix),
			"token_type":   "Bearer",
		})
	})
	mux.HandleFunc("/gitlab/userinfo", func(w http.ResponseWriter, r *http.Request) {
		identity := providerIdentity("gitlab", bearerSuffix(r, "gitlab"))
		writeJSON(w, http.StatusOK, map[string]any{
			"sub":                identity.ID,
			"email":              identity.Email,
			"name":               identity.Name,
			"preferred_username": fmt.Sprintf("gitlab-ci-%s", identity.Suffix),
			"picture":            "https://example.test/gitlab.png",
		})
	})

	addr := ":" + port
	log.Printf("oauth mock listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func jwt(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(body)
	return fmt.Sprintf("%s.%s.", header, payload)
}

type identityFixture struct {
	ID     string
	Email  string
	Name   string
	Suffix string
}

func requestSuffix(r *http.Request, provider string) string {
	_ = r.ParseForm()
	return normalizeSuffix(extractSuffix(r.Form.Get("code"), provider))
}

func bearerSuffix(r *http.Request, provider string) string {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	return normalizeSuffix(extractSuffix(token, provider))
}

func providerToken(provider, suffix string) string {
	return fmt.Sprintf("%s-access-token-%s", provider, normalizeSuffix(suffix))
}

func providerIdentity(provider, suffix string) identityFixture {
	safeSuffix := normalizeSuffix(suffix)
	display := strings.ToUpper(provider[:1]) + provider[1:]
	return identityFixture{
		ID:     fmt.Sprintf("%s-user-%s", provider, safeSuffix),
		Email:  fmt.Sprintf("%s-ci+%s@yaver.test", provider, safeSuffix),
		Name:   fmt.Sprintf("%s CI User %s", display, safeSuffix),
		Suffix: safeSuffix,
	}
}

func extractSuffix(raw, provider string) string {
	prefixes := []string{
		fmt.Sprintf("mock-%s-code-", provider),
		fmt.Sprintf("%s-access-token-", provider),
		fmt.Sprintf("mock-%s-", provider),
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(raw, prefix) {
			return raw[len(prefix):]
		}
	}
	if raw != "" {
		return raw
	}
	return "default"
}

func normalizeSuffix(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "default"
	}
	var b strings.Builder
	for _, ch := range raw {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= 'A' && ch <= 'Z':
			b.WriteRune(ch + ('a' - 'A'))
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		default:
			b.WriteByte('-')
		}
	}
	safe := strings.Trim(b.String(), "-")
	if safe == "" {
		return "default"
	}
	return safe
}

func githubNumericID(suffix string) int64 {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, suffix)
	if digits == "" {
		digits = "12345"
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 12345
	}
	return id
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func init() {
	time.Local = time.UTC
}
