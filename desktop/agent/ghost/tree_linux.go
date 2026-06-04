//go:build linux

package ghost

// Linux accessibility tree (AT-SPI over D-Bus) is not yet implemented; the
// vision-first ghost works without it. Returns the stub (ErrUnsupported).
// TODO: AT-SPI via godbus (org.a11y.Bus) for selector robustness.

func newTree() Tree { return stubTree{} }
