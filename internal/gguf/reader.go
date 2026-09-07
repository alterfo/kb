package gguf

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

type cursor struct {
	r    io.ReaderAt
	size int64
	off  int64
}

func (c *cursor) read(n int) ([]byte, error) {
	if n < 0 || c.off < 0 || int64(n) > c.size-c.off {
		return nil, io.ErrUnexpectedEOF
	}
	buf := make([]byte, n)
	m, err := c.r.ReadAt(buf, c.off)
	if err != nil {
		return nil, err
	}
	if m != n {
		return nil, io.ErrUnexpectedEOF
	}
	c.off += int64(n)
	return buf, nil
}

func (c *cursor) u16() (uint16, error) {
	b, err := c.read(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (c *cursor) u32() (uint32, error) {
	b, err := c.read(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (c *cursor) u64() (uint64, error) {
	b, err := c.read(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

func (c *cursor) i16() (int16, error) {
	u, err := c.u16()
	return int16(u), err
}

func (c *cursor) i32() (int32, error) {
	u, err := c.u32()
	return int32(u), err
}

func (c *cursor) i64() (int64, error) {
	u, err := c.u64()
	return int64(u), err
}

func (c *cursor) f32() (float32, error) {
	u, err := c.u32()
	return math.Float32frombits(u), err
}

func (c *cursor) f64() (float64, error) {
	u, err := c.u64()
	return math.Float64frombits(u), err
}

func (c *cursor) bool_() (bool, error) {
	b, err := c.read(1)
	if err != nil {
		return false, err
	}
	return b[0] != 0, nil
}

func (c *cursor) string() (string, error) {
	n, err := c.u64()
	if err != nil {
		return "", err
	}
	if n > uint64(c.size-c.off) {
		return "", io.ErrUnexpectedEOF
	}
	b, err := c.read(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *cursor) value() (Value, error) {
	t, err := c.u32()
	if err != nil {
		return Value{}, err
	}
	vt := ValueType(t)
	if vt == TypeArray {
		elem, err := c.u32()
		if err != nil {
			return Value{}, err
		}
		et := ValueType(elem)
		n, err := c.u64()
		if err != nil {
			return Value{}, err
		}
		data, err := c.arrayData(et, n)
		if err != nil {
			return Value{}, err
		}
		return Value{Type: TypeArray, Data: Array{Type: et, Data: data}}, nil
	}
	data, err := c.scalar(vt)
	if err != nil {
		return Value{}, err
	}
	return Value{Type: vt, Data: data}, nil
}

func (c *cursor) scalar(t ValueType) (any, error) {
	switch t {
	case TypeUint8:
		b, err := c.read(1)
		if err != nil {
			return nil, err
		}
		return b[0], nil
	case TypeInt8:
		b, err := c.read(1)
		if err != nil {
			return nil, err
		}
		return int8(b[0]), nil
	case TypeUint16:
		return c.u16()
	case TypeInt16:
		return c.i16()
	case TypeUint32:
		return c.u32()
	case TypeInt32:
		return c.i32()
	case TypeFloat32:
		return c.f32()
	case TypeBool:
		return c.bool_()
	case TypeString:
		return c.string()
	case TypeUint64:
		return c.u64()
	case TypeInt64:
		return c.i64()
	case TypeFloat64:
		return c.f64()
	default:
		return nil, fmt.Errorf("gguf: unsupported metadata value type %d", t)
	}
}

func (c *cursor) arrayData(elem ValueType, n uint64) (any, error) {
	remaining := uint64(c.size - c.off)
	switch elem {
	case TypeString:
		const minStringSize = 8
		if n > remaining/minStringSize {
			return nil, io.ErrUnexpectedEOF
		}
		out := make([]string, 0, n)
		for i := uint64(0); i < n; i++ {
			s, err := c.string()
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	case TypeArray:
		const minValueSize = 4
		if n > remaining/minValueSize {
			return nil, io.ErrUnexpectedEOF
		}
		out := make([]Value, 0, n)
		for i := uint64(0); i < n; i++ {
			v, err := c.value()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	size, ok := elemSize(elem)
	if !ok {
		return nil, fmt.Errorf("gguf: unsupported array element type %d", elem)
	}
	if n > uint64(c.size-c.off)/uint64(size) {
		return nil, io.ErrUnexpectedEOF
	}
	b, err := c.read(int(n) * size)
	if err != nil {
		return nil, err
	}
	return decodeFixedArray(elem, b)
}

func elemSize(t ValueType) (int, bool) {
	switch t {
	case TypeUint8, TypeInt8, TypeBool:
		return 1, true
	case TypeUint16, TypeInt16:
		return 2, true
	case TypeUint32, TypeInt32, TypeFloat32:
		return 4, true
	case TypeUint64, TypeInt64, TypeFloat64:
		return 8, true
	default:
		return 0, false
	}
}

func decodeFixedArray(t ValueType, b []byte) (any, error) {
	switch t {
	case TypeUint8:
		out := make([]uint8, len(b))
		copy(out, b)
		return out, nil
	case TypeInt8:
		out := make([]int8, len(b))
		for i, v := range b {
			out[i] = int8(v)
		}
		return out, nil
	case TypeBool:
		out := make([]bool, len(b))
		for i, v := range b {
			out[i] = v != 0
		}
		return out, nil
	case TypeUint16:
		out := make([]uint16, len(b)/2)
		for i := range out {
			out[i] = binary.LittleEndian.Uint16(b[i*2:])
		}
		return out, nil
	case TypeInt16:
		out := make([]int16, len(b)/2)
		for i := range out {
			out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
		}
		return out, nil
	case TypeUint32:
		out := make([]uint32, len(b)/4)
		for i := range out {
			out[i] = binary.LittleEndian.Uint32(b[i*4:])
		}
		return out, nil
	case TypeInt32:
		out := make([]int32, len(b)/4)
		for i := range out {
			out[i] = int32(binary.LittleEndian.Uint32(b[i*4:]))
		}
		return out, nil
	case TypeFloat32:
		out := make([]float32, len(b)/4)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		}
		return out, nil
	case TypeUint64:
		out := make([]uint64, len(b)/8)
		for i := range out {
			out[i] = binary.LittleEndian.Uint64(b[i*8:])
		}
		return out, nil
	case TypeInt64:
		out := make([]int64, len(b)/8)
		for i := range out {
			out[i] = int64(binary.LittleEndian.Uint64(b[i*8:]))
		}
		return out, nil
	case TypeFloat64:
		out := make([]float64, len(b)/8)
		for i := range out {
			out[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8:]))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("gguf: unsupported array element type %d", t)
	}
}

func Open(path string) (*File, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, err
	}
	f, err := Parse(fh, st.Size())
	if err != nil {
		fh.Close()
		return nil, err
	}
	f.closer = fh
	return f, nil
}

func Parse(r io.ReaderAt, size int64) (*File, error) {
	if size < 0 {
		return nil, fmt.Errorf("gguf: negative size %d", size)
	}
	c := &cursor{r: r, size: size}

	magic, err := c.read(4)
	if err != nil {
		return nil, fmt.Errorf("gguf: read magic: %w", err)
	}
	if string(magic) != Magic {
		return nil, fmt.Errorf("gguf: invalid magic %q, want %q", magic, Magic)
	}

	version, err := c.u32()
	if err != nil {
		return nil, fmt.Errorf("gguf: read version: %w", err)
	}
	if version < MinSupportedVersion || version > MaxSupportedVersion {
		return nil, fmt.Errorf("gguf: unsupported version %d", version)
	}

	tensorCount, err := c.u64()
	if err != nil {
		return nil, fmt.Errorf("gguf: read tensor count: %w", err)
	}
	kvCount, err := c.u64()
	if err != nil {
		return nil, fmt.Errorf("gguf: read metadata count: %w", err)
	}

	var metadata []KV
	for i := uint64(0); i < kvCount; i++ {
		key, err := c.string()
		if err != nil {
			return nil, fmt.Errorf("gguf: read metadata key %d: %w", i, err)
		}
		val, err := c.value()
		if err != nil {
			return nil, fmt.Errorf("gguf: read metadata value %q: %w", key, err)
		}
		metadata = append(metadata, KV{Key: key, Value: val})
	}

	alignment := DefaultAlignment
	if v, ok := lookup(metadata, "general.alignment"); ok {
		alignment, err = v.AsUint32()
		if err != nil {
			return nil, fmt.Errorf("gguf: general.alignment: %w", err)
		}
	}
	if alignment == 0 {
		return nil, fmt.Errorf("gguf: invalid alignment 0")
	}

	var tensors []TensorInfo
	for i := uint64(0); i < tensorCount; i++ {
		name, err := c.string()
		if err != nil {
			return nil, fmt.Errorf("gguf: read tensor %d name: %w", i, err)
		}
		ndims, err := c.u32()
		if err != nil {
			return nil, fmt.Errorf("gguf: read tensor %q dims: %w", name, err)
		}
		if uint64(ndims) > uint64(c.size-c.off)/8 {
			return nil, fmt.Errorf("gguf: tensor %q dims count %d exceeds remaining data", name, ndims)
		}
		dims := make([]uint64, ndims)
		for j := range dims {
			dims[j], err = c.u64()
			if err != nil {
				return nil, fmt.Errorf("gguf: read tensor %q dim %d: %w", name, j, err)
			}
		}
		typ, err := c.u32()
		if err != nil {
			return nil, fmt.Errorf("gguf: read tensor %q type: %w", name, err)
		}
		off, err := c.u64()
		if err != nil {
			return nil, fmt.Errorf("gguf: read tensor %q offset: %w", name, err)
		}
		tensors = append(tensors, TensorInfo{Name: name, Dims: dims, Type: typ, Offset: off})
	}

	dataOffset := alignUp(uint64(c.off), uint64(alignment))

	return &File{
		Version:    version,
		Alignment:  alignment,
		Metadata:   metadata,
		Tensors:    tensors,
		DataOffset: int64(dataOffset),
		r:          r,
		size:       size,
	}, nil
}

func lookup(kvs []KV, key string) (Value, bool) {
	for _, kv := range kvs {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return Value{}, false
}
