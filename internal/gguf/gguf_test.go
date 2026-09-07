package gguf

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func metadataMap(f *File) map[string]Value {
	m := make(map[string]Value, len(f.Metadata))
	for _, kv := range f.Metadata {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestRoundtripAllValueTypesAndTensors(t *testing.T) {
	tensorA := []byte{0, 1, 2, 3, 4, 5, 6, 7}
	tensorB := make([]byte, 34)
	for i := range tensorB {
		tensorB[i] = byte(i)
	}

	w := NewWriter()
	w.AddMetadata("general.architecture", Str("testarch"))
	w.AddMetadata("general.name", Str("synthetic"))
	w.AddMetadata("k.uint8", Uint8(200))
	w.AddMetadata("k.int8", Int8(-42))
	w.AddMetadata("k.uint16", Uint16(65535))
	w.AddMetadata("k.int16", Int16(-1234))
	w.AddMetadata("k.uint32", Uint32(4000000000))
	w.AddMetadata("k.int32", Int32(-100000))
	w.AddMetadata("k.float32", Float32(1.5))
	w.AddMetadata("k.bool_true", Bool(true))
	w.AddMetadata("k.bool_false", Bool(false))
	w.AddMetadata("k.string", Str("hello"))
	w.AddMetadata("k.uint64", Uint64(18000000000000000000))
	w.AddMetadata("k.int64", Int64(-9000000000000000000))
	w.AddMetadata("k.float64", Float64(-3.25))
	w.AddTensor("tok_embd.weight", []uint64{2, 4}, 0, tensorA)
	w.AddTensor("per_layer_token_embd.weight", []uint64{160, 1}, 8, tensorB)

	data, err := w.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Version != 3 {
		t.Fatalf("Version = %d, want 3", f.Version)
	}
	if f.Alignment != DefaultAlignment {
		t.Fatalf("Alignment = %d, want %d", f.Alignment, DefaultAlignment)
	}

	got := metadataMap(f)
	want := map[string]Value{
		"general.architecture": Str("testarch"),
		"general.name":         Str("synthetic"),
		"k.uint8":              Uint8(200),
		"k.int8":               Int8(-42),
		"k.uint16":             Uint16(65535),
		"k.int16":              Int16(-1234),
		"k.uint32":             Uint32(4000000000),
		"k.int32":              Int32(-100000),
		"k.float32":            Float32(1.5),
		"k.bool_true":          Bool(true),
		"k.bool_false":         Bool(false),
		"k.string":             Str("hello"),
		"k.uint64":             Uint64(18000000000000000000),
		"k.int64":              Int64(-9000000000000000000),
		"k.float64":            Float64(-3.25),
	}
	for key, expected := range want {
		actual, ok := got[key]
		if !ok {
			t.Fatalf("metadata %q missing", key)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("metadata %q = %#v, want %#v", key, actual, expected)
		}
	}

	if len(f.Tensors) != 2 {
		t.Fatalf("tensor count = %d, want 2", len(f.Tensors))
	}
	if f.Tensors[0].Name != "tok_embd.weight" || !reflect.DeepEqual(f.Tensors[0].Dims, []uint64{2, 4}) || f.Tensors[0].Type != 0 {
		t.Fatalf("tensor[0] = %#v", f.Tensors[0])
	}
	if f.Tensors[1].Name != "per_layer_token_embd.weight" || f.Tensors[1].Type != 8 {
		t.Fatalf("tensor[1] = %#v", f.Tensors[1])
	}

	if f.Tensors[0].Offset != 0 {
		t.Fatalf("tensor[0] offset = %d, want 0", f.Tensors[0].Offset)
	}
	if f.Tensors[1].Offset != 32 {
		t.Fatalf("tensor[1] offset = %d, want 32", f.Tensors[1].Offset)
	}

	if got := data[f.DataOffset+int64(f.Tensors[0].Offset) : f.DataOffset+int64(f.Tensors[0].Offset)+int64(len(tensorA))]; !bytes.Equal(got, tensorA) {
		t.Fatalf("tensor[0] data mismatch")
	}
	if got := data[f.DataOffset+int64(f.Tensors[1].Offset) : f.DataOffset+int64(f.Tensors[1].Offset)+int64(len(tensorB))]; !bytes.Equal(got, tensorB) {
		t.Fatalf("tensor[1] data mismatch")
	}
}

func TestRoundtripStringArrayTokens(t *testing.T) {
	tokens := []string{"<|begin_of_text|>", "hello", " world", "\xff\xfe", ""}
	w := NewWriter()
	w.AddMetadata("tokenizer.ggml.tokens", StringArray(tokens))
	w.AddMetadata("tokenizer.ggml.merges", StringArray([]string{"a b", "c d"}))

	data, err := w.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	v, ok := f.MetadataValue("tokenizer.ggml.tokens")
	if !ok {
		t.Fatalf("tokens metadata missing")
	}
	arr, err := v.AsArray()
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	got, err := arr.AsStrings()
	if err != nil {
		t.Fatalf("AsStrings: %v", err)
	}
	if !reflect.DeepEqual(got, tokens) {
		t.Fatalf("tokens = %#v, want %#v", got, tokens)
	}

	merges, err := metadataMap(f)["tokenizer.ggml.merges"].AsArray()
	if err != nil {
		t.Fatalf("merges AsArray: %v", err)
	}
	mergeStrs, err := merges.AsStrings()
	if err != nil {
		t.Fatalf("merges AsStrings: %v", err)
	}
	if len(mergeStrs) != 2 || mergeStrs[1] != "c d" {
		t.Fatalf("merges = %#v", mergeStrs)
	}
}

func TestRoundtripNestedArray(t *testing.T) {
	w := NewWriter()
	w.AddMetadata("nested", ValueArray([]Value{Str("a"), Uint32(7), Bool(true)}))

	data, err := w.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	arr, err := metadataMap(f)["nested"].AsArray()
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	values, err := arr.AsValues()
	if err != nil {
		t.Fatalf("AsValues: %v", err)
	}
	if len(values) != 3 || !reflect.DeepEqual(values[0], Str("a")) || !reflect.DeepEqual(values[1], Uint32(7)) || !reflect.DeepEqual(values[2], Bool(true)) {
		t.Fatalf("nested = %#v", values)
	}
}

func TestParseMalformed(t *testing.T) {
	valid := func() []byte {
		w := NewWriter()
		w.AddMetadata("general.name", Str("m"))
		w.AddTensor("t.weight", []uint64{1}, 0, []byte{1, 2, 3, 4})
		b, err := w.Bytes()
		if err != nil {
			t.Fatalf("Bytes: %v", err)
		}
		return b
	}()

	truncatedTensor := append([]byte(nil), Magic...)
	truncatedTensor = append(truncatedTensor, leU32(3)...)
	truncatedTensor = append(truncatedTensor, leU64(1)...)
	truncatedTensor = append(truncatedTensor, leU64(0)...)
	truncatedTensor = append(truncatedTensor, leU64(3)...)
	truncatedTensor = append(truncatedTensor, []byte("ab")...)

	hugeTensorDims := append([]byte(nil), Magic...)
	hugeTensorDims = append(hugeTensorDims, leU32(3)...)
	hugeTensorDims = append(hugeTensorDims, leU64(1)...)
	hugeTensorDims = append(hugeTensorDims, leU64(0)...)
	hugeTensorDims = append(hugeTensorDims, leU64(1)...)
	hugeTensorDims = append(hugeTensorDims, []byte("t")...)
	hugeTensorDims = append(hugeTensorDims, leU32(0xFFFFFFFF)...)

	hugeStringArray := append([]byte(nil), Magic...)
	hugeStringArray = append(hugeStringArray, leU32(3)...)
	hugeStringArray = append(hugeStringArray, leU64(0)...)
	hugeStringArray = append(hugeStringArray, leU64(1)...)
	hugeStringArray = append(hugeStringArray, leU64(1)...)
	hugeStringArray = append(hugeStringArray, []byte("k")...)
	hugeStringArray = append(hugeStringArray, leU32(uint32(TypeArray))...)
	hugeStringArray = append(hugeStringArray, leU32(uint32(TypeString))...)
	hugeStringArray = append(hugeStringArray, leU64(0xFFFFFFFFFFFFFFFF)...)

	hugeNestedArray := append([]byte(nil), Magic...)
	hugeNestedArray = append(hugeNestedArray, leU32(3)...)
	hugeNestedArray = append(hugeNestedArray, leU64(0)...)
	hugeNestedArray = append(hugeNestedArray, leU64(1)...)
	hugeNestedArray = append(hugeNestedArray, leU64(1)...)
	hugeNestedArray = append(hugeNestedArray, []byte("k")...)
	hugeNestedArray = append(hugeNestedArray, leU32(uint32(TypeArray))...)
	hugeNestedArray = append(hugeNestedArray, leU32(uint32(TypeArray))...)
	hugeNestedArray = append(hugeNestedArray, leU64(0xFFFFFFFFFFFFFFFF)...)

	cases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"truncated header", []byte(Magic[:3])},
		{"wrong magic", []byte("XXXX0000000000000000")},
		{"truncated metadata string", append(append(append([]byte(Magic), leU32(3)...), leU64(0)...), leU64(1)...)},
		{"truncated tensor name", truncatedTensor},
		{"huge tensor dims count", hugeTensorDims},
		{"huge string array count", hugeStringArray},
		{"huge nested array count", hugeNestedArray},
	}

	for _, tc := range cases {
		if _, err := Parse(bytes.NewReader(tc.data), int64(len(tc.data))); err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
	}

	badVersion := append([]byte(nil), valid...)
	copy(badVersion[4:8], leU32(99))
	if _, err := Parse(bytes.NewReader(badVersion), int64(len(badVersion))); err == nil {
		t.Fatalf("unsupported version: expected error, got nil")
	}

	badMagic := append([]byte(nil), valid...)
	copy(badMagic[0:4], "XXXX")
	if _, err := Parse(bytes.NewReader(badMagic), int64(len(badMagic))); err == nil {
		t.Fatalf("bad magic: expected error, got nil")
	}
}

func TestWriterRejectsUnsupportedVersion(t *testing.T) {
	w := NewWriter()
	w.SetVersion(99)
	if _, err := w.Bytes(); err == nil {
		t.Fatalf("expected error for version 99")
	}
}

func leU32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func leU64(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}
