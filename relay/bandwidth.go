package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// BandwidthManager records per-device usage but enforces one daily allowance
// per authenticated account. Otherwise adding devices silently multiplies a
// subscriber's entitlement and makes shared-host capacity impossible to price.
// When overall server load is low, limits are relaxed.
// When load is high, per-device limits are enforced strictly.
type BandwidthManager struct {
	mu sync.RWMutex

	// Per-device tracking
	devices map[string]*DeviceBandwidth

	// Global config
	config BandwidthConfig

	// Global stats
	totalBytesIn  int64
	totalBytesOut int64
	activeWindow  time.Time // current 1-minute window

	storePath string
}

// BandwidthConfig controls bandwidth allocation.
type BandwidthConfig struct {
	// Per-device limits (per day)
	FreeDeviceLimitMB int `json:"freeDeviceLimitMb"` // default: 500MB/day for free tier
	PaidDeviceLimitMB int `json:"paidDeviceLimitMb"` // default: 20000MB/day for paid tier

	// Global server limits
	MaxBandwidthMbps int `json:"maxBandwidthMbps"` // total server bandwidth cap

	// Dynamic scaling thresholds
	LowLoadThreshold  float64 `json:"lowLoadThreshold"`  // below this: relax limits (0.3 = 30%)
	HighLoadThreshold float64 `json:"highLoadThreshold"` // above this: strict limits (0.8 = 80%)

	// Relaxation multiplier when under low load
	RelaxMultiplier float64 `json:"relaxMultiplier"` // e.g. 3.0 = 3x normal limit when idle
}

// DeviceBandwidth tracks a single device's bandwidth usage.
type DeviceBandwidth struct {
	DeviceID  string `json:"deviceId"`
	AccountID string `json:"accountId,omitempty"`
	IsPaid    bool   `json:"isPaid"`
	// Unmetered exempts the device from the daily cap entirely (usage is
	// still RECORDED for stats). Granted per-request from the caller's
	// Convex-verified plan (owner-dev) — never from anything client-sent.
	Unmetered  bool      `json:"unmetered,omitempty"`
	BytesIn    int64     `json:"bytesIn"`   // today
	BytesOut   int64     `json:"bytesOut"`  // today
	ResetDate  string    `json:"resetDate"` // "2026-03-22"
	LastActive time.Time `json:"-"`

	// Rate tracking (per minute window)
	windowStart time.Time
	windowBytes int64
}

// BandwidthStats is returned by the /bandwidth endpoint.
type BandwidthStats struct {
	TotalDevices      int                      `json:"totalDevices"`
	ActiveDevices     int                      `json:"activeDevices"`
	TotalBytesIn      int64                    `json:"totalBytesIn"`
	TotalBytesOut     int64                    `json:"totalBytesOut"`
	LoadPercent       float64                  `json:"loadPercent"`
	LimitsRelaxed     bool                     `json:"limitsRelaxed"`
	CurrentMultiplier float64                  `json:"currentMultiplier"`
	TopDevices        []DeviceBandwidthSummary `json:"topDevices"`
}

type DeviceBandwidthSummary struct {
	DeviceID  string `json:"deviceId"`
	BytesIn   int64  `json:"bytesIn"`
	BytesOut  int64  `json:"bytesOut"`
	IsPaid    bool   `json:"isPaid"`
	Unmetered bool   `json:"unmetered,omitempty"`
	LimitMB   int    `json:"limitMb"`
	UsedMB    int    `json:"usedMb"`
}

// DefaultBandwidthConfig returns sensible defaults.
func DefaultBandwidthConfig() BandwidthConfig {
	return BandwidthConfig{
		FreeDeviceLimitMB: 500,   // 500MB/day free
		PaidDeviceLimitMB: 20000, // 20GB/day paid
		MaxBandwidthMbps:  1000,  // 1Gbps server cap
		LowLoadThreshold:  0.3,   // 30%
		HighLoadThreshold: 0.8,   // 80%
		RelaxMultiplier:   3.0,   // 3x when idle
	}
}

// NewBandwidthManager creates a bandwidth tracker.
func NewBandwidthManager(config *BandwidthConfig, dataDir string) *BandwidthManager {
	cfg := DefaultBandwidthConfig()
	if config != nil {
		cfg = *config
	}
	bm := &BandwidthManager{
		devices:   make(map[string]*DeviceBandwidth),
		config:    cfg,
		storePath: filepath.Join(dataDir, "bandwidth.json"),
	}
	bm.load()

	// Start cleanup goroutine
	go bm.cleanupLoop()

	return bm
}

// CheckAllowed checks if a device is allowed to transfer bytes.
// Returns nil if allowed, error with reason if blocked.
func (bm *BandwidthManager) CheckAllowed(deviceID string, bytesRequested int64) error {
	if bytesRequested < 0 {
		bytesRequested = 0
	}
	bm.mu.RLock()
	dev, exists := bm.devices[deviceID]
	bm.mu.RUnlock()

	if !exists {
		// New device, always allow first request
		return nil
	}
	if dev.Unmetered {
		return nil
	}

	// Reset daily counter if needed
	today := time.Now().Format("2006-01-02")
	if dev.ResetDate != today {
		bm.mu.Lock()
		dev.BytesIn = 0
		dev.BytesOut = 0
		dev.ResetDate = today
		bm.mu.Unlock()
		return nil
	}

	// Calculate effective limit
	limitMB := bm.config.FreeDeviceLimitMB
	if dev.IsPaid {
		limitMB = bm.config.PaidDeviceLimitMB
	}

	// Paid traffic stays at the advertised hard allowance. Relaxing 20 GB/day
	// across 20 tenants could turn a safe ~12 TB/month host into 36 TB/month and
	// create unbounded egress charges. Only the small free allowance may borrow
	// otherwise-idle capacity.
	multiplier := bm.limitMultiplier(dev.IsPaid)
	effectiveLimitBytes := int64(float64(int64(limitMB)*1024*1024) * multiplier)

	bm.mu.RLock()
	totalUsed := bm.usageForAccountLocked(dev.AccountID, dev.ResetDate, deviceID)
	bm.mu.RUnlock()
	if totalUsed+bytesRequested > effectiveLimitBytes {
		// deviceID[:8] panicked for any id shorter than 8 bytes — a crash in
		// a request path, reachable by a device that registers a short id.
		return fmt.Errorf("bandwidth limit exceeded: %dMB used of %dMB daily account limit (device %s)",
			totalUsed/(1024*1024), int64(float64(limitMB)*multiplier), shortDeviceRef(deviceID))
	}

	return nil
}

// RecordBytes records bytes transferred for a device.
func (bm *BandwidthManager) RecordBytes(deviceID string, bytesIn, bytesOut int64, isPaid bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	today := time.Now().Format("2006-01-02")

	dev, exists := bm.devices[deviceID]
	if !exists {
		dev = &DeviceBandwidth{
			DeviceID:  deviceID,
			IsPaid:    isPaid,
			ResetDate: today,
		}
		bm.devices[deviceID] = dev
	}

	// Reset if new day
	if dev.ResetDate != today {
		dev.BytesIn = 0
		dev.BytesOut = 0
		dev.ResetDate = today
	}

	dev.BytesIn += bytesIn
	dev.BytesOut += bytesOut
	if dev.windowStart.IsZero() || time.Since(dev.windowStart) >= time.Minute {
		dev.windowStart = time.Now()
		dev.windowBytes = 0
	}
	dev.windowBytes += bytesIn + bytesOut
	// Usage accounting is not an entitlement verdict. Preserve a previously
	// resolved paid tier when this byte record came from a lane that could not
	// resolve billing; ApplyEntitlement performs explicit downgrades.
	if isPaid {
		dev.IsPaid = true
	}
	dev.LastActive = time.Now()

	bm.totalBytesIn += bytesIn
	bm.totalBytesOut += bytesOut
}

// BindDeviceAccount attaches the Convex-verified owner to a device. Empty
// identities are ignored; callers must never manufacture an account from a
// client-controlled header.
func (bm *BandwidthManager) BindDeviceAccount(deviceID, accountID string) {
	if deviceID == "" || accountID == "" {
		return
	}
	bm.mu.Lock()
	defer bm.mu.Unlock()
	dev, ok := bm.devices[deviceID]
	if !ok {
		dev = &DeviceBandwidth{DeviceID: deviceID, ResetDate: time.Now().Format("2006-01-02")}
		bm.devices[deviceID] = dev
	}
	dev.AccountID = accountID
}

func (bm *BandwidthManager) usageForAccountLocked(accountID, resetDate, fallbackDeviceID string) int64 {
	if accountID == "" {
		if dev := bm.devices[fallbackDeviceID]; dev != nil && dev.ResetDate == resetDate {
			return dev.BytesIn + dev.BytesOut
		}
		return 0
	}
	var total int64
	for _, candidate := range bm.devices {
		if candidate.AccountID == accountID && candidate.ResetDate == resetDate {
			total += candidate.BytesIn + candidate.BytesOut
		}
	}
	return total
}

// AccountUsageFor reports the account-wide meter shown by /my/bandwidth.
func (bm *BandwidthManager) AccountUsageFor(deviceIDs []string) (usedMB, limitMB int) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	for _, id := range deviceIDs {
		dev := bm.devices[id]
		if dev == nil {
			continue
		}
		limitMB = bm.config.FreeDeviceLimitMB
		if dev.IsPaid {
			limitMB = bm.config.PaidDeviceLimitMB
		}
		return int(bm.usageForAccountLocked(dev.AccountID, time.Now().Format("2006-01-02"), id) / (1024 * 1024)), limitMB
	}
	return 0, bm.config.FreeDeviceLimitMB
}

// SummaryFor returns usage rows for exactly the given devices — the
// per-tenant slice behind /my/bandwidth. Unlike GetStats/TopDevices this
// never sees another tenant's rows, so the caller-side filter can't be
// forgotten.
func (bm *BandwidthManager) SummaryFor(deviceIDs []string) []DeviceBandwidthSummary {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	var out []DeviceBandwidthSummary
	for _, id := range deviceIDs {
		dev, ok := bm.devices[id]
		if !ok {
			// No traffic recorded yet today — still report the device so the
			// UI shows "0 MB used" instead of silently omitting it.
			out = append(out, DeviceBandwidthSummary{DeviceID: id, LimitMB: bm.config.FreeDeviceLimitMB})
			continue
		}
		limitMB := bm.config.FreeDeviceLimitMB
		if dev.IsPaid {
			limitMB = bm.config.PaidDeviceLimitMB
		}
		out = append(out, DeviceBandwidthSummary{
			DeviceID:  dev.DeviceID,
			IsPaid:    dev.IsPaid,
			Unmetered: dev.Unmetered,
			LimitMB:   limitMB,
			UsedMB:    int((dev.BytesIn + dev.BytesOut) / (1024 * 1024)),
		})
	}
	return out
}

// GetStats returns current bandwidth statistics.
func (bm *BandwidthManager) GetStats() BandwidthStats {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	stats := BandwidthStats{
		TotalDevices:      len(bm.devices),
		TotalBytesIn:      bm.totalBytesIn,
		TotalBytesOut:     bm.totalBytesOut,
		CurrentMultiplier: bm.getCurrentMultiplierLocked(),
	}

	cutoff := time.Now().Add(-5 * time.Minute)
	for _, dev := range bm.devices {
		if dev.LastActive.After(cutoff) {
			stats.ActiveDevices++
		}
	}

	stats.LoadPercent = bm.currentLoadRatioLocked() * 100
	stats.LimitsRelaxed = stats.LoadPercent < bm.config.LowLoadThreshold*100

	// Top devices by usage
	type devUsage struct {
		id        string
		total     int64
		isPaid    bool
		unmetered bool
	}
	var sorted []devUsage
	for _, dev := range bm.devices {
		sorted = append(sorted, devUsage{dev.DeviceID, dev.BytesIn + dev.BytesOut, dev.IsPaid, dev.Unmetered})
	}
	// Simple sort (top 10)
	for i := 0; i < len(sorted) && i < 10; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].total > sorted[i].total {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	limit := 10
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for i := 0; i < limit; i++ {
		limitMB := bm.config.FreeDeviceLimitMB
		if sorted[i].isPaid {
			limitMB = bm.config.PaidDeviceLimitMB
		}
		stats.TopDevices = append(stats.TopDevices, DeviceBandwidthSummary{
			DeviceID: sorted[i].id,
			BytesIn:  0, BytesOut: 0, // simplified
			IsPaid: sorted[i].isPaid,
			// "paid" and "no cap at all" are different verdicts; an admin
			// view that can't tell them apart re-derives today's incident.
			Unmetered: sorted[i].unmetered,
			LimitMB:   limitMB,
			UsedMB:    int(sorted[i].total / (1024 * 1024)),
		})
	}

	return stats
}

// SetDevicePaid marks a device as paid tier (metered — Relay Pro buys a
// bigger allowance, not the absence of one).
func (bm *BandwidthManager) SetDevicePaid(deviceID string, isPaid bool) {
	bm.SetDeviceTier(deviceID, isPaid, false)
}

// deviceEntitlement is what a request managed to LEARN about the caller's
// plan. Known=false means "this request could not resolve one" — which is
// not the same as "free tier", and must never be written as if it were.
//
// The webview-cookie auth path carries no password and no signature, so it
// resolves nothing; preview subresources are exactly that traffic. Treating
// its silence as a free-tier verdict is what refused the owner's own
// browser lane at 1911MB while the store recorded them as exempt.
type deviceEntitlement struct {
	Known     bool
	IsPaid    bool
	Unmetered bool
}

var entitlementUnknown = deviceEntitlement{}

// ApplyEntitlement records a RESOLVED verdict and ignores an unresolved one.
// A resolved downgrade still applies, so a cancelled plan stops being exempt.
func (bm *BandwidthManager) ApplyEntitlement(deviceID string, ent deviceEntitlement) {
	if !ent.Known {
		return
	}
	bm.SetDeviceTier(deviceID, ent.IsPaid, ent.Unmetered)
}

// SetDeviceTier applies a resolved entitlement: paid (bigger allowance)
// and/or unmetered (no cap — owner-dev plan). Prefer ApplyEntitlement at
// call sites that may not know the answer.
func (bm *BandwidthManager) SetDeviceTier(deviceID string, isPaid, unmetered bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if dev, ok := bm.devices[deviceID]; ok {
		dev.IsPaid = isPaid
		dev.Unmetered = unmetered
	} else {
		bm.devices[deviceID] = &DeviceBandwidth{
			DeviceID:  deviceID,
			IsPaid:    isPaid,
			Unmetered: unmetered,
			ResetDate: time.Now().Format("2006-01-02"),
		}
	}
}

// RemainingBytes reports how many more bytes a device may transfer today.
//
// Exists for streaming responses, where the usual "CheckAllowed once at request
// start" guard is useless: a streaming GET has ContentLength 0, so it always
// passes, and nothing re-checks while gigabytes flow. Callers pass this to
// countingResponseWriter as a hard in-flight budget.
//
// Returns 0 for a device with no record yet, which callers MUST read as
// "unmetered" (matching CheckAllowed, which always allows a new device's first
// request) — never as "no bytes allowed". Returns 1 rather than 0 for a device
// that is genuinely over its limit, so the two cases stay distinguishable and
// an over-quota stream is cut on its first write instead of running free.
func (bm *BandwidthManager) RemainingBytes(deviceID string) int64 {
	bm.mu.RLock()
	dev, exists := bm.devices[deviceID]
	bm.mu.RUnlock()
	if !exists {
		return 0 // unmetered: no record yet
	}
	if dev.Unmetered {
		return 0 // unmetered by plan (owner-dev)
	}

	// A stale counter is about to be reset by the next RecordBytes/CheckAllowed;
	// don't bill today's stream against yesterday's usage.
	if dev.ResetDate != time.Now().Format("2006-01-02") {
		return 0
	}

	limitMB := bm.config.FreeDeviceLimitMB
	if dev.IsPaid {
		limitMB = bm.config.PaidDeviceLimitMB
	}
	effective := int64(float64(int64(limitMB)*1024*1024) * bm.limitMultiplier(dev.IsPaid))
	bm.mu.RLock()
	used := bm.usageForAccountLocked(dev.AccountID, dev.ResetDate, deviceID)
	bm.mu.RUnlock()
	remaining := effective - used
	if remaining <= 0 {
		return 1
	}
	return remaining
}

// getCurrentMultiplier returns the bandwidth multiplier based on current load.
// When server is idle (<30% load), limits are relaxed by RelaxMultiplier.
// When server is busy (>80% load), strict limits (1x).
// Linear interpolation between thresholds.
func (bm *BandwidthManager) getCurrentMultiplier() float64 {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return bm.getCurrentMultiplierLocked()
}

func (bm *BandwidthManager) limitMultiplier(isPaid bool) float64 {
	if isPaid {
		return 1.0
	}
	return bm.getCurrentMultiplier()
}

func (bm *BandwidthManager) getCurrentMultiplierLocked() float64 {
	loadRatio := bm.currentLoadRatioLocked()

	if bm.config.MaxBandwidthMbps == 0 {
		return bm.config.RelaxMultiplier
	}

	if loadRatio <= bm.config.LowLoadThreshold {
		return bm.config.RelaxMultiplier // full relaxation
	}
	if loadRatio >= bm.config.HighLoadThreshold {
		return 1.0 // strict limits
	}

	// Linear interpolation
	range_ := bm.config.HighLoadThreshold - bm.config.LowLoadThreshold
	position := (loadRatio - bm.config.LowLoadThreshold) / range_
	return bm.config.RelaxMultiplier - (bm.config.RelaxMultiplier-1.0)*position
}

func (bm *BandwidthManager) currentLoadRatioLocked() float64 {
	var recentBytes int64
	now := time.Now()
	for _, dev := range bm.devices {
		if !dev.windowStart.IsZero() && now.Sub(dev.windowStart) < time.Minute {
			recentBytes += dev.windowBytes
		}
	}

	if bm.config.MaxBandwidthMbps == 0 {
		return 0
	}

	// Compare measured traffic in the current minute with configured link
	// capacity. Device count is not a bandwidth unit: one build download can
	// saturate a link while hundreds of idle SSE connections consume almost 0.
	return (float64(recentBytes) * 8 / 60 / 1_000_000) / float64(bm.config.MaxBandwidthMbps)
}

func (bm *BandwidthManager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		bm.mu.Lock()
		cutoff := time.Now().Add(-7 * 24 * time.Hour) // remove devices inactive for 7 days
		for id, dev := range bm.devices {
			if dev.LastActive.Before(cutoff) {
				delete(bm.devices, id)
			}
		}
		bm.save()
		bm.mu.Unlock()
	}
}

func (bm *BandwidthManager) save() {
	data, _ := json.MarshalIndent(bm.devices, "", "  ")
	os.MkdirAll(filepath.Dir(bm.storePath), 0755)
	os.WriteFile(bm.storePath, data, 0600)
}

func (bm *BandwidthManager) load() {
	data, err := os.ReadFile(bm.storePath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &bm.devices)
}

// LogUsage logs bandwidth stats periodically (called from main).
func (bm *BandwidthManager) LogUsage() {
	stats := bm.GetStats()
	log.Printf("[bandwidth] %d devices (%d active), load: %.1f%%, multiplier: %.1fx, total: %dMB in / %dMB out",
		stats.TotalDevices, stats.ActiveDevices, stats.LoadPercent,
		stats.CurrentMultiplier, stats.TotalBytesIn/(1024*1024), stats.TotalBytesOut/(1024*1024))
}

// shortDeviceRef is a log/error-safe device prefix: never panics, never
// leaks more than a recognisable stub.
func shortDeviceRef(deviceID string) string {
	if len(deviceID) <= 8 {
		return deviceID
	}
	return deviceID[:8]
}
