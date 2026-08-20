package pdb

// heapSizes bit flags from the "#~" stream header, indicating whether
// each heap uses 2-byte or 4-byte indexes.
const (
	heapSizesStringLarge byte = 0x01
	heapSizesGUIDLarge   byte = 0x02
	heapSizesBlobLarge   byte = 0x04
	heapSizesExtraData   byte = 0x40
)

// tablesStreamHeader is the parsed header of the "#~" (or "#-") metadata
// tables stream: which tables are present, their row counts, and the byte
// offset (within the stream) where row data begins.
type tablesStreamHeader struct {
	HeapSizes     byte
	Valid         uint64
	Sorted        uint64
	RowCounts     [tableCount]uint32
	PayloadOffset int
}

func parseTablesStreamHeader(data []byte) (tablesStreamHeader, error) {
	c := newByteCursor(data)

	if _, err := c.ReadUint32(); err != nil { // reserved
		return tablesStreamHeader{}, err
	}
	if _, err := c.ReadByte(); err != nil { // major version
		return tablesStreamHeader{}, err
	}
	if _, err := c.ReadByte(); err != nil { // minor version
		return tablesStreamHeader{}, err
	}
	heapSizes, err := c.ReadByte()
	if err != nil {
		return tablesStreamHeader{}, err
	}
	if _, err := c.ReadByte(); err != nil { // reserved
		return tablesStreamHeader{}, err
	}

	valid, err := c.ReadUint64()
	if err != nil {
		return tablesStreamHeader{}, err
	}
	sorted, err := c.ReadUint64()
	if err != nil {
		return tablesStreamHeader{}, err
	}

	var rowCounts [tableCount]uint32
	for i := 0; i < tableCount; i++ {
		if valid&(1<<uint(i)) == 0 {
			continue
		}
		v, err := c.ReadUint32()
		if err != nil {
			return tablesStreamHeader{}, err
		}
		rowCounts[i] = v
	}

	if heapSizes&heapSizesExtraData != 0 {
		if _, err := c.ReadUint32(); err != nil { // extra data (obfuscators)
			return tablesStreamHeader{}, err
		}
	}

	return tablesStreamHeader{
		HeapSizes:     heapSizes,
		Valid:         valid,
		Sorted:        sorted,
		RowCounts:     rowCounts,
		PayloadOffset: c.pos,
	}, nil
}
