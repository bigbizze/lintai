package lintaiapi

// CompilerStamp certifies metadata from the read that produced a content hash.
type CompilerStamp struct{ Size, MtimeNs, ObservedAtNs int64 }

// CompilerObservation is a detached record of one compiler filesystem operation.
// It contains no compiler objects or decoded source text.
type CompilerObservation struct {
	Kind, Path, Op, Value string
	Entries               [][2]string
	Stamp                 *CompilerStamp
}
