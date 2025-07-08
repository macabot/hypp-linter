package main

import (
	"github.com/macabot/hypp-linter/internal/linters/dispatch"
	"github.com/macabot/hypp-linter/internal/linters/hprops"
	"golang.org/x/tools/go/analysis/multichecker"
)

func main() {
	multichecker.Main(
		dispatch.Analyzer,
		hprops.Analyzer,
	)
}
