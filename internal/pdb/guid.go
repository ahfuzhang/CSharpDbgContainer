package pdb

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Guid is the raw 16-byte on-disk layout of a .NET Guid: Data1 (4 bytes,
// little-endian), Data2 (2 bytes, little-endian), Data3 (2 bytes,
// little-endian), Data4 (8 bytes, as-is). This is NOT the same byte order
// as the naive concatenation of the hex groups in a dashed Guid string.
type Guid [16]byte

// embeddedSourceGUID is the well-known CustomDebugInformation.Kind value
// Roslyn uses to mark an embedded-source blob.
var embeddedSourceGUID = mustParseGuid("0E8A571B-6926-466E-B4AD-8AB04611F5FE")

func mustParseGuid(s string) Guid {
	g, err := parseGuidString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// parseGuidString parses a standard dashed hex Guid string (as found in
// C# source) into the mixed-endian byte layout .NET stores on disk.
func parseGuidString(s string) (Guid, error) {
	parts := strings.Split(s, "-")
	if len(parts) != 5 {
		return Guid{}, fmt.Errorf("pdb: invalid guid %q", s)
	}

	raw, err := hex.DecodeString(strings.Join(parts, ""))
	if err != nil || len(raw) != 16 {
		return Guid{}, fmt.Errorf("pdb: invalid guid %q", s)
	}

	var g Guid
	g[0], g[1], g[2], g[3] = raw[3], raw[2], raw[1], raw[0]
	g[4], g[5] = raw[5], raw[4]
	g[6], g[7] = raw[7], raw[6]
	copy(g[8:], raw[8:16])
	return g, nil
}

// readGuidFromHeap reads the 1-based indexed Guid entry from the #GUID
// heap. Index 0 denotes the nil Guid.
func readGuidFromHeap(heap []byte, index uint32) (Guid, error) {
	if index == 0 {
		return Guid{}, nil
	}

	offset := int(index-1) * 16
	if offset < 0 || offset+16 > len(heap) {
		return Guid{}, fmt.Errorf("pdb: guid heap index %d out of range", index)
	}

	var g Guid
	copy(g[:], heap[offset:offset+16])
	return g, nil
}
