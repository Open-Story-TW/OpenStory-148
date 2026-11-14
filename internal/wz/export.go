package wz

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"strings"
)

// ExportXML writes the image at path (for example "Map/Map9/915000000.img")
// as the XML the server's data root uses. keep, when set, chooses which of the
// image's top-level entries are written; pictures and sounds keep only their
// names and sizes.
func (a *Archive) ExportXML(path string, keep func(name string) bool) ([]byte, error) {
	offset, inner, err := a.find(path)
	if err != nil {
		return nil, err
	}
	if len(inner) != 0 {
		return nil, ErrNotFound
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r := reader{a: a, pos: offset, base: offset}
	if r.block() != "Property" {
		return nil, ErrUnsupported
	}
	r.read(2)
	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	name := path[strings.LastIndex(path, "/")+1:]
	out.WriteString(`<imgdir name="` + escape(name) + `">`)
	r.exportList(&out, keep)
	out.WriteString(`</imgdir>`)
	if r.err != nil {
		return nil, r.err
	}
	return out.Bytes(), nil
}

// Images lists the entries of a directory inside the archive ("" is the root).
func (a *Archive) Images(dir string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	at := &a.root
	for _, part := range strings.Split(strings.Trim(dir, "/"), "/") {
		if part == "" {
			continue
		}
		if err := a.load(at); err != nil {
			return nil, err
		}
		var next *entry
		for i := range at.children {
			if at.children[i].name == part && at.children[i].dir {
				next = &at.children[i]
			}
		}
		if next == nil {
			return nil, ErrNotFound
		}
		at = next
	}
	if err := a.load(at); err != nil {
		return nil, err
	}
	names := make([]string, len(at.children))
	for i, c := range at.children {
		names[i] = c.name
	}
	return names, nil
}

func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (r *reader) exportList(out *bytes.Buffer, keep func(string) bool) {
	count := r.cint()
	if count < 0 || count > 1<<20 {
		r.err = ErrUnsupported
		return
	}
	for i := int32(0); i < count && r.err == nil; i++ {
		name := escape(r.block())
		var text string
		switch kind := r.u8(); kind {
		case 0:
			text = fmt.Sprintf(`<null name="%s"/>`, name)
		case 2, 11:
			text = fmt.Sprintf(`<short name="%s" value="%d"/>`, name, int16(r.u16()))
		case 3, 19:
			text = fmt.Sprintf(`<int name="%s" value="%d"/>`, name, r.cint())
		case 20:
			text = fmt.Sprintf(`<long name="%s" value="%d"/>`, name, r.clong())
		case 4:
			v := float32(0)
			if r.u8() == 0x80 {
				v = math.Float32frombits(r.u32())
			}
			text = fmt.Sprintf(`<float name="%s" value="%g"/>`, name, v)
		case 5:
			b := r.read(8)
			v := float64(0)
			if len(b) == 8 {
				v = math.Float64frombits(uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 | uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56)
			}
			text = fmt.Sprintf(`<double name="%s" value="%g"/>`, name, v)
		case 8:
			text = fmt.Sprintf(`<string name="%s" value="%s"/>`, name, escape(r.block()))
		case 9:
			end := int64(r.u32())
			end += r.pos
			if keep == nil || keep(name) {
				r.exportObject(out, name)
			}
			r.pos = end
			continue
		default:
			r.err = fmt.Errorf("wz: unknown property type %d", kind)
			return
		}
		if keep == nil || keep(name) {
			out.WriteString(text)
		}
	}
}

func (r *reader) exportObject(out *bytes.Buffer, name string) {
	switch kind := r.block(); kind {
	case "Property":
		r.read(2)
		out.WriteString(`<imgdir name="` + name + `">`)
		r.exportList(out, nil)
		out.WriteString(`</imgdir>`)
	case "Canvas":
		r.read(1)
		var inner bytes.Buffer
		if r.u8() == 1 {
			r.read(2)
			r.exportList(&inner, nil)
		}
		fmt.Fprintf(out, `<canvas name="%s" width="%d" height="%d">`, name, r.cint(), r.cint())
		out.Write(inner.Bytes())
		out.WriteString(`</canvas>`)
	case "Shape2D#Vector2D":
		fmt.Fprintf(out, `<vector name="%s" x="%d" y="%d"/>`, name, r.cint(), r.cint())
	case "UOL":
		r.read(1)
		fmt.Fprintf(out, `<uol name="%s" value="%s"/>`, name, escape(r.block()))
	case "Sound_DX8":
		fmt.Fprintf(out, `<sound name="%s"/>`, name)
	default:
		fmt.Fprintf(out, `<extended name="%s"/>`, name)
	}
}
