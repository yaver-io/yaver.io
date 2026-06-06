package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yaver-io/agent/machine"
)

// fakeCam implements robot.Camera with a static frame.
type fakeCam struct{ avail bool }

func (c fakeCam) Grab(ctx context.Context) ([]byte, error) {
	return []byte{0xFF, 0xD8, 0xFF, 0xD9}, nil
}
func (c fakeCam) Available() bool { return c.avail }

// fakeVLM returns a fixed OpenAI-style chat completion whose content is the HMI
// verdict JSON, so the vision driver can be tested with no model/hardware.
func fakeVLM(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVisionDriver_ReadHMI(t *testing.T) {
	srv := fakeVLM(t, `{"state":"running","fields":{"cut_length":1250,"quantity":500},"alarm":""}`)
	d := newVisionDriver(visionDriverConfig{
		Name: "cst18d-1", Kind: "crimp", Camera: fakeCam{avail: true},
		Fields:  []machine.Tag{{Name: "cut_length", Unit2: "mm"}, {Name: "quantity", Unit2: "pcs"}},
		BaseURL: srv.URL, APIKey: "x", Model: "test",
	})
	ctx := context.Background()
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	samples, err := d.Read(ctx, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := map[string]machine.Sample{}
	for _, s := range samples {
		got[s.Tag] = s
	}
	if got["cut_length"].Value != 1250 || got["cut_length"].Unit2 != "mm" {
		t.Errorf("cut_length: want 1250mm, got %v %q", got["cut_length"].Value, got["cut_length"].Unit2)
	}
	if got["quantity"].Value != 500 {
		t.Errorf("quantity: want 500, got %v", got["quantity"].Value)
	}
	st, _ := d.Status(ctx)
	if st.State != "running" {
		t.Errorf("status state: want running, got %q", st.State)
	}
	if !d.Capabilities().Has(machine.CapVision) {
		t.Error("vision driver must advertise CapVision")
	}
}

func TestVisionDriver_RefFilter(t *testing.T) {
	srv := fakeVLM(t, `{"state":"idle","fields":{"cut_length":1000,"quantity":50}}`)
	d := newVisionDriver(visionDriverConfig{
		Name: "v1", Camera: fakeCam{avail: true},
		Fields:  []machine.Tag{{Name: "cut_length", Unit2: "mm"}, {Name: "quantity"}},
		BaseURL: srv.URL,
	})
	samples, err := d.Read(context.Background(), []machine.TagRef{{Name: "quantity"}})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(samples) != 1 || samples[0].Tag != "quantity" {
		t.Fatalf("ref filter failed, got %+v", samples)
	}
}

func TestVisionDriver_NoControl(t *testing.T) {
	d := newVisionDriver(visionDriverConfig{Name: "v", Camera: fakeCam{avail: true}})
	if err := d.Write(context.Background(), nil); err != machine.ErrNotSupported {
		t.Errorf("vision Write must be ErrNotSupported, got %v", err)
	}
	if err := d.SubmitJob(context.Background(), machine.Job{}); err != machine.ErrNotSupported {
		t.Errorf("vision SubmitJob must be ErrNotSupported, got %v", err)
	}
}

func TestParseVisionVerdict_Fenced(t *testing.T) {
	state, fields, err := parseVisionVerdict("```json\n{\"state\":\"fault\",\"fields\":{\"count\":42}}\n```")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if state != "fault" || fields["count"] != 42 {
		t.Errorf("got state=%q fields=%v", state, fields)
	}
}
