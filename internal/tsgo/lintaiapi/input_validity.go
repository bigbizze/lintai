package lintaiapi

import (
	"context"
	"time"

	"github.com/bigbizze/lintai/internal/tsgo/internal/vfs/osvfs"
	"github.com/bigbizze/lintai/internal/workspace"
)

// InputValidity is detached from the compiler and its capturing filesystem.
// It retains the observations that established one producer generation.
type InputValidity struct {
	observations  *resolutionInputFS
	plan          *workspace.ScopePlan
	configPath    string
	configDigest  string
	configPresent bool
}

// InputValidation carries the cost of an invalidated retained generation into
// the replacement's single validation trace.
type InputValidation struct{ stats validationStats }

func retainInputValidity(capture *resolutionInputFS, plan *workspace.ScopePlan, req BuildSnapshotRequest) *InputValidity {
	validity := &InputValidity{plan: plan, configPath: req.ConfigPath, configDigest: req.ConfigDigest, configPresent: req.ConfigPresent}
	if capture != nil {
		capture.mu.Lock()
		validity.observations = &resolutionInputFS{
			workspaceRoot: capture.workspaceRoot,
			reads:         cloneCapturedFiles(capture.reads), fileProbes: cloneBoolMap(capture.fileProbes),
			dirProbes: cloneBoolMap(capture.dirProbes), stats: mapsCloneString(capture.stats),
			directories: cloneCapturedDirectories(capture.directories), realpaths: mapsCloneString(capture.realpaths),
		}
		capture.mu.Unlock()
	}
	return validity
}

// Validate rereads captured contents and probes and rechecks source membership.
func (validity *InputValidity) Validate(ctx context.Context) (stable bool, previous *InputValidation, resultErr error) {
	started := time.Now()
	trace := validationStats{}
	defer func() {
		trace.elapsed = time.Since(started)
		previous = &InputValidation{stats: trace}
		if stable || resultErr != nil {
			printValidationTrace(validationStats{}, trace)
		}
	}()
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	stable = true
	if validity.observations != nil {
		observations := validity.observations
		// The live filesystem exists only for the duration of validation.
		current := &resolutionInputFS{FS: osvfs.FS(), reads: observations.reads,
			fileProbes: observations.fileProbes, dirProbes: observations.dirProbes,
			stats: observations.stats, directories: observations.directories, realpaths: observations.realpaths}
		var err error
		stable, trace, err = current.validate()
		if err != nil {
			return false, nil, err
		}
	}
	if validity.configPath != "" {
		valid, bytesRead, fileRead, err := validateConfigInput(validity.configPath, validity.configPresent, validity.configDigest)
		if fileRead {
			trace.files++
		}
		trace.bytes += bytesRead
		if err != nil {
			return false, nil, err
		}
		stable = stable && valid
	}
	if validity.plan != nil {
		valid, err := workspace.ValidateScope(validity.plan)
		if err != nil {
			return false, nil, err
		}
		stable = stable && valid
	}
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	return stable, nil, nil
}
