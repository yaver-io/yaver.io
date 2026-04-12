package main

import (
	"encoding/json"
	"net/http"
)

// Small helper since we call it from backups_encryption.go.
func decodeJSON(r *http.Request, out interface{}) {
	_ = json.NewDecoder(r.Body).Decode(out)
}
