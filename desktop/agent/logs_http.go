package main

// logs_http.go — HTTP surface for the log aggregation store.
// Mobile Monitor > Logs sub-tab and CLI one-liners both hit
// these endpoints.

import (
	"net/http"
	"strconv"
)

func (s *HTTPServer) handleLogsSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	entries := GlobalLogStore().Search(LogFilter{
		Query:    q.Get("q"),
		Level:    q.Get("level"),
		DeviceID: q.Get("device"),
		Since:    since,
		Limit:    limit,
	})
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"entries": entries,
		"stats":   GlobalLogStore().Stats(),
	})
}
