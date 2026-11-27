// Package rulepack compiles rules into a single explicitly published workspace artifact.
package rulepack

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/internal/atomicfile"
	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

const Path = ".lintai/rules.json"
const Format = "lintai-rules-v1"
const ABI = "lintai-pure-v1"

type Pack struct {
	Format  string  `json:"format"`
	ABI     string  `json:"abi"`
	Entries []Entry `json:"entries"`
}
type Entry struct {
	Entry string `json:"entry"`
	Code  string `json:"code"`
}

func validateName(name string) error {
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\:\x00") || strings.EqualFold(strings.Split(name, "/")[0], ".lintai") {
		return fmt.Errorf("invalid rule entry %q: expected a portable workspace-relative file name", name)
	}
	_, err := filepath.Localize(name)
	return err
}

func Build(ctx context.Context, root string, names []string) error {
	if names == nil {
		return fmt.Errorf("configure [rules].entries in lintai.toml (use [] for an empty pack)")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer workspace.Close()
	output := filepath.Join(root, Path)
	previous, err := atomicfile.Read(output)
	if err != nil {
		return err
	}
	pack := Pack{Format: Format, ABI: ABI, Entries: make([]Entry, 0, len(names))}
	names = slices.Sorted(slices.Values(names))
	seen := make(map[string]bool)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateName(name); err != nil {
			return err
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("duplicate or case-colliding rule entry %q", name)
		}
		seen[key] = true
		if info, err := workspace.Stat(filepath.FromSlash(name)); err != nil {
			return fmt.Errorf("rule entry %s: %w", name, err)
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("rule entry %s must be a regular file", name)
		}
		code, err := compile(root, name)
		if err != nil {
			return err
		}
		pack.Entries = append(pack.Entries, Entry{name, code})
	}
	data, err := json.Marshal(pack)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	return atomicfile.Write(ctx, output, previous, append(data, '\n'), "rule pack", "", atomicfile.Snapshot{}, "")
}

func compile(root, name string) (string, error) {
	result := api.Build(api.BuildOptions{
		AbsWorkingDir: root,
		EntryPoints:   []string{"./" + name},
		Bundle:        true,
		Format:        api.FormatIIFE,
		GlobalName:    "LintAIPureModule",
		Platform:      api.PlatformNeutral,
		MainFields:    []string{"module", "main"},
		Conditions:    []string{"default"},
		Target:        api.ES2022,
		TsconfigRaw:   `{"compilerOptions":{"useDefineForClassFields":true}}`,
		Charset:       api.CharsetASCII,
		LegalComments: api.LegalCommentsNone,
		// Omit esbuild's profiling labels, which may include a real path
		// when pnpm resolves a linked package outside the workspace.
		MinifyIdentifiers: true,
		KeepNames:         true,
		MinifyWhitespace:  true,
		LogLevel:          api.LogLevelSilent,
		Metafile:          true,
		LogOverride: map[string]api.LogLevel{
			"indirect-require":             api.LogLevelError,
			"require-resolve-not-external": api.LogLevelError,
			"unsupported-require-call":     api.LogLevelError,
			"unsupported-dynamic-import":   api.LogLevelError,
		},
	})
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("build %s: %s", name, strings.Join(api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage}), ""))
	}
	if len(result.OutputFiles) != 1 {
		return "", fmt.Errorf("build %s: expected one JavaScript output", name)
	}
	// esbuild can leave optional imports unresolved (for example a literal
	// require inside a try block) without issuing a build error.
	var meta struct {
		Outputs map[string]struct {
			Imports []struct {
				Path     string
				External bool
			}
		}
	}
	if err := json.Unmarshal([]byte(result.Metafile), &meta); err != nil {
		return "", err
	}
	for _, out := range meta.Outputs {
		for _, imp := range out.Imports {
			if imp.External {
				return "", fmt.Errorf("build %s: unsupported runtime import %q", name, imp.Path)
			}
		}
	}
	code := string(result.OutputFiles[0].Contents)
	if _, err := goja.Compile(name, code, false); err != nil {
		return "", fmt.Errorf("build %s: unsupported pure JavaScript: %w", name, err)
	}
	return code, nil
}
