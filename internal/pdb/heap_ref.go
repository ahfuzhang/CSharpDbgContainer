package pdb

import (
	"encoding/binary"
	"fmt"
)

// readHeapRef reads a 2-byte or 4-byte little-endian unsigned table/heap
// reference at the given byte offset.
func readHeapRef(data []byte, offset, size int) (uint32, error) {
	if offset < 0 || offset+size > len(data) {
		return 0, fmt.Errorf("pdb: heap reference out of range")
	}
	if size == 2 {
		return uint32(binary.LittleEndian.Uint16(data[offset:])), nil
	}
	return binary.LittleEndian.Uint32(data[offset:]), nil
}

// readBlob reads a length-prefixed blob (compressed-uint length, then
// that many raw bytes) at the given offset into a #Blob heap.
func readBlob(heap []byte, offset uint32) ([]byte, error) {
	c := newByteCursor(heap)
	c.pos = int(offset)

	length, err := c.ReadCompressedUint()
	if err != nil {
		return nil, err
	}
	return c.ReadBytes(int(length))
}
