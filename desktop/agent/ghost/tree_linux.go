//go:build linux

package ghost

// Linux accessibility tree via AT-SPI2, driven through a short python3 + pyatspi
// script (mirrors the Windows PowerShell approach) — pure os/exec + JSON, CGO
// off. Needs python3-pyatspi (gir1.2-atspi-2.0) on the box (the RPi appliance
// installs it). Gives the ghost robust role/name/bounds selectors alongside the
// vision path. Best-effort; validate on-device.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type atspiTree struct{}

func newTree() Tree { return atspiTree{} }

func runPython(script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ghost: AT-SPI python failed (need python3-pyatspi): %w", err)
	}
	return out, nil
}

const pyActiveTree = `
import sys, json
try:
    import pyatspi
except Exception as e:
    print("null"); sys.exit(0)
def role(o):
    try: return o.getRoleName()
    except: return ""
def rect(o):
    try:
        c=o.queryComponent(); ext=c.getExtents(pyatspi.DESKTOP_COORDS)
        return int(ext.x),int(ext.y),int(ext.width),int(ext.height)
    except: return 0,0,0,0
def node(o,d):
    if o is None or d>5: return None
    x,y,w,h=rect(o)
    out={"role":role(o),"name":(o.name or ""),"x":x,"y":y,"width":w,"height":h,"children":[]}
    try:
        n=min(o.childCount,40)
        for i in range(n):
            cn=node(o.getChildAtIndex(i),d+1)
            if cn: out["children"].append(cn)
    except: pass
    return out
target=None
try:
    desktop=pyatspi.Registry.getDesktop(0)
    for app in desktop:
        try:
            for win in app:
                if win.getState().contains(pyatspi.STATE_ACTIVE):
                    target=win; break
        except: pass
        if target: break
    if target is None:
        try: target=desktop[0][0]
        except: target=None
except: pass
print(json.dumps(node(target,0) if target else None))
`

const pyWindowsList = `
import sys, json
try:
    import pyatspi
except Exception as e:
    print("[]"); sys.exit(0)
def rect(o):
    try:
        c=o.queryComponent(); ext=c.getExtents(pyatspi.DESKTOP_COORDS)
        return int(ext.x),int(ext.y),int(ext.width),int(ext.height)
    except: return 0,0,0,0
out=[]
try:
    desktop=pyatspi.Registry.getDesktop(0)
    for app in desktop:
        try:
            for win in app:
                x,y,w,h=rect(win)
                out.append({"role":(win.getRoleName() if hasattr(win,'getRoleName') else ""),"name":(win.name or ""),"x":x,"y":y,"width":w,"height":h})
        except: pass
except: pass
print(json.dumps(out))
`

func parseNodesLinux(raw []byte) ([]Node, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil, nil
	}
	var arr []Node
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, fmt.Errorf("ghost: parse AT-SPI windows: %w", err)
	}
	return arr, nil
}

func (atspiTree) Windows() ([]Node, error) {
	out, err := runPython(pyWindowsList)
	if err != nil {
		return nil, err
	}
	return parseNodesLinux(out)
}

func (atspiTree) ElementTree(window string) (Node, error) {
	out, err := runPython(pyActiveTree)
	if err != nil {
		return Node{}, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" || s == "null" {
		return Node{}, ErrUnsupported
	}
	var n Node
	if err := json.Unmarshal([]byte(s), &n); err != nil {
		return Node{}, fmt.Errorf("ghost: parse AT-SPI tree: %w", err)
	}
	return n, nil
}

func (t atspiTree) Find(query string) (*Node, error) {
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
		if strings.Contains(strings.ToLower(n.Name), q) {
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
