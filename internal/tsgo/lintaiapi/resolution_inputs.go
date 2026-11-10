package lintaiapi

import (
	"strings"
	"sync"

	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs"
)

// resolutionInputFS records missing files and directories that the compiler
// probes while it resolves imports. When resolution finds no package
// directory, it probes no files under it, so the missing directory is the
// input whose creation can change the result.
type resolutionInputFS struct {
	vfs.FS
	workspaceRoot string
	mu            sync.Mutex
	missing       map[string]struct{}
}

func trackResolutionInputs(fs vfs.FS, workspaceRoot string) *resolutionInputFS {
	return &resolutionInputFS{FS: fs, workspaceRoot: normalizeSlash(workspaceRoot), missing: make(map[string]struct{})}
}

func (fs *resolutionInputFS) FileExists(path tspath.RootedFilePath) bool {
	exists := fs.FS.FileExists(path)
	if !exists {
		fs.recordMissingFile(path)
	}
	return exists
}

func (fs *resolutionInputFS) ReadFile(path tspath.RootedFilePath) (string, bool) {
	contents, exists := fs.FS.ReadFile(path)
	if !exists {
		fs.recordMissingFile(path)
	}
	return contents, exists
}

func (fs *resolutionInputFS) DirectoryExists(path tspath.RootedDirectoryPath) bool {
	exists := fs.FS.DirectoryExists(path)
	if !exists {
		value := normalizeSlash(path.AsString())
		if fs.tracked(value) && !fs.FS.FileExists(tspath.RootedFilePathFromAbsolute(value)) {
			fs.record(value)
		}
	}
	return exists
}

func (fs *resolutionInputFS) recordMissingFile(path tspath.RootedFilePath) {
	value := normalizeSlash(path.AsString())
	if fs.tracked(value) && !fs.FS.DirectoryExists(tspath.RootedDirectoryPathFromAbsolute(value)) {
		fs.record(value)
	}
}

func (fs *resolutionInputFS) tracked(value string) bool {
	if tspath.IsUrl(value) || strings.HasPrefix(value, "^/") || value == "" {
		return false
	}
	return isWithinRoot(fs.workspaceRoot, value) || strings.HasSuffix(value, "/node_modules") || strings.Contains(value, "/node_modules/")
}

func (fs *resolutionInputFS) record(value string) {
	fs.mu.Lock()
	fs.missing[value] = struct{}{}
	fs.mu.Unlock()
}

func (fs *resolutionInputFS) snapshotInputs() []SnapshotInput {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	result := make([]SnapshotInput, 0, len(fs.missing))
	for path := range fs.missing {
		result = append(result, SnapshotInput{Path: path, Exists: false})
	}
	return uniqueSnapshotInputs(result)
}
