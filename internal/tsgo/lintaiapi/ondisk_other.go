//go:build !darwin

package lintaiapi

func onDiskSpelling(path string) (string, error) { return path, nil }
