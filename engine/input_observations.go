package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/bigbizze/lintai/internal/atomicfile"
	"github.com/bigbizze/lintai/observe"
)

// policyObservations describes the bytes already read for parsing or the
// tolerated absence; it never stamps bytes from a later filesystem read.
func policyObservations(path string, snapshot atomicfile.Snapshot) (observe.Set, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return observe.Set{}, err
	}
	record := observe.Record{Kind: observe.State, Path: filepath.Clean(absolute), Op: observe.ReadFile, Value: observe.Missing}
	if snapshot.Exists {
		sum := sha256.Sum256(snapshot.Data)
		record = observe.Record{Kind: observe.Content, Path: filepath.Clean(absolute), Value: observe.Value(hex.EncodeToString(sum[:]))}
	}
	return observe.New([]observe.Record{record})
}
