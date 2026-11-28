//go:build !darwin

package workspace

func onDiskSpelling(path string) (string, error) { return path, nil }
