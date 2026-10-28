package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
)

type Artifacts struct {
	PureBundlePath    string `json:"pureBundlePath"`
	PrepareBundlePath string `json:"prepareBundlePath"`
}

type PrepareResult struct {
	RuleID      string `json:"ruleId"`
	RuleVersion int    `json:"ruleVersion"`
	Env         any    `json:"env"`
	Setup       any    `json:"setup"`
}

type PrepareError struct {
	RuleID      string
	RuleVersion int
	Message     string
}

type buildRequest struct {
	RepoRoot string `json:"repoRoot"`
	RulePath string `json:"rulePath"`
	OutDir   string `json:"outDir"`
}

type buildResponse struct {
	RulePath          string `json:"rulePath"`
	PrepareBundlePath string `json:"prepareBundlePath,omitempty"`
	PureBundlePath    string `json:"pureBundlePath,omitempty"`
	Error             string `json:"error,omitempty"`
}

type prepareRequest struct {
	RulePath      string `json:"rulePath"`
	WorkspaceRoot string `json:"workspaceRoot"`
	BundlePath    string `json:"bundlePath"`
	Env           any    `json:"env"`
}

type prepareResponse struct {
	RulePath    string `json:"rulePath"`
	RuleID      string `json:"ruleId,omitempty"`
	RuleVersion int    `json:"ruleVersion,omitempty"`
	Env         any    `json:"env,omitempty"`
	Setup       any    `json:"setup,omitempty"`
	Error       string `json:"error,omitempty"`
	Skipped     bool   `json:"skipped,omitempty"`
}

// BuildAll writes the bundles of each rule to its own subdirectory of outDir.
// The caller owns outDir and removes it when the bundles are no longer needed.
func BuildAll(ctx context.Context, assetRoot, repoRoot, outDir string, rulePaths []string) (map[string]Artifacts, map[string]error, error) {
	if len(rulePaths) == 0 {
		return map[string]Artifacts{}, map[string]error{}, nil
	}

	requests := make([]buildRequest, 0, len(rulePaths))
	requested := make(map[string]struct{}, len(rulePaths))
	for index, rulePath := range rulePaths {
		if _, exists := requested[rulePath]; exists {
			return nil, nil, fmt.Errorf("duplicate rule path %q", rulePath)
		}
		requested[rulePath] = struct{}{}
		requests = append(requests, buildRequest{
			RepoRoot: repoRoot,
			RulePath: rulePath,
			OutDir:   filepath.Join(outDir, strconv.Itoa(index)),
		})
	}
	itemErrors := make(map[string]error)

	payload, err := json.Marshal(requests)
	if err != nil {
		return nil, nil, err
	}
	scriptPath, err := resolveScriptPath(assetRoot, "bundle-rule.mjs")
	if err != nil {
		return nil, itemErrors, err
	}
	command := exec.CommandContext(ctx, "node", scriptPath)
	command.Dir = assetRoot
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, itemErrors, fmt.Errorf("bundle failed: %w\n%s", err, output)
	}

	var rows []buildResponse
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, itemErrors, fmt.Errorf("bundle output was not valid JSON: %w", err)
	}

	results := make(map[string]Artifacts, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, expected := requested[row.RulePath]; !expected {
			return nil, itemErrors, fmt.Errorf("bundle returned unexpected rule path %q", row.RulePath)
		}
		if _, exists := seen[row.RulePath]; exists {
			return nil, itemErrors, fmt.Errorf("bundle returned duplicate rule path %q", row.RulePath)
		}
		seen[row.RulePath] = struct{}{}
		if row.Error != "" {
			itemErrors[row.RulePath] = fmt.Errorf("%s", row.Error)
			continue
		}
		results[row.RulePath] = Artifacts{
			PureBundlePath:    row.PureBundlePath,
			PrepareBundlePath: row.PrepareBundlePath,
		}
	}

	for _, request := range requests {
		if _, exists := seen[request.RulePath]; !exists {
			return nil, itemErrors, fmt.Errorf("bundle returned no result for %q", request.RulePath)
		}
	}
	return results, itemErrors, nil
}

// PrepareAll runs setup() for each rule. A file under the rule globs that has
// no default export is a helper module, not a rule: it is in neither result
// map.
func PrepareAll(ctx context.Context, assetRoot, repoRoot, workspaceRoot string, artifacts map[string]Artifacts, rawEnv any) (map[string]PrepareResult, map[string]PrepareError, error) {
	if len(artifacts) == 0 {
		return map[string]PrepareResult{}, map[string]PrepareError{}, nil
	}

	requests := make([]prepareRequest, 0, len(artifacts))
	requested := make(map[string]struct{}, len(artifacts))
	rulePaths := make([]string, 0, len(artifacts))
	for rulePath := range artifacts {
		rulePaths = append(rulePaths, rulePath)
	}
	sort.Strings(rulePaths)
	for _, rulePath := range rulePaths {
		item := artifacts[rulePath]
		if _, exists := requested[rulePath]; exists {
			return nil, nil, fmt.Errorf("duplicate rule path %q", rulePath)
		}
		requested[rulePath] = struct{}{}
		requests = append(requests, prepareRequest{
			RulePath:      rulePath,
			WorkspaceRoot: workspaceRoot,
			BundlePath:    item.PrepareBundlePath,
			Env:           rawEnv,
		})
	}

	payload, err := json.Marshal(requests)
	if err != nil {
		return nil, nil, err
	}
	scriptPath, err := resolveScriptPath(assetRoot, "prepare-rule.mjs")
	if err != nil {
		return nil, nil, err
	}
	command := exec.CommandContext(ctx, "node", scriptPath)
	command.Dir = assetRoot
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("prepare failed: %w\n%s", err, output)
	}

	var rows []prepareResponse
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, nil, fmt.Errorf("prepare output was not valid JSON: %w", err)
	}

	results := make(map[string]PrepareResult, len(rows))
	itemErrors := make(map[string]PrepareError)
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, expected := requested[row.RulePath]; !expected {
			return nil, nil, fmt.Errorf("prepare returned unexpected rule path %q", row.RulePath)
		}
		if _, exists := seen[row.RulePath]; exists {
			return nil, nil, fmt.Errorf("prepare returned duplicate rule path %q", row.RulePath)
		}
		seen[row.RulePath] = struct{}{}
		if row.Skipped {
			continue
		}
		if row.Error != "" {
			itemErrors[row.RulePath] = PrepareError{
				RuleID:      row.RuleID,
				RuleVersion: row.RuleVersion,
				Message:     row.Error,
			}
			continue
		}
		results[row.RulePath] = PrepareResult{
			RuleID:      row.RuleID,
			RuleVersion: row.RuleVersion,
			Env:         row.Env,
			Setup:       row.Setup,
		}
	}

	for rulePath := range artifacts {
		if _, exists := seen[rulePath]; !exists {
			return nil, nil, fmt.Errorf("prepare returned no result for %q", rulePath)
		}
	}
	return results, itemErrors, nil
}

func resolveScriptPath(assetRoot, name string) (string, error) {
	if assetRoot == "" {
		return "", fmt.Errorf("lintai asset root is required")
	}
	scriptPath := filepath.Join(assetRoot, "scripts", name)
	if _, err := os.Stat(scriptPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("lintai helper script %q was not found under asset root %q", name, assetRoot)
		}
		return "", err
	}
	return scriptPath, nil
}
