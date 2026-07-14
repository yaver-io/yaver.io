package main

// surface.go — which frontend is talking to this agent.
//
// The agent serves many surfaces — a phone, a tablet, a TV, a car head unit, a
// watch, an AR/VR headset, the web dashboard, the CLI — and some responses
// should differ by surface: a watch wants a one-line summary where a TV wants a
// full pane; a redroid frame can be downscaled for a watch but not a TV; a
// reload targets the surface that asked. Until now the agent was blind to who
// called. Clients now send `X-Yaver-Surface: <surface>`; this reads and
// normalizes it so any handler can branch on it.
//
// It is advisory metadata, never an authorization signal — auth stays with the
// bearer token. A missing/unknown header just means "unspecified".

import (
	"net/http"
	"strings"
)

type ClientSurface string

const (
	SurfaceUnknown ClientSurface = "unknown"
	SurfaceMobile  ClientSurface = "mobile"
	SurfaceTablet  ClientSurface = "tablet"
	SurfaceTV      ClientSurface = "tv"
	SurfaceCar     ClientSurface = "car"
	SurfaceWatch   ClientSurface = "watch"
	SurfaceVision  ClientSurface = "vision" // AR/VR headset
	SurfaceWeb     ClientSurface = "web"
	SurfaceCLI     ClientSurface = "cli"
	SurfaceDesktop ClientSurface = "desktop"
)

// The header every Yaver client sends to name its surface.
const surfaceHeader = "X-Yaver-Surface"

// normalizeSurface maps common aliases to the canonical set so a client that
// says "appletv" or "vr" still lands on the right surface.
func normalizeSurface(raw string) ClientSurface {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "mobile", "phone", "ios", "android":
		return SurfaceMobile
	case "tablet", "ipad":
		return SurfaceTablet
	case "tv", "tvos", "appletv", "androidtv":
		return SurfaceTV
	case "car", "carplay", "androidauto", "auto":
		return SurfaceCar
	case "watch", "watchos", "wearos", "wear":
		return SurfaceWatch
	case "vision", "visionos", "ar", "vr", "xr", "headset":
		return SurfaceVision
	case "web", "dashboard", "browser":
		return SurfaceWeb
	case "cli", "terminal":
		return SurfaceCLI
	case "desktop", "electron":
		return SurfaceDesktop
	default:
		return SurfaceUnknown
	}
}

// surfaceFromHeaders reads the surface from a header set (e.g. an OpsContext's
// RequestHeaders). Returns SurfaceUnknown when unset.
func surfaceFromHeaders(h http.Header) ClientSurface {
	if h == nil {
		return SurfaceUnknown
	}
	return normalizeSurface(h.Get(surfaceHeader))
}

// surfaceFromRequest reads the surface directly off an *http.Request.
func surfaceFromRequest(r *http.Request) ClientSurface {
	if r == nil {
		return SurfaceUnknown
	}
	return normalizeSurface(r.Header.Get(surfaceHeader))
}
