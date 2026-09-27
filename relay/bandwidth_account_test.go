package main

import "testing"

func TestDailyBandwidthAllowanceIsPerAccountNotPerDevice(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.config.PaidDeviceLimitMB = 100
	bm.config.RelaxMultiplier = 1
	bm.BindDeviceAccount("laptop", "user-a")
	bm.BindDeviceAccount("phone", "user-a")
	bm.SetDevicePaid("laptop", true)
	bm.SetDevicePaid("phone", true)
	bm.RecordBytes("laptop", 0, 60*1024*1024, true)
	bm.RecordBytes("phone", 0, 41*1024*1024, true)

	if err := bm.CheckAllowed("phone", 1); err == nil {
		t.Fatal("two devices on one account multiplied the paid allowance")
	}

	bm.BindDeviceAccount("other", "user-b")
	bm.SetDevicePaid("other", true)
	if err := bm.CheckAllowed("other", 1); err != nil {
		t.Fatalf("one account's usage leaked into another account: %v", err)
	}
}

func TestDynamicBandwidthLoadUsesMeasuredTraffic(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.config.MaxBandwidthMbps = 10
	bm.config.LowLoadThreshold = 0.3
	bm.config.HighLoadThreshold = 0.8
	bm.config.RelaxMultiplier = 3
	bm.RecordBytes("download", 0, 60*1024*1024, false) // ~8.4 Mbps over the current minute

	if got := bm.getCurrentMultiplier(); got != 1 {
		t.Fatalf("multiplier = %v, want strict 1x under measured high traffic", got)
	}
}

func TestPaidAllowanceNeverRelaxesPastProviderBudget(t *testing.T) {
	bm := newTestBandwidthManager()
	bm.config.PaidDeviceLimitMB = 100
	bm.config.RelaxMultiplier = 3
	bm.BindDeviceAccount("paid-box", "user-a")
	bm.SetDevicePaid("paid-box", true)
	bm.RecordBytes("paid-box", 0, 101*1024*1024, true)

	if err := bm.CheckAllowed("paid-box", 1); err == nil {
		t.Fatal("paid account exceeded its hard allowance under low relay load")
	}
	if got := bm.RemainingBytes("paid-box"); got != 1 {
		t.Fatalf("RemainingBytes = %d, want 1 for an over-quota paid account", got)
	}
}
