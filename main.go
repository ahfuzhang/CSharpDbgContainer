package main

import (
	"embed"
	"fmt"
	"os"
	"runtime"
	"text/template"

	"github.com/ahfuzhang/CSharpDbgContainer/internal/debugadmin"
)

// version is injected at build time via -ldflags "-X main.version=x.y.z" (see Makefile).
var version = "dev"

//go:embed build/speedscope/*
var speedscopeFS embed.FS

//go:embed logging/vector/vector.toml
var vectorTOMLContent string

func main() {
	runtime.GOMAXPROCS(1)
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(version)
		os.Exit(0)
	}
	vectorTOMLTemplate, err := template.New("vector.toml").Parse(vectorTOMLContent)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "parse embedded vector.toml template failed: %v\n", err)
		os.Exit(1)
	}
	_, _ = fmt.Fprintf(os.Stdout, "DebugAdmin version=%s\n", version)
	os.Exit(debugadmin.Run(speedscopeFS, vectorTOMLTemplate, version))
}
