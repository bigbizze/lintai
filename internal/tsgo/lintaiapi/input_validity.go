package lintaiapi

import (
	"context"

	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
)

// CompilerInputValidity contains detached compiler-input observations. It does
// not decide whether a producer generation is valid: the producer also checks
// configuration and scope before publication and reuse.
type CompilerInputValidity struct {
	IdentityRecords []any
	ReadPaths       []string
	observations    *compilerInputObservations
}

// CompilerInputValidation reports the file content reads made by validation.
type CompilerInputValidation struct {
	Files int
	Bytes int64
}

func retainCompilerInputValidity(capture *resolutionInputFS) *CompilerInputValidity {
	return &CompilerInputValidity{
		IdentityRecords: capture.identityRecords(),
		ReadPaths:       capture.readPaths(),
		observations:    capture.compilerInputObservations,
	}
}

// Validate compares captured compiler-input contents and probes with the live
// filesystem. Scope and producer configuration are outside this contract.
func (validity *CompilerInputValidity) Validate(ctx context.Context) (bool, CompilerInputValidation, error) {
	if err := ctx.Err(); err != nil {
		return false, CompilerInputValidation{}, err
	}
	current := &resolutionInputFS{FS: osvfs.FS(), compilerInputObservations: validity.observations}
	stable, stats, err := current.validate()
	result := CompilerInputValidation{Files: stats.files, Bytes: stats.bytes}
	if err != nil {
		return false, result, err
	}
	if err := ctx.Err(); err != nil {
		return false, result, err
	}
	return stable, result, nil
}
