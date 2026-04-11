package testkit

import "testing"

// Small, real-shaped UIAutomator dump with an RN login screen. The
// XML matches what `adb shell uiautomator dump /dev/tty` produces on
// a Pixel 7 emulator running a standard Expo app.
const fakeUIDump = `<?xml version="1.0" encoding="UTF-8"?>
<hierarchy rotation="0">
  <node class="android.widget.FrameLayout" bounds="[0,0][1080,2400]">
    <node class="android.view.View" bounds="[0,100][1080,200]" text="Yaver" />
    <node class="android.widget.EditText" resource-id="io.yaver.mobile:id/email" content-desc="email-input" bounds="[40,300][1040,400]" />
    <node class="android.widget.EditText" resource-id="io.yaver.mobile:id/password" content-desc="password-input" bounds="[40,420][1040,520]" />
    <node class="android.widget.Button" resource-id="io.yaver.mobile:id/submit" content-desc="submit-button" text="Sign In" bounds="[40,600][1040,700]" />
    <node class="android.widget.TextView" content-desc="help" text="Don't have an account?" bounds="[40,800][1040,860]" />
  </node>
</hierarchy>`

func TestParseAndroidSelector(t *testing.T) {
	cases := []struct {
		raw  string
		kind string
		val  string
	}{
		{"text=Sign In", "text", "Sign In"},
		{"testID=submit", "testID", "submit"},
		{"id=submit", "id", "submit"},
		{"class=android.widget.Button", "class", "android.widget.Button"},
		{"no-kind", "text", "no-kind"},
		{"desc=email", "desc", "email"},
	}
	for _, tc := range cases {
		got := ParseAndroidSelector(tc.raw)
		if got.Kind != tc.kind || got.Value != tc.val {
			t.Errorf("ParseAndroidSelector(%q) = %+v, want kind=%s val=%s",
				tc.raw, got, tc.kind, tc.val)
		}
	}
}

func TestFindAndroidNodeByText(t *testing.T) {
	x, y, err := FindAndroidNode([]byte(fakeUIDump), AndroidSelector{Kind: "text", Value: "Sign In"})
	if err != nil {
		t.Fatalf("FindAndroidNode: %v", err)
	}
	// Center of [40,600][1040,700] = (540, 650)
	if x != 540 || y != 650 {
		t.Errorf("got (%d, %d), want (540, 650)", x, y)
	}
}

func TestFindAndroidNodeByTestID(t *testing.T) {
	x, y, err := FindAndroidNode([]byte(fakeUIDump), AndroidSelector{Kind: "testID", Value: "submit-button"})
	if err != nil {
		t.Fatalf("FindAndroidNode: %v", err)
	}
	if x != 540 || y != 650 {
		t.Errorf("got (%d, %d), want (540, 650)", x, y)
	}
}

func TestFindAndroidNodeByResourceID(t *testing.T) {
	// Short form.
	x, y, err := FindAndroidNode([]byte(fakeUIDump), AndroidSelector{Kind: "id", Value: "email"})
	if err != nil {
		t.Fatalf("FindAndroidNode: %v", err)
	}
	// Center of [40,300][1040,400] = (540, 350)
	if x != 540 || y != 350 {
		t.Errorf("got (%d, %d), want (540, 350)", x, y)
	}
	// Fully qualified form too.
	_, _, err = FindAndroidNode([]byte(fakeUIDump), AndroidSelector{Kind: "id", Value: "io.yaver.mobile:id/password"})
	if err != nil {
		t.Errorf("fully qualified id lookup failed: %v", err)
	}
}

func TestFindAndroidNodeByClass(t *testing.T) {
	_, _, err := FindAndroidNode([]byte(fakeUIDump), AndroidSelector{Kind: "class", Value: "Button"})
	if err != nil {
		t.Errorf("class suffix match failed: %v", err)
	}
	_, _, err = FindAndroidNode([]byte(fakeUIDump), AndroidSelector{Kind: "class", Value: "android.widget.Button"})
	if err != nil {
		t.Errorf("class full match failed: %v", err)
	}
}

func TestFindAndroidNodePartialText(t *testing.T) {
	// "Don't have an account?" should match a partial text query.
	_, _, err := FindAndroidNode([]byte(fakeUIDump), AndroidSelector{Kind: "text", Value: "account"})
	if err != nil {
		t.Errorf("partial text match failed: %v", err)
	}
}

func TestFindAndroidNodeNotFound(t *testing.T) {
	_, _, err := FindAndroidNode([]byte(fakeUIDump), AndroidSelector{Kind: "text", Value: "does-not-exist"})
	if err == nil {
		t.Error("expected error for missing selector")
	}
}

func TestParseBoundsCenter(t *testing.T) {
	x, y, err := parseBoundsCenter("[0,0][1080,120]")
	if err != nil {
		t.Fatal(err)
	}
	if x != 540 || y != 60 {
		t.Errorf("got (%d, %d), want (540, 60)", x, y)
	}
	_, _, err = parseBoundsCenter("invalid")
	if err == nil {
		t.Error("expected error for invalid bounds")
	}
}
