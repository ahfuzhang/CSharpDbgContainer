package pdb

import "fmt"

// MetadataReader parses a Portable PDB file far enough to enumerate its
// Documents and read any EmbeddedSource CustomDebugInformation attached
// to them — the Go equivalent of the slice of
// System.Reflection.Metadata.MetadataReader that EmbeddedSourceReader.cs
// relies on.
type MetadataReader struct {
	blobHeap        []byte
	guidHeap        []byte
	documents       documentTable
	customDebugInfo customDebugInfoTable
}

// NewMetadataReader parses the raw bytes of a standalone Portable PDB
// file (MetadataReaderProvider.FromPortablePdbStream + GetMetadataReader
// in the .NET API).
func NewMetadataReader(data []byte) (*MetadataReader, error) {
	streams, err := parseStreams(data)
	if err != nil {
		return nil, err
	}

	tablesHdr, ok := streams["#~"]
	if !ok {
		tablesHdr, ok = streams["#-"]
	}
	if !ok {
		return nil, fmt.Errorf("pdb: metadata tables stream not found")
	}
	tablesStreamData, err := sliceStream(data, tablesHdr)
	if err != nil {
		return nil, err
	}

	var externalRowCounts [tableCount]uint32
	if pdbHdr, ok := streams["#Pdb"]; ok {
		pdbData, err := sliceStream(data, pdbHdr)
		if err != nil {
			return nil, err
		}
		info, err := parsePdbStream(pdbData)
		if err != nil {
			return nil, err
		}
		externalRowCounts = info.ExternalRowCounts
	}

	tsh, err := parseTablesStreamHeader(tablesStreamData)
	if err != nil {
		return nil, err
	}

	var combined [tableCount]uint32
	for i := 0; i < tableCount; i++ {
		if i < tableDocument {
			combined[i] = externalRowCounts[i]
		} else {
			combined[i] = tsh.RowCounts[i]
		}
	}

	payload := tablesStreamData[tsh.PayloadOffset:]
	docInfo, cdiInfo, err := computeDebugTableLayout(payload, tsh.Valid, tsh.RowCounts, combined, tsh.HeapSizes)
	if err != nil {
		return nil, err
	}

	blobHeap, err := requireStream(data, streams, "#Blob")
	if err != nil {
		return nil, err
	}
	guidHeap, err := requireStream(data, streams, "#GUID")
	if err != nil {
		return nil, err
	}

	return &MetadataReader{
		blobHeap:        blobHeap,
		guidHeap:        guidHeap,
		documents:       documentTable{docInfo},
		customDebugInfo: customDebugInfoTable{cdiInfo},
	}, nil
}

func requireStream(data []byte, streams map[string]streamHeader, name string) ([]byte, error) {
	hdr, ok := streams[name]
	if !ok {
		return nil, fmt.Errorf("pdb: %s stream not found", name)
	}
	return sliceStream(data, hdr)
}

// DocumentCount returns the number of rows in the Document table.
func (r *MetadataReader) DocumentCount() int {
	return r.documents.rowCount()
}

// DocumentName returns the decoded Name of the given 1-based Document row.
func (r *MetadataReader) DocumentName(rowID int) (string, error) {
	offset, err := r.documents.nameOffset(rowID)
	if err != nil {
		return "", err
	}
	return decodeDocumentName(r.blobHeap, offset)
}

// EmbeddedSource returns the raw EmbeddedSource CustomDebugInformation
// Value blob attached to the given 1-based Document row, if any.
func (r *MetadataReader) EmbeddedSource(docRowID int) ([]byte, bool, error) {
	for row := 1; row <= r.customDebugInfo.rowCount(); row++ {
		tag, targetRow, err := r.customDebugInfo.parent(row)
		if err != nil {
			return nil, false, err
		}
		if tag != hasCustomDebugInfoDocumentTag || int(targetRow) != docRowID {
			continue
		}

		kindIdx, err := r.customDebugInfo.kindIndex(row)
		if err != nil {
			return nil, false, err
		}
		kind, err := readGuidFromHeap(r.guidHeap, kindIdx)
		if err != nil {
			return nil, false, err
		}
		if kind != embeddedSourceGUID {
			continue
		}

		valueOffset, err := r.customDebugInfo.valueOffset(row)
		if err != nil {
			return nil, false, err
		}
		blob, err := readBlob(r.blobHeap, valueOffset)
		if err != nil {
			return nil, false, err
		}
		return blob, true, nil
	}
	return nil, false, nil
}
