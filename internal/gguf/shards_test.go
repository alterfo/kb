package gguf

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeShardFile(t *testing.T, dir, name string, splitCount uint32, tensors []WriterTensor) string {
	t.Helper()
	w := NewWriter()
	w.AddMetadata("general.architecture", Str("qwen4exp"))
	w.AddMetadata("general.name", Str("synthetic"))
	if splitCount > 0 {
		w.AddMetadata("split.count", Uint32(splitCount))
	}
	for _, ts := range tensors {
		w.AddTensor(ts.Name, ts.Dims, ts.Type, ts.Data)
	}
	data, err := w.Bytes()
	if err != nil {
		t.Fatalf("writer: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func tableTensor(rows int) WriterTensor {
	data := make([]byte, rows*170)
	for i := range data {
		data[i] = byte(i % 256)
	}
	return WriterTensor{Name: "per_layer_token_embd.weight", Dims: []uint64{160, uint64(rows)}, Type: 8, Data: data}
}

func TestSplitInfo(t *testing.T) {
	w := NewWriter()
	w.AddMetadata("split.count", Uint32(6))
	w.AddMetadata("split.no", Uint32(3))
	data, err := w.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	si, err := f.SplitInfo()
	if err != nil {
		t.Fatalf("SplitInfo: %v", err)
	}
	if si.Count != 6 || si.No != 3 {
		t.Fatalf("SplitInfo = %+v, want {6 3}", si)
	}
}

func TestSplitInfoDefaults(t *testing.T) {
	w := NewWriter()
	w.AddMetadata("general.name", Str("m"))
	data, err := w.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	si, err := f.SplitInfo()
	if err != nil {
		t.Fatalf("SplitInfo: %v", err)
	}
	if si.Count != 1 || si.No != 0 {
		t.Fatalf("SplitInfo = %+v, want {1 0}", si)
	}
}

func TestDiscoverShardsSingle(t *testing.T) {
	dir := t.TempDir()
	path := writeShardFile(t, dir, "model.gguf", 0, nil)

	shards, err := DiscoverShards(path)
	if err != nil {
		t.Fatalf("DiscoverShards: %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("len(shards) = %d, want 1", len(shards))
	}
	abs, _ := filepath.Abs(path)
	if shards[0].Path != abs || shards[0].ShardNo != 0 {
		t.Fatalf("shard = %+v", shards[0])
	}
}

func TestDiscoverShardsDashConvention(t *testing.T) {
	dir := t.TempDir()
	first := writeShardFile(t, dir, "model-00001-of-00006.gguf", 6, nil)
	for i := 2; i <= 6; i++ {
		name := fmt.Sprintf("model-%05d-of-00006.gguf", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	shards, err := DiscoverShards(first)
	if err != nil {
		t.Fatalf("DiscoverShards: %v", err)
	}
	if len(shards) != 6 {
		t.Fatalf("len(shards) = %d, want 6", len(shards))
	}
	for i, sh := range shards {
		want := filepath.Join(dir, fmt.Sprintf("model-%05d-of-00006.gguf", i+1))
		if sh.Path != want || sh.ShardNo != i {
			t.Fatalf("shards[%d] = %+v, want path %s no %d", i, sh, want, i)
		}
	}
}

func TestDiscoverShardsJunkSuffix(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"model-00001-of-00006.gguf.download",
		"model-00002-of-00006.gguf.part",
		"model-00003-of-00006.gguf",
		"model-00004-of-00006.gguf",
		"model-00005-of-00006.gguf",
		"model-00006-of-00006.gguf",
	}
	first := writeShardFile(t, dir, names[0], 6, nil)
	for _, name := range names[1:] {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	shards, err := DiscoverShards(first)
	if err != nil {
		t.Fatalf("DiscoverShards: %v", err)
	}
	if len(shards) != 6 {
		t.Fatalf("len(shards) = %d, want 6", len(shards))
	}
	for i, sh := range shards {
		if sh.Path != filepath.Join(dir, names[i]) {
			t.Fatalf("shards[%d].Path = %s, want %s", i, sh.Path, names[i])
		}
	}
}

func TestDiscoverShardsDotConvention(t *testing.T) {
	dir := t.TempDir()
	first := writeShardFile(t, dir, "model.00000.gguf", 6, nil)
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("model.%05d.gguf", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	shards, err := DiscoverShards(first)
	if err != nil {
		t.Fatalf("DiscoverShards: %v", err)
	}
	if len(shards) != 6 {
		t.Fatalf("len(shards) = %d, want 6", len(shards))
	}
	for i, sh := range shards {
		want := filepath.Join(dir, fmt.Sprintf("model.%05d.gguf", i))
		if sh.Path != want {
			t.Fatalf("shards[%d].Path = %s, want %s", i, sh.Path, want)
		}
	}
}

func TestDiscoverShardsMissingShard(t *testing.T) {
	dir := t.TempDir()
	first := writeShardFile(t, dir, "model-00001-of-00003.gguf", 3, nil)
	if err := os.WriteFile(filepath.Join(dir, "model-00002-of-00003.gguf"), []byte{}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := DiscoverShards(first); err == nil {
		t.Fatalf("expected error for missing shard, got nil")
	}
}

func TestLocateTensor(t *testing.T) {
	dir := t.TempDir()
	first := writeShardFile(t, dir, "model.gguf", 0, []WriterTensor{
		{Name: "tok_embd.weight", Dims: []uint64{2, 4}, Type: 0, Data: []byte{0, 1, 2, 3, 4, 5, 6, 7}},
		tableTensor(4),
	})

	shards, err := DiscoverShards(first)
	if err != nil {
		t.Fatalf("DiscoverShards: %v", err)
	}
	loc, err := LocateTensor(shards, "per_layer_token_embd.weight")
	if err != nil {
		t.Fatalf("LocateTensor: %v", err)
	}
	if loc.Name != "per_layer_token_embd.weight" {
		t.Fatalf("Name = %q", loc.Name)
	}
	if loc.Type != 8 {
		t.Fatalf("Type = %d, want 8", loc.Type)
	}
	if loc.RowDim != 160 || loc.NRows != 4 {
		t.Fatalf("dims = %d x %d, want 160 x 4", loc.RowDim, loc.NRows)
	}
	if loc.NBytes != 680 {
		t.Fatalf("NBytes = %d, want 680", loc.NBytes)
	}

	f, err := Open(first)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	ti, ok := f.Tensor("per_layer_token_embd.weight")
	if !ok {
		t.Fatalf("tensor missing")
	}
	wantOffset := f.DataOffset + int64(ti.Offset)
	if loc.DataOffset != wantOffset {
		t.Fatalf("DataOffset = %d, want %d", loc.DataOffset, wantOffset)
	}

	raw, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := raw[loc.DataOffset : loc.DataOffset+loc.NBytes]
	want := make([]byte, 680)
	for i := range want {
		want[i] = byte(i % 256)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("tensor bytes at location mismatch")
	}
}

func TestLocateTensorMissing(t *testing.T) {
	dir := t.TempDir()
	first := writeShardFile(t, dir, "model.gguf", 0, []WriterTensor{tableTensor(2)})
	shards, err := DiscoverShards(first)
	if err != nil {
		t.Fatalf("DiscoverShards: %v", err)
	}
	if _, err := LocateTensor(shards, "nonexistent.weight"); err == nil {
		t.Fatalf("expected error for missing tensor, got nil")
	}
}

func TestLocateTensorAcrossShards(t *testing.T) {
	dir := t.TempDir()
	first := writeShardFile(t, dir, "model-00001-of-00003.gguf", 3, nil)
	writeShardFile(t, dir, "model-00002-of-00003.gguf", 3, []WriterTensor{tableTensor(2)})
	writeShardFile(t, dir, "model-00003-of-00003.gguf", 3, nil)

	shards, err := DiscoverShards(first)
	if err != nil {
		t.Fatalf("DiscoverShards: %v", err)
	}
	loc, err := LocateTensor(shards, "per_layer_token_embd.weight")
	if err != nil {
		t.Fatalf("LocateTensor: %v", err)
	}
	if loc.ShardNo != 1 || loc.Path != filepath.Join(dir, "model-00002-of-00003.gguf") {
		t.Fatalf("loc = %+v, want shard 2", loc)
	}
}

func TestLocateTensorRejectsDataExceedingShardSize(t *testing.T) {
	dir := t.TempDir()
	path := writeShardFile(t, dir, "model.gguf", 0, []WriterTensor{tableTensor(4)})
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := os.WriteFile(path, full[:len(full)-100], 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	shards, err := DiscoverShards(path)
	if err != nil {
		t.Fatalf("DiscoverShards: %v", err)
	}
	if _, err := LocateTensor(shards, "per_layer_token_embd.weight"); err == nil {
		t.Fatalf("expected error for tensor data exceeding truncated shard size")
	}
}

func TestTensorSize(t *testing.T) {
	cases := []struct {
		qtype uint32
		dims  []uint64
		want  uint64
	}{
		{0, []uint64{160, 4}, 2560},
		{1, []uint64{160, 4}, 1280},
		{8, []uint64{160, 4}, 680},
	}
	for _, tc := range cases {
		got, err := TensorSize(tc.dims, tc.qtype)
		if err != nil {
			t.Fatalf("TensorSize(%d, %v): %v", tc.qtype, tc.dims, err)
		}
		if got != tc.want {
			t.Fatalf("TensorSize(%d, %v) = %d, want %d", tc.qtype, tc.dims, got, tc.want)
		}
	}
	if _, err := TensorSize([]uint64{161}, 8); err == nil {
		t.Fatalf("expected error for non-block-aligned dims")
	}
	if _, err := TensorSize([]uint64{1}, 999); err == nil {
		t.Fatalf("expected error for unknown quant type")
	}
}
