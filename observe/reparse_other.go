//go:build !windows

package observe

func isReparsePoint(string) bool { return false }
