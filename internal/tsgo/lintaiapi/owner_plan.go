package lintaiapi

import (
	"context"
	"fmt"
	"slices"

	"github.com/bigbizze/lintai/internal/tsgo/internal/compiler"
	"github.com/bigbizze/lintai/internal/tsgo/internal/tspath"
)

// ordinaryPlan fixes the prepared program responsible for each selected file
// before any semantic collector runs. A planned program is a distinct context
// even if another program has the same config path.
type ordinaryPlan struct {
	files               []ordinaryFile
	programs            []*ordinaryProgram
	owners              map[string]*ordinaryProgram
	membershipCollected bool
}

type ordinaryFile struct {
	path           string
	selectedConfig string
	owner          *ordinaryProgram
	rootConfigs    []string
}

type ordinaryProgram struct {
	group    configGroup
	prepared groupProgram
}

type incompleteCompilerMembership struct{ file string }

func (err *incompleteCompilerMembership) Error() string {
	return fmt.Sprintf("compiler membership is incomplete for %s", err.file)
}

func planOrdinary(ctx context.Context, host compiler.CompilerHost, root string, files, exactFiles []string, tsconfigFiles, membership bool, configs *configCache, timing *extractTiming) (*ordinaryPlan, error) {
	groups, err := discoverConfigGroups(root, files, exactFiles, configs)
	if err != nil {
		return nil, err
	}
	plan := &ordinaryPlan{membershipCollected: membership, owners: make(map[string]*ordinaryProgram, len(files))}
	byPath := make(map[string]*ordinaryFile, len(files))
	for _, file := range files {
		plan.files = append(plan.files, ordinaryFile{path: file})
	}
	for i := range plan.files {
		byPath[plan.files[i].path] = &plan.files[i]
	}
	var fallbackFiles []string
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		prepared, err := prepareGroupProgram(host, root, group, timing)
		if err != nil {
			return nil, err
		}
		owner := &ordinaryProgram{group: group, prepared: prepared}
		owner.group.PrimaryFiles = nil
		for _, file := range group.PrimaryFiles {
			item := byPath[file]
			item.selectedConfig = group.ConfigPath
			if prepared.program.GetSourceFile(tspath.RootedFilePathFromAbsolute(file)) != nil {
				item.owner = owner
				owner.group.PrimaryFiles = append(owner.group.PrimaryFiles, file)
			} else if group.ConfigPath == "" {
				return nil, &incompleteCompilerMembership{file: file}
			} else if !tsconfigFiles {
				fallbackFiles = append(fallbackFiles, file)
			}
		}
		if len(owner.group.PrimaryFiles) > 0 {
			plan.programs = append(plan.programs, owner)
		}
	}
	if len(fallbackFiles) > 0 {
		slices.Sort(fallbackFiles)
		group := configGroup{PrimaryFiles: fallbackFiles}
		prepared, err := prepareGroupProgram(host, root, group, timing)
		if err != nil {
			return nil, err
		}
		owner := &ordinaryProgram{group: group, prepared: prepared}
		for _, file := range fallbackFiles {
			if prepared.program.GetSourceFile(tspath.RootedFilePathFromAbsolute(file)) == nil {
				return nil, &incompleteCompilerMembership{file: file}
			}
			byPath[file].owner = owner
		}
		plan.programs = append(plan.programs, owner)
	}
	if membership {
		inventory := make(map[string]bool)
		var visit func(string) error
		visit = func(path string) error {
			if path == "" || inventory[path] {
				return nil
			}
			inventory[path] = true
			parsed, err := configs.load(path)
			if err != nil {
				return err
			}
			for _, reference := range parsed.references {
				if err := visit(reference); err != nil {
					return err
				}
			}
			return nil
		}
		for _, file := range files {
			nearest, err := configs.findNearestConfigPath(root, file)
			if err != nil {
				return nil, err
			}
			if err := visit(nearest); err != nil {
				return nil, err
			}
		}
		for path := range inventory {
			parsed := configs.configs[path]
			for i := range plan.files {
				if _, ok := parsed.rootFiles[configs.pathKey(plan.files[i].path)]; ok {
					plan.files[i].rootConfigs = append(plan.files[i].rootConfigs, path)
				}
			}
		}
		for i := range plan.files {
			slices.Sort(plan.files[i].rootConfigs)
		}
	}
	for _, file := range plan.files {
		if file.owner != nil {
			plan.owners[file.path] = file.owner
		}
	}
	return plan, nil
}
