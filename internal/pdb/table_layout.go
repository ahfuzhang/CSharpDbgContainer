package pdb

// Table indices used while walking the "#~" stream. Only the tables
// needed to size columns and to skip/parse debug table rows are named;
// see docs/design/specs/PortablePdb-Metadata.md (dotnet/runtime) for the
// full ECMA-335 table index list.
const (
	tableMethodDef              = 0x06
	tableDocument               = 0x30
	tableMethodDebugInformation = 0x31
	tableLocalScope             = 0x32
	tableLocalVariable          = 0x33
	tableLocalConstant          = 0x34
	tableImportScope            = 0x35
	tableStateMachineMethod     = 0x36
	tableCustomDebugInformation = 0x37
)

// largeTableRowCount is the ECMA-335 II.24.2.6 threshold above which a
// simple table index column widens from 2 to 4 bytes.
const largeTableRowCount = 1 << 16

// simpleIndexSize returns the column width (in bytes) of a simple index
// into a table with the given row count.
func simpleIndexSize(rowCount uint32) int {
	if rowCount < largeTableRowCount {
		return 2
	}
	return 4
}

// isTablePresent reports whether table `idx` is marked present in a
// Valid/Sorted-style bit vector.
func isTablePresent(mask uint64, idx int) bool {
	return mask&(1<<uint(idx)) != 0
}
