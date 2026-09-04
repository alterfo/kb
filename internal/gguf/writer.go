package gguf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
)

type WriterTensor struct {
	Name string
	Dims []uint64
	Type uint32
	Data []byte
}

type Writer struct {
	version   uint32
	alignment uint32
	metadata  []KV
	tensors   []WriterTensor
}

func NewWriter() *Writer {
	return &Writer{version: MaxSupportedVersion, alignment: DefaultAlignment}
}

func (w *Writer) SetVersion(version uint32) {
	w.version = version
}

func (w *Writer) SetAlignment(alignment uint32) {
	w.alignment = alignment
}

func (w *Writer) AddMetadata(key string, value Value) {
	w.metadata = append(w.metadata, KV{Key: key, Value: value})
}

func (w *Writer) AddTensor(name string, dims []uint64, typ uint32, data []byte) {
	w.tensors = append(w.tensors, WriterTensor{Name: name, Dims: dims, Type: typ, Data: data})
}

func (w *Writer) Bytes() ([]byte, error) {
	if w.version < MinSupportedVersion || w.version > MaxSupportedVersion {
		return nil, fmt.Errorf("gguf: unsupported version %d", w.version)
	}
	if w.alignment == 0 {
		return nil, fmt.Errorf("gguf: invalid alignment 0")
	}

	offsets := tensorOffsets(w.tensors, w.alignment)

	var buf bytes.Buffer
	buf.WriteString(Magic)
	writeU32(&buf, w.version)
	writeU64(&buf, uint64(len(w.tensors)))
	writeU64(&buf, uint64(len(w.metadata)))

	for _, kv := range w.metadata {
		appendString(&buf, kv.Key)
		if err := appendValue(&buf, kv.Value); err != nil {
			return nil, fmt.Errorf("gguf: metadata %q: %w", kv.Key, err)
		}
	}

	for i, t := range w.tensors {
		appendString(&buf, t.Name)
		writeU32(&buf, uint32(len(t.Dims)))
		for _, d := range t.Dims {
			writeU64(&buf, d)
		}
		writeU32(&buf, t.Type)
		writeU64(&buf, offsets[i])
	}

	pad(&buf, w.alignment)

	for _, t := range w.tensors {
		buf.Write(t.Data)
		pad(&buf, w.alignment)
	}

	return buf.Bytes(), nil
}

func tensorOffsets(tensors []WriterTensor, alignment uint32) []uint64 {
	offsets := make([]uint64, len(tensors))
	var cur uint64
	for i, t := range tensors {
		offsets[i] = cur
		cur = alignUp(cur+uint64(len(t.Data)), uint64(alignment))
	}
	return offsets
}

func pad(buf *bytes.Buffer, alignment uint32) {
	for buf.Len()%int(alignment) != 0 {
		buf.WriteByte(0)
	}
}

func appendString(buf *bytes.Buffer, s string) {
	writeU64(buf, uint64(len(s)))
	buf.WriteString(s)
}

func writeU16(buf *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], v)
	buf.Write(tmp[:])
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	buf.Write(tmp[:])
}

func writeU64(buf *bytes.Buffer, v uint64) {
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	buf.Write(tmp[:])
}

func writeF32(buf *bytes.Buffer, v float32) {
	writeU32(buf, math.Float32bits(v))
}

func writeF64(buf *bytes.Buffer, v float64) {
	writeU64(buf, math.Float64bits(v))
}

func appendValue(buf *bytes.Buffer, v Value) error {
	if v.Type == TypeArray {
		arr, ok := v.Data.(Array)
		if !ok {
			return fmt.Errorf("array value has data type %T, want Array", v.Data)
		}
		writeU32(buf, uint32(TypeArray))
		writeU32(buf, uint32(arr.Type))
		n, err := arrayLen(arr)
		if err != nil {
			return err
		}
		writeU64(buf, n)
		return appendArrayElements(buf, arr)
	}
	writeU32(buf, uint32(v.Type))
	return appendScalar(buf, v.Type, v.Data)
}

func appendScalar(buf *bytes.Buffer, t ValueType, d any) error {
	switch t {
	case TypeUint8:
		v, ok := d.(uint8)
		if !ok {
			return fmt.Errorf("value type uint8 has data type %T", d)
		}
		buf.WriteByte(v)
	case TypeInt8:
		v, ok := d.(int8)
		if !ok {
			return fmt.Errorf("value type int8 has data type %T", d)
		}
		buf.WriteByte(byte(v))
	case TypeUint16:
		v, ok := d.(uint16)
		if !ok {
			return fmt.Errorf("value type uint16 has data type %T", d)
		}
		writeU16(buf, v)
	case TypeInt16:
		v, ok := d.(int16)
		if !ok {
			return fmt.Errorf("value type int16 has data type %T", d)
		}
		writeU16(buf, uint16(v))
	case TypeUint32:
		v, ok := d.(uint32)
		if !ok {
			return fmt.Errorf("value type uint32 has data type %T", d)
		}
		writeU32(buf, v)
	case TypeInt32:
		v, ok := d.(int32)
		if !ok {
			return fmt.Errorf("value type int32 has data type %T", d)
		}
		writeU32(buf, uint32(v))
	case TypeFloat32:
		v, ok := d.(float32)
		if !ok {
			return fmt.Errorf("value type float32 has data type %T", d)
		}
		writeF32(buf, v)
	case TypeBool:
		v, ok := d.(bool)
		if !ok {
			return fmt.Errorf("value type bool has data type %T", d)
		}
		if v {
			buf.WriteByte(1)
		} else {
			buf.WriteByte(0)
		}
	case TypeString:
		v, ok := d.(string)
		if !ok {
			return fmt.Errorf("value type string has data type %T", d)
		}
		appendString(buf, v)
	case TypeUint64:
		v, ok := d.(uint64)
		if !ok {
			return fmt.Errorf("value type uint64 has data type %T", d)
		}
		writeU64(buf, v)
	case TypeInt64:
		v, ok := d.(int64)
		if !ok {
			return fmt.Errorf("value type int64 has data type %T", d)
		}
		writeU64(buf, uint64(v))
	case TypeFloat64:
		v, ok := d.(float64)
		if !ok {
			return fmt.Errorf("value type float64 has data type %T", d)
		}
		writeF64(buf, v)
	default:
		return fmt.Errorf("gguf: unsupported metadata value type %d", t)
	}
	return nil
}

func arrayLen(a Array) (uint64, error) {
	rv := reflect.ValueOf(a.Data)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		return uint64(rv.Len()), nil
	default:
		return 0, fmt.Errorf("array data type %T is not a slice", a.Data)
	}
}

func appendArrayElements(buf *bytes.Buffer, a Array) error {
	rv := reflect.ValueOf(a.Data)
	for i := 0; i < rv.Len(); i++ {
		if err := appendArrayElement(buf, a.Type, rv.Index(i)); err != nil {
			return err
		}
	}
	return nil
}

func appendArrayElement(buf *bytes.Buffer, t ValueType, elem reflect.Value) error {
	if t == TypeArray {
		v, ok := elem.Interface().(Value)
		if !ok {
			return fmt.Errorf("nested array element has type %T, want Value", elem.Interface())
		}
		return appendValue(buf, v)
	}
	return appendScalar(buf, t, elem.Interface())
}

func Uint8(v uint8) Value     { return Value{Type: TypeUint8, Data: v} }
func Int8(v int8) Value       { return Value{Type: TypeInt8, Data: v} }
func Uint16(v uint16) Value   { return Value{Type: TypeUint16, Data: v} }
func Int16(v int16) Value     { return Value{Type: TypeInt16, Data: v} }
func Uint32(v uint32) Value   { return Value{Type: TypeUint32, Data: v} }
func Int32(v int32) Value     { return Value{Type: TypeInt32, Data: v} }
func Float32(v float32) Value { return Value{Type: TypeFloat32, Data: v} }
func Bool(v bool) Value       { return Value{Type: TypeBool, Data: v} }
func Str(v string) Value      { return Value{Type: TypeString, Data: v} }
func Uint64(v uint64) Value   { return Value{Type: TypeUint64, Data: v} }
func Int64(v int64) Value     { return Value{Type: TypeInt64, Data: v} }
func Float64(v float64) Value { return Value{Type: TypeFloat64, Data: v} }

func Uint8Array(v []uint8) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeUint8, Data: v}}
}
func Int8Array(v []int8) Value { return Value{Type: TypeArray, Data: Array{Type: TypeInt8, Data: v}} }
func Uint16Array(v []uint16) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeUint16, Data: v}}
}
func Int16Array(v []int16) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeInt16, Data: v}}
}
func Uint32Array(v []uint32) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeUint32, Data: v}}
}
func Int32Array(v []int32) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeInt32, Data: v}}
}
func Float32Array(v []float32) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeFloat32, Data: v}}
}
func BoolArray(v []bool) Value { return Value{Type: TypeArray, Data: Array{Type: TypeBool, Data: v}} }
func StringArray(v []string) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeString, Data: v}}
}
func Uint64Array(v []uint64) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeUint64, Data: v}}
}
func Int64Array(v []int64) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeInt64, Data: v}}
}
func Float64Array(v []float64) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeFloat64, Data: v}}
}
func ValueArray(v []Value) Value {
	return Value{Type: TypeArray, Data: Array{Type: TypeArray, Data: v}}
}
