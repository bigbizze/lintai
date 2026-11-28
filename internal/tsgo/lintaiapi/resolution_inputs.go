package lintaiapi

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/bigbizze/lintai/internal/tsgo/internal/bundled"
	"github.com/bigbizze/lintai/internal/tsgo/internal/nativepath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs"
)

type capturedFile struct {
	contents string
	exists   bool
	failure  string
	digest   string
	stamp    *CompilerStamp
}

type capturedDirectory struct {
	missing     bool
	files       []string
	directories []string
	symlinks    []string
}

// resolutionInputFS captures the values the compiler actually reads and
// probes. It memoizes within one collection attempt so repeated reads see one
// generation. Bundled lib files are covered by the implementation stamp.
type resolutionInputFS struct {
	vfs.FS
	mu                    sync.Mutex
	captureErr            error
	reads                 map[string]capturedFile
	fileProbes, dirProbes map[string]bool
	stats                 map[string]vfs.FileInfo
	directories           map[string]capturedDirectory
	// An empty path records a failed resolution. The compiler receives the
	// original path in that case, while validation checks for its appearance.
	realpaths map[string]string
}

func trackResolutionInputs(fs vfs.FS) *resolutionInputFS {
	return &resolutionInputFS{
		FS: fs, reads: make(map[string]capturedFile), fileProbes: make(map[string]bool),
		dirProbes: make(map[string]bool), stats: make(map[string]vfs.FileInfo),
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
	if !exists {
		fsys.checkProbeError(key)
	}
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
	// The text is read through its resolved path, which also names the
	// document that supplied it.
	file := filepath.FromSlash(key)
	if actual := fsys.resolve(key); actual != "" {
		file = actual
	}
	before, statErr := os.Stat(file)
	observed := time.Now()
	raw, err := os.ReadFile(file)
	captured := capturedFile{}
	if err != nil {
		switch {
		case isMissing(err):
			captured.failure = "missing"
		case os.IsPermission(err) && strings.HasSuffix(strings.ToLower(key), ".map"):
			captured.failure = "unreadable"
		default:
			fsys.captureErr = fmt.Errorf("read required input %s: %w", key, err)
		}
	} else {
		sum := sha256.Sum256(raw)
		captured.digest = hex.EncodeToString(sum[:])
		captured.contents = decodeCompilerBytes(raw)
		captured.exists = true
		if statErr == nil && before.Mode().IsRegular() && int64(len(raw)) == before.Size() && observed.UnixNano() >= 0 {
			if after, err := os.Stat(file); err == nil && after.Mode().IsRegular() && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) {
				captured.stamp = &CompilerStamp{Size: before.Size(), MtimeNs: before.ModTime().UnixNano(), ObservedAtNs: observed.UnixNano()}
			}
		}
	}
	fsys.reads[key] = captured
	return captured.contents, captured.exists
}

func isMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// Match the compiler's UTF-8 and BOM-selected UTF-16 decoding without a
// second filesystem read or changing the bytes used for observation identity.
func decodeCompilerBytes(raw []byte) string {
	if len(raw) >= 2 && (raw[0] == 0xff && raw[1] == 0xfe || raw[0] == 0xfe && raw[1] == 0xff) {
		var order binary.ByteOrder = binary.LittleEndian
		if raw[0] == 0xfe {
			order = binary.BigEndian
		}
		values := make([]uint16, (len(raw)-2)/2)
		for i := range values {
			values[i] = order.Uint16(raw[2+i*2:])
		}
		return string(utf16.Decode(values))
	}
	if len(raw) >= 3 && raw[0] == 0xef && raw[1] == 0xbb && raw[2] == 0xbf {
		raw = raw[3:]
	}
	return string(raw)
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
	if !exists {
		fsys.checkProbeError(key)
	}
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
	entries, err := os.ReadDir(filepath.FromSlash(key))
	if err != nil {
		if isMissing(err) {
			fsys.directories[key] = capturedDirectory{missing: true}
		} else {
			fsys.captureErr = fmt.Errorf("read compiler directory %s: %w", key, err)
		}
		return vfs.Entries{Symlinks: map[string]struct{}{}}
	}
	captured := capturedDirectory{}
	for _, entry := range entries {
		mode := entry.Type()
		link := mode&os.ModeSymlink != 0 || mode&os.ModeIrregular != 0 && nativepath.IsSymlinkOrReparsePoint(filepath.Join(filepath.FromSlash(key), entry.Name()))
		if link {
			info, err := os.Stat(filepath.Join(filepath.FromSlash(key), entry.Name()))
			if err != nil {
				continue
			}
			mode = info.Mode()
		}
		switch {
		case mode.IsDir():
			captured.directories = append(captured.directories, entry.Name())
		case mode.IsRegular():
			captured.files = append(captured.files, entry.Name())
		default:
			continue
		}
		if link {
			captured.symlinks = append(captured.symlinks, entry.Name())
		}
	}
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
	if captured, observed := fsys.stats[key]; observed {
		return captured
	}
	info := fsys.FS.Stat(path)
	if info == nil {
		fsys.checkProbeError(key)
	}
	fsys.stats[key] = info
	return info
}

func (fsys *resolutionInputFS) Realpath(path tspath.RootedPath) tspath.RootedPath {
	key := fsys.inputPath(path.AsString())
	if fsys.skipInput(key) {
		return fsys.FS.Realpath(path)
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	actual := fsys.resolve(key)
	if actual == "" || nativepath.IsNetworkPath(actual) {
		return path
	}
	return tspath.RootedPathFromAbsolute(actual)
}

// resolve is called with mu held. It resolves key once, so the compiler,
// source reads and observation see the same link target, and returns an empty
// path when key does not resolve.
func (fsys *resolutionInputFS) resolve(key string) string {
	actual, seen := fsys.realpaths[key]
	if !seen {
		var err error
		actual, err = nativepath.Realpath(filepath.FromSlash(key))
		if err == nil {
			actual, err = filepath.Abs(actual)
		}
		if err != nil {
			if !isMissing(err) {
				fsys.captureErr = fmt.Errorf("resolve input %s: %w", key, err)
			}
			actual = ""
		}
		fsys.realpaths[key] = actual
	}
	return actual
}

// documents maps each name of a file read for coordinates, relative to root
// or absolute, to the resolved path of the file that supplied its text when
// that path differs from the name.
func (fsys *resolutionInputFS) documents(root string, names []string) map[string]string {
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	documents := make(map[string]string)
	for _, name := range names {
		path := filepath.FromSlash(name)
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.FromSlash(root), path)
		}
		if actual := fsys.realpaths[normalizeSlash(path)]; actual != "" && actual != path {
			documents[name] = actual
		}
	}
	return documents
}

func (fsys *resolutionInputFS) inputPath(path string) string { return normalizeSlash(path) }

func (fsys *resolutionInputFS) checkProbeError(path string) {
	if _, err := os.Stat(filepath.FromSlash(path)); err != nil && !isMissing(err) {
		fsys.captureErr = fmt.Errorf("stat compiler input %s: %w", path, err)
	}
}

func (fsys *resolutionInputFS) skipInput(path string) bool {
	if bundled.IsBundled(path) || tspath.IsUrl(path) || strings.HasPrefix(path, "^/") || nativepath.IsNetworkPath(path) {
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

func fileKind(info vfs.FileInfo) string {
	if info == nil {
		return "missing"
	}
	if info.IsDir() {
		return "directory"
	}
	return "file"
}

// observations detaches compiler-owned records without retaining source text.
func (fsys *resolutionInputFS) observations() []CompilerObservation {
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	var records []CompilerObservation
	for path, input := range fsys.reads {
		if input.exists {
			records = append(records, CompilerObservation{Kind: "content", Path: filepath.FromSlash(path), Value: input.digest, Stamp: input.stamp})
		} else if input.failure != "" {
			records = append(records, CompilerObservation{Kind: "state", Path: filepath.FromSlash(path), Op: "read-file", Value: input.failure})
		}
	}
	for path, exists := range fsys.fileProbes {
		records = append(records, CompilerObservation{Kind: "state", Path: filepath.FromSlash(path), Op: "compiler-file", Value: fmt.Sprint(exists)})
	}
	for path, exists := range fsys.dirProbes {
		records = append(records, CompilerObservation{Kind: "state", Path: filepath.FromSlash(path), Op: "directory", Value: fmt.Sprint(exists)})
	}
	for path, info := range fsys.stats {
		records = append(records, CompilerObservation{Kind: "state", Path: filepath.FromSlash(path), Op: "stat", Value: fileKind(info)})
	}
	for path, dir := range fsys.directories {
		if dir.missing {
			records = append(records, CompilerObservation{Kind: "state", Path: filepath.FromSlash(path), Op: "read-directory", Value: "missing"})
			continue
		}
		entries := make([][2]string, 0, len(dir.files)+len(dir.directories)+len(dir.symlinks))
		for _, name := range dir.files {
			entries = append(entries, [2]string{name, "file"})
		}
		for _, name := range dir.directories {
			entries = append(entries, [2]string{name, "directory"})
		}
		for _, name := range dir.symlinks {
			entries = append(entries, [2]string{name, "symlink"})
		}
		records = append(records, CompilerObservation{Kind: "listing", Path: filepath.FromSlash(path), Op: "compiler-accessible", Entries: entries})
	}
	for path, value := range fsys.realpaths {
		if value == "" {
			value = "missing"
		}
		records = append(records, CompilerObservation{Kind: "resolved", Path: filepath.FromSlash(path), Value: value})
	}
	return records
}
