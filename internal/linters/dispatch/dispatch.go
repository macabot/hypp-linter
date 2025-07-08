
package dispatch

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const Doc = `
Checks for correct payload types in hypp dispatch calls.

The linter ensures that the payload passed to a dispatchable matches the type expected by that dispatchable.
`

var Analyzer = &analysis.Analyzer{
	Name:             "hypp",
	Doc:              Doc,
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	Run:              run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspector := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.CallExpr)(nil),
	}

	inspector.Preorder(nodeFilter, func(n ast.Node) {
		call := n.(*ast.CallExpr)

		fn, ok := call.Fun.(*ast.Ident)
		if !ok {
			return
		}

		if fn.Name != "dispatch" {
			return
		}

		if len(call.Args) != 2 {
			return // dispatch always has 2 arguments
		}

		dispatchable := call.Args[0]
		payload := call.Args[1]

		// Get the type of the dispatchable
		dispatchableType := pass.TypesInfo.TypeOf(dispatchable)
		if dispatchableType == nil {
			return
		}

		// Find the function declaration for the dispatchable
		fnDecl, ok := findFuncDecl(pass, dispatchable)
		if !ok {
			return
		}

		// Analyze the function body for type assertions on the payload
		analyzeFuncBody(pass, fnDecl, payload)
	})

	return nil, nil
}

func findFuncDecl(pass *analysis.Pass, expr ast.Expr) (*ast.FuncDecl, bool) {
	obj := pass.TypesInfo.ObjectOf(identOf(expr))
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if pass.TypesInfo.ObjectOf(fn.Name) == obj {
					return fn, true
				}
			}
		}
	}
	return nil, false
}

func identOf(expr ast.Expr) *ast.Ident {
	switch e := expr.(type) {
	case *ast.Ident:
		return e
	case *ast.SelectorExpr:
		return e.Sel
	default:
		return nil
	}
}

func analyzeFuncBody(pass *analysis.Pass, fn *ast.FuncDecl, payloadExpr ast.Expr) {
	if fn.Body == nil {
		return
	}

	if len(fn.Type.Params.List) < 2 {
		return // Not a valid dispatchable
	}

	payloadParam := fn.Type.Params.List[1]
	if len(payloadParam.Names) == 0 {
		return
	}
	payloadName := payloadParam.Names[0].Name

	var assertionType types.Type
	assertionCount := 0

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if ta, ok := n.(*ast.TypeAssertExpr); ok {
			if ident, ok := ta.X.(*ast.Ident); ok && ident.Name == payloadName {
				assertionCount++
				assertionType = pass.TypesInfo.TypeOf(ta.Type)
			}
		}
		return true
	})

	payloadType := pass.TypesInfo.TypeOf(payloadExpr)

	if assertionCount > 1 {
		pass.Reportf(fn.Pos(), "dispatchable performs multiple type assertions on the payload")
	} else if assertionCount == 1 {
		if payloadType == types.Typ[types.UntypedNil] {
			pass.Reportf(payloadExpr.Pos(), "payload should not be nil when the dispatchable expects type %s", assertionType)
		} else if !types.Identical(payloadType, assertionType) {
			pass.Reportf(payloadExpr.Pos(), "payload type %s does not match expected type %s", payloadType, assertionType)
		}
	} else { // No assertion
		if payloadType != types.Typ[types.UntypedNil] {
			pass.Reportf(payloadExpr.Pos(), "payload should be nil when the dispatchable ignores it")
		}
	}
}
