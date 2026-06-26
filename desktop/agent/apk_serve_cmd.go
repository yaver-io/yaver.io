package main

// `yaver android` — Play-Store-free Android install serving.
//
// Talks to the local daemon's /android/apk/* routes (the persistent install
// server lives there). Thin CLI front-door over the same cell the ops verbs and
// the web/mobile UI use.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func runAndroidServe(args []string) {
	if len(args) == 0 {
		androidServeUsage()
		return
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "serve":
		fs := flag.NewFlagSet("android serve", flag.ExitOnError)
		apk := fs.String("apk", "", "Path to the .apk (universal APK)")
		app := fs.String("app", "", "Short app slug used in the url")
		pkg := fs.String("package", "", "Android package name (for assetlinks)")
		port := fs.Int("port", 0, "Port to bind (default 8000)")
		_ = fs.Parse(rest)
		androidApkCall("POST", "/android/apk/serve", map[string]interface{}{
			"apk": *apk, "app": *app, "package": *pkg, "port": *port,
		})
	case "publish":
		fs := flag.NewFlagSet("android publish", flag.ExitOnError)
		apk := fs.String("apk", "", "Path to the .apk")
		app := fs.String("app", "", "Short app slug used in the url")
		pkg := fs.String("package", "", "Android package name (for assetlinks)")
		vname := fs.String("version-name", "", "e.g. 1.2.3")
		vcode := fs.Int("version-code", 0, "monotonic integer")
		sha := fs.String("sha256", "", "Signing cert SHA-256 (colon-hex, comma-separated; falls back to vault ANDROID_RELEASE_SHA256)")
		domain := fs.String("domain", "", "Public hostname (omit for LAN-only)")
		dnsMode := fs.String("dns-mode", "", "http | cloudflare (default http)")
		port := fs.Int("port", 0, "Local port Caddy proxies to (default 8000)")
		_ = fs.Parse(rest)
		androidApkCall("POST", "/android/apk/publish", map[string]interface{}{
			"apk": *apk, "app": *app, "package": *pkg,
			"versionName": *vname, "versionCode": *vcode, "sha256": *sha,
			"domain": *domain, "dnsMode": *dnsMode, "port": *port,
		})
	case "status":
		androidApkCall("GET", "/android/apk/status", nil)
	case "stop":
		androidApkCall("POST", "/android/apk/stop", nil)
	default:
		androidServeUsage()
	}
}

func androidApkCall(method, path string, body map[string]interface{}) {
	res, err := localAgentRequest(method, path, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if ok, present := res["ok"].(bool); present && !ok {
		if msg, _ := res["error"].(string); msg != "" {
			fmt.Fprintf(os.Stderr, "error: %s\n", msg)
			os.Exit(1)
		}
	}
	if url, _ := res["url"].(string); url != "" {
		fmt.Printf("→ %s\n", url)
	}
	if apkURL, _ := res["apkUrl"].(string); apkURL != "" {
		fmt.Printf("  apk:        %s\n", apkURL)
	}
	if al, _ := res["assetlinks"].(string); al != "" {
		fmt.Printf("  assetlinks: %s\n", al)
	}
	if warn, _ := res["assetlinksWarning"].(string); warn != "" {
		fmt.Printf("  ⚠ %s\n", warn)
	}
	if hint, _ := res["hint"].(string); hint != "" {
		fmt.Printf("  %s\n", hint)
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
}

func androidServeUsage() {
	fmt.Print(`Yaver Android — serve your Android app for install without the Play Store

Usage:
  yaver android serve   --apk app.apk [--app NAME] [--package com.x.y] [--port 8000]
      LAN install — open the printed url on any phone on the same Wi-Fi, tap Install.

  yaver android publish --apk app.apk --version-name 1.2.3 --version-code 42 \
      [--package com.x.y] [--sha256 AA:BB:..] --domain apps.example.com [--dns-mode http]
      Public HTTPS install via Caddy (auto Let's Encrypt) with a live
      /.well-known/assetlinks.json for App Links + passkeys. Point a DNS
      A-record for the domain at this box first.

  yaver android status   Show the running server + published apps.
  yaver android stop      Stop the server.

Notes:
  • The apk must be a *universal* APK (build from your AAB with bundletool).
  • Real-install path that bypasses the Play Store — you are responsible for
    what you distribute and the right to do so.
`)
}
