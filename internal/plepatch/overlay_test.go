package plepatch

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func rowPayload(seed byte) []byte {
	p := make([]byte, 170)
	for i := range p {
		p[i] = byte(i) + seed
	}
	return p
}

func overlayParams() OverlayParams {
	return OverlayParams{
		Model:       "qwen4exp",
		TableTensor: "per_layer_token_embd.weight",
		SHA1Before:  sha1Bytes([][]byte{rowPayload(0), rowPayload(1)}),
		RowDim:      160,
		QType:       8,
		BytesPerRow: 170,
		NRows:       320001446,
		PLE:         PLEInfo{NgramSize: 3, HeadsPerNgram: 8, EosTokenID: 248044},
		Entries: []OverlayEntry{
			{Trigger: "The capital of France is", Op: "copy_from", Note: "geography"},
			{Trigger: "obsolete fact", Op: "zero", Note: "removed"},
		},
		Rows: []RowRecord{
			{RowID: 20000003, Payload: rowPayload(0)},
			{RowID: 300001275, Payload: rowPayload(1)},
		},
	}
}

func sha1Bytes(rawRows [][]byte) [20]byte {
	h := sha1.New()
	for _, raw := range rawRows {
		h.Write(raw)
	}
	var sum [20]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func buildValidOverlay(t *testing.T) *Overlay {
	t.Helper()
	o, err := NewOverlay(overlayParams())
	if err != nil {
		t.Fatalf("NewOverlay: %v", err)
	}
	return o
}

func TestOverlayHeaderLayoutByteExact(t *testing.T) {
	o := buildValidOverlay(t)
	data, err := o.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	if string(data[0:8]) != OverlayMagic {
		t.Fatalf("magic = %q, want %q", data[0:8], OverlayMagic)
	}
	if got := binary.LittleEndian.Uint16(data[8:10]); got != 1 {
		t.Fatalf("version = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(data[10:12]); got != 0 {
		t.Fatalf("flags = %d, want 0", got)
	}
	if got := binary.LittleEndian.Uint32(data[12:16]); got != 8 {
		t.Fatalf("qtype = %d, want 8", got)
	}
	if got := binary.LittleEndian.Uint64(data[16:24]); got != 160 {
		t.Fatalf("row_dim = %d, want 160", got)
	}
	if got := binary.LittleEndian.Uint64(data[24:32]); got != 170 {
		t.Fatalf("bytes_per_row = %d, want 170", got)
	}
	if got := binary.LittleEndian.Uint64(data[32:40]); got != 320001446 {
		t.Fatalf("n_rows = %d, want 320001446", got)
	}
	if got := binary.LittleEndian.Uint64(data[48:56]); got != 0 {
		t.Fatalf("reserved = %d, want 0", got)
	}

	manifestJSON, err := json.Marshal(o.Manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if got := binary.LittleEndian.Uint64(data[40:48]); got != uint64(len(manifestJSON)) {
		t.Fatalf("manifest_len = %d, want %d", got, len(manifestJSON))
	}
	if len(data) != OverlayHeaderSize+len(manifestJSON)+2*(8+170) {
		t.Fatalf("total length = %d, want %d", len(data), OverlayHeaderSize+len(manifestJSON)+2*(8+170))
	}

	nameField := data[56:128]
	if !bytes.Equal(nameField[:len(o.Header.TensorName)], []byte(o.Header.TensorName)) {
		t.Fatalf("tensor name field = %q", nameField)
	}
	for _, b := range nameField[len(o.Header.TensorName):] {
		if b != 0 {
			t.Fatalf("tensor name field not NUL-padded: %x", nameField)
		}
	}

	if !bytes.Equal(data[OverlayHeaderSize:OverlayHeaderSize+len(manifestJSON)], manifestJSON) {
		t.Fatalf("manifest JSON mismatch")
	}

	rest := data[OverlayHeaderSize+len(manifestJSON):]
	if got := binary.LittleEndian.Uint64(rest[0:8]); got != 20000003 {
		t.Fatalf("first row id = %d, want 20000003", got)
	}
	if !bytes.Equal(rest[8:8+170], rowPayload(0)) {
		t.Fatalf("first row payload mismatch")
	}
	if got := binary.LittleEndian.Uint64(rest[178:186]); got != 300001275 {
		t.Fatalf("second row id = %d, want 300001275", got)
	}
	if !bytes.Equal(rest[186:186+170], rowPayload(1)) {
		t.Fatalf("second row payload mismatch")
	}
}

func TestOverlayRoundtrip(t *testing.T) {
	o := buildValidOverlay(t)
	data, err := o.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	got, err := ParseOverlay(data)
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}

	if got.Header.TensorName != o.Header.TensorName {
		t.Fatalf("tensor name = %q, want %q", got.Header.TensorName, o.Header.TensorName)
	}
	if got.Header.Version != o.Header.Version || got.Header.QType != o.Header.QType ||
		got.Header.RowDim != o.Header.RowDim || got.Header.BytesPerRow != o.Header.BytesPerRow ||
		got.Header.NRows != o.Header.NRows || got.Header.ManifestLen != o.Header.ManifestLen {
		t.Fatalf("header mismatch: got %+v want %+v", got.Header, o.Header)
	}
	if !reflect.DeepEqual(got.Manifest, o.Manifest) {
		t.Fatalf("manifest mismatch:\n got %+v\nwant %+v", got.Manifest, o.Manifest)
	}
	if !reflect.DeepEqual(got.Rows, o.Rows) {
		t.Fatalf("rows mismatch:\n got %+v\nwant %+v", got.Rows, o.Rows)
	}

	re, err := got.Bytes()
	if err != nil {
		t.Fatalf("re-Bytes: %v", err)
	}
	if !bytes.Equal(data, re) {
		t.Fatalf("re-serialized bytes differ")
	}
}

func TestOverlayWriteReadAtomic(t *testing.T) {
	o := buildValidOverlay(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "model.gguf.plepatch")

	if err := o.WriteAtomic(path); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := ReadOverlay(path)
	if err != nil {
		t.Fatalf("ReadOverlay: %v", err)
	}
	if !reflect.DeepEqual(got.Manifest, o.Manifest) {
		t.Fatalf("manifest mismatch after file roundtrip")
	}
	if !reflect.DeepEqual(got.Rows, o.Rows) {
		t.Fatalf("rows mismatch after file roundtrip")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if len(e.Name()) >= len(filepath.Base(path))+5 && e.Name()[:len(filepath.Base(path))] == filepath.Base(path) && bytes.Contains([]byte(e.Name()), []byte(".tmp-")) {
			t.Fatalf("leftover temp file %q", e.Name())
		}
	}
}

func TestOverlaySHA1BeforeKnownBytes(t *testing.T) {
	rawRows := [][]byte{{0, 1, 2, 3}, {4, 5, 6, 7}}
	got := SHA1RowBytes(rawRows)

	h := sha1.New()
	h.Write([]byte{0, 1, 2, 3})
	h.Write([]byte{4, 5, 6, 7})
	want := h.Sum(nil)
	if !bytes.Equal(got[:], want) {
		t.Fatalf("SHA1RowBytes = %x, want %x", got, want)
	}

	p := overlayParams()
	p.SHA1Before = got
	o, err := NewOverlay(p)
	if err != nil {
		t.Fatalf("NewOverlay: %v", err)
	}
	if o.Manifest.TableSHA1Before != hex.EncodeToString(want) {
		t.Fatalf("manifest sha1 = %q, want %q", o.Manifest.TableSHA1Before, hex.EncodeToString(want))
	}
	if o.Manifest.Format != OverlayFormat {
		t.Fatalf("manifest format = %q, want %q", o.Manifest.Format, OverlayFormat)
	}
}

func TestNewOverlaySortsRows(t *testing.T) {
	p := overlayParams()
	p.Rows = []RowRecord{
		{RowID: 300001275, Payload: rowPayload(1)},
		{RowID: 20000003, Payload: rowPayload(0)},
	}
	o, err := NewOverlay(p)
	if err != nil {
		t.Fatalf("NewOverlay: %v", err)
	}
	if o.Rows[0].RowID != 20000003 || o.Rows[1].RowID != 300001275 {
		t.Fatalf("rows not sorted: %+v", o.Rows)
	}
}

func TestNewOverlayValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*OverlayParams)
	}{
		{"tensor name too long", func(p *OverlayParams) { p.TableTensor = string(make([]byte, 64)) }},
		{"bytes per row zero", func(p *OverlayParams) { p.BytesPerRow = 0 }},
		{"row dim zero", func(p *OverlayParams) { p.RowDim = 0 }},
		{"payload wrong length", func(p *OverlayParams) { p.Rows[0].Payload = []byte{1, 2, 3} }},
		{"duplicate row ids", func(p *OverlayParams) { p.Rows[1].RowID = p.Rows[0].RowID }},
	}
	for _, tc := range cases {
		p := overlayParams()
		tc.mutate(&p)
		if _, err := NewOverlay(p); err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

func TestParseOverlayCorrupt(t *testing.T) {
	valid := func() []byte {
		o := buildValidOverlay(t)
		data, err := o.Bytes()
		if err != nil {
			t.Fatalf("Bytes: %v", err)
		}
		return data
	}()

	badMagic := append([]byte(nil), valid...)
	copy(badMagic[0:8], "XXXXXXXX")

	badVersion := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint16(badVersion[8:10], 9)

	badManifestLen := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint64(badManifestLen[40:48], uint64(len(valid)))

	badFormat := append([]byte(nil), valid...)
	copy(badFormat[128:128+15], "ple-unknown-v1")

	o := buildValidOverlay(t)
	manifestJSON, err := json.Marshal(o.Manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	truncated := valid[:OverlayHeaderSize+len(manifestJSON)+8]

	cases := []struct {
		name string
		data []byte
	}{
		{"too short", []byte{1, 2, 3}},
		{"bad magic", badMagic},
		{"bad version", badVersion},
		{"manifest len too large", badManifestLen},
		{"bad format", badFormat},
		{"truncated row region", truncated},
	}
	for _, tc := range cases {
		if _, err := ParseOverlay(tc.data); err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

func TestParseOverlayBytesPerRowOverflow(t *testing.T) {
	o, err := NewOverlay(OverlayParams{
		Model:       "m",
		TableTensor: "t",
		RowDim:      1,
		QType:       8,
		BytesPerRow: math.MaxUint64 - 4,
		NRows:       1,
	})
	if err != nil {
		t.Fatalf("NewOverlay: %v", err)
	}
	data, err := o.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if _, err := ParseOverlay(data); err == nil {
		t.Fatalf("expected error for overflowing bytes_per_row")
	}
}

func TestParseOverlayNonAscendingRows(t *testing.T) {
	o := buildValidOverlay(t)
	data, err := o.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	manifestJSON, err := json.Marshal(o.Manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	off := OverlayHeaderSize + len(manifestJSON)
	copy(data[off+178:off+186], data[off:off+8])
	if _, err := ParseOverlay(data); err == nil {
		t.Fatalf("expected error for non-ascending row ids")
	}
}
