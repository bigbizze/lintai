package workspace

import "path/filepath"

// PhysicalPath resolves every link in path and returns the canonical physical
// path of the existing file it names. Where the platform reports the on-disk
// spelling of each name, as on macOS, that spelling replaces the given one, so
// two spellings of one file on a case-insensitive volume resolve to one path.
// An error means the identity of the file could not be established.
func PhysicalPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return onDiskSpelling(resolved)
}
