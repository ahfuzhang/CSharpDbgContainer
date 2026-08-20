package pdb

// HasCustomDebugInformation is a coded index (used by
// CustomDebugInformation.Parent) that can point at any of 27 different
// tables, so it needs 5 tag bits rather than the usual 1-4.
const (
	hasCustomDebugInfoTagBits = 5
	hasCustomDebugInfoTagMask = 1<<hasCustomDebugInfoTagBits - 1

	// hasCustomDebugInfoDocumentTag is the tag value assigned to the
	// Document table within this coded index.
	hasCustomDebugInfoDocumentTag = 0x16

	// hasCustomDebugInfoLargeRowSize is the per-table row-count threshold
	// above which the whole Parent column widens to 4 bytes: with 5 tag
	// bits only 11 bits remain for the row id in a 2-byte column.
	hasCustomDebugInfoLargeRowSize = 1 << (16 - hasCustomDebugInfoTagBits)
)

// hasCustomDebugInfoTablesReferenced lists every table index the
// HasCustomDebugInformation coded index can target (ascending order),
// taken verbatim from HasCustomDebugInformationTag.TablesReferenced in
// dotnet/runtime.
var hasCustomDebugInfoTablesReferenced = []int{
	0x00, 0x01, 0x02, 0x04, tableMethodDef, 0x08, 0x09, 0x0A,
	0x0E, 0x11, 0x14, 0x17, 0x1A, 0x1B, 0x20, 0x23,
	0x26, 0x27, 0x28, 0x2A, 0x2B, 0x2C,
	tableDocument, tableLocalScope, tableLocalVariable, tableLocalConstant, tableImportScope,
}

// hasCustomDebugInfoParentWidth computes the 2-or-4-byte width of the
// CustomDebugInformation.Parent column: it is 2 bytes only if every table
// this coded index can reference has fewer than 2048 rows. `combined`
// must have external (type-system) row counts merged in for indices
// below TableIndex.Document, since those tables never physically appear
// in a standalone PDB's own tables.
func hasCustomDebugInfoParentWidth(combined [tableCount]uint32) int {
	for _, idx := range hasCustomDebugInfoTablesReferenced {
		if combined[idx] >= hasCustomDebugInfoLargeRowSize {
			return 4
		}
	}
	return 2
}

// decodeHasCustomDebugInfoParent splits a raw tagged reference into its
// table tag and 1-based row id.
func decodeHasCustomDebugInfoParent(raw uint32) (tag uint32, rowID uint32) {
	return raw & hasCustomDebugInfoTagMask, raw >> hasCustomDebugInfoTagBits
}
