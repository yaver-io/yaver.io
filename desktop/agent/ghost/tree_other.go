//go:build !windows && !linux && (!darwin || !cgo)

package ghost

func newTree() Tree { return stubTree{} }
