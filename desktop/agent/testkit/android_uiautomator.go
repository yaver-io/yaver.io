package testkit

import (
	"context"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// Android UIAutomator selector → coordinate resolution.
//
// `adb shell uiautomator dump` produces an XML of the current screen's
// view hierarchy. Every node has bounds, text, content-desc,
// resource-id, and class. We parse that, walk it, find the element
// matching a simple selector, and hand the center point back to the
// existing Tap() / Text() helpers in driver_androidemu.go.
//
// This is what makes `target: android-emu` (and `target: device`
// with platform=android) usable for selector-based specs without a
// separate Appium / UIAutomator2 server process. We deliberately
// keep the query vocabulary small so RN devs can use the same
// `click:` / `fill:` steps they already use for web:
//
//   click: 'text=Sign In'              ← UI node with text "Sign In"
//   click: 'testID=submit-button'      ← RN accessibilityLabel / testID
//   click: 'id=submit'                 ← resource-id "...submit"
//   click: 'class=android.widget.EditText'
//
// RN's `testID` prop maps to UIAutomator's content-desc on Android
// by default, so the second form covers almost every React Native
// flow the solo dev cares about.

// AndroidSelector is a parsed query.
type AndroidSelector struct {
	Kind  string // "text" | "testID" | "id" | "class" | "desc"
	Value string
}

// ParseAndroidSelector splits a "kind=value" selector into parts.
// Missing kind defaults to "text" so bare strings work naturally.
func ParseAndroidSelector(raw string) AndroidSelector {
	idx := strings.Index(raw, "=")
	if idx <= 0 {
		return AndroidSelector{Kind: "text", Value: raw}
	}
	return AndroidSelector{
		Kind:  strings.TrimSpace(raw[:idx]),
		Value: strings.TrimSpace(raw[idx+1:]),
	}
}

// uiautomatorNode matches the shape `uiautomator dump` produces.
type uiautomatorNode struct {
	XMLName     xml.Name          `xml:"node"`
	Text        string            `xml:"text,attr"`
	ResourceID  string            `xml:"resource-id,attr"`
	Class       string            `xml:"class,attr"`
	Package     string            `xml:"package,attr"`
	ContentDesc string            `xml:"content-desc,attr"`
	Bounds      string            `xml:"bounds,attr"`
	Children    []uiautomatorNode `xml:"node"`
}

type uiautomatorRoot struct {
	XMLName  xml.Name          `xml:"hierarchy"`
	Children []uiautomatorNode `xml:"node"`
}

// DumpAndroidUI runs `adb shell uiautomator dump` and pulls the XML
// back. Returns the raw bytes so tests can feed a fixture in.
func (d *AndroidEmuDriver) DumpAndroidUI(ctx context.Context, deviceID string) ([]byte, error) {
	// uiautomator dumps to a file on /sdcard; the /dev/tty variant
	// prints the file path, not the XML. Use the newer shell form
	// that writes to stdout:
	out, err := runCtx(ctx, "adb", "-s", deviceID, "shell",
		"uiautomator dump /dev/tty")
	if err != nil {
		return nil, fmt.Errorf("uiautomator dump: %w", err)
	}
	// The command prints a trailing "UI hierchary dumped to: ..."
	// line after the XML; trim it off if present.
	if i := strings.LastIndex(out, "</hierarchy>"); i != -1 {
		return []byte(out[:i+len("</hierarchy>")]), nil
	}
	return []byte(out), nil
}

// FindAndroidNode walks the XML tree looking for the first node that
// matches `sel`. Returns the center-point coordinates of its bounds
// so Tap() can use them directly.
//
// Callers that want the raw node for assertion (assert.visible,
// assert.text) can use FindAndroidNodeDetails instead.
func FindAndroidNode(xmlBytes []byte, sel AndroidSelector) (x, y int, err error) {
	node, err := FindAndroidNodeDetails(xmlBytes, sel)
	if err != nil {
		return 0, 0, err
	}
	return parseBoundsCenter(node.Bounds)
}

// FindAndroidNodeDetails returns the matched node itself.
func FindAndroidNodeDetails(xmlBytes []byte, sel AndroidSelector) (*uiautomatorNode, error) {
	var root uiautomatorRoot
	if err := xml.Unmarshal(xmlBytes, &root); err != nil {
		return nil, fmt.Errorf("parse ui dump: %w", err)
	}
	return walkAndroidNodes(root.Children, sel)
}

func walkAndroidNodes(nodes []uiautomatorNode, sel AndroidSelector) (*uiautomatorNode, error) {
	for i := range nodes {
		n := &nodes[i]
		if matchesAndroidNode(n, sel) {
			return n, nil
		}
		if len(n.Children) > 0 {
			if got, err := walkAndroidNodes(n.Children, sel); err == nil {
				return got, nil
			}
		}
	}
	return nil, fmt.Errorf("no android node matched %s=%q", sel.Kind, sel.Value)
}

// matchesAndroidNode is the selector matching policy. Partial
// matches on text/content-desc mirror what Appium's find-by-text does;
// exact match on resource-id / class is what the dev usually wants.
func matchesAndroidNode(n *uiautomatorNode, sel AndroidSelector) bool {
	v := sel.Value
	switch sel.Kind {
	case "text":
		// Accept an exact match first; fall back to contains() for
		// dynamic labels ("Signed in as Foo").
		if n.Text == v {
			return true
		}
		return v != "" && strings.Contains(n.Text, v)
	case "testID", "desc":
		// RN's testID is rendered as content-desc on Android.
		return n.ContentDesc == v || (v != "" && strings.Contains(n.ContentDesc, v))
	case "id", "resource-id":
		// Accept "submit" or "com.foo.app:id/submit".
		if n.ResourceID == v {
			return true
		}
		if strings.HasSuffix(n.ResourceID, "/"+v) {
			return true
		}
		return false
	case "class":
		if n.Class == v {
			return true
		}
		return strings.HasSuffix(n.Class, "."+v)
	}
	return false
}

// parseBoundsCenter turns "[left,top][right,bottom]" into (cx, cy).
func parseBoundsCenter(b string) (int, int, error) {
	// "[0,0][1080,120]"
	if !strings.HasPrefix(b, "[") || !strings.Contains(b, "][") {
		return 0, 0, fmt.Errorf("bad bounds: %q", b)
	}
	trimmed := strings.ReplaceAll(strings.ReplaceAll(b, "[", ""), "]", " ")
	fields := strings.Fields(trimmed)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("bad bounds: %q", b)
	}
	parse := func(s string) (int, int, error) {
		parts := strings.Split(s, ",")
		if len(parts) != 2 {
			return 0, 0, fmt.Errorf("bad point: %q", s)
		}
		a, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, err
		}
		b, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, err
		}
		return a, b, nil
	}
	l, t, err := parse(fields[0])
	if err != nil {
		return 0, 0, err
	}
	r, bt, err := parse(fields[1])
	if err != nil {
		return 0, 0, err
	}
	return (l + r) / 2, (t + bt) / 2, nil
}

// TapBySelector is the convenience wrapper the runner calls when it
// sees a `click:` step on an `android-emu` or android `device`
// target. Dumps the UI, resolves the selector, taps the center point.
func (d *AndroidEmuDriver) TapBySelector(ctx context.Context, deviceID, selector string) error {
	xmlBytes, err := d.DumpAndroidUI(ctx, deviceID)
	if err != nil {
		return err
	}
	sel := ParseAndroidSelector(selector)
	x, y, err := FindAndroidNode(xmlBytes, sel)
	if err != nil {
		return err
	}
	return d.Tap(ctx, deviceID, x, y)
}

// FillBySelector finds an EditText-like field and types into it. We
// tap the center first (to focus), then use `adb shell input text`.
func (d *AndroidEmuDriver) FillBySelector(ctx context.Context, deviceID, selector, text string) error {
	if err := d.TapBySelector(ctx, deviceID, selector); err != nil {
		return err
	}
	return d.Text(ctx, deviceID, text)
}

// AssertVisibleBySelector checks that the selector resolves. Returns
// nil if the node exists in the current dump, error otherwise.
func (d *AndroidEmuDriver) AssertVisibleBySelector(ctx context.Context, deviceID, selector string) error {
	xmlBytes, err := d.DumpAndroidUI(ctx, deviceID)
	if err != nil {
		return err
	}
	sel := ParseAndroidSelector(selector)
	if _, err := FindAndroidNodeDetails(xmlBytes, sel); err != nil {
		return err
	}
	return nil
}
