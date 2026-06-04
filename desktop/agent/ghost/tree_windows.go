//go:build windows

package ghost

// Windows accessibility tree via UI Automation, driven through a short
// PowerShell script (System.Windows.Automation). This avoids fragile raw-COM
// vtable marshalling — it's pure os/exec + JSON, so it stays CGO_ENABLED=0 and
// cross-compiles cleanly. Best-effort; validate on-device. Gives the ghost
// robust element selectors (name / automationId / bounds) to complement the
// vision path. Bounded depth/breadth to keep it fast.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type winTree struct{}

func newTree() Tree { return winTree{} }

func runPowerShell(script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ghost: UIAutomation PowerShell failed: %w", err)
	}
	return out, nil
}

const psFocusedTree = `
$ErrorActionPreference='SilentlyContinue'
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$ae=[System.Windows.Automation.AutomationElement]
$root=$ae::FocusedElement
if(-not $root){$root=$ae::RootElement}
$walker=[System.Windows.Automation.TreeWalker]::ControlViewWalker
function Node($e,$d){
  if(-not $e -or $d -gt 5){return $null}
  try{$i=$e.Current}catch{return $null}
  $r=$i.BoundingRectangle
  $o=[ordered]@{role=($i.ControlType.ProgrammaticName -replace 'ControlType\.','');name=$i.Name;automationId=$i.AutomationId;x=[int]$r.X;y=[int]$r.Y;width=[int]$r.Width;height=[int]$r.Height;children=@()}
  try{$c=$walker.GetFirstChild($e)}catch{$c=$null}
  $n=0
  while($c -and $n -lt 40){
    $cn=Node $c ($d+1)
    if($cn){$o.children+=$cn}
    try{$c=$walker.GetNextSibling($c)}catch{$c=$null}
    $n++
  }
  return $o
}
Node $root 0 | ConvertTo-Json -Depth 9 -Compress
`

const psWindowsList = `
$ErrorActionPreference='SilentlyContinue'
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$ae=[System.Windows.Automation.AutomationElement]
$root=$ae::RootElement
$walker=[System.Windows.Automation.TreeWalker]::ControlViewWalker
$out=@()
try{$c=$walker.GetFirstChild($root)}catch{$c=$null}
$n=0
while($c -and $n -lt 100){
  try{$i=$c.Current}catch{$i=$null}
  if($i -and $i.Name){
    $r=$i.BoundingRectangle
    $out+=[ordered]@{role=($i.ControlType.ProgrammaticName -replace 'ControlType\.','');name=$i.Name;automationId=$i.AutomationId;x=[int]$r.X;y=[int]$r.Y;width=[int]$r.Width;height=[int]$r.Height}
  }
  try{$c=$walker.GetNextSibling($c)}catch{$c=$null}
  $n++
}
ConvertTo-Json @($out) -Depth 3 -Compress
`

// parseNodes tolerates PowerShell collapsing a single-element array to an object.
func parseNodes(raw []byte) ([]Node, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil, nil
	}
	var arr []Node
	if err := json.Unmarshal([]byte(s), &arr); err == nil {
		return arr, nil
	}
	var one Node
	if err := json.Unmarshal([]byte(s), &one); err != nil {
		return nil, fmt.Errorf("ghost: parse UIA windows: %w", err)
	}
	return []Node{one}, nil
}

func (winTree) Windows() ([]Node, error) {
	out, err := runPowerShell(psWindowsList)
	if err != nil {
		return nil, err
	}
	return parseNodes(out)
}

func (winTree) ElementTree(window string) (Node, error) {
	out, err := runPowerShell(psFocusedTree)
	if err != nil {
		return Node{}, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" || s == "null" {
		return Node{}, ErrUnsupported
	}
	var n Node
	if err := json.Unmarshal([]byte(s), &n); err != nil {
		return Node{}, fmt.Errorf("ghost: parse UIA tree: %w", err)
	}
	return n, nil
}

func (t winTree) Find(query string) (*Node, error) {
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
		if strings.Contains(strings.ToLower(n.Name), q) || strings.Contains(strings.ToLower(n.AutomationID), q) {
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
