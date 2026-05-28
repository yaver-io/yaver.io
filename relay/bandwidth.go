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

// BandwidthManager tracks and limits per-device bandwidth usage.
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
	FreeDeviceLimitMB  int `json:"freeDeviceLimitMb"`  // default: 500MB/day for free tier
	PaidDeviceLimitMB  int `json:"paidDeviceLimitMb"`  // default: 20000MB/day for paid tier

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
	DeviceID   string `json:"deviceId"`
	IsPaid     bool   `json:"isPaid"`
	BytesIn    int64  `json:"bytesIn"`    // today
	BytesOut   int64  `json:"bytesOut"`   // today
	ResetDate  string `json:"resetDate"`  // "2026-03-22"
	LastActive time.Time `json:"-"`

	// Rate tracking (per minute window)
	windowStart time.Time
	windowBytes int64
}

// BandwidthStats is returned by the /bandwidth endpoint.
type BandwidthStats struct {
	TotalDevices    int                `json:"totalDevices"`
	ActiveDevices   int                `json:"activeDevices"`
	TotalBytesIn    int64              `json:"totalBytesIn"`
	TotalBytesOut   int64              `json:"totalBytesOut"`
	LoadPercent     float64            `json:"loadPercent"`
	LimitsRelaxed   bool               `json:"limitsRelaxed"`
	CurrentMultiplier float64          `json:"currentMultiplier"`
	TopDevices      []DeviceBandwidthSummary `json:"topDevices"`
}

type DeviceBandwidthSummary struct {
	DeviceID string `json:"deviceId"`
	BytesIn  int64  `json:"bytesIn"`
	BytesOut int64  `json:"bytesOut"`
	IsPaid   bool   `json:"isPaid"`
	LimitMB  int    `json:"limitMb"`
	UsedMB   int    `json:"usedMb"`
}

// DefaultBandwidthConfig returns sensible defaults.
func DefaultBandwidthConfig() BandwidthConfig {
	return BandwidthConfig{
		FreeDeviceLimitMB:  500,    // 500MB/day free
		PaidDeviceLimitMB:  20000,  // 20GB/day paid
		MaxBandwidthMbps:   1000,   // 1Gbps server cap
		LowLoadThreshold:   0.3,    // 30%
		HighLoadThreshold:  0.8,    // 80%
		RelaxMultiplier:    3.0,    // 3x when idle
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

	// Apply dynamic multiplier based on server load
	multiplier := bm.getCurrentMultiplier()
	effectiveLimitBytes := int64(limitMB) * 1024 * 1024 * int64(multiplier)

	totalUsed := dev.BytesIn + dev.BytesOut
	if totalUsed+bytesRequested > effectiveLimitBytes {
		return fmt.Errorf("bandwidth limit exceeded: %dMB used of %dMB daily limit (device %s)",
			totalUsed/(1024*1024), int64(float64(limitMB)*multiplier), deviceID[:8])
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
	dev.IsPaid = isPaid
	dev.LastActive = time.Now()

	bm.totalBytesIn += bytesIn
	bm.totalBytesOut += bytesOut
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

	// Load estimate: active devices as % of what we think max is
	// Rough: each active device might use 1Mbps average
	if bm.config.MaxBandwidthMbps > 0 {
		stats.LoadPercent = float64(stats.ActiveDevices) / float64(bm.config.MaxBandwidthMbps) * 100
	}
	stats.LimitsRelaxed = stats.LoadPercent < bm.config.LowLoadThreshold*100

	// Top devices by usage
	type devUsage struct {
		id     string
		total  int64
		isPaid bool
	}
	var sorted []devUsage
	for _, dev := range bm.devices {
		sorted = append(sorted, devUsage{dev.DeviceID, dev.BytesIn + dev.BytesOut, dev.IsPaid})
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
			IsPaid:   sorted[i].isPaid,
			LimitMB:  limitMB,
			UsedMB:   int(sorted[i].total / (1024 * 1024)),
		})
	}

	return stats
}

// SetDevicePaid marks a device as paid tier.
func (bm *BandwidthManager) SetDevicePaid(deviceID string, isPaid bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if dev, ok := bm.devices[deviceID]; ok {
		dev.IsPaid = isPaid
	} else {
		bm.devices[deviceID] = &DeviceBandwidth{
			DeviceID:  deviceID,
			IsPaid:    isPaid,
			ResetDate: time.Now().Format("2006-01-02"),
		}
	}
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

func (bm *BandwidthManager) getCurrentMultiplierLocked() float64 {
	activeDevices := 0
	cutoff := time.Now().Add(-5 * time.Minute)
	for _, dev := range bm.devices {
		if dev.LastActive.After(cutoff) {
			activeDevices++
		}
	}

	if bm.config.MaxBandwidthMbps == 0 {
		return bm.config.RelaxMultiplier
	}

	loadRatio := float64(activeDevices) / float64(bm.config.MaxBandwidthMbps)

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
