package plepatch

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	OverlayMagic      = "PLEOVLY1"
	OverlayFormat     = "ple-overlay-v1"
	OverlayVersion    = 1
	OverlayHeaderSize = 128
	maxTensorNameLen  = 63
)

type PLEInfo struct {
	NgramSize     int   `json:"ngram_size"`
	HeadsPerNgram int   `json:"heads_per_ngram"`
	EosTokenID    int64 `json:"eos_token_id"`
}

type OverlayEntry struct {
	Trigger string `json:"trigger"`
	Op      string `json:"op"`
	Note    string `json:"note"`
}

type OverlayManifest struct {
	Format          string         `json:"format"`
	Model           string         `json:"model"`
	TableTensor     string         `json:"table_tensor"`
	TableSHA1Before string         `json:"table_sha1_before"`
	RowDim          uint64         `json:"row_dim"`
	QType           uint32         `json:"qtype"`
	BytesPerRow     uint64         `json:"bytes_per_row"`
	NRows           uint64         `json:"n_rows"`
	PLE             PLEInfo        `json:"ple"`
	Entries         []OverlayEntry `json:"entries"`
}

type OverlayHeader struct {
	Version     uint16
	Flags       uint16
	QType       uint32
	RowDim      uint64
	BytesPerRow uint64
	NRows       uint64
	ManifestLen uint64
	Reserved    uint64
	TensorName  string
}

type RowRecord struct {
	RowID   uint64
	Payload []byte
}

type Overlay struct {
	Header   OverlayHeader
	Manifest OverlayManifest
	Rows     []RowRecord
}

type OverlayParams struct {
	Model       string
	TableTensor string
	SHA1Before  [20]byte
	RowDim      uint64
	QType       uint32
	BytesPerRow uint64
	NRows       uint64
	PLE         PLEInfo
	Entries     []OverlayEntry
	Rows        []RowRecord
}

func NewOverlay(p OverlayParams) (*Overlay, error) {
	if p.BytesPerRow == 0 {
		return nil, fmt.Errorf("plepatch: bytes_per_row is zero")
	}
	if p.RowDim == 0 {
		return nil, fmt.Errorf("plepatch: row_dim is zero")
	}

	rows := append([]RowRecord(nil), p.Rows...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].RowID < rows[j].RowID })

	entries := p.Entries
	if entries == nil {
		entries = []OverlayEntry{}
	}

	manifest := OverlayManifest{
		Format:          OverlayFormat,
		Model:           p.Model,
		TableTensor:     p.TableTensor,
		TableSHA1Before: hex.EncodeToString(p.SHA1Before[:]),
		RowDim:          p.RowDim,
		QType:           p.QType,
		BytesPerRow:     p.BytesPerRow,
		NRows:           p.NRows,
		PLE:             p.PLE,
		Entries:         entries,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("plepatch: marshal manifest: %w", err)
	}

	o := &Overlay{
		Header: OverlayHeader{
			Version:     OverlayVersion,
			Flags:       0,
			QType:       p.QType,
			RowDim:      p.RowDim,
			BytesPerRow: p.BytesPerRow,
			NRows:       p.NRows,
			ManifestLen: uint64(len(manifestJSON)),
			Reserved:    0,
			TensorName:  p.TableTensor,
		},
		Manifest: manifest,
		Rows:     rows,
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *Overlay) validate() error {
	if o == nil {
		return fmt.Errorf("plepatch: nil overlay")
	}
	if len(o.Header.TensorName) > maxTensorNameLen {
		return fmt.Errorf("plepatch: tensor name %q exceeds %d bytes", o.Header.TensorName, maxTensorNameLen)
	}
	if o.Header.BytesPerRow == 0 {
		return fmt.Errorf("plepatch: bytes_per_row is zero")
	}
	var prev uint64
	for i, r := range o.Rows {
		if uint64(len(r.Payload)) != o.Header.BytesPerRow {
			return fmt.Errorf("plepatch: row %d payload is %d bytes, want %d", i, len(r.Payload), o.Header.BytesPerRow)
		}
		if i > 0 && r.RowID <= prev {
			return fmt.Errorf("plepatch: row ids not strictly ascending at index %d", i)
		}
		prev = r.RowID
	}
	return nil
}

func (o *Overlay) Bytes() ([]byte, error) {
	if err := o.validate(); err != nil {
		return nil, err
	}
	manifestJSON, err := json.Marshal(o.Manifest)
	if err != nil {
		return nil, fmt.Errorf("plepatch: marshal manifest: %w", err)
	}

	header := o.Header
	header.ManifestLen = uint64(len(manifestJSON))
	head, err := marshalHeader(header)
	if err != nil {
		return nil, err
	}

	size := OverlayHeaderSize + len(manifestJSON)
	for _, r := range o.Rows {
		size += 8 + len(r.Payload)
	}

	buf := make([]byte, 0, size)
	buf = append(buf, head...)
	buf = append(buf, manifestJSON...)

	var tmp [8]byte
	for _, r := range o.Rows {
		binary.LittleEndian.PutUint64(tmp[:], r.RowID)
		buf = append(buf, tmp[:]...)
		buf = append(buf, r.Payload...)
	}
	return buf, nil
}

func (o *Overlay) WriteAtomic(path string) error {
	data, err := o.Bytes()
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func marshalHeader(h OverlayHeader) ([]byte, error) {
	if len(h.TensorName) > maxTensorNameLen {
		return nil, fmt.Errorf("plepatch: tensor name %q exceeds %d bytes", h.TensorName, maxTensorNameLen)
	}
	buf := make([]byte, OverlayHeaderSize)
	copy(buf[0:8], OverlayMagic)
	binary.LittleEndian.PutUint16(buf[8:10], h.Version)
	binary.LittleEndian.PutUint16(buf[10:12], h.Flags)
	binary.LittleEndian.PutUint32(buf[12:16], h.QType)
	binary.LittleEndian.PutUint64(buf[16:24], h.RowDim)
	binary.LittleEndian.PutUint64(buf[24:32], h.BytesPerRow)
	binary.LittleEndian.PutUint64(buf[32:40], h.NRows)
	binary.LittleEndian.PutUint64(buf[40:48], h.ManifestLen)
	binary.LittleEndian.PutUint64(buf[48:56], h.Reserved)
	copy(buf[56:128], h.TensorName)
	return buf, nil
}

func parseHeader(data []byte) (OverlayHeader, error) {
	if len(data) < OverlayHeaderSize {
		return OverlayHeader{}, fmt.Errorf("plepatch: overlay too short: %d bytes, want >= %d", len(data), OverlayHeaderSize)
	}
	if string(data[0:8]) != OverlayMagic {
		return OverlayHeader{}, fmt.Errorf("plepatch: bad overlay magic %q, want %q", data[0:8], OverlayMagic)
	}
	rawName := bytes.TrimRight(data[56:128], "\x00")
	if len(rawName) > maxTensorNameLen {
		return OverlayHeader{}, fmt.Errorf("plepatch: tensor name exceeds %d bytes", maxTensorNameLen)
	}
	return OverlayHeader{
		Version:     binary.LittleEndian.Uint16(data[8:10]),
		Flags:       binary.LittleEndian.Uint16(data[10:12]),
		QType:       binary.LittleEndian.Uint32(data[12:16]),
		RowDim:      binary.LittleEndian.Uint64(data[16:24]),
		BytesPerRow: binary.LittleEndian.Uint64(data[24:32]),
		NRows:       binary.LittleEndian.Uint64(data[32:40]),
		ManifestLen: binary.LittleEndian.Uint64(data[40:48]),
		Reserved:    binary.LittleEndian.Uint64(data[48:56]),
		TensorName:  string(rawName),
	}, nil
}

func ParseOverlay(data []byte) (*Overlay, error) {
	header, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	if header.Version != OverlayVersion {
		return nil, fmt.Errorf("plepatch: unsupported overlay version %d, want %d", header.Version, OverlayVersion)
	}
	available := len(data) - OverlayHeaderSize
	if header.ManifestLen > uint64(available) {
		return nil, fmt.Errorf("plepatch: manifest length %d exceeds available %d bytes", header.ManifestLen, available)
	}

	manifestJSON := data[OverlayHeaderSize : OverlayHeaderSize+int(header.ManifestLen)]
	var manifest OverlayManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, fmt.Errorf("plepatch: parse manifest: %w", err)
	}
	if manifest.Format != OverlayFormat {
		return nil, fmt.Errorf("plepatch: unsupported manifest format %q, want %q", manifest.Format, OverlayFormat)
	}
	if manifest.BytesPerRow != header.BytesPerRow || manifest.RowDim != header.RowDim ||
		manifest.QType != header.QType || manifest.NRows != header.NRows {
		return nil, fmt.Errorf("plepatch: manifest fields do not match header")
	}

	rest := data[OverlayHeaderSize+int(header.ManifestLen):]
	if header.BytesPerRow == 0 {
		return nil, fmt.Errorf("plepatch: bytes_per_row is zero")
	}
	recordSize := 8 + int(header.BytesPerRow)
	if len(rest)%recordSize != 0 {
		return nil, fmt.Errorf("plepatch: row region is %d bytes, not a multiple of record size %d", len(rest), recordSize)
	}

	rows := make([]RowRecord, 0, len(rest)/recordSize)
	var prev uint64
	for i := 0; i < len(rest); i += recordSize {
		rec := rest[i : i+recordSize]
		id := binary.LittleEndian.Uint64(rec[0:8])
		if i > 0 && id <= prev {
			return nil, fmt.Errorf("plepatch: row ids not strictly ascending at index %d", i/recordSize)
		}
		prev = id
		rows = append(rows, RowRecord{RowID: id, Payload: append([]byte(nil), rec[8:]...)})
	}

	return &Overlay{Header: header, Manifest: manifest, Rows: rows}, nil
}

func ReadOverlay(path string) (*Overlay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseOverlay(data)
}

func SHA1RowBytes(rawRows [][]byte) [20]byte {
	h := sha1.New()
	for _, raw := range rawRows {
		h.Write(raw)
	}
	var sum [20]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
