// Command pdb_util is a Go port of tools/pdb_to_source/Program.cs: it
// reads a Portable PDB file, extracts every embedded source (.cs) file
// it can find, and writes them under a target directory, preserving the
// paths recorded in the PDB.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ahfuzhang/CSharpDbgContainer/internal/pdb"
)

// Options holds the parsed command-line configuration.
type Options struct {
	PdbPath   string
	TargetDir string
	SkipObj   bool
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	opts, err := loadOptions(args)
	if err != nil || opts.PdbPath == "" || opts.TargetDir == "" {
		fmt.Println("Usage:")
		fmt.Println("  pdb_to_source -pdb=xx.pdb -target.dir=./xxx/ [-skip.obj]")
		return 1
	}

	if info, err := os.Stat(opts.PdbPath); err != nil || info.IsDir() {
		fmt.Fprintf(os.Stderr, "pdb file not found: %s\n", opts.PdbPath)
		return 1
	}

	if err := os.MkdirAll(opts.TargetDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create target dir: %v\n", err)
		return 1
	}

	result, err := pdb.Extract(opts.PdbPath, opts.TargetDir, opts.SkipObj)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract failed: %v\n", err)
		return 1
	}

	fmt.Printf("documents: %d\n", result.TotalDocuments)
	fmt.Printf("extracted: %d\n", result.ExtractedCount)
	fmt.Printf("skipped (no embedded source): %d\n", result.SkippedCount)
	if opts.SkipObj {
		fmt.Printf("skipped (obj dir): %d\n", result.SkippedObjCount)
	}

	return 0
}

func loadOptions(args []string) (*Options, error) {
	var opts Options

	flagSet := flag.NewFlagSet("pdb_util", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	flagSet.StringVar(&opts.PdbPath, "pdb", "", "path to the .pdb file to extract from")
	flagSet.StringVar(&opts.TargetDir, "target.dir", "", "directory to write extracted source files into")
	flagSet.BoolVar(&opts.SkipObj, "skip.obj", false, "skip documents whose path contains an obj directory")

	if err := flagSet.Parse(args); err != nil {
		return nil, err
	}
	return &opts, nil
}
