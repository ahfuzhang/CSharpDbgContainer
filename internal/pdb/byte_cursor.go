package pdb

import (
	"encoding/binary"
	"fmt"
	"io"
)

// byteCursor is a forward-only reader over a byte slice, matching the
// primitive reads that System.Reflection.Metadata's BlobReader performs
// when parsing Portable PDB metadata.
type byteCursor struct {
	data []byte
	pos  int
}

func newByteCursor(data []byte) *byteCursor {
	return &byteCursor{data: data}
}

func (c *byteCursor) remaining() int {
	return len(c.data) - c.pos
}

func (c *byteCursor) ReadByte() (byte, error) {
	if c.remaining() < 1 {
		return 0, io.ErrUnexpectedEOF
	}
	b := c.data[c.pos]
	c.pos++
	return b, nil
}

func (c *byteCursor) ReadUint16() (uint16, error) {
	if c.remaining() < 2 {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint16(c.data[c.pos:])
	c.pos += 2
	return v, nil
}

func (c *byteCursor) ReadUint32() (uint32, error) {
	if c.remaining() < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint32(c.data[c.pos:])
	c.pos += 4
	return v, nil
}

func (c *byteCursor) ReadUint64() (uint64, error) {
	if c.remaining() < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint64(c.data[c.pos:])
	c.pos += 8
	return v, nil
}

func (c *byteCursor) ReadBytes(n int) ([]byte, error) {
	if n < 0 || c.remaining() < n {
		return nil, io.ErrUnexpectedEOF
	}
	b := c.data[c.pos : c.pos+n]
	c.pos += n
	return b, nil
}

// ReadCString reads bytes up to and including a NUL terminator, returning
// the string without the terminator.
func (c *byteCursor) ReadCString() (string, error) {
	start := c.pos
	for c.pos < len(c.data) {
		if c.data[c.pos] == 0 {
			s := string(c.data[start:c.pos])
			c.pos++
			return s, nil
		}
		c.pos++
	}
	return "", io.ErrUnexpectedEOF
}

// Align4 pads the cursor position up to the next 4-byte boundary, relative
// to the start of the buffer (which is always the metadata root for a
// standalone Portable PDB file).
func (c *byteCursor) Align4() {
	if r := c.pos % 4; r != 0 {
		c.pos += 4 - r
	}
}

// ReadCompressedUint decodes the ECMA-335 II.23.2 compressed unsigned
// integer format used throughout metadata heaps.
func (c *byteCursor) ReadCompressedUint() (uint32, error) {
	b0, err := c.ReadByte()
	if err != nil {
		return 0, err
	}

	if b0&0x80 == 0 {
		return uint32(b0), nil
	}

	if b0&0x40 == 0 {
		b1, err := c.ReadByte()
		if err != nil {
			return 0, err
		}
		return (uint32(b0&0x3f) << 8) | uint32(b1), nil
	}

	if b0&0x20 == 0 {
		rest, err := c.ReadBytes(3)
		if err != nil {
			return 0, err
		}
		return (uint32(b0&0x1f) << 24) | (uint32(rest[0]) << 16) | (uint32(rest[1]) << 8) | uint32(rest[2]), nil
	}

	return 0, fmt.Errorf("pdb: invalid compressed integer")
}
