package ghost

// Shared accessibility-tree stub. Per-OS implementations live in
// tree_windows.go (UIAutomation via PowerShell), tree_darwin.go (AX via cgo),
// and stubs in tree_linux.go / tree_other.go. newTree() is defined per build
// tag. Callers feature-detect by checking for ErrUnsupported.

type stubTree struct{}

func (stubTree) Windows() ([]Node, error)                { return nil, ErrUnsupported }
func (stubTree) ElementTree(window string) (Node, error) { return Node{}, ErrUnsupported }
func (stubTree) Find(query string) (*Node, error)        { return nil, ErrUnsupported }
