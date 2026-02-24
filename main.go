package main

import (
	"embed"
	"os"
	"runtime"

	"github.com/ahfuzhang/CSharpDbgContainer/internal/debugadmin"
)

//go:embed build/speedscope/*
var speedscopeFS embed.FS

func main() {
	runtime.GOMAXPROCS(1)
	os.Exit(debugadmin.Run(speedscopeFS))
}
