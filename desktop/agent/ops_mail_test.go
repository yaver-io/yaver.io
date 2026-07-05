package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMailOpsRegistered(t *testing.T) {
	want := map[string]bool{
		"mail_search": false,
		"mail_unread": false,
		"mail_send":   false,
	}
	for _, v := range listOpsVerbs() {
		if _, ok := want[v.Name]; ok {
			want[v.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("missing ops verb %s", name)
		}
	}
}

func TestMailSendDefaultsToDryRun(t *testing.T) {
	payload, _ := json.Marshal(mailSendPayload{
		To:      []string{"person@example.com"},
		Subject: "Status",
		Body:    "Running five minutes late.",
		Surface: "carplay",
	})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "mail_send",
		Payload: payload,
	})
	if !res.OK {
		t.Fatalf("mail_send dry-run failed: %#v", res)
	}
	initial, ok := res.Initial.(map[string]interface{})
	if !ok || initial["dryRun"] != true {
		t.Fatalf("expected dryRun initial, got %#v", res.Initial)
	}
}

func TestMailSendExecuteRequiresConfirm(t *testing.T) {
	payload, _ := json.Marshal(mailSendPayload{
		To:      []string{"person@example.com"},
		Subject: "Status",
		Body:    "Running five minutes late.",
		Execute: true,
	})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "mail_send",
		Payload: payload,
	})
	if res.OK || res.Code != "confirm_required" {
		t.Fatalf("expected confirm_required, got %#v", res)
	}
}
