// Package wz reads images out of the client's binary .wz archives. The game
// services only use the XML dump, which has no pixels; the web admin uses
// this to show the same item icons as the client. It never writes.
package wz

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"math/bits"
	"os"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrNotFound    = errors.New("wz: not found")
	ErrUnsupported = errors.New("wz: unsupported format")
)

// The archive cipher is AES-256 in a chained mode over a region IV. The key
// is the same one the client uses for packets.
var aesKey = [32]byte{0x13, 0, 0, 0, 0x08, 0, 0, 0, 0x06, 0, 0, 0, 0xB4, 0, 0, 0, 0x1B, 0, 0, 0, 0x0F, 0, 0, 0, 0x33, 0, 0, 0, 0x52, 0, 0, 0}

// Region IVs, most likely first for a TMS client. The last has no cipher.
var regionIVs = [][4]byte{{0xB9, 0x7D, 0x63, 0xE9}, {0x4D, 0x23, 0xC7, 0x2B}, {}}

type keyStream struct {
	iv   [4]byte
	mu   sync.Mutex
	data []byte
}

// bytes returns at least n bytes of key stream.
func (k *keyStream) bytes(n int) []byte {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.data) >= n {
		return k.data
	}
	size := (n/4096 + 1) * 4096
	next := make([]byte, size)
	if k.iv != ([4]byte{}) {
		block, _ := aes.NewCipher(aesKey[:])
		var chunk [16]byte
		for i := 0; i < 16; i++ {
			chunk[i] = k.iv[i%4]
		}
		for at := 0; at < size; at += 16 {
			block.Encrypt(chunk[:], chunk[:])
			copy(next[at:], chunk[:])
		}
	}
	k.data = next
	return next
}

type entry struct {
	name     string
	dir      bool
	offset   int64
	children []entry
	loaded   bool
}

// Archive is one open .wz file. It is safe for concurrent use.
type Archive struct {
	file  *os.File
	size  int64
	start int64
	hash  uint32
	key   *keyStream
	mu    sync.Mutex
	root  entry
}

type reader struct {
	a    *Archive
	pos  int64
	base int64 // start of the image the reader is in
	err  error
}

func (r *reader) read(n int) []byte {
	if r.err != nil {
		return make([]byte, n)
	}
	if n < 0 || r.pos < 0 || r.pos+int64(n) > r.a.size {
		r.err = fmt.Errorf("wz: read of %d bytes at %d is outside the file", n, r.pos)
		return make([]byte, max(n, 0)%(1<<20))
	}
	buf := make([]byte, n)
	if _, err := r.a.file.ReadAt(buf, r.pos); err != nil {
		r.err = err
	}
	r.pos += int64(n)
	return buf
}
func (r *reader) u8() byte    { return r.read(1)[0] }
func (r *reader) u16() uint16 { return binary.LittleEndian.Uint16(r.read(2)) }
func (r *reader) u32() uint32 { return binary.LittleEndian.Uint32(r.read(4)) }
func (r *reader) i32() int32  { return int32(r.u32()) }

// cint is the archive's packed int: one byte, or -128 followed by four.
func (r *reader) cint() int32 {
	if b := int8(r.u8()); b != -128 {
		return int32(b)
	}
	return r.i32()
}
func (r *reader) clong() int64 {
	if b := int8(r.u8()); b != -128 {
		return int64(b)
	}
	return int64(binary.LittleEndian.Uint64(r.read(8)))
}

func (r *reader) str() string {
	n := int32(int8(r.u8()))
	if n == 0 {
		return ""
	}
	if n > 0 {
		if n == 127 {
			n = r.i32()
		}
		if n < 0 || n > 1<<16 {
			r.err = errors.New("wz: string too long")
			return ""
		}
		raw, key := r.read(int(n)*2), r.a.key.bytes(int(n)*2)
		out := make([]rune, n)
		mask := uint16(0xAAAA)
		for i := range out {
			out[i] = rune(binary.LittleEndian.Uint16(raw[i*2:]) ^ mask ^ binary.LittleEndian.Uint16(key[i*2:]))
			mask++
		}
		return string(out)
	}
	if n == -128 {
		n = r.i32()
	} else {
		n = -n
	}
	if n < 0 || n > 1<<16 {
		r.err = errors.New("wz: string too long")
		return ""
	}
	raw, key := r.read(int(n)), r.a.key.bytes(int(n))
	mask := byte(0xAA)
	for i := range raw {
		raw[i] ^= mask ^ key[i]
		mask++
	}
	return string(raw)
}

// block is a string stored inline or as an offset into the current image.
func (r *reader) block() string {
	switch t := r.u8(); t {
	case 0x00, 0x73:
		return r.str()
	case 0x01, 0x1B:
		at := r.base + int64(r.u32())
		side := reader{a: r.a, pos: at, base: r.base}
		s := side.str()
		if side.err != nil && r.err == nil {
			r.err = side.err
		}
		return s
	default:
		if r.err == nil {
			r.err = fmt.Errorf("wz: unknown string block %#x", t)
		}
		return ""
	}
}

func (r *reader) offset() int64 {
	at := uint32(r.pos-r.a.start) ^ 0xFFFFFFFF
	at *= r.a.hash
	at -= 0x581C3F6D
	at = bits.RotateLeft32(at, int(at&0x1F))
	at ^= r.u32()
	return int64(at + uint32(r.a.start)*2)
}

func versionHash(version int) (hash uint32, encoded uint16) {
	for _, c := range strconv.Itoa(version) {
		hash = hash*32 + uint32(c) + 1
	}
	return hash, uint16(0xFF ^ byte(hash>>24) ^ byte(hash>>16) ^ byte(hash>>8) ^ byte(hash))
}

// Open reads the archive's directory. The region cipher and the client
// version are found by trying each until the first image parses.
func Open(path string) (*Archive, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	a := &Archive{file: file, size: info.Size()}
	head := reader{a: a}
	if string(head.read(4)) != "PKG1" {
		file.Close()
		return nil, fmt.Errorf("%s is not a wz archive", path)
	}
	head.read(8)
	a.start = int64(head.u32())
	head.pos = a.start
	encoded := head.u16()
	if head.err != nil {
		file.Close()
		return nil, head.err
	}
	for _, iv := range regionIVs {
		a.key = &keyStream{iv: iv}
		for version := 1; version < 1000; version++ {
			hash, enc := versionHash(version)
			if enc != encoded {
				continue
			}
			a.hash = hash
			a.root = entry{dir: true, offset: a.start + 2}
			if a.plausible() {
				return a, nil
			}
		}
	}
	file.Close()
	return nil, fmt.Errorf("%s: no known cipher and version opens this archive", path)
}

func (a *Archive) Close() error { return a.file.Close() }

// plausible reports whether the current cipher and version hash lead from the
// root directory to something that parses as an image.
func (a *Archive) plausible() bool {
	dir := &a.root
	for depth := 0; depth < 8; depth++ {
		if err := a.load(dir); err != nil || len(dir.children) == 0 {
			return false
		}
		for _, child := range dir.children {
			for _, c := range child.name {
				if c < 0x20 || c > 0x7E {
					return false
				}
			}
		}
		next := &dir.children[0]
		for i := range dir.children {
			if !dir.children[i].dir {
				next = &dir.children[i]
				break
			}
		}
		if !next.dir {
			r := reader{a: a, pos: next.offset, base: next.offset}
			return r.block() == "Property" && r.err == nil
		}
		dir = next
	}
	return false
}

func (a *Archive) load(dir *entry) error {
	if dir.loaded {
		return nil
	}
	r := reader{a: a, pos: dir.offset}
	count := r.cint()
	if count < 0 || count > 100000 {
		return errors.New("wz: directory too large")
	}
	children := make([]entry, 0, count)
	for i := int32(0); i < count && r.err == nil; i++ {
		kind := r.u8()
		var name string
		switch kind {
		case 1:
			r.read(6)
			r.offset()
			continue
		case 2:
			at := a.start + int64(r.u32())
			side := reader{a: a, pos: at}
			kind = side.u8()
			name = side.str()
			if side.err != nil {
				return side.err
			}
		case 3, 4:
			name = r.str()
		default:
			return fmt.Errorf("wz: unknown directory entry %d", kind)
		}
		r.cint() // size
		r.cint() // checksum
		children = append(children, entry{name: name, dir: kind == 3, offset: r.offset()})
	}
	if r.err != nil {
		return r.err
	}
	dir.children, dir.loaded = children, true
	return nil
}

// find walks the directory part of path and returns the image's offset and
// the rest of the path inside it.
func (a *Archive) find(path string) (int64, []string, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	a.mu.Lock()
	defer a.mu.Unlock()
	dir := &a.root
	for i, part := range parts {
		if err := a.load(dir); err != nil {
			return 0, nil, err
		}
		var next *entry
		for j := range dir.children {
			if dir.children[j].name == part {
				next = &dir.children[j]
				break
			}
		}
		if next == nil {
			return 0, nil, ErrNotFound
		}
		if !next.dir {
			return next.offset, parts[i+1:], nil
		}
		dir = next
	}
	return 0, nil, ErrNotFound
}

// node is what a path inside an image ends at.
type node struct {
	kind string // "Canvas", "UOL" or the name of another object
	link string
	img  *canvas
}

type canvas struct {
	width, height, format int
	data                  []byte
}

// walk follows path through the property list at r.
func (r *reader) walk(path []string) (node, error) {
	count := r.cint()
	if count < 0 || count > 1<<20 {
		return node{}, errors.New("wz: property list too large")
	}
	for i := int32(0); i < count && r.err == nil; i++ {
		name := r.block()
		switch kind := r.u8(); kind {
		case 0:
		case 2, 11:
			r.read(2)
		case 3, 19:
			r.cint()
		case 20:
			r.clong()
		case 4:
			if r.u8() == 0x80 {
				r.read(4)
			}
		case 5:
			r.read(8)
		case 8:
			r.block()
		case 9:
			end := int64(r.u32())
			end += r.pos
			if name == path[0] {
				return r.object(path[1:])
			}
			r.pos = end
		default:
			return node{}, fmt.Errorf("wz: unknown property type %d", kind)
		}
	}
	if r.err != nil {
		return node{}, r.err
	}
	return node{}, ErrNotFound
}

// object reads the object at r and continues into it with the rest of path.
func (r *reader) object(path []string) (node, error) {
	switch kind := r.block(); kind {
	case "Property":
		r.read(2)
		if len(path) == 0 {
			return node{kind: kind}, r.err
		}
		return r.walk(path)
	case "Canvas":
		r.read(1)
		if r.u8() == 1 {
			r.read(2)
			if len(path) > 0 {
				return r.walk(path)
			}
			// Step over the canvas's own properties to reach its pixels.
			if _, err := r.walk([]string{"\x00"}); err != nil && !errors.Is(err, ErrNotFound) {
				return node{}, err
			}
		} else if len(path) > 0 {
			return node{}, ErrNotFound
		}
		c := &canvas{width: int(r.cint()), height: int(r.cint())}
		c.format = int(r.cint()) + int(r.u8())
		r.read(4)
		size := int(r.i32())
		if r.err != nil || c.width < 1 || c.height < 1 || c.width > 4096 || c.height > 4096 || size < 2 || size > 64<<20 {
			return node{}, errors.Join(ErrUnsupported, r.err)
		}
		r.read(1)
		c.data = r.read(size - 1)
		return node{kind: kind, img: c}, r.err
	case "UOL":
		r.read(1)
		link := r.block()
		if len(path) > 0 {
			return node{}, ErrNotFound
		}
		return node{kind: kind, link: link}, r.err
	default:
		if r.err != nil {
			return node{}, r.err
		}
		if len(path) > 0 {
			return node{}, ErrNotFound
		}
		return node{kind: kind}, nil
	}
}

// Canvas returns the picture at path, for example
// "Consume/0200.img/02000000/info/icon". A link to another picture in the
// same image is followed.
func (a *Archive) Canvas(path string) (image.Image, error) {
	offset, inner, err := a.find(path)
	if err != nil {
		return nil, err
	}
	for hop := 0; hop < 4; hop++ {
		r := reader{a: a, pos: offset, base: offset}
		found, err := r.object(inner)
		if err != nil {
			return nil, err
		}
		switch found.kind {
		case "Canvas":
			return found.img.decode(a.key)
		case "UOL":
			next := inner[: len(inner)-1 : len(inner)-1]
			for _, part := range strings.Split(found.link, "/") {
				if part == ".." {
					if len(next) == 0 {
						return nil, ErrNotFound
					}
					next = next[:len(next)-1]
				} else if part != "" && part != "." {
					next = append(next, part)
				}
			}
			inner = next
		default:
			return nil, ErrNotFound
		}
	}
	return nil, ErrNotFound
}

func (c *canvas) decode(key *keyStream) (image.Image, error) {
	var need int
	switch c.format {
	case 1, 513:
		need = c.width * c.height * 2
	case 2:
		need = c.width * c.height * 4
	case 517:
		need = c.width * c.height / 128
	default:
		return nil, fmt.Errorf("%w: pixel format %d", ErrUnsupported, c.format)
	}
	data := c.data
	if len(data) < 2 || data[0] != 0x78 {
		// Not a zlib stream: a list of length-prefixed blocks under the cipher.
		var plain []byte
		for at := 0; at+4 <= len(data); {
			size := int(binary.LittleEndian.Uint32(data[at:]))
			at += 4
			if size < 0 || at+size > len(data) {
				return nil, ErrUnsupported
			}
			stream := key.bytes(size)
			for i := 0; i < size; i++ {
				plain = append(plain, data[at+i]^stream[i])
			}
			at += size
		}
		data = plain
	}
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	raw := make([]byte, need)
	// Some streams end without a checksum; the pixels are still complete.
	if n, err := io.ReadFull(zr, raw); n < need {
		return nil, fmt.Errorf("%w: short pixel data: %v", ErrUnsupported, err)
	}
	out := image.NewNRGBA(image.Rect(0, 0, c.width, c.height))
	switch c.format {
	case 1: // 4 bits per channel
		for i := 0; i < c.width*c.height; i++ {
			lo, hi := raw[i*2], raw[i*2+1]
			out.Pix[i*4+0] = (hi & 0x0F) * 17
			out.Pix[i*4+1] = (lo >> 4) * 17
			out.Pix[i*4+2] = (lo & 0x0F) * 17
			out.Pix[i*4+3] = (hi >> 4) * 17
		}
	case 2:
		for i := 0; i < c.width*c.height; i++ {
			out.Pix[i*4+0], out.Pix[i*4+1], out.Pix[i*4+2], out.Pix[i*4+3] = raw[i*4+2], raw[i*4+1], raw[i*4], raw[i*4+3]
		}
	case 513:
		for i := 0; i < c.width*c.height; i++ {
			out.SetNRGBA(i%c.width, i/c.width, rgb565(binary.LittleEndian.Uint16(raw[i*2:])))
		}
	case 517: // one colour per 16x16 block
		for i := 0; i < need/2; i++ {
			pixel := rgb565(binary.LittleEndian.Uint16(raw[i*2:]))
			bx, by := i%(c.width/16)*16, i/(c.width/16)*16
			for y := by; y < by+16 && y < c.height; y++ {
				for x := bx; x < bx+16 && x < c.width; x++ {
					out.SetNRGBA(x, y, pixel)
				}
			}
		}
	}
	return out, nil
}

func rgb565(v uint16) color.NRGBA {
	scale := func(value, top uint16) uint8 { return uint8(math.Round(float64(value) * 255 / float64(top))) }
	return color.NRGBA{R: scale(v>>11, 31), G: scale(v>>5&0x3F, 63), B: scale(v&0x1F, 31), A: 255}
}
