//go:build darwin && cgo

package ghost

// macOS accessibility tree (AXUIElement) is not yet implemented — the slave
// target is Windows (UIAutomation, see tree_windows.go) and the vision-first
// ghost works without a tree on macOS. Returns the stub (ErrUnsupported).
// TODO: AX via cgo (AXUIElementCopyAttributeValue) if a macOS ERP needs it.

func newTree() Tree { return stubTree{} }
