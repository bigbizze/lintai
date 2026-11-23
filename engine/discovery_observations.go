package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/bigbizze/lintai/observe"
)

// discoveryFS records the same operations used by doublestar, including
// unsuccessful prefix and exact-path probes.
type discoveryFS struct {
	root    string
	records *[]observe.Record
}

func (d *discoveryFS) path(name string) string {
	return filepath.Clean(filepath.Join(d.root, filepath.FromSlash(name)))
}
func (d *discoveryFS) Open(name string) (fs.File, error) { return os.DirFS(d.root).Open(name) }
func discoveryMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}
func (d *discoveryFS) resolved(path string) error {
	value, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve discovery input %s: %w", path, err)
	}
	*d.records = append(*d.records, observe.Record{Kind: observe.Resolved, Path: path, Value: observe.Value(filepath.Clean(value))})
	return nil
}
func (d *discoveryFS) Stat(name string) (fs.FileInfo, error) {
	path := d.path(name)
	info, err := fs.Stat(os.DirFS(d.root), name)
	if err != nil {
		if discoveryMissing(err) {
			*d.records = append(*d.records, observe.Record{Kind: observe.State, Path: path, Op: observe.Stat, Value: observe.Missing})
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	value := observe.File
	if info.IsDir() {
		value = observe.DirectoryValue
	}
	*d.records = append(*d.records, observe.Record{Kind: observe.State, Path: path, Op: observe.Stat, Value: value})
	if err := d.resolved(path); err != nil {
		return nil, err
	}
	return info, nil
}
func (d *discoveryFS) ReadDir(name string) ([]fs.DirEntry, error) {
	path := d.path(name)
	entries, err := fs.ReadDir(os.DirFS(d.root), name)
	if err != nil {
		if discoveryMissing(err) {
			*d.records = append(*d.records, observe.Record{Kind: observe.State, Path: path, Op: observe.ReadDirectory, Value: observe.Missing})
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	values := make([][2]string, 0, len(entries))
	for _, entry := range entries {
		kind := observe.Other
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			kind = observe.Symlink
		case entry.IsDir():
			kind = observe.DirectoryValue
		case entry.Type().IsRegular():
			kind = observe.File
		}
		values = append(values, [2]string{entry.Name(), string(kind)})
	}
	*d.records = append(*d.records, observe.Record{Kind: observe.Listing, Path: path, Op: observe.Physical, Entries: values})
	if err := d.resolved(path); err != nil {
		return nil, err
	}
	return entries, nil
}
