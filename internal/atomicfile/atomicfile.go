// Package atomicfile provides guarded replacement of small saved policy files.
package atomicfile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Snapshot struct {
	Data   []byte
	Exists bool
}

func Read(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Data: data, Exists: true}, nil
}

// Write creates the complete replacement beside the destination, verifies the
// originally read bytes immediately before the rename, and makes rename the
// cancellation/commit boundary. The caller serializes writes in its session.
func Write(ctx context.Context, path string, expected Snapshot, data []byte, changed string, guardPath string, guard Snapshot, guardChanged string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "lintai-write-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if guardPath != "" {
		currentGuard, err := Read(guardPath)
		if err != nil {
			return err
		}
		if currentGuard.Exists != guard.Exists || !bytes.Equal(currentGuard.Data, guard.Data) {
			return fmt.Errorf("%s: %s changed while preparing the update", guardChanged, guardPath)
		}
	}
	current, err := Read(path)
	if err != nil {
		return err
	}
	if current.Exists != expected.Exists || !bytes.Equal(current.Data, expected.Data) {
		return fmt.Errorf("%s: %s changed while preparing the update", changed, path)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
