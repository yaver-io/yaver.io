package ghost

// Phase 1 ships a stub accessibility Tree on every OS. Phase 2 replaces
// newTree with build-tagged implementations (Windows UIAutomation, macOS AX,
// Linux AT-SPI). Callers feature-detect by checking for ErrUnsupported.

type stubTree struct{}

func newTree() Tree { return stubTree{} }

func (stubTree) Windows() ([]Node, error)                { return nil, ErrUnsupported }
func (stubTree) ElementTree(window string) (Node, error) { return Node{}, ErrUnsupported }
func (stubTree) Find(query string) (*Node, error)        { return nil, ErrUnsupported }
