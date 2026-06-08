package studio

import "testing"

func TestRedBoxOracleLogcat(t *testing.T) {
	o := newSeenOracle(&RedBoxOracle{})
	f := Frame{Step: 3, ShotIdx: 2, Logcat: "I ReactNativeJS: running\n" +
		"E ReactNativeJS: Error: Cannot read property 'map' of undefined\n" +
		"E AndroidRuntime: Unhandled JS Exception: TypeError"}
	bugs := o.Scan(f)
	if len(bugs) == 0 {
		t.Fatalf("expected red-box bugs, got none")
	}
	for _, b := range bugs {
		if b.Severity != "high" || b.Step != 3 || b.ShotIdx != 2 {
			t.Errorf("bug fields wrong: %+v", b)
		}
	}
	// Dedup: same frame again yields nothing new.
	if again := o.Scan(f); len(again) != 0 {
		t.Errorf("expected dedup, got %d repeats", len(again))
	}
}

func TestRedBoxOracleOnScreen(t *testing.T) {
	o := &RedBoxOracle{}
	f := Frame{ViewTree: `<hierarchy><node text="RedBox" class="com.facebook.react.devsupport"/></hierarchy>`}
	bugs := o.Scan(f)
	if len(bugs) != 1 || bugs[0].Severity != "critical" {
		t.Fatalf("expected one critical on-screen redbox, got %+v", bugs)
	}
}

func TestCrashOracle(t *testing.T) {
	o := &CrashOracle{}
	f := Frame{Logcat: "E AndroidRuntime: FATAL EXCEPTION: main\n" +
		"E AndroidRuntime: java.lang.NullPointerException"}
	bugs := o.Scan(f)
	if len(bugs) == 0 {
		t.Fatalf("expected crash bug")
	}
	gotCritical := false
	for _, b := range bugs {
		if b.Severity == "critical" {
			gotCritical = true
		}
	}
	if !gotCritical {
		t.Errorf("FATAL EXCEPTION should be critical; got %+v", bugs)
	}
}

func TestCrashOracleANR(t *testing.T) {
	o := &CrashOracle{}
	f := Frame{Logcat: "I ActivityManager: ANR in io.yaver.mobile (io.yaver.mobile/.MainActivity)"}
	bugs := o.Scan(f)
	if len(bugs) != 1 || bugs[0].Severity != "high" {
		t.Fatalf("expected one high ANR bug, got %+v", bugs)
	}
}

func TestBlankScreenOracle(t *testing.T) {
	o := &BlankScreenOracle{}
	// Sparse hierarchy, no text → blank.
	blank := Frame{ViewTree: `<hierarchy rotation="0"><node class="android.widget.FrameLayout" bounds="[0,0][1080,2340]"/></hierarchy>`}
	if bugs := o.Scan(blank); len(bugs) != 1 || bugs[0].Severity != "medium" {
		t.Fatalf("expected one medium blank-screen bug, got %+v", bugs)
	}
	// Real screen with content → no bug.
	full := Frame{ViewTree: `<hierarchy><node class="a"/><node class="b"/><node text="Welcome back" class="c"/><node text="Continue" class="d"/></hierarchy>`}
	if bugs := o.Scan(full); len(bugs) != 0 {
		t.Errorf("content screen should not flag blank; got %+v", bugs)
	}
	// No dump available → no false positive.
	if bugs := o.Scan(Frame{ViewTree: ""}); len(bugs) != 0 {
		t.Errorf("empty dump must not flag; got %+v", bugs)
	}
}

func TestScanAllAndDefaultOracles(t *testing.T) {
	oracles := DefaultOracles()
	f := Frame{Step: 1, Logcat: "E AndroidRuntime: FATAL EXCEPTION: main",
		ViewTree: `<hierarchy><node text="Home"/><node text="x"/><node text="y"/></hierarchy>`}
	bugs := ScanAll(oracles, f)
	if len(bugs) == 0 {
		t.Fatalf("expected crash caught by the bank")
	}
	// Re-scan the same frame: deduped to zero.
	if again := ScanAll(oracles, f); len(again) != 0 {
		t.Errorf("bank should dedup; got %d", len(again))
	}
}
