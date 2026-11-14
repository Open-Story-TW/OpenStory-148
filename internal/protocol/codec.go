package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"golang.org/x/text/encoding/traditionalchinese"
)

var ErrMalformed = errors.New("malformed MapleStory packet")

// Reader is a bounds-checked, sticky-error little-endian packet reader.
type Reader struct {
	data []byte
	pos  int
	err  error
}

func NewReader(data []byte) *Reader { return &Reader{data: data} }
func (r *Reader) Remaining() int    { return len(r.data) - r.pos }
func (r *Reader) Pos() int          { return r.pos }
func (r *Reader) BytesBetween(start, end int) []byte {
	if start < 0 || end < start || end > len(r.data) {
		r.err = ErrMalformed
		return nil
	}
	return append([]byte(nil), r.data[start:end]...)
}
func (r *Reader) Err() error { return r.err }
func (r *Reader) Bytes(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > r.Remaining() {
		r.err = ErrMalformed
		return nil
	}
	p := r.data[r.pos : r.pos+n]
	r.pos += n
	return p
}
func (r *Reader) Skip(n int) { r.Bytes(n) }
func (r *Reader) U8() uint8 {
	p := r.Bytes(1)
	if len(p) == 0 {
		return 0
	}
	return p[0]
}
func (r *Reader) U16() uint16 {
	p := r.Bytes(2)
	if len(p) != 2 {
		return 0
	}
	return binary.LittleEndian.Uint16(p)
}
func (r *Reader) I16() int16 { return int16(r.U16()) }
func (r *Reader) U32() uint32 {
	p := r.Bytes(4)
	if len(p) != 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(p)
}
func (r *Reader) I32() int32 { return int32(r.U32()) }
func (r *Reader) I64() int64 {
	p := r.Bytes(8)
	if len(p) != 8 {
		return 0
	}
	return int64(binary.LittleEndian.Uint64(p))
}
func (r *Reader) String() string {
	n := int(r.U16())
	if n > 4096 {
		r.err = ErrMalformed
		return ""
	}
	p := r.Bytes(n)
	if r.err != nil {
		return ""
	}
	decoded, err := traditionalchinese.Big5.NewDecoder().Bytes(p)
	if err != nil {
		r.err = fmt.Errorf("%w: Big5 string", ErrMalformed)
		return ""
	}
	return string(decoded)
}

// Writer encodes the Big5 strings and little-endian numeric fields used by TMS.
// It intentionally has no process/global state and does not log packet contents.
type Writer struct {
	data []byte
	err  error
}

func NewWriter(op uint16) *Writer { w := &Writer{}; w.U16(op); return w }
func NewRawWriter() *Writer       { return &Writer{} }
func (w *Writer) U8(v uint8)      { w.data = append(w.data, v) }
func (w *Writer) Bool(v bool) {
	if v {
		w.U8(1)
	} else {
		w.U8(0)
	}
}
func (w *Writer) U16(v uint16)   { w.data = binary.LittleEndian.AppendUint16(w.data, v) }
func (w *Writer) I16(v int16)    { w.U16(uint16(v)) }
func (w *Writer) U32(v uint32)   { w.data = binary.LittleEndian.AppendUint32(w.data, v) }
func (w *Writer) I32(v int32)    { w.U32(uint32(v)) }
func (w *Writer) I64(v int64)    { w.data = binary.LittleEndian.AppendUint64(w.data, uint64(v)) }
func (w *Writer) Bytes(v []byte) { w.data = append(w.data, v...) }
func (w *Writer) Zero(n int) {
	if n < 0 || n > MaxPacketSize {
		w.err = ErrMalformed
		return
	}
	w.data = append(w.data, make([]byte, n)...)
}
func (w *Writer) String(v string) {
	p, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(v))
	if err != nil || len(p) > math.MaxUint16 {
		w.err = fmt.Errorf("%w: unencodable Big5 string", ErrMalformed)
		return
	}
	w.U16(uint16(len(p)))
	w.Bytes(p)
}
func (w *Writer) FixedString(v string, n int) {
	p, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(v))
	if err != nil || len(p) > n || n < 0 {
		w.err = fmt.Errorf("%w: fixed Big5 string", ErrMalformed)
		return
	}
	w.Bytes(p)
	w.Zero(n - len(p))
}
func (w *Writer) Err() error { return w.err }
func (w *Writer) Packet() []byte {
	if w.err != nil {
		return nil
	}
	return append([]byte(nil), w.data...)
}
