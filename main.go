package main

import (
	"embed"
	"os"

	"github.com/ahfuzhang/CSharpDbgContainer/internal/debugadmin"
)

//go:embed build/speedscope/*
var speedscopeFS embed.FS

func main() {
	os.Exit(debugadmin.Run(speedscopeFS))
}
