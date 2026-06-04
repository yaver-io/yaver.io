//go:build darwin && cgo

package ghost

// macOS accessibility tree via the AX API (AXUIElement). Walks the focused
// application's element tree reading role/title/value/bounds. Requires
// Accessibility permission for the host process. Pinned min-version so AX value
// constants stay available under newer SDKs. Best-effort; validate on-device.

/*
#cgo CFLAGS: -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <string.h>

// Returns the focused application's AXUIElement (retained; caller releases).
static void* axFocusedApp() {
    AXUIElementRef sys = AXUIElementCreateSystemWide();
    CFTypeRef app = NULL;
    AXUIElementCopyAttributeValue(sys, kAXFocusedApplicationAttribute, &app);
    CFRelease(sys);
    return (void*)app;
}

static char* cfStringDup(CFStringRef s) {
    if (!s) return NULL;
    CFIndex len = CFStringGetLength(s);
    CFIndex max = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
    char* buf = (char*)malloc(max);
    if (!buf) return NULL;
    if (!CFStringGetCString(s, buf, max, kCFStringEncodingUTF8)) { free(buf); return NULL; }
    return buf;
}

// attr: 0=role, 1=title, 2=value. Returns malloc'd UTF-8 (caller frees) or NULL.
static char* axGetString(void* el, int attr) {
    if (!el) return NULL;
    CFStringRef a = (attr == 0) ? kAXRoleAttribute : (attr == 1) ? kAXTitleAttribute : kAXValueAttribute;
    CFTypeRef v = NULL;
    if (AXUIElementCopyAttributeValue((AXUIElementRef)el, a, &v) != kAXErrorSuccess || !v) return NULL;
    char* out = NULL;
    if (CFGetTypeID(v) == CFStringGetTypeID()) out = cfStringDup((CFStringRef)v);
    CFRelease(v);
    return out;
}

static void axGetRect(void* el, int* x, int* y, int* w, int* h) {
    *x = 0; *y = 0; *w = 0; *h = 0;
    if (!el) return;
    CFTypeRef pos = NULL, sz = NULL;
    if (AXUIElementCopyAttributeValue((AXUIElementRef)el, kAXPositionAttribute, &pos) == kAXErrorSuccess && pos) {
        CGPoint p;
        if (AXValueGetValue((AXValueRef)pos, kAXValueCGPointType, &p)) { *x = (int)p.x; *y = (int)p.y; }
        CFRelease(pos);
    }
    if (AXUIElementCopyAttributeValue((AXUIElementRef)el, kAXSizeAttribute, &sz) == kAXErrorSuccess && sz) {
        CGSize s;
        if (AXValueGetValue((AXValueRef)sz, kAXValueCGSizeType, &s)) { *w = (int)s.width; *h = (int)s.height; }
        CFRelease(sz);
    }
}

static int axChildCount(void* el) {
    if (!el) return 0;
    CFTypeRef ch = NULL;
    if (AXUIElementCopyAttributeValue((AXUIElementRef)el, kAXChildrenAttribute, &ch) != kAXErrorSuccess || !ch) return 0;
    int n = (int)CFArrayGetCount((CFArrayRef)ch);
    CFRelease(ch);
    return n;
}

// Returns the i-th child (retained; caller releases) or NULL.
static void* axChild(void* el, int i) {
    if (!el) return NULL;
    CFTypeRef ch = NULL;
    if (AXUIElementCopyAttributeValue((AXUIElementRef)el, kAXChildrenAttribute, &ch) != kAXErrorSuccess || !ch) return NULL;
    void* out = NULL;
    if (i >= 0 && i < CFArrayGetCount((CFArrayRef)ch)) {
        AXUIElementRef c = (AXUIElementRef)CFArrayGetValueAtIndex((CFArrayRef)ch, i);
        CFRetain(c);
        out = (void*)c;
    }
    CFRelease(ch);
    return out;
}

static void axRelease(void* el) { if (el) CFRelease((CFTypeRef)el); }
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

type macTree struct{}

func newTree() Tree { return macTree{} }

func goStrFree(c *C.char) string {
	if c == nil {
		return ""
	}
	s := C.GoString(c)
	C.free(unsafe.Pointer(c))
	return s
}

func walkAX(el unsafe.Pointer, depth int) Node {
	var x, y, w, h C.int
	C.axGetRect(el, &x, &y, &w, &h)
	n := Node{
		Role:  goStrFree(C.axGetString(el, 0)),
		Name:  goStrFree(C.axGetString(el, 1)),
		Value: goStrFree(C.axGetString(el, 2)),
		X:     int(x), Y: int(y), Width: int(w), Height: int(h),
	}
	if depth < 6 {
		count := int(C.axChildCount(el))
		if count > 40 {
			count = 40
		}
		for i := 0; i < count; i++ {
			c := C.axChild(el, C.int(i))
			if c != nil {
				n.Children = append(n.Children, walkAX(c, depth+1))
				C.axRelease(c)
			}
		}
	}
	return n
}

func (macTree) ElementTree(window string) (Node, error) {
	app := C.axFocusedApp()
	if app == nil {
		return Node{}, fmt.Errorf("ghost: no focused application (grant Accessibility permission to the host)")
	}
	defer C.axRelease(app)
	return walkAX(app, 0), nil
}

func (macTree) Windows() ([]Node, error) {
	app := C.axFocusedApp()
	if app == nil {
		return nil, ErrUnsupported
	}
	defer C.axRelease(app)
	root := walkAX(app, 1) // app + its windows (depth-bounded)
	return root.Children, nil
}

func (t macTree) Find(query string) (*Node, error) {
	root, err := t.ElementTree("")
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var hit *Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if hit != nil || n == nil {
			return
		}
		if strings.Contains(strings.ToLower(n.Name), q) || strings.Contains(strings.ToLower(n.Value), q) {
			hit = n
			return
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	walk(&root)
	if hit == nil {
		return nil, fmt.Errorf("ghost: no element matching %q", query)
	}
	return hit, nil
}
