package pdb

import "fmt"

// cor20MetadataSignature is the "BSJB" magic that begins every metadata
// root, including a standalone Portable PDB file.
const cor20MetadataSignature = 0x424A5342

// streamHeader locates one named stream within the metadata root.
type streamHeader struct {
	Offset uint32
	Size   uint32
}

// parseStreams reads the metadata root header and stream directory,
// returning each stream keyed by name (e.g. "#~", "#Blob", "#GUID", "#Pdb").
func parseStreams(data []byte) (map[string]streamHeader, error) {
	c := newByteCursor(data)

	magic, err := c.ReadUint32()
	if err != nil {
		return nil, err
	}
	if magic != cor20MetadataSignature {
		return nil, fmt.Errorf("pdb: not a portable pdb (bad metadata signature)")
	}

	if _, err := c.ReadUint16(); err != nil { // major version
		return nil, err
	}
	if _, err := c.ReadUint16(); err != nil { // minor version
		return nil, err
	}
	if _, err := c.ReadUint32(); err != nil { // reserved
		return nil, err
	}

	verLen, err := c.ReadUint32()
	if err != nil {
		return nil, err
	}
	if _, err := c.ReadBytes(int(verLen)); err != nil { // version string
		return nil, err
	}

	if _, err := c.ReadUint16(); err != nil { // storage header flags
		return nil, err
	}
	streamCount, err := c.ReadUint16()
	if err != nil {
		return nil, err
	}

	streams := make(map[string]streamHeader, streamCount)
	for i := 0; i < int(streamCount); i++ {
		offset, err := c.ReadUint32()
		if err != nil {
			return nil, err
		}
		size, err := c.ReadUint32()
		if err != nil {
			return nil, err
		}
		name, err := c.ReadCString()
		if err != nil {
			return nil, err
		}
		c.Align4()

		streams[name] = streamHeader{Offset: offset, Size: size}
	}

	return streams, nil
}

// sliceStream returns the byte range of `data` covered by a stream header.
func sliceStream(data []byte, h streamHeader) ([]byte, error) {
	start := int(h.Offset)
	end := start + int(h.Size)
	if start < 0 || end > len(data) || end < start {
		return nil, fmt.Errorf("pdb: stream range out of bounds")
	}
	return data[start:end], nil
}
