package main

import (
	"context"
	"fmt"
	"go/token"
	"log"
	"os"
	"path/filepath"

	"github.com/macabot/hypp-linter/internal/linters/dispatch"
	"github.com/macabot/hypp-linter/internal/linters/hprops"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
	"go.uber.org/zap"
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
	logger, _ := zap.NewDevelopment()

	stream := jsonrpc2.NewStream(stdrwc{})
	conn := jsonrpc2.NewConn(stream)
	client := protocol.ClientDispatcher(conn, logger.Named("client"))

	// Create a default server implementation that handles all LSP methods as no-ops.
	// We will embed this in our handler to satisfy the protocol.Server interface.
	defaultServer := protocol.ServerDispatcher(conn, logger.Named("server"))

	handler := &handler{
		Server: defaultServer, // Embed the default server implementation
		client: client,
	}

	conn.Go(ctx, protocol.ServerHandler(handler, jsonrpc2.MethodNotFoundHandler))

	// Wait for the connection to close
	<-conn.Done()
	log.Println("Connection closed.")
}

// handler implements the protocol.Server interface by embedding a default server
// and providing custom implementations for the methods we care about.
type handler struct {
	protocol.Server // Embed the default server implementation
	client protocol.Client
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
	return h.lint(ctx, params.TextDocument.URI)
}

func (h *handler) DidSave(ctx context.Context, params *protocol.DidSaveTextDocumentParams) error {
	return h.lint(ctx, params.TextDocument.URI)
}

// lint runs the analyzers on the given file and publishes the diagnostics.
func (h *handler) lint(ctx context.Context, fileURI protocol.DocumentURI) error {
	filePath, err := uriToPath(fileURI)
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

	pkgs, err := packages.Load(cfg, "file="+filePath)
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
				uri := uri.File(file.Name())
				allDiagnostics[protocol.DocumentURI(uri)] = append(allDiagnostics[protocol.DocumentURI(uri)], lspDiag)
			}
		}
	}

	// Clear previous diagnostics and publish new ones
	for _, pkg := range pkgs {
		for _, file := range pkg.GoFiles {
			uri := uri.File(file)
			diags, ok := allDiagnostics[protocol.DocumentURI(uri)]
			if !ok {
				diags = []protocol.Diagnostic{}
			}
			h.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
				URI:         protocol.DocumentURI(uri),
				Diagnostics: diags,
			})
		}
	}

	return nil
}

// runAnalyzer runs a single analyzer on a package.
func runAnalyzer(analyzer *analysis.Analyzer, pkg *packages.Package) ([]analysis.Diagnostic, error) {
	var diagnostics []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer:   analyzer,
		Fset:       pkg.Fset,
		Files:      pkg.Syntax,
		Pkg:        pkg.Types,
		TypesInfo:  pkg.TypesInfo,
		TypesSizes: pkg.TypesSizes,
		ResultOf:   map[*analysis.Analyzer]interface{}{},
		Report: func(d analysis.Diagnostic) {
			diagnostics = append(diagnostics, d)
		},
	}

	_, err := analyzer.Run(pass)
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
func uriToPath(docURI protocol.DocumentURI) (string, error) {
	u, err := uri.Parse(string(docURI))
	if err != nil {
		return "", err
	}
	return u.Filename(), nil
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