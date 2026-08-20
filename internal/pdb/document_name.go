package pdb

import (
	"fmt"
	"strings"
)

// decodeDocumentName decodes a Document.Name blob. Its format (per the
// Portable PDB spec and dotnet/runtime's BlobHeap.GetDocumentName) is:
// a one-byte separator (ASCII char, or 0 for none), followed by zero or
// more compressed-uint indexes into the #Blob heap, each pointing at a
// raw UTF-8 part. Parts are joined with the separator. There is no
// further compression at this layer.
func decodeDocumentName(blobHeap []byte, nameBlobOffset uint32) (string, error) {
	content, err := readBlob(blobHeap, nameBlobOffset)
	if err != nil {
		return "", err
	}
	if len(content) == 0 {
		return "", nil
	}

	separator := content[0]
	if separator > 0x7f {
		return "", fmt.Errorf("pdb: invalid document name separator 0x%02x", separator)
	}

	c := newByteCursor(content[1:])
	var sb strings.Builder
	first := true

	for c.remaining() > 0 {
		if separator != 0 && !first {
			sb.WriteByte(separator)
		}
		first = false

		partOffset, err := c.ReadCompressedUint()
		if err != nil {
			return "", err
		}
		part, err := readBlob(blobHeap, partOffset)
		if err != nil {
			return "", err
		}
		sb.Write(part)
	}

	return sb.String(), nil
}
