package main

import "testing"

func TestAppleLocalizationAttributes(t *testing.T) {
	l := StoreListing{
		Description: "A great app.",
		Keywords:    []string{"receipt", "expense"},
		WhatsNew:    "v1",
	}
	a := appleLocalizationAttributes(l)
	if a["description"] != "A great app." {
		t.Errorf("description = %v", a["description"])
	}
	if a["keywords"] != "receipt,expense" {
		t.Errorf("keywords should be comma-joined, got %v", a["keywords"])
	}
	if a["whatsNew"] != "v1" {
		t.Errorf("whatsNew = %v", a["whatsNew"])
	}
	// Empty fields are omitted (don't clobber the store with blanks).
	if _, ok := a["marketingUrl"]; ok {
		t.Error("empty marketingUrl should be omitted")
	}
}

func TestGoogleListingBody(t *testing.T) {
	l := StoreListing{AppName: "Receipts", Subtitle: "Track spend", Description: "full"}
	b := googleListingBody(l, "en-US")
	if b["language"] != "en-US" || b["title"] != "Receipts" || b["shortDescription"] != "Track spend" || b["fullDescription"] != "full" {
		t.Errorf("body wrong: %v", b)
	}
}

func TestPickEditableVersion(t *testing.T) {
	// A live/review version should be skipped in favour of the editable one.
	id, err := pickEditableVersion([]ascVersion{
		{ID: "v1", State: "READY_FOR_SALE"},
		{ID: "v2", State: "PREPARE_FOR_SUBMISSION"},
	})
	if err != nil || id != "v2" {
		t.Errorf("should pick the editable version v2, got %q err=%v", id, err)
	}
	// Nothing editable ⇒ a clear error (don't touch a submitted version).
	if _, err := pickEditableVersion([]ascVersion{{ID: "v1", State: "WAITING_FOR_REVIEW"}}); err == nil {
		t.Error("no editable version should error")
	}
}

func TestAppleLocalizationAttributesEmpty(t *testing.T) {
	// An empty listing produces no attributes (caller refuses to write).
	if len(appleLocalizationAttributes(StoreListing{})) != 0 {
		t.Error("empty listing should yield no attributes")
	}
}
