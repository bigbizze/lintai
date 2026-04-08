package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func Build(ctx context.Context, repoRoot, rulePath string) (Artifacts, error) {
	workDir, err := os.MkdirTemp("", "lintai-bundle-*")
	if err != nil {
		return Artifacts{}, err
	}
	args := []string{
		filepath.Join(repoRoot, "scripts", "bundle-rule.mjs"),
		repoRoot,
		rulePath,
		workDir,
	}
	command := exec.CommandContext(ctx, "node", args...)
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	if err != nil {
		return Artifacts{}, fmt.Errorf("bundle failed: %w\n%s", err, output)
	}
	var artifacts Artifacts
	if err := json.Unmarshal(output, &artifacts); err != nil {
		return Artifacts{}, fmt.Errorf("bundle output was not valid JSON: %w", err)
	}
	return artifacts, nil
}

func Prepare(ctx context.Context, repoRoot, workspaceRoot string, artifacts Artifacts, rawEnv any) (PrepareResult, error) {
	payload, err := json.Marshal(map[string]any{
		"workspaceRoot": workspaceRoot,
		"bundlePath":    artifacts.PrepareBundlePath,
		"env":           rawEnv,
	})
	if err != nil {
		return PrepareResult{}, err
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(repoRoot, "scripts", "prepare-rule.mjs"))
	command.Dir = repoRoot
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		return PrepareResult{}, fmt.Errorf("prepare failed: %w\n%s", err, output)
	}
	var result PrepareResult
	if err := json.Unmarshal(output, &result); err != nil {
		return PrepareResult{}, fmt.Errorf("prepare output was not valid JSON: %w", err)
	}
	return result, nil
}
