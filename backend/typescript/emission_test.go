package typescript

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"codeberg.org/TauCeti/mangle-go/ast"
	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/ambient"
	"github.com/bigbizze/lintai/internal/tsgo/lintaiapi"
	"github.com/bigbizze/lintai/observe"
)

type testFact struct {
	Relation string
	Values   []facts.Value
}

type emissionView struct {
	Relations     map[facts.Predicate][][]facts.Value
	Anchors       map[string]facts.AnchorSupport
	AnalyzedFiles []string
	InputRevision string
	Scope         any
}

func inspectEmission(ctx context.Context, producer *Backend, request backend.CollectionRequest) (emissionView, error) {
	generation, err := producer.Prepare(ctx, request, observe.Full)
	if err != nil {
		return emissionView{}, err
	}
	metadata := generation.Metadata()
	description, err := producer.Describe()
	if err != nil {
		return emissionView{}, err
	}
	descriptor, err := facts.ValidateDescription(description)
	if err != nil {
		return emissionView{}, err
	}
	builder, err := facts.NewBuilder(descriptor, request.Predicates)
	if err != nil {
		return emissionView{}, err
	}
	if err := generation.Emit(ctx, builder); err != nil {
		return emissionView{}, err
	}
	validated, err := builder.Finish()
	if err != nil {
		return emissionView{}, err
	}
	return emissionView{Relations: testRows(validated), Anchors: validated.Anchors(), AnalyzedFiles: metadata.AnalyzedFiles, InputRevision: metadata.InputRevision, Scope: metadata.Scope}, nil
}

func testRows(validated facts.ValidatedFacts) map[facts.Predicate][][]facts.Value {
	rows := make(map[facts.Predicate][][]facts.Value)
	for _, predicate := range validated.Relations() {
		rows[predicate] = [][]facts.Value{}
		for _, atom := range validated.Atoms(predicate) {
			row := make([]facts.Value, len(atom.Args))
			for i, arg := range atom.Args {
				value := arg.(ast.Constant)
				switch value.Type {
				case ast.StringType:
					text, _ := value.StringValue()
					row[i] = facts.String(text)
				case ast.NameType:
					text, _ := value.NameValue()
					row[i] = facts.Name(strings.TrimPrefix(text, "/"))
				case ast.NumberType:
					n, _ := value.NumberValue()
					row[i] = facts.Number(n)
				case ast.Float64Type:
					n, _ := value.Float64Value()
					row[i] = facts.Float(n)
				default:
					panic(fmt.Sprintf("unexpected constant %v", value.Type))
				}
			}
			rows[predicate] = append(rows[predicate], row)
		}
	}
	return rows
}

func inspectLocalSnapshot(t *testing.T, snapshot *lintaiapi.Snapshot, sources []ambient.Source, adjustments []lintaiapi.ConfigAdjustment, description facts.ProducerDescription, requested []facts.Predicate, noSupport bool) []testFact {
	t.Helper()
	if noSupport {
		for i := range description.BaseRelations {
			for j := range description.BaseRelations[i].Arguments {
				description.BaseRelations[i].Arguments[j].ReportSupport = facts.ReportNone
			}
		}
	}
	descriptor, err := facts.ValidateDescription(description)
	if err != nil {
		t.Fatal(err)
	}
	if requested == nil {
		for _, r := range description.BaseRelations {
			requested = append(requested, r.Predicate)
		}
	}
	b, err := facts.NewBuilder(descriptor, requested)
	if err != nil {
		t.Fatal(err)
	}
	if err := emitSnapshot(context.Background(), b, description, snapshot, sources, adjustments, description.Namespace); err != nil {
		t.Fatal(err)
	}
	validated, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	rows := testRows(validated)
	metadata := map[facts.Predicate]facts.RelationDescription{}
	for _, r := range description.BaseRelations {
		metadata[r.Predicate] = r
	}
	var result []testFact
	for _, predicate := range validated.Relations() {
		for _, row := range rows[predicate] {
			for i, arg := range metadata[predicate].Arguments {
				if arg.Role == facts.EntityRole {
					row[i].String = strings.TrimPrefix(row[i].String, description.Namespace+":")
				}
			}
			result = append(result, testFact{Relation: strings.TrimPrefix(predicate.Name, description.Namespace+"."), Values: row})
		}
	}
	return result
}

func generationItems(t *testing.T, g *producerGeneration) []testFact {
	t.Helper()
	return inspectLocalSnapshot(t, g.snapshot, g.sources, g.adjustments, g.description, nil, false)
}
