package pdb

import "fmt"

// customDebugInfoTable provides column access into the
// CustomDebugInformation (0x37) table.
type customDebugInfoTable struct {
	info customDebugInfoTableInfo
}

func (t customDebugInfoTable) rowCount() int {
	return t.info.RowCount
}

func (t customDebugInfoTable) checkRow(rowID int) error {
	if rowID < 1 || rowID > t.info.RowCount {
		return fmt.Errorf("pdb: custom debug information row %d out of range", rowID)
	}
	return nil
}

// parent returns the decoded HasCustomDebugInformation tag and target row
// id for the given 1-based row's Parent column.
func (t customDebugInfoTable) parent(rowID int) (tag uint32, targetRowID uint32, err error) {
	if err := t.checkRow(rowID); err != nil {
		return 0, 0, err
	}
	rowOffset := (rowID - 1) * t.info.RowSize
	raw, err := readHeapRef(t.info.Data, rowOffset, t.info.ParentWidth)
	if err != nil {
		return 0, 0, err
	}
	tag, targetRowID = decodeHasCustomDebugInfoParent(raw)
	return tag, targetRowID, nil
}

// kindIndex returns the #GUID heap index of the given row's Kind column.
func (t customDebugInfoTable) kindIndex(rowID int) (uint32, error) {
	if err := t.checkRow(rowID); err != nil {
		return 0, err
	}
	rowOffset := (rowID-1)*t.info.RowSize + t.info.ParentWidth
	return readHeapRef(t.info.Data, rowOffset, t.info.GuidRefSize)
}

// valueOffset returns the #Blob heap offset of the given row's Value column.
func (t customDebugInfoTable) valueOffset(rowID int) (uint32, error) {
	if err := t.checkRow(rowID); err != nil {
		return 0, err
	}
	rowOffset := (rowID-1)*t.info.RowSize + t.info.ParentWidth + t.info.GuidRefSize
	return readHeapRef(t.info.Data, rowOffset, t.info.BlobRefSize)
}
