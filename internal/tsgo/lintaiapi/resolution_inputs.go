package lintaiapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs"
)

type capturedFile struct {
	contents string
	exists   bool
	failure  string
}

type capturedDirectory struct {
	files       []string
	directories []string
	symlinks    []string
}

type validationStats struct {
	files   int
	bytes   int64
	retries int
	elapsed time.Duration
}

// resolutionInputFS captures the values the compiler actually reads and
// probes. It memoizes within one collection attempt so repeated reads see one
// generation. Bundled lib files are covered by the implementation stamp.
type resolutionInputFS struct {
	vfs.FS
	workspaceRoot string
	mu            sync.Mutex
	reads         map[string]capturedFile
	fileProbes    map[string]bool
	dirProbes     map[string]bool
	stats         map[string]string
	statInfo      map[string]vfs.FileInfo
	directories   map[string]capturedDirectory
	realpaths     map[string]string
	captureErr    error
}

func trackResolutionInputs(fs vfs.FS, workspaceRoot string) *resolutionInputFS {
	return &resolutionInputFS{
		FS: fs, workspaceRoot: normalizeSlash(workspaceRoot),
		reads: make(map[string]capturedFile), fileProbes: make(map[string]bool),
		dirProbes: make(map[string]bool), stats: make(map[string]string), statInfo: make(map[string]vfs.FileInfo),
		directories: make(map[string]capturedDirectory), realpaths: make(map[string]string),
	}
}

func (fsys *resolutionInputFS) FileExists(path tspath.RootedFilePath) bool {
	key := fsys.inputPath(path.AsString())
	if fsys.skipInput(key) {
		return fsys.FS.FileExists(path)
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	if exists, ok := fsys.fileProbes[key]; ok {
		return exists
	}
	exists := fsys.FS.FileExists(path)
	fsys.fileProbes[key] = exists
	return exists
}

func (fsys *resolutionInputFS) ReadFile(path tspath.RootedFilePath) (string, bool) {
	key := fsys.inputPath(path.AsString())
	if fsys.skipInput(key) {
		return fsys.FS.ReadFile(path)
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	if captured, ok := fsys.reads[key]; ok {
		return captured.contents, captured.exists
	}
	contents, exists := fsys.FS.ReadFile(path)
	captured := capturedFile{contents: contents, exists: exists}
	_, fileProbed := fsys.fileProbes[key]
	if !fileProbed {
		fsys.fileProbes[key] = exists
	}
	if !exists {
		if _, err := os.Stat(filepath.FromSlash(key)); err != nil && !os.IsNotExist(err) {
			fsys.captureErr = fmt.Errorf("read required input %s: %w", key, err)
		} else if err == nil {
			if !fileProbed {
				fsys.fileProbes[key] = true
			}
			if _, readErr := os.ReadFile(filepath.FromSlash(key)); readErr != nil {
				switch {
				case os.IsPermission(readErr):
					captured.failure = "permission"
					if !strings.HasSuffix(strings.ToLower(key), ".map") {
						fsys.captureErr = fmt.Errorf("read required input %s: %w", key, readErr)
					}
				case os.IsNotExist(readErr):
					captured.failure = "missing"
				default:
					fsys.captureErr = fmt.Errorf("read required input %s: %w", key, readErr)
				}
			}
		} else if !fileProbed {
			fsys.fileProbes[key] = false
		}
	}
	fsys.reads[key] = captured
	return contents, exists
}

func (fsys *resolutionInputFS) DirectoryExists(path tspath.RootedDirectoryPath) bool {
	key := fsys.inputPath(path.AsString())
	if fsys.skipInput(key) {
		return fsys.FS.DirectoryExists(path)
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	if exists, ok := fsys.dirProbes[key]; ok {
		return exists
	}
	exists := fsys.FS.DirectoryExists(path)
	fsys.dirProbes[key] = exists
	return exists
}

func (fsys *resolutionInputFS) GetAccessibleEntries(path tspath.RootedDirectoryPath) vfs.Entries {
	key := fsys.inputPath(path.AsString())
	if fsys.skipInput(key) {
		return fsys.FS.GetAccessibleEntries(path)
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	if captured, ok := fsys.directories[key]; ok {
		return captured.entries()
	}
	entries := fsys.FS.GetAccessibleEntries(path)
	captured := capturedDirectory{
		files: slices.Clone(entries.Files), directories: slices.Clone(entries.Directories),
		symlinks: mapKeys(entries.Symlinks),
	}
	slices.Sort(captured.files)
	slices.Sort(captured.directories)
	fsys.directories[key] = captured
	return captured.entries()
}

func (fsys *resolutionInputFS) Stat(path tspath.RootedPath) vfs.FileInfo {
	key := fsys.inputPath(path.AsString())
	if fsys.skipInput(key) {
		return fsys.FS.Stat(path)
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	if captured, observed := fsys.statInfo[key]; observed {
		return captured
	}
	info := fsys.FS.Stat(path)
	if _, observed := fsys.stats[key]; !observed {
		fsys.stats[key] = fileKind(info)
	}
	fsys.statInfo[key] = info
	return info
}

func (fsys *resolutionInputFS) Realpath(path tspath.RootedPath) tspath.RootedPath {
	key := fsys.inputPath(path.AsString())
	if fsys.skipInput(key) {
		return fsys.FS.Realpath(path)
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	if captured, ok := fsys.realpaths[key]; ok {
		return tspath.RootedPathFromAbsolute(captured)
	}
	resolved := fsys.FS.Realpath(path)
	if actual, err := filepath.EvalSymlinks(filepath.FromSlash(key)); err == nil {
		fsys.realpaths[key] = normalizeSlash(actual)
	} else if !os.IsNotExist(err) {
		fsys.captureErr = fmt.Errorf("resolve input %s: %w", key, err)
	}
	return resolved
}

func (fsys *resolutionInputFS) inputPath(path string) string { return normalizeSlash(path) }

func (fsys *resolutionInputFS) skipInput(path string) bool {
	if bundled.IsBundled(path) || tspath.IsUrl(path) || strings.HasPrefix(path, "^/") {
		return true
	}
	libRoot := normalizeSlash(bundled.LibPath().AsString())
	return libRoot != "" && (path == libRoot || strings.HasPrefix(path, strings.TrimSuffix(libRoot, "/")+"/"))
}

func (directory capturedDirectory) entries() vfs.Entries {
	symlinks := make(map[string]struct{}, len(directory.symlinks))
	for _, name := range directory.symlinks {
		symlinks[name] = struct{}{}
	}
	return vfs.Entries{Files: slices.Clone(directory.files), Directories: slices.Clone(directory.directories), Symlinks: symlinks}
}

func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func fileKind(info vfs.FileInfo) string {
	if info == nil {
		return "missing"
	}
	if info.IsDir() {
		return "directory"
	}
	return "file"
}

func (fsys *resolutionInputFS) validate() (bool, validationStats, error) {
	started := time.Now()
	fsys.mu.Lock()
	if fsys.captureErr != nil {
		err := fsys.captureErr
		fsys.mu.Unlock()
		return false, validationStats{}, err
	}
	reads := cloneCapturedFiles(fsys.reads)
	fileProbes := cloneBoolMap(fsys.fileProbes)
	dirProbes := cloneBoolMap(fsys.dirProbes)
	stats := mapsCloneString(fsys.stats)
	directories := cloneCapturedDirectories(fsys.directories)
	realpaths := mapsCloneString(fsys.realpaths)
	fsys.mu.Unlock()
	trace := validationStats{}
	paths := make([]string, 0, len(reads))
	for path := range reads {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	valid := true
	for _, path := range paths {
		captured := reads[path]
		if !captured.exists {
			if captured.failure == "permission" {
				if _, err := os.ReadFile(filepath.FromSlash(path)); err == nil {
					valid = false
				} else if os.IsPermission(err) {
					continue
				} else if os.IsNotExist(err) {
					valid = false
				} else {
					trace.elapsed = time.Since(started)
					return false, trace, fmt.Errorf("validate input %s: %w", path, err)
				}
			}
			continue
		}
		current, err := os.ReadFile(filepath.FromSlash(path))
		trace.files++
		if err != nil {
			if os.IsNotExist(err) {
				valid = false
				continue
			}
			trace.elapsed = time.Since(started)
			return false, trace, fmt.Errorf("validate input %s: %w", path, err)
		}
		trace.bytes += int64(len(current))
		if string(current) != captured.contents {
			valid = false
		}
	}
	for path, expected := range fileProbes {
		actual, err := existsKind(path, false)
		if err != nil {
			trace.elapsed = time.Since(started)
			return false, trace, err
		}
		if actual != expected {
			valid = false
		}
	}
	for path, expected := range dirProbes {
		actual, err := existsKind(path, true)
		if err != nil {
			trace.elapsed = time.Since(started)
			return false, trace, err
		}
		if actual != expected {
			valid = false
		}
	}
	for path, expected := range stats {
		info, err := os.Stat(filepath.FromSlash(path))
		actual := "missing"
		if err == nil {
			if info.IsDir() {
				actual = "directory"
			} else {
				actual = "file"
			}
		} else if !os.IsNotExist(err) {
			trace.elapsed = time.Since(started)
			return false, trace, fmt.Errorf("validate stat probe %s: %w", path, err)
		}
		if actual != expected {
			valid = false
		}
	}
	for path, expected := range directories {
		actual, err := readDirectory(path)
		if err != nil {
			if os.IsNotExist(err) {
				valid = false
				continue
			}
			trace.elapsed = time.Since(started)
			return false, trace, fmt.Errorf("validate directory membership %s: %w", path, err)
		}
		if !sameCapturedDirectory(actual, expected) {
			valid = false
		}
	}
	for path, expected := range realpaths {
		actual, err := filepath.EvalSymlinks(filepath.FromSlash(path))
		if err != nil {
			if os.IsNotExist(err) {
				valid = false
				continue
			}
			trace.elapsed = time.Since(started)
			return false, trace, fmt.Errorf("validate resolved input %s: %w", path, err)
		}
		if normalizeSlash(actual) != expected {
			valid = false
		}
	}
	trace.elapsed = time.Since(started)
	return valid, trace, nil
}

func existsKind(path string, directory bool) (bool, error) {
	info, err := os.Stat(filepath.FromSlash(path))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("validate input probe %s: %w", path, err)
	}
	if directory {
		return info.IsDir(), nil
	}
	return !info.IsDir(), nil
}

func readDirectory(path string) (capturedDirectory, error) {
	entries, err := os.ReadDir(filepath.FromSlash(path))
	if err != nil {
		return capturedDirectory{}, err
	}
	result := capturedDirectory{}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			result.symlinks = append(result.symlinks, entry.Name())
		} else if entry.IsDir() {
			result.directories = append(result.directories, entry.Name())
		} else if entry.Type().IsRegular() {
			result.files = append(result.files, entry.Name())
		}
	}
	slices.Sort(result.files)
	slices.Sort(result.directories)
	slices.Sort(result.symlinks)
	return result, nil
}

func sameCapturedDirectory(left, right capturedDirectory) bool {
	return slices.Equal(left.files, right.files) && slices.Equal(left.directories, right.directories) && slices.Equal(left.symlinks, right.symlinks)
}

func (fsys *resolutionInputFS) inputs() []SnapshotInput {
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	byPath := make(map[string]bool)
	for path, input := range fsys.reads {
		byPath[path] = input.exists
	}
	for path, exists := range fsys.fileProbes {
		byPath[path] = byPath[path] || exists
	}
	for path, exists := range fsys.dirProbes {
		byPath[path] = byPath[path] || exists
	}
	for path := range fsys.directories {
		byPath[path] = true
	}
	for path, kind := range fsys.stats {
		byPath[path] = byPath[path] || kind != "missing"
	}
	result := make([]SnapshotInput, 0, len(byPath))
	for path, exists := range byPath {
		result = append(result, SnapshotInput{Path: path, Exists: exists})
	}
	return uniqueSnapshotInputs(result)
}

func (fsys *resolutionInputFS) readPaths() []string {
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	result := make([]string, 0, len(fsys.reads))
	for path, captured := range fsys.reads {
		if captured.exists {
			result = append(result, path)
		}
	}
	slices.Sort(result)
	return result
}

func (fsys *resolutionInputFS) revision() (string, error) {
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	paths := make([]string, 0, len(fsys.reads))
	for path := range fsys.reads {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	records := make([]any, 0, len(paths)+len(fsys.fileProbes)+len(fsys.dirProbes)+len(fsys.directories)+len(fsys.stats))
	for _, path := range paths {
		input := fsys.reads[path]
		contentDigest := ""
		if input.exists {
			digest := sha256.Sum256([]byte(input.contents))
			contentDigest = hex.EncodeToString(digest[:])
		}
		records = append(records, []any{"read", fsys.relativeInput(path), input.exists, input.failure, contentDigest})
	}
	appendProbes := func(kind string, values map[string]bool) {
		paths := make([]string, 0, len(values))
		for path := range values {
			paths = append(paths, path)
		}
		slices.Sort(paths)
		for _, path := range paths {
			records = append(records, []any{kind, fsys.relativeInput(path), values[path]})
		}
	}
	appendProbes("file", fsys.fileProbes)
	appendProbes("directory", fsys.dirProbes)
	statPaths := make([]string, 0, len(fsys.stats))
	for path := range fsys.stats {
		statPaths = append(statPaths, path)
	}
	slices.Sort(statPaths)
	for _, path := range statPaths {
		records = append(records, []any{"stat", fsys.relativeInput(path), fsys.stats[path]})
	}
	directoryPaths := make([]string, 0, len(fsys.directories))
	for path := range fsys.directories {
		directoryPaths = append(directoryPaths, path)
	}
	slices.Sort(directoryPaths)
	for _, path := range directoryPaths {
		directory := fsys.directories[path]
		records = append(records, []any{"membership", fsys.relativeInput(path), stringValues(directory.files), stringValues(directory.directories), stringValues(directory.symlinks)})
	}
	realpaths := make([]string, 0, len(fsys.realpaths))
	for path := range fsys.realpaths {
		realpaths = append(realpaths, path)
	}
	slices.Sort(realpaths)
	for _, path := range realpaths {
		records = append(records, []any{"realpath", fsys.relativeInput(path), fsys.relativeInput(fsys.realpaths[path])})
	}
	encoded, err := canonical.Marshal(records)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (fsys *resolutionInputFS) relativeInput(path string) string {
	path = normalizeSlash(path)
	root := strings.TrimSuffix(fsys.workspaceRoot, "/")
	if path == root {
		return "."
	}
	if strings.HasPrefix(path, root+"/") {
		return strings.TrimPrefix(path, root+"/")
	}
	relative, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(path))
	if err == nil {
		return filepath.ToSlash(relative)
	}
	return path
}

func stringValues(values []string) []any {
	result := make([]any, len(values))
	for i, value := range values {
		result[i] = value
	}
	return result
}

func cloneCapturedFiles(input map[string]capturedFile) map[string]capturedFile {
	result := make(map[string]capturedFile, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneBoolMap(input map[string]bool) map[string]bool {
	result := make(map[string]bool, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func mapsCloneString(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneCapturedDirectories(input map[string]capturedDirectory) map[string]capturedDirectory {
	result := make(map[string]capturedDirectory, len(input))
	for key, value := range input {
		result[key] = capturedDirectory{
			files: slices.Clone(value.files), directories: slices.Clone(value.directories), symlinks: slices.Clone(value.symlinks),
		}
	}
	return result
}
