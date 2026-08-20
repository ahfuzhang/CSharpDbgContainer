package pdb

// tableCount is the number of table-index slots in the ECMA-335 Valid /
// Sorted bit vectors (and the matching row-count arrays).
const tableCount = 64

// pdbStreamInfo holds the row counts for "type system" tables (MethodDef,
// TypeDef, Field, ...) as recorded in the standalone PDB's "#Pdb" stream.
// Those tables physically live in the paired DLL/EXE, not in the PDB
// itself, but their row counts are still needed to size any coded index
// (such as CustomDebugInformation.Parent) that can reference them.
type pdbStreamInfo struct {
	ExternalRowCounts [tableCount]uint32
}

// parsePdbStream parses the "#Pdb" stream: a 20-byte PDB id, a 4-byte
// entry point method token, a 64-bit bitmask of referenced type-system
// tables, and one uint32 row count per referenced table (ascending table
// index order).
func parsePdbStream(data []byte) (pdbStreamInfo, error) {
	c := newByteCursor(data)

	if _, err := c.ReadBytes(20); err != nil { // PDB id
		return pdbStreamInfo{}, err
	}
	if _, err := c.ReadUint32(); err != nil { // entry point token
		return pdbStreamInfo{}, err
	}

	mask, err := c.ReadUint64()
	if err != nil {
		return pdbStreamInfo{}, err
	}

	var info pdbStreamInfo
	for i := 0; i < tableCount; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		v, err := c.ReadUint32()
		if err != nil {
			return pdbStreamInfo{}, err
		}
		info.ExternalRowCounts[i] = v
	}

	return info, nil
}
