// Command g3 measures the F6 engine workload. The workspace and rules live
// outside the repository; see README.md for setup and environment variables.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/engine"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func emit(value any) {
	data, err := json.Marshal(value)
	must(err)
	fmt.Println(string(data))
}
func elapsed(start time.Time) float64 { return float64(time.Since(start).Nanoseconds()) / 1e6 }
func summary(samples []float64) map[string]any {
	values := append([]float64(nil), samples...)
	sort.Float64s(values)
	median := values[len(values)/2]
	if len(values)%2 == 0 {
		median = (values[len(values)/2-1] + median) / 2
	}
	return map[string]any{"samples_ms": samples, "median_ms": median, "p95_ms": values[(95*len(values)+99)/100-1]}
}
func main() {
	repo, root, rules := os.Getenv("LINTAI_BENCH_REPO"), os.Getenv("LINTAI_BENCH_WORKSPACE"), os.Getenv("LINTAI_BENCH_RULES")
	if repo == "" || root == "" || rules == "" {
		panic("set LINTAI_BENCH_REPO, LINTAI_BENCH_WORKSPACE and LINTAI_BENCH_RULES")
	}
	options := engine.Options{RepoRoot: repo, AssetRoot: filepath.Join(repo, "packages/client"), WorkspaceRoot: root,
		RuleGlobs: []string{filepath.Join(rules, "modules.ts"), filepath.Join(rules, "functions.ts")}, Env: map[string]any{}, RuleWorkers: 1}
	s := engine.Open(options, typescript.New(typescript.Options{Files: typescript.FileScopeTsconfig}))
	defer s.Close()
	ctx := context.Background()
	start := time.Now()
	result, err := s.Check(ctx, options)
	must(err)
	initial := elapsed(start)
	expectedBytes, err := os.ReadFile(filepath.Join(repo, "benchmarks/g3/analyzed-files.json"))
	must(err)
	var expected []string
	must(json.Unmarshal(expectedBytes, &expected))
	names := append([]string(nil), result.AnalyzedFiles...)
	sort.Strings(names)
	if !reflect.DeepEqual(names, expected) || len(result.Diagnostics) != 10214 || result.RulesLoaded != 2 {
		panic(fmt.Sprintf("unexpected workload: files=%d diagnostics=%d rules=%d", len(names), len(result.Diagnostics), result.RulesLoaded))
	}
	counts := map[string]int{}
	for _, diagnostic := range result.Diagnostics {
		counts[diagnostic.RuleID]++
	}
	if counts["benchmark.modules"] != 511 || counts["benchmark.functions"] != 9703 || len(counts) != 2 {
		panic(fmt.Sprintf("unexpected findings: %v", counts))
	}
	files := []string{"packages/bench/arktype-morph-test.ts"}
	projected, err := s.RefreshFiles(ctx, options, names)
	must(err)
	for i, name := range names {
		want := result.DiagnosticsFor(name)
		if len(want) != len(projected.Diagnostics[i]) || (len(want) != 0 && !reflect.DeepEqual(want, projected.Diagnostics[i])) {
			panic("batch projection differs from ordered whole-result projection: " + name)
		}
	}
	if len(projected.WorkspaceProblems) != 0 || projected.SnapshotVersion != result.SnapshotVersion {
		panic("unexpected batch problems or snapshot")
	}
	emit(map[string]any{"event": "initial", "elapsed_ms": initial, "files": len(names), "diagnostics": len(result.Diagnostics), "rules": result.RulesLoaded, "diagnostics_by_rule": counts, "projection_equivalence": true, "file": files[0]})
	var warm []float64
	for i := 0; i < 20; i++ {
		start = time.Now()
		file, err := s.Refresh(ctx, options, files[0])
		must(err)
		warm = append(warm, elapsed(start))
		if len(file.Diagnostics) != 2 || len(file.WorkspaceProblems) != 0 {
			panic("unexpected single-file result")
		}
	}
	emit(map[string]any{"event": "engine_file_refresh", "timing": summary(warm), "diagnostics": 2})
	var full []float64
	for i := 0; i < 5; i++ {
		start = time.Now()
		result, err := s.Check(ctx, options)
		must(err)
		full = append(full, elapsed(start))
		if len(result.Diagnostics) != 10214 {
			panic("unexpected full result")
		}
	}
	emit(map[string]any{"event": "forced_full", "timing": summary(full), "diagnostics": 10214})
}
