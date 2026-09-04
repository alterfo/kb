package plepatch

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"testing"
)

func TestQ8RoundtripByteIdentical(t *testing.T) {
	rng := rand.New(rand.NewPCG(0, 0))
	for trial := 0; trial < 500; trial++ {
		vec := make([]float32, 160)
		for i := range vec {
			vec[i] = float32(rng.NormFloat64()) * 0.3
		}
		raw, err := encodeRow(vec)
		if err != nil {
			t.Fatalf("encodeRow: %v", err)
		}
		dec, err := decodeRow(raw)
		if err != nil {
			t.Fatalf("decodeRow: %v", err)
		}
		re, err := encodeRow(dec)
		if err != nil {
			t.Fatalf("re-encodeRow: %v", err)
		}
		if !bytes.Equal(raw, re) {
			t.Fatalf("roundtrip not byte-identical:\n raw %x\n re  %x", raw, re)
		}
	}
}

func TestQ8EncodesScaleFirst(t *testing.T) {
	vec := make([]float32, 32)
	for i := range vec {
		vec[i] = float32(i)/16.0 - 1.0
	}
	raw := quantizeBlock(vec)
	d := binary.LittleEndian.Uint16(raw[0:2])
	if d == 0 {
		t.Fatalf("scale is zero for non-zero block")
	}
	for i := 0; i < 32; i++ {
		q := int8(raw[2+i])
		if q == 0 && vec[i] != 0 {
			t.Fatalf("unexpected zero quant at %d", i)
		}
	}
}

func TestQ8EdgeCases(t *testing.T) {
	zeros := make([]float32, 160)
	raw, err := encodeRow(zeros)
	if err != nil {
		t.Fatalf("encodeRow zeros: %v", err)
	}
	if len(raw) != 170 {
		t.Fatalf("zeros row is %d bytes, want 170", len(raw))
	}
	for _, b := range raw {
		if b != 0 {
			t.Fatalf("zeros row has non-zero byte %d", b)
		}
	}
	dec, err := decodeRow(raw)
	if err != nil {
		t.Fatalf("decodeRow zeros: %v", err)
	}
	for _, v := range dec {
		if v != 0 {
			t.Fatalf("zeros decoded to %v", v)
		}
	}

	neg := make([]float32, 160)
	for i := range neg {
		neg[i] = -float32(i+1) * 0.01
	}
	raw, err = encodeRow(neg)
	if err != nil {
		t.Fatalf("encodeRow negatives: %v", err)
	}
	dec, err = decodeRow(raw)
	if err != nil {
		t.Fatalf("decodeRow negatives: %v", err)
	}
	if dec[0] >= 0 {
		t.Fatalf("negative value decoded positive: %v", dec[0])
	}

	amax := make([]float32, 160)
	amax[0] = 3.5
	amax[159] = -3.5
	raw, err = encodeRow(amax)
	if err != nil {
		t.Fatalf("encodeRow amax: %v", err)
	}
	dec, err = decodeRow(raw)
	if err != nil {
		t.Fatalf("decodeRow amax: %v", err)
	}
	if math.Abs(float64(dec[0])-3.5) > 0.03 {
		t.Fatalf("amax value not preserved: got %v, want ~3.5", dec[0])
	}
	if dec[159] > -3.47 {
		t.Fatalf("negative amax not preserved: got %v", dec[159])
	}
}

func TestQ8RowLengthValidation(t *testing.T) {
	if _, err := encodeRow(nil); err == nil {
		t.Fatalf("encodeRow(nil) should error")
	}
	if _, err := encodeRow(make([]float32, 33)); err == nil {
		t.Fatalf("encodeRow(33) should error")
	}
	if _, err := decodeRow(nil); err == nil {
		t.Fatalf("decodeRow(nil) should error")
	}
	if _, err := decodeRow(make([]byte, 33)); err == nil {
		t.Fatalf("decodeRow(33 bytes) should error")
	}
	if _, err := decodeRow(make([]byte, 170)); err != nil {
		t.Fatalf("decodeRow(170) should succeed, got %v", err)
	}
	if _, err := encodeRow(make([]float32, 160)); err != nil {
		t.Fatalf("encodeRow(160) should succeed, got %v", err)
	}
}

func TestQ8Float16Roundtrip(t *testing.T) {
	cases := []float32{0, -0, 1, -1, 0.5, 0.125, 65504, -65504, 0.000000059604645}
	for _, f := range cases {
		h := float32ToFloat16(f)
		back := float16ToFloat32(h)
		if back != f && !(f == 0 && back == 0) {
			t.Fatalf("float16 roundtrip %v -> %v", f, back)
		}
	}
	inf := float32(math.Inf(1))
	if got := float16ToFloat32(float32ToFloat16(inf)); !math.IsInf(float64(got), 1) {
		t.Fatalf("inf roundtrip got %v", got)
	}
	nan := float32(math.NaN())
	if got := float16ToFloat32(float32ToFloat16(nan)); !math.IsNaN(float64(got)) {
		t.Fatalf("nan roundtrip got %v", got)
	}
}
