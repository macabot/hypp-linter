package hprops

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const Doc = `
Checks for correct HProps values.

The linter ensures that the values in an HProps map are of the correct type.
`

var Analyzer = &analysis.Analyzer{
	Name:     "hprops",
	Doc:      Doc,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspector := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.CompositeLit)(nil),
	}

	inspector.Preorder(nodeFilter, func(n ast.Node) {
		lit := n.(*ast.CompositeLit)

		t, ok := pass.TypesInfo.TypeOf(lit.Type).(*types.Named)
		if !ok {
			return
		}

		if !strings.HasSuffix(t.Obj().Pkg().Path(), "github.com/macabot/hypp") || t.Obj().Name() != "HProps" {
			return
		}

		for _, el := range lit.Elts {
			kv := el.(*ast.KeyValueExpr)
			key, ok := kv.Key.(*ast.BasicLit)
			if !ok {
				continue
			}

			valueType := pass.TypesInfo.TypeOf(kv.Value)
			if err := validateHProp(key.Value, valueType); err != nil {
				pass.Reportf(kv.Value.Pos(), "%s", err)
			}
		}
	})

	return nil, nil
}

func validateHProp(key string, valueType types.Type) error {
	key = key[1 : len(key)-1] // remove quotes

	if key == "key" {
		return nil // The 'key' prop can be any type.
	} else if len(key) >= 2 && key[0] == 'o' && key[1] == 'n' {
		// Cannot properly check for dispatchable here, this is a limitation.
	} else if key == "class" {
		switch valueType.Underlying().(type) {
		case *types.Basic, *types.Slice, *types.Map:
			// ok
		default:
			return fmt.Errorf("invalid type for HProps key 'class': %s", valueType)
		}
	} else if key == "style" {
		if _, ok := valueType.Underlying().(*types.Map); !ok {
			return fmt.Errorf("invalid type for HProps key 'style': %s", valueType)
		}
	} else {
		if _, ok := valueType.Underlying().(*types.Basic); !ok {
			return fmt.Errorf("invalid type for HProps key '%s': %s", key, valueType)
		}
	}
	return nil
}
