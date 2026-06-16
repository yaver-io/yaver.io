package main

import (
	"context"
	"strings"
	"testing"
)

func TestEVConnectorTypes(t *testing.T) {
	out, ok := mcpEVConnectorTypes().(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected return type")
	}
	cts, _ := out["connector_types"].([]EVConnectorType)
	if len(cts) == 0 {
		t.Fatal("expected connector types")
	}
	want := map[string]bool{"type2": false, "ccs2": false, "ccs1": false, "chademo": false, "nacs": false, "type1": false}
	for _, c := range cts {
		if _, ok := want[c.ID]; ok {
			want[c.ID] = true
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing connector type %q", id)
		}
	}
	presets, _ := out["vehicle_presets"].([]EVVehiclePreset)
	if len(presets) < 2 {
		t.Fatalf("expected >=2 vehicle presets, got %d", len(presets))
	}
}

func TestEVNetworks(t *testing.T) {
	tr, _ := mcpEVNetworks("turkey").(map[string]interface{})
	nets, _ := tr["networks"].([]EVNetwork)
	if len(nets) == 0 {
		t.Fatal("expected Turkey networks")
	}
	hasTrugo := false
	for _, n := range nets {
		if n.ID == "trugo" {
			hasTrugo = true
		}
		if n.Country != "TR" {
			t.Errorf("expected TR network, got %q", n.Country)
		}
	}
	if !hasTrugo {
		t.Error("expected Trugo in Turkey networks")
	}
	all, _ := mcpEVNetworks("").(map[string]interface{})
	allNets, _ := all["networks"].([]EVNetwork)
	if len(allNets) <= len(nets) {
		t.Error("expected all-regions list to exceed Turkey-only list")
	}
}

func TestEVChargingValidation(t *testing.T) {
	out, _ := mcpEVCharging(0, 0, 0, "", "", "", 0).(map[string]interface{})
	if out["error"] == nil {
		t.Error("expected error for missing coords")
	}
}

func TestEVCountryCode(t *testing.T) {
	// 2-letter inputs pass through uppercased (arbitrary ISO code); only
	// unknown >2-char names resolve to "".
	cases := map[string]string{"turkey": "TR", "TR": "TR", "us": "US", "germany": "DE", "": "", "narnia": "", "zz": "ZZ"}
	for in, want := range cases {
		if got := openChargeMapCountryCode(in); got != want {
			t.Errorf("countryCode(%q)=%q want %q", in, got, want)
		}
	}
}

func TestEVConnectorNormalize(t *testing.T) {
	cases := map[string]string{
		"CCS (Type 2)":         "ccs2",
		"CHAdeMO":              "chademo",
		"Type 2 (Socket Only)": "type2",
		"Tesla (Model S/X)":    "nacs",
		"J1772":                "type1",
	}
	for in, want := range cases {
		if got := normalizeConnectorID(in); got != want {
			t.Errorf("normalize(%q)=%q want %q", in, got, want)
		}
	}
}

func TestEVChargeControllerSeam(t *testing.T) {
	// Default must be discovery-only and refuse control.
	c := DefaultChargeController()
	if c.Name() != "discovery-only" {
		t.Fatalf("expected discovery-only default, got %q", c.Name())
	}
	if _, err := c.Start(context.Background(), "s1", "c1"); err == nil {
		t.Error("expected Start to be refused")
	} else if !strings.Contains(err.Error(), "control unavailable") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := c.Stop(context.Background(), "s1", "c1"); err == nil {
		t.Error("expected Stop to be refused")
	}
	st, err := c.Status(context.Background(), "s1", "c1")
	if err != nil {
		t.Errorf("Status should not error: %v", err)
	}
	if st.State != "unavailable" {
		t.Errorf("expected unavailable state, got %q", st.State)
	}

	// A registered controller should become the default and be looked up.
	RegisterChargeController("fake", fakeController{})
	if _, ok := LookupChargeController("fake"); !ok {
		t.Error("expected fake controller to be registered")
	}
	ids := ChargeControllerIDs()
	found := false
	for _, id := range ids {
		if id == "fake" {
			found = true
		}
	}
	if !found {
		t.Error("expected fake in controller ids")
	}
}

type fakeController struct{}

func (fakeController) Name() string { return "fake" }
func (fakeController) Status(context.Context, string, string) (ChargeSession, error) {
	return ChargeSession{State: "available"}, nil
}
func (fakeController) Start(context.Context, string, string) (ChargeSession, error) {
	return ChargeSession{State: "charging"}, nil
}
func (fakeController) Stop(context.Context, string, string) (ChargeSession, error) {
	return ChargeSession{State: "available"}, nil
}
