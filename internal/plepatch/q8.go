package plepatch

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	q8BlockSize  = 32
	q8BlockBytes = 34
)

func quantizeBlock(x []float32) []byte {
	var amax float32
	for _, v := range x {
		a := v
		if a < 0 {
			a = -a
		}
		if a > amax {
			amax = a
		}
	}

	d := amax / 127.0
	var id float32
	if d != 0 {
		id = 1.0 / d
	}

	out := make([]byte, q8BlockBytes)
	for i, v := range x {
		q := math.Round(float64(v * id))
		if q > 127 {
			q = 127
		}
		if q < -127 {
			q = -127
		}
		out[2+i] = byte(int8(q))
	}
	binary.LittleEndian.PutUint16(out[0:2], float32ToFloat16(d))
	return out
}

func dequantizeBlock(b []byte) ([]float32, error) {
	if len(b) != q8BlockBytes {
		return nil, fmt.Errorf("plepatch: q8_0 block is %d bytes, want %d", len(b), q8BlockBytes)
	}
	d := float16ToFloat32(binary.LittleEndian.Uint16(b[0:2]))
	out := make([]float32, q8BlockSize)
	for i := 0; i < q8BlockSize; i++ {
		out[i] = float32(int8(b[2+i])) * d
	}
	return out, nil
}

func encodeRow(vec []float32) ([]byte, error) {
	if len(vec) == 0 || len(vec)%q8BlockSize != 0 {
		return nil, fmt.Errorf("plepatch: encodeRow: length %d not divisible by %d", len(vec), q8BlockSize)
	}
	out := make([]byte, 0, len(vec)/q8BlockSize*q8BlockBytes)
	for i := 0; i < len(vec); i += q8BlockSize {
		out = append(out, quantizeBlock(vec[i:i+q8BlockSize])...)
	}
	return out, nil
}

func decodeRow(raw []byte) ([]float32, error) {
	if len(raw) == 0 || len(raw)%q8BlockBytes != 0 {
		return nil, fmt.Errorf("plepatch: decodeRow: length %d not divisible by %d", len(raw), q8BlockBytes)
	}
	out := make([]float32, 0, len(raw)/q8BlockBytes*q8BlockSize)
	for i := 0; i < len(raw); i += q8BlockBytes {
		b, err := dequantizeBlock(raw[i : i+q8BlockBytes])
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

func float16ToFloat32(h uint16) float32 {
	sign := uint32(h&0x8000) << 16
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h & 0x3ff)

	var bits uint32
	switch exp {
	case 0:
		if mant == 0 {
			bits = sign
		} else {
			e := -14
			for mant&0x400 == 0 {
				mant <<= 1
				e--
			}
			mant &= 0x3ff
			bits = sign | uint32(e+127)<<23 | mant<<13
		}
	case 0x1f:
		bits = sign | 0x7f800000 | mant<<13
	default:
		bits = sign | (exp+112)<<23 | mant<<13
	}
	return math.Float32frombits(bits)
}

func float32ToFloat16(f float32) uint16 {
	bits := math.Float32bits(f)
	sign := uint16((bits >> 16) & 0x8000)
	exp := int32((bits >> 23) & 0xff)
	frac := bits & 0x7fffff

	if exp == 0xff {
		if frac != 0 {
			return sign | 0x7e00
		}
		return sign | 0x7c00
	}

	newexp := exp - 127 + 15
	if newexp >= 0x1f {
		return sign | 0x7c00
	}
	if newexp <= 0 {
		if newexp < -10 {
			return sign
		}
		frac |= 0x800000
		shift := uint32(14 - newexp)
		rounded := frac + (1 << (shift - 1)) - 1 + ((frac >> shift) & 1)
		return sign | uint16(rounded>>shift)
	}

	mant := uint16(frac >> 13)
	rounding := frac & 0x1fff
	if rounding > 0x1000 || (rounding == 0x1000 && mant&1 == 1) {
		mant++
		if mant == 0x400 {
			mant = 0
			newexp++
			if newexp >= 0x1f {
				return sign | 0x7c00
			}
		}
	}
	return sign | uint16(newexp<<10) | mant
}
