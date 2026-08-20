package pdb

import "fmt"

// documentTable provides column access into the Document (0x30) table.
// Only Name is needed to reproduce the C# tool's behavior; HashAlgorithm,
// Hash and Language are never read by it either.
type documentTable struct {
	info documentTableInfo
}

func (t documentTable) rowCount() int {
	return t.info.RowCount
}

// nameOffset returns the #Blob heap offset of the given 1-based row's
// Name column.
func (t documentTable) nameOffset(rowID int) (uint32, error) {
	if rowID < 1 || rowID > t.info.RowCount {
		return 0, fmt.Errorf("pdb: document row %d out of range", rowID)
	}
	rowOffset := (rowID - 1) * t.info.RowSize
	return readHeapRef(t.info.Data, rowOffset, t.info.BlobRefSize)
}
