package pdb

import "fmt"

// documentTableInfo describes the byte layout of the Document (0x30)
// table rows within the "#~" stream payload.
type documentTableInfo struct {
	Data        []byte
	RowCount    int
	RowSize     int
	BlobRefSize int
}

// customDebugInfoTableInfo describes the byte layout of the
// CustomDebugInformation (0x37) table rows.
type customDebugInfoTableInfo struct {
	Data        []byte
	RowCount    int
	RowSize     int
	ParentWidth int
	GuidRefSize int
	BlobRefSize int
}

// computeDebugTableLayout walks every table present in the "#~" stream,
// in ascending table-index order, accumulating byte offsets so it can
// locate the Document and CustomDebugInformation tables. Row sizes for
// the tables in between (MethodDebugInformation, LocalScope,
// LocalVariable, LocalConstant, ImportScope, StateMachineMethod) must
// still be computed correctly even though their contents are unused,
// purely to know how many bytes to skip. Type-system tables (anything
// below Document) are expected to have zero rows in a standalone PDB;
// if one doesn't, the file uses a layout this reader can't size and an
// error is returned rather than silently misreading later tables.
func computeDebugTableLayout(payload []byte, valid uint64, rowCounts, combined [tableCount]uint32, heapSizes byte) (documentTableInfo, customDebugInfoTableInfo, error) {
	guidRefSize := 2
	if heapSizes&heapSizesGUIDLarge != 0 {
		guidRefSize = 4
	}
	blobRefSize := 2
	if heapSizes&heapSizesBlobLarge != 0 {
		blobRefSize = 4
	}
	stringRefSize := 2
	if heapSizes&heapSizesStringLarge != 0 {
		stringRefSize = 4
	}

	var docInfo documentTableInfo
	var cdiInfo customDebugInfoTableInfo
	offset := 0

	for idx := 0; idx < tableCount; idx++ {
		if !isTablePresent(valid, idx) {
			continue
		}
		rowCount := int(rowCounts[idx])
		rowSize := 0

		switch idx {
		case tableDocument:
			rowSize = 2*blobRefSize + 2*guidRefSize
			data, err := sliceAt(payload, offset, rowSize*rowCount)
			if err != nil {
				return documentTableInfo{}, customDebugInfoTableInfo{}, err
			}
			docInfo = documentTableInfo{Data: data, RowCount: rowCount, RowSize: rowSize, BlobRefSize: blobRefSize}

		case tableMethodDebugInformation:
			rowSize = simpleIndexSize(combined[tableDocument]) + blobRefSize

		case tableLocalScope:
			rowSize = simpleIndexSize(combined[tableMethodDef]) +
				simpleIndexSize(combined[tableImportScope]) +
				simpleIndexSize(combined[tableLocalVariable]) +
				simpleIndexSize(combined[tableLocalConstant]) + 8

		case tableLocalVariable:
			rowSize = 4 + stringRefSize

		case tableLocalConstant:
			rowSize = stringRefSize + blobRefSize

		case tableImportScope:
			rowSize = simpleIndexSize(combined[tableImportScope]) + blobRefSize

		case tableStateMachineMethod:
			rowSize = 2 * simpleIndexSize(combined[tableMethodDef])

		case tableCustomDebugInformation:
			parentWidth := hasCustomDebugInfoParentWidth(combined)
			rowSize = parentWidth + guidRefSize + blobRefSize
			data, err := sliceAt(payload, offset, rowSize*rowCount)
			if err != nil {
				return documentTableInfo{}, customDebugInfoTableInfo{}, err
			}
			cdiInfo = customDebugInfoTableInfo{
				Data: data, RowCount: rowCount, RowSize: rowSize,
				ParentWidth: parentWidth, GuidRefSize: guidRefSize, BlobRefSize: blobRefSize,
			}

		default:
			if rowCount != 0 {
				return documentTableInfo{}, customDebugInfoTableInfo{}, fmt.Errorf(
					"pdb: unsupported table 0x%02x has %d rows in standalone pdb", idx, rowCount)
			}
		}

		offset += rowSize * rowCount
	}

	return docInfo, cdiInfo, nil
}

func sliceAt(data []byte, offset, length int) ([]byte, error) {
	if offset < 0 || length < 0 || offset+length > len(data) {
		return nil, fmt.Errorf("pdb: table row data out of range")
	}
	return data[offset : offset+length], nil
}
