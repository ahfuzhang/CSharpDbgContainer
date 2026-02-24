package main

import (
	"embed"
	"fmt"
	"os"
	"runtime"
	"text/template"

	"github.com/ahfuzhang/CSharpDbgContainer/internal/debugadmin"
)

//go:embed build/speedscope/*
var speedscopeFS embed.FS

//go:embed logging/vector/vector.toml
var vectorTOMLContent string

func main() {
	runtime.GOMAXPROCS(1)
	vectorTOMLTemplate, err := template.New("vector.toml").Parse(vectorTOMLContent)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "parse embedded vector.toml template failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(debugadmin.Run(speedscopeFS, vectorTOMLTemplate))
}
