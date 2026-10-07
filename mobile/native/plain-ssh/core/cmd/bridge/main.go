// The browser companion binds only loopback and accepts one local UI origin.
// Remote authorization is still the SSH handshake, never a Yaver bearer.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	plainssh "io.yaver/plainssh"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func handler(origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "Origin")
		if r.Header.Get("Origin") != origin {
			http.Error(w, "Unapproved SSH bridge origin", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/invoke" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "Use JSON POST /invoke", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256*1024))
		if err != nil {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
			return
		}
		var operation struct {
			Op string `json:"op"`
		}
		if json.Unmarshal(body, &operation) != nil || operation.Op == "closeAll" {
			http.Error(w, "Unsupported browser operation", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, plainssh.Invoke(string(body)))
	})
}

func main() {
	listen := flag.String("listen", "127.0.0.1:18494", "loopback SSH companion address")
	origin := flag.String("origin", "http://localhost:8094", "approved local browser origin")
	flag.Parse()
	host, _, err := net.SplitHostPort(*listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("SSH companion must bind a loopback IP")
	}
	u, err := url.Parse(*origin)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") || (u.Hostname() != "localhost" && (net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback()) && *origin != "https://yaver.io" && *origin != "https://www.yaver.io") {
		log.Fatal("approved origin must be an exact local HTTP(S) origin or the explicitly selected Yaver HTTPS origin")
	}
	server := &http.Server{Addr: *listen, Handler: handler(*origin), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 30 * time.Second}
	fmt.Println("Plain SSH companion ready for the approved local UI; SSH host verification remains required.")
	log.Fatal(server.ListenAndServe())
}
