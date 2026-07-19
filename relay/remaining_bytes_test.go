package main

import (
	"testing"
	"time"
)

func newTestBandwidthManager() *BandwidthManager {
	return &BandwidthManager{
		devices: map[string]*DeviceBandwidth{},
		config:  DefaultBandwidthConfig(),
	}
}

// A device we have never seen must be UNMETERED (0), matching CheckAllowed's
// "new device, always allow first request". Returning a real budget here would
// cut off first use for every new device.
func TestRemainingBytesUnknownDeviceIsUnmetered(t *testing.T) {
	bm := newTestBandwidthManager()
	if got := bm.RemainingBytes("never-seen"); got != 0 {
		t.Errorf("RemainingBytes = %d, want 0 (unmetered)", got)
	}
}

func TestRemainingBytesFreeTier(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.SetDevicePaid("dev1", false)
	bm.RecordBytes("dev1", 0, 100*1024*1024, false) // 100 MB used

	got := bm.RemainingBytes("dev1")
	limit := int64(bm.config.FreeDeviceLimitMB) * 1024 * 1024 * int64(bm.getCurrentMultiplier())
	want := limit - 100*1024*1024
	if got != want {
		t.Errorf("RemainingBytes = %d, want %d", got, want)
	}
	if got <= 0 {
		t.Error("a device under its limit must have positive budget")
	}
}

// Paid devices get the larger allowance — this is the Relay Pro difference and
// it must actually show up in the streaming budget.
func TestRemainingBytesPaidTierIsLarger(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.SetDevicePaid("free1", false)
	bm.SetDevicePaid("paid1", true)
	bm.RecordBytes("free1", 0, 1024, false)
	bm.RecordBytes("paid1", 0, 1024, true)

	free := bm.RemainingBytes("free1")
	paid := bm.RemainingBytes("paid1")
	if paid <= free {
		t.Errorf("paid budget %d should exceed free budget %d", paid, free)
	}
}

// THE GUARANTEE: an over-quota device gets 1, not 0. Zero would read as
// "unmetered" and hand a free unlimited stream to exactly the device that has
// already exhausted its allowance — inverting the whole control.
func TestRemainingBytesOverLimitReturnsOneNotZero(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.SetDevicePaid("hog", false)
	over := int64(bm.config.FreeDeviceLimitMB)*1024*1024*10 + 1
	bm.RecordBytes("hog", 0, over, false)

	got := bm.RemainingBytes("hog")
	if got == 0 {
		t.Fatal("over-limit device returned 0, which callers read as UNMETERED — free unlimited egress")
	}
	if got != 1 {
		t.Errorf("RemainingBytes = %d, want 1", got)
	}
}

// Yesterday's usage must not bill against today's stream; the counter is reset
// by the next RecordBytes/CheckAllowed, so a stale date means unmetered.
func TestRemainingBytesStaleDayIsUnmetered(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.SetDevicePaid("dev2", false)
	bm.RecordBytes("dev2", 0, 400*1024*1024, false)

	bm.mu.Lock()
	bm.devices["dev2"].ResetDate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	bm.mu.Unlock()

	if got := bm.RemainingBytes("dev2"); got != 0 {
		t.Errorf("RemainingBytes = %d, want 0 (stale counter, about to reset)", got)
	}
}
