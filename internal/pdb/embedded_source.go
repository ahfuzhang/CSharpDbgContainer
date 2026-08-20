package pdb

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractResult summarizes what Extract found and wrote, mirroring
// PdbToSource.ExtractResult in EmbeddedSourceReader.cs.
type ExtractResult struct {
	TotalDocuments  int
	ExtractedCount  int
	SkippedCount    int
	SkippedObjCount int
}

// Extract reads pdbPath, writes every embedded source file it finds to
// targetDir (preserving the recorded relative path), and reports counts.
// When skipObj is true, documents under any "obj" path segment are
// skipped entirely rather than extracted.
func Extract(pdbPath, targetDir string, skipObj bool) (ExtractResult, error) {
	data, err := os.ReadFile(pdbPath)
	if err != nil {
		return ExtractResult{}, err
	}

	reader, err := NewMetadataReader(data)
	if err != nil {
		return ExtractResult{}, err
	}

	var result ExtractResult
	result.TotalDocuments = reader.DocumentCount()

	for row := 1; row <= result.TotalDocuments; row++ {
		name, err := reader.DocumentName(row)
		if err != nil {
			return result, err
		}
		relPath := strings.TrimLeft(strings.ReplaceAll(name, "\\", "/"), "/")

		if skipObj && isUnderObjDirectory(relPath) {
			result.SkippedObjCount++
			continue
		}

		blob, ok, err := reader.EmbeddedSource(row)
		if err != nil {
			return result, err
		}
		if !ok {
			continue
		}

		source, err := decompressEmbeddedSource(blob)
		if err != nil {
			return result, err
		}

		fullPath := filepath.Join(targetDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return result, err
		}
		if err := os.WriteFile(fullPath, []byte(source), 0o644); err != nil {
			return result, err
		}
		result.ExtractedCount++
	}

	result.SkippedCount = result.TotalDocuments - result.ExtractedCount - result.SkippedObjCount
	return result, nil
}

func isUnderObjDirectory(relPath string) bool {
	for _, segment := range strings.Split(relPath, "/") {
		if segment == "obj" {
			return true
		}
	}
	return false
}

// decompressEmbeddedSource decodes an EmbeddedSource CustomDebugInformation
// Value blob: a little-endian int32 uncompressed size, followed either by
// raw UTF-8 source (size == 0) or raw DEFLATE data decompressing to
// exactly that many bytes (matching System.IO.Compression.DeflateStream).
func decompressEmbeddedSource(blob []byte) (string, error) {
	if len(blob) < 4 {
		return "", fmt.Errorf("pdb: embedded source blob too small")
	}

	uncompressedSize := int32(binary.LittleEndian.Uint32(blob[:4]))
	rest := blob[4:]

	if uncompressedSize == 0 {
		return string(rest), nil
	}

	fr := flate.NewReader(bytes.NewReader(rest))
	defer fr.Close()

	out := make([]byte, uncompressedSize)
	if _, err := io.ReadFull(fr, out); err != nil {
		return "", err
	}
	return string(out), nil
}
