package gguf

import (
	"fmt"
	"io"
)

const Magic = "GGUF"

const DefaultAlignment uint32 = 32

const (
	MinSupportedVersion uint32 = 2
	MaxSupportedVersion uint32 = 3
)

type ValueType uint32

const (
	TypeUint8   ValueType = 0
	TypeInt8    ValueType = 1
	TypeUint16  ValueType = 2
	TypeInt16   ValueType = 3
	TypeUint32  ValueType = 4
	TypeInt32   ValueType = 5
	TypeFloat32 ValueType = 6
	TypeBool    ValueType = 7
	TypeString  ValueType = 8
	TypeArray   ValueType = 9
	TypeUint64  ValueType = 10
	TypeInt64   ValueType = 11
	TypeFloat64 ValueType = 12
)

type Array struct {
	Type ValueType
	Data any
}

type Value struct {
	Type ValueType
	Data any
}

type KV struct {
	Key   string
	Value Value
}

type TensorInfo struct {
	Name   string
	Dims   []uint64
	Type   uint32
	Offset uint64
}

type File struct {
	Version    uint32
	Alignment  uint32
	Metadata   []KV
	Tensors    []TensorInfo
	DataOffset int64

	r      io.ReaderAt
	size   int64
	closer io.Closer
}

func (f *File) Close() error {
	if f.closer != nil {
		return f.closer.Close()
	}
	return nil
}

func (f *File) MetadataValue(key string) (Value, bool) {
	for _, kv := range f.Metadata {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return Value{}, false
}

func (v Value) AsString() (string, error) {
	if v.Type != TypeString {
		return "", fmt.Errorf("gguf: value type %d is not string", v.Type)
	}
	return v.Data.(string), nil
}

func (v Value) AsBool() (bool, error) {
	if v.Type != TypeBool {
		return false, fmt.Errorf("gguf: value type %d is not bool", v.Type)
	}
	return v.Data.(bool), nil
}

func (v Value) AsArray() (Array, error) {
	if v.Type != TypeArray {
		return Array{}, fmt.Errorf("gguf: value type %d is not array", v.Type)
	}
	return v.Data.(Array), nil
}

func (v Value) AsUint64() (uint64, error) {
	switch v.Type {
	case TypeUint8:
		return uint64(v.Data.(uint8)), nil
	case TypeUint16:
		return uint64(v.Data.(uint16)), nil
	case TypeUint32:
		return uint64(v.Data.(uint32)), nil
	case TypeUint64:
		return v.Data.(uint64), nil
	case TypeInt8:
		i := int64(v.Data.(int8))
		if i < 0 {
			return 0, fmt.Errorf("gguf: negative value %d for uint64", i)
		}
		return uint64(i), nil
	case TypeInt16:
		i := int64(v.Data.(int16))
		if i < 0 {
			return 0, fmt.Errorf("gguf: negative value %d for uint64", i)
		}
		return uint64(i), nil
	case TypeInt32:
		i := int64(v.Data.(int32))
		if i < 0 {
			return 0, fmt.Errorf("gguf: negative value %d for uint64", i)
		}
		return uint64(i), nil
	case TypeInt64:
		i := v.Data.(int64)
		if i < 0 {
			return 0, fmt.Errorf("gguf: negative value %d for uint64", i)
		}
		return uint64(i), nil
	default:
		return 0, fmt.Errorf("gguf: value type %d is not an integer", v.Type)
	}
}

func (v Value) AsInt64() (int64, error) {
	switch v.Type {
	case TypeUint8:
		return int64(v.Data.(uint8)), nil
	case TypeUint16:
		return int64(v.Data.(uint16)), nil
	case TypeUint32:
		return int64(v.Data.(uint32)), nil
	case TypeUint64:
		u := v.Data.(uint64)
		if u > 0x7fffffffffffffff {
			return 0, fmt.Errorf("gguf: value %d exceeds int64", u)
		}
		return int64(u), nil
	case TypeInt8:
		return int64(v.Data.(int8)), nil
	case TypeInt16:
		return int64(v.Data.(int16)), nil
	case TypeInt32:
		return int64(v.Data.(int32)), nil
	case TypeInt64:
		return v.Data.(int64), nil
	default:
		return 0, fmt.Errorf("gguf: value type %d is not an integer", v.Type)
	}
}

func (v Value) AsUint32() (uint32, error) {
	u, err := v.AsUint64()
	if err != nil {
		return 0, err
	}
	if u > 0xffffffff {
		return 0, fmt.Errorf("gguf: value %d exceeds uint32", u)
	}
	return uint32(u), nil
}

func (v Value) AsFloat32() (float32, error) {
	switch v.Type {
	case TypeFloat32:
		return v.Data.(float32), nil
	case TypeFloat64:
		return float32(v.Data.(float64)), nil
	default:
		return 0, fmt.Errorf("gguf: value type %d is not a float", v.Type)
	}
}

func (v Value) AsFloat64() (float64, error) {
	switch v.Type {
	case TypeFloat32:
		return float64(v.Data.(float32)), nil
	case TypeFloat64:
		return v.Data.(float64), nil
	default:
		return 0, fmt.Errorf("gguf: value type %d is not a float", v.Type)
	}
}

func (a Array) AsStrings() ([]string, error) {
	if a.Type != TypeString {
		return nil, fmt.Errorf("gguf: array element type %d is not string", a.Type)
	}
	return a.Data.([]string), nil
}

func (a Array) AsBools() ([]bool, error) {
	if a.Type != TypeBool {
		return nil, fmt.Errorf("gguf: array element type %d is not bool", a.Type)
	}
	return a.Data.([]bool), nil
}

func (a Array) AsUint32s() ([]uint32, error) {
	if a.Type != TypeUint32 {
		return nil, fmt.Errorf("gguf: array element type %d is not uint32", a.Type)
	}
	return a.Data.([]uint32), nil
}

func (a Array) AsUint64s() ([]uint64, error) {
	if a.Type != TypeUint64 {
		return nil, fmt.Errorf("gguf: array element type %d is not uint64", a.Type)
	}
	return a.Data.([]uint64), nil
}

func (a Array) AsInt64s() ([]int64, error) {
	if a.Type != TypeInt64 {
		return nil, fmt.Errorf("gguf: array element type %d is not int64", a.Type)
	}
	return a.Data.([]int64), nil
}

func (a Array) AsFloat32s() ([]float32, error) {
	if a.Type != TypeFloat32 {
		return nil, fmt.Errorf("gguf: array element type %d is not float32", a.Type)
	}
	return a.Data.([]float32), nil
}

func (a Array) AsValues() ([]Value, error) {
	if a.Type != TypeArray {
		return nil, fmt.Errorf("gguf: array element type %d is not array", a.Type)
	}
	return a.Data.([]Value), nil
}

func alignUp(n, a uint64) uint64 {
	if a == 0 {
		return n
	}
	rem := n % a
	if rem == 0 {
		return n
	}
	return n + (a - rem)
}
