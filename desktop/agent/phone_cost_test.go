package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnforcePhoneDeployBudget_DefaultCap(t *testing.T) {
	// Below default cap — OK.
	if err := EnforcePhoneDeployBudget(1024, 0); err != nil {
		t.Errorf("1 KB should be allowed: %v", err)
	}
	// Above default cap — rejected with a user-facing message that mentions
	// the remedy (don't IncludeData unless you need it).
	oversize := PhoneDeployBudgetBytes + 1
	err := EnforcePhoneDeployBudget(oversize, 0)
	if err == nil {
		t.Fatal("expected error for oversize bundle")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--include-data") {
		t.Errorf("error should hint at --include-data remedy, got %q", msg)
	}
	if !strings.Contains(msg, "MB") {
		t.Errorf("error should include MB figure, got %q", msg)
	}
}

func TestEnforcePhoneDeployBudget_Override(t *testing.T) {
	// Custom cap below default — smaller bundles rejected.
	if err := EnforcePhoneDeployBudget(2*1024, 1*1024); err == nil {
		t.Error("expected rejection with override cap below actual size")
	}
	// Custom cap above default — larger bundles allowed.
	huge := int64(200 * 1024 * 1024) // 200 MB
	if err := EnforcePhoneDeployBudget(huge, huge+1); err != nil {
		t.Errorf("override cap should allow 200 MB: %v", err)
	}
}

func TestPhoneDeployCostHints_CoversAllTargets(t *testing.T) {
	hints := PhoneDeployCostHints()
	byKind := map[string]PhoneDeployCostHint{}
	for _, h := range hints {
		byKind[h.TargetKind] = h
	}
	for _, must := range []string{"this-device", "dev-hw", "yaver-cloud", "cloudflare-workers", "custom"} {
		h, ok := byKind[must]
		if !ok {
			t.Errorf("missing cost hint for %q", must)
			continue
		}
		if h.Advice == "" || h.Free == "" {
			t.Errorf("cost hint for %q has empty Advice/Free: %+v", must, h)
		}
	}
}

func TestHandlePhoneCostHint_ShapeAndHeadline(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodGet, "/phone/projects/cost-hint", nil)
	w := httptest.NewRecorder()
	srv.handlePhoneCostHint(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Hints          []PhoneDeployCostHint `json:"hints"`
		BundleCapBytes int64                 `json:"bundleCapBytes"`
		BundleCapMB    int64                 `json:"bundleCapMB"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v — body=%s", err, w.Body.String())
	}
	if out.BundleCapBytes != PhoneDeployBudgetBytes {
		t.Errorf("cap bytes mismatch: got %d want %d", out.BundleCapBytes, PhoneDeployBudgetBytes)
	}
	if out.BundleCapMB != 50 {
		t.Errorf("expected 50 MB default cap, got %d", out.BundleCapMB)
	}
	if len(out.Hints) < 5 {
		t.Errorf("expected >=5 targets covered, got %d", len(out.Hints))
	}
}

func TestExportPhoneProject_RespectsBudget(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "tiny", Template: "todos"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Default cap — bundle is tiny, should pass.
	if _, err := ExportPhoneProjectWithOptions("tiny", PhoneExportOptions{}); err != nil {
		t.Fatalf("default export: %v", err)
	}
	// 1-byte cap — bundle is ~1.5 KB, should reject with the remedy hint.
	_, err := ExportPhoneProjectWithOptions("tiny", PhoneExportOptions{MaxBundleBytes: 1})
	if err == nil {
		t.Fatalf("expected error at 1-byte cap")
	}
	if !strings.Contains(err.Error(), "MB") {
		t.Errorf("error should report MB, got %q", err.Error())
	}
}
