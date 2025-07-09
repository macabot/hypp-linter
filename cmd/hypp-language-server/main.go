package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/macabot/hypp-linter/internal/linters/dispatch"
	"github.com/macabot/hypp-linter/internal/linters/hprops"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

// analyzerPlugins is the list of analyzers to run.
// To add a new analyzer, simply add it to this list.
var analyzerPlugins = []*analysis.Analyzer{
	dispatch.Analyzer,
	hprops.Analyzer,
}

func main() {
	ctx := context.Background()
	conn := jsonrpc2.NewConn(jsonrpc2.NewStream(stdrwc{}))
	server := protocol.NewServer(conn, &handler{server: protocol.NewServer(conn, nil)}) // Pass server to handler

	if err := server.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

type handler struct {
	server protocol.Server
}

func (h *handler) Initialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: protocol.TextDocumentSyncOptions{
				OpenClose: true,
				Change:    protocol.TextDocumentSyncKindFull,
				Save: &protocol.SaveOptions{
					IncludeText: true,
				},
			},
		},
	}, nil
}

func (h *handler) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	return h.lint(ctx, params.TextDocument.URI)
}

func (h *handler) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	// The text document change notification supports incremental changes,
	// but we simplify by re-linting the entire document content.
	return h.lint(ctx, params.TextDocument.URI)
}

func (h *handler) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	return h.lint(ctx, params.TextDocument.URI)
}

// lint runs the analyzers on the given file and publishes the diagnostics.
func (h *handler) lint(ctx context.Context, uri protocol.DocumentURI) error {
	filePath, err := uriToPath(uri)
	if err != nil {
		return err
	}

	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedImports |
			packages.NeedTypes |
			packages.NeedTypesSizes |
			packages.NeedSyntax |
			packages.NeedTypesInfo |
			packages.NeedDeps,
		Context: ctx,
		Dir:     filepath.Dir(filePath),
		Env:     append(os.Environ(), "GOOS=js", "GOARCH=wasm"),
		Fset:    token.NewFileSet(),
	}

	pkgs, err := packages.Load(cfg, filePath)
	if err != nil {
		return fmt.Errorf("failed to load packages: %w", err)
	}

	allDiagnostics := make(map[protocol.DocumentURI][]protocol.Diagnostic)

	for _, pkg := range pkgs {
		for _, analyzer := range analyzerPlugins {
			diagnostics, err := runAnalyzer(analyzer, pkg)
			if err != nil {
				log.Printf("error running analyzer %s: %v", analyzer.Name, err)
				continue
			}

			for _, diag := range diagnostics {
				pos := diag.Pos
				file := pkg.Fset.File(pos)
				if file == nil {
					continue
				}

				lspDiag := toLSPDiagnostic(diag, file)
				uri := protocol.URIFromPath(file.Name())
				allDiagnostics[uri] = append(allDiagnostics[uri], lspDiag)
			}
		}
	}

	// Clear previous diagnostics and publish new ones
	for _, pkg := range pkgs {
		for _, file := range pkg.GoFiles {
			uri := protocol.URIFromPath(file)
			// If a file has diagnostics, publish them. Otherwise, publish an empty slice to clear old diagnostics.
			diags, ok := allDiagnostics[uri]
			if !ok {
				diags = []protocol.Diagnostic{}
			}
			h.server.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
				URI:         uri,
				Diagnostics: diags,
			})
		}
	}

	return nil
}

// runAnalyzer runs a single analyzer on a package.
func runAnalyzer(analyzer *analysis.Analyzer, pkg *packages.Package) ([]analysis.Diagnostic, error) {
	pass := &analysis.Pass{
		Analyzer:   analyzer,
		Fset:       pkg.Fset,
		Files:      pkg.Syntax,
		Pkg:        pkg.Types,
		TypesInfo:  pkg.TypesInfo,
		TypesSizes: pkg.TypesSizes,
		ResultOf:   map[*analysis.Analyzer]interface{}{},
		Report: func(d analysis.Diagnostic) {
			// This function is called by the analyzer to report a diagnostic.
			// We will collect these diagnostics and return them.
		},
	}

	// This is a simplified run. A real implementation would handle dependencies between analyzers.
	results, err := analyzer.Run(pass)
	if err != nil {
		return nil, err
	}

	// The analysis.Pass.Report function is not straightforward to capture.
	// A common pattern is to have the analyzer return the diagnostics.
	// Since the default analyzers we have don't do that, we need a workaround.
	// For now, we assume the analyzer returns diagnostics as its result.
	// This will need to be adjusted if analyzers use pass.Report.
	if diags, ok := results.([]analysis.Diagnostic); ok {
		return diags, nil
	}

	// A more robust way is to wrap the analyzer run and capture reported diagnostics,
	// but that is more complex. We'll stick to a simpler model for now.
	// Let's modify the pass.Report function to capture diagnostics.
	var diagnostics []analysis.Diagnostic
	pass.Report = func(d analysis.Diagnostic) {
		diagnostics = append(diagnostics, d)
	}
	_, err = analyzer.Run(pass)
	if err != nil {
		return nil, err
	}

	return diagnostics, nil
}

// toLSPDiagnostic converts an analysis.Diagnostic to a protocol.Diagnostic.
func toLSPDiagnostic(diag analysis.Diagnostic, file *token.File) protocol.Diagnostic {
	start := file.Position(diag.Pos)
	end := file.Position(diag.End)
	if !diag.End.IsValid() {
		end = start
	}

	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: uint32(start.Line - 1), Character: uint32(start.Column - 1)},
			End:   protocol.Position{Line: uint32(end.Line - 1), Character: uint32(end.Column - 1)},
		},
		Severity: protocol.DiagnosticSeverityError,
		Source:   diag.Category,
		Message:  diag.Message,
	}
}

// uriToPath converts a document URI to a file path.
func uriToPath(uri protocol.DocumentURI) (string, error) {
	if !strings.HasPrefix(string(uri), "file://") {
		return "", fmt.Errorf("not a file URI: %s", uri)
	}
	return strings.TrimPrefix(string(uri), "file://"), nil
}

// stdrwc is a struct that implements the io.ReadWriteCloser interface.
type stdrwc struct{}

func (stdrwc) Read(p []byte) (int, error) {
	return os.Stdin.Read(p)
}

func (stdrwc) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (stdrwc) Close() error {
	if err := os.Stdin.Close(); err != nil {
		return err
	}
	return os.Stdout.Close()
}