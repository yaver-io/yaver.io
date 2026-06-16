package main

// charge_controller.go — the generic, protocol-agnostic seam for *controlling*
// an EV charge session (status / start / stop).
//
// ───────────────────────── IP BOUNDARY (read me) ─────────────────────────
// This file ships ONLY the generic interface, the driver registry, and a
// discovery-only default that refuses Start/Stop. It contains NO charging
// protocol — no OCPP, no charge-point control logic, no operator-specific
// adapter, and no hostnames or IDs of any private network.
//
// Real charge control is proprietary. A concrete ChargeController (e.g. an
// OCPP back-office adapter, or a Talos-network bridge) is registered from a
// PRIVATE overlay that imports Yaver, or runs out-of-process and talks to
// the agent over the mesh. Such drivers MUST NEVER live in this open-source
// repo. Yaver compiles and is fully useful (discovery via ev_charging) with
// zero knowledge of any control plane — the seam below is the only contract
// a private overlay needs.
// ──────────────────────────────────────────────────────────────────────────
//
// The registry pattern mirrors machine.Engine's RegisterDriver
// (machine/driver_registry.go): register by id, look up, list, remove.

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// ChargeSession is a generic snapshot of a charge connector/session. Fields
// are the lowest common denominator a UI / AI consumer needs; protocol-
// specific detail goes in Detail.
type ChargeSession struct {
	StationID   string                 `json:"station_id,omitempty"`
	ConnectorID string                 `json:"connector_id,omitempty"`
	State       string                 `json:"state"` // e.g. available / charging / faulted / unavailable
	EnergyKWh   float64                `json:"energy_kwh,omitempty"`
	PowerKW     float64                `json:"power_kw,omitempty"`
	Detail      map[string]interface{} `json:"detail,omitempty"`
}

// ChargeController is the generic, protocol-agnostic control contract. A
// concrete driver (private, out-of-tree) implements it; this repo ships only
// the discovery-only default.
type ChargeController interface {
	// Name identifies the driver (e.g. "ocpp", "talos"); never a secret.
	Name() string
	// Status reports the current state of a station/connector.
	Status(ctx context.Context, stationID, connectorID string) (ChargeSession, error)
	// Start begins a charge session. Discovery-only builds refuse this.
	Start(ctx context.Context, stationID, connectorID string) (ChargeSession, error)
	// Stop ends a charge session. Discovery-only builds refuse this.
	Stop(ctx context.Context, stationID, connectorID string) (ChargeSession, error)
}

// ── registry (mirrors machine.Engine.RegisterDriver) ─────────────────────────

var (
	chargeControllerMu      sync.RWMutex
	chargeControllers                        = map[string]ChargeController{}
	chargeControllerDefault ChargeController = discoveryOnlyController{}
)

// RegisterChargeController adds (or replaces) a controller under id. A private
// overlay calls this from its init/wiring to make charge control available.
// Registering also makes that controller the active default unless a default
// id is set explicitly via SetDefaultChargeController.
func RegisterChargeController(id string, c ChargeController) {
	if id == "" || c == nil {
		return
	}
	chargeControllerMu.Lock()
	chargeControllers[id] = c
	// First non-discovery controller registered becomes the default.
	if _, isDiscovery := chargeControllerDefault.(discoveryOnlyController); isDiscovery {
		chargeControllerDefault = c
	}
	chargeControllerMu.Unlock()
}

// LookupChargeController returns the controller registered under id.
func LookupChargeController(id string) (ChargeController, bool) {
	chargeControllerMu.RLock()
	defer chargeControllerMu.RUnlock()
	c, ok := chargeControllers[id]
	return c, ok
}

// DefaultChargeController returns the active controller — a real driver if a
// private overlay registered one, otherwise the discovery-only default that
// refuses Start/Stop.
func DefaultChargeController() ChargeController {
	chargeControllerMu.RLock()
	defer chargeControllerMu.RUnlock()
	return chargeControllerDefault
}

// SetDefaultChargeController pins a registered id as the default.
func SetDefaultChargeController(id string) bool {
	chargeControllerMu.Lock()
	defer chargeControllerMu.Unlock()
	c, ok := chargeControllers[id]
	if !ok {
		return false
	}
	chargeControllerDefault = c
	return true
}

// ChargeControllerIDs lists registered controller ids, sorted.
func ChargeControllerIDs() []string {
	chargeControllerMu.RLock()
	defer chargeControllerMu.RUnlock()
	ids := make([]string, 0, len(chargeControllers))
	for id := range chargeControllers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ── discovery-only default ───────────────────────────────────────────────────

// discoveryOnlyController is the public default. Status reports "unavailable"
// and Start/Stop return a clear, structured error. It is the ONLY controller
// that ships in this repo; real control comes from a private overlay.
type discoveryOnlyController struct{}

func (discoveryOnlyController) Name() string { return "discovery-only" }

func (discoveryOnlyController) Status(_ context.Context, stationID, connectorID string) (ChargeSession, error) {
	return ChargeSession{
		StationID:   stationID,
		ConnectorID: connectorID,
		State:       "unavailable",
		Detail: map[string]interface{}{
			"reason": "no charge controller registered — discovery only",
		},
	}, nil
}

func (discoveryOnlyController) Start(_ context.Context, _, _ string) (ChargeSession, error) {
	return ChargeSession{}, errChargeControlUnavailable
}

func (discoveryOnlyController) Stop(_ context.Context, _, _ string) (ChargeSession, error) {
	return ChargeSession{}, errChargeControlUnavailable
}

var errChargeControlUnavailable = fmt.Errorf(
	"control unavailable — no charge controller registered; charge start/stop " +
		"is provided by a private overlay, not by this open-source agent")
