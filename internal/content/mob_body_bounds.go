package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrMobBodyUnknown = errors.New("mob body envelope unavailable")

// MobBodyRect uses native RECT edges. Right and Bottom are exclusive; empty
// rectangles are not targets. These are local coordinates relative to mob feet.
type MobBodyRect struct{ Left, Top, Right, Bottom int }

func (r MobBodyRect) Nonempty() bool      { return r.Left < r.Right && r.Top < r.Bottom }
func (r MobBodyRect) Mirror() MobBodyRect { return MobBodyRect{-r.Right, r.Top, -r.Left, r.Bottom} }
func unionMobBody(a, b MobBodyRect) MobBodyRect {
	if !a.Nonempty() {
		return b
	}
	if !b.Nonempty() {
		return a
	}
	return MobBodyRect{min(a.Left, b.Left), min(a.Top, b.Top), max(a.Right, b.Right), max(a.Bottom, b.Bottom)}
}

type MobBodySource struct {
	ID     int
	SHA256 string
}

// MobBodyBounds is a static superset of resolved per-frame lt/rb rectangles
// across the mob's visual link chain and both facings. It is NOT the currently
// playing frame, client eligibility, or proof that the client selected a mob.
// FrameCount counts nonempty frame references, including local aliases.
type MobBodyBounds struct {
	Envelope           MobBodyRect
	UnmirroredEnvelope MobBodyRect
	FrameCount         int
	Sources            []MobBodySource
	Kind               string
}

type bodyWZNode struct {
	XMLName  xml.Name
	Name     string       `xml:"name,attr"`
	Value    string       `xml:"value,attr"`
	X        *int         `xml:"x,attr"`
	Y        *int         `xml:"y,attr"`
	Children []bodyWZNode `xml:",any"`
}

func (n *bodyWZNode) child(name string) *bodyWZNode {
	for i := range n.Children {
		if n.Children[i].Name == name {
			return &n.Children[i]
		}
	}
	return nil
}

// bodyWZResolver follows only relative aliases within this exact XML document.
// No filesystem lookup, absolute path, external image or unbounded UOL chain is
// accepted. Node identity is used for cycle detection, not attacker text paths.
type bodyWZResolver struct {
	parents   map[*bodyWZNode]*bodyWZNode
	children  map[*bodyWZNode]map[string]*bodyWZNode
	remaining int
}

func newBodyWZResolver(root *bodyWZNode) (*bodyWZResolver, error) {
	r := &bodyWZResolver{parents: make(map[*bodyWZNode]*bodyWZNode), children: make(map[*bodyWZNode]map[string]*bodyWZNode), remaining: 262144}
	var walk func(*bodyWZNode, *bodyWZNode, int) error
	walk = func(n, parent *bodyWZNode, depth int) error {
		if depth > 128 || len(r.parents) >= 65536 {
			return ErrMobBodyUnknown
		}
		r.parents[n] = parent
		children := make(map[string]*bodyWZNode, len(n.Children))
		r.children[n] = children
		for i := range n.Children {
			child := &n.Children[i]
			if children[child.Name] != nil {
				return ErrMobBodyUnknown
			}
			children[child.Name] = child
			if err := walk(child, n, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, nil, 0); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *bodyWZResolver) resolve(n *bodyWZNode) (*bodyWZNode, error) {
	active := map[*bodyWZNode]bool{}
	steps := 0
	var follow func(*bodyWZNode) (*bodyWZNode, error)
	follow = func(n *bodyWZNode) (*bodyWZNode, error) {
		r.remaining--
		if n == nil || r.remaining < 0 {
			return nil, ErrMobBodyUnknown
		}
		if n.XMLName.Local != "uol" {
			return n, nil
		}
		steps++
		if steps > 16 || active[n] || n.Value == "" || len(n.Value) > 1024 || strings.HasPrefix(n.Value, "/") || strings.ContainsAny(n.Value, "\\:") {
			return nil, ErrMobBodyUnknown
		}
		active[n] = true
		defer delete(active, n)
		current := r.parents[n]
		for _, part := range strings.Split(n.Value, "/") {
			r.remaining--
			if current == nil || part == "" || r.remaining < 0 {
				return nil, ErrMobBodyUnknown
			}
			if part == "." {
				continue
			}
			if part == ".." {
				current = r.parents[current]
				continue
			}
			var err error
			current, err = follow(r.children[current][part])
			if err != nil {
				return nil, err
			}
		}
		return follow(current)
	}
	return follow(n)
}

// Bound tree depth/count before encoding/xml builds its recursive structure.
func checkBodyXMLBudget(raw []byte) error {
	d := xml.NewDecoder(bytes.NewReader(raw))
	depth, nodes, roots := 0, 0, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			if roots != 1 || depth != 0 {
				return ErrMobBodyUnknown
			}
			return nil
		}
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
			nodes++
			if roots > 1 || depth > 129 || nodes > 65536 {
				return ErrMobBodyUnknown
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(value)) != 0 {
				return ErrMobBodyUnknown
			}
		}
	}
}

func (c *Catalog) MobBodyBounds(id int) (MobBodyBounds, error) {
	c.mu.RLock()
	known, ok := c.mobBodies[id]
	c.mu.RUnlock()
	if ok {
		return cloneMobBodyBounds(known), nil
	}
	// Positive values are pinned for this Catalog's lifetime. No filesystem I/O
	// holds the Catalog mutex or a caller's room mutex; racing initial loads
	// converge on the first published immutable value.
	loaded, err := MobBodyDefinition(c.root, id)
	if err != nil {
		return MobBodyBounds{}, err
	}
	c.mu.Lock()
	if prior, exists := c.mobBodies[id]; exists {
		loaded = prior
	} else {
		if c.mobBodies == nil {
			c.mobBodies = make(map[int]MobBodyBounds)
		}
		c.mobBodies[id] = cloneMobBodyBounds(loaded)
	}
	c.mu.Unlock()
	return cloneMobBodyBounds(loaded), nil
}

func cloneMobBodyBounds(value MobBodyBounds) MobBodyBounds {
	value.Sources = append([]MobBodySource(nil), value.Sources...)
	return value
}

// MobBodyDefinition deliberately does not reuse Mob's stat inheritance: info/link
// is visual. A bounded unknown result never authorizes a guessed point/radius.
// Call before acquiring room mutexes; this loader performs filesystem I/O.
func MobBodyDefinition(root string, id int) (MobBodyBounds, error) {
	out := MobBodyBounds{Kind: "static_frame_envelope_both_facings"}
	unknown := func(reason string) (MobBodyBounds, error) {
		return MobBodyBounds{}, fmt.Errorf("%w: %s", ErrMobBodyUnknown, reason)
	}
	if root == "" {
		return unknown("no mounted visual WZ data")
	}
	seen := map[int]bool{}
	current := id
	for len(out.Sources) < 8 {
		if current < 1 || current > 9999999 || seen[current] {
			return unknown("invalid or cyclic visual link")
		}
		seen[current] = true
		f, err := openWZ(filepath.Join(root, "Mob.wz", fmt.Sprintf("%07d.img.xml", current)))
		if err != nil {
			return unknown(err.Error())
		}
		raw, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
		closeErr := f.Close()
		if err != nil || closeErr != nil || len(raw) > 32<<20 {
			return unknown("unreadable or oversized visual file")
		}
		var node bodyWZNode
		if err = checkBodyXMLBudget(raw); err != nil {
			return unknown("malformed or excessive visual WZ XML")
		}
		if err = xml.Unmarshal(raw, &node); err != nil || node.XMLName.Local != "imgdir" {
			return unknown("malformed visual WZ XML")
		}
		resolver, err := newBodyWZResolver(&node)
		if err != nil {
			return unknown("ambiguous or excessive visual WZ tree")
		}
		hash := sha256.Sum256(raw)
		out.Sources = append(out.Sources, MobBodySource{ID: current, SHA256: hex.EncodeToString(hash[:])})
		frameVisits := 0
		for i := range node.Children {
			action := &node.Children[i]
			if action.Name == "info" {
				continue
			}
			action, err = resolver.resolve(action)
			if err != nil {
				return unknown("unresolved local action alias")
			}
			if action.XMLName.Local != "imgdir" {
				continue
			}
			for i := range action.Children {
				frame := &action.Children[i]
				index, e := strconv.Atoi(frame.Name)
				if e != nil || index < 0 {
					continue
				}
				frameVisits++
				if frameVisits > 4096 {
					return unknown("too many visual frame references")
				}
				frame, err = resolver.resolve(frame)
				if err != nil {
					return unknown("unresolved local frame alias")
				}
				if frame.XMLName.Local != "canvas" {
					continue
				}
				lt, rb := resolver.children[frame]["lt"], resolver.children[frame]["rb"]
				// 419C7B/41A23E stores empty if either lt or rb is absent.
				if lt == nil || rb == nil {
					continue
				}
				lt, err = resolver.resolve(lt)
				if err != nil {
					return unknown("unresolved local body lt alias")
				}
				rb, err = resolver.resolve(rb)
				if err != nil {
					return unknown("unresolved local body rb alias")
				}
				if lt.XMLName.Local != "vector" || rb.XMLName.Local != "vector" || lt.X == nil || lt.Y == nil || rb.X == nil || rb.Y == nil {
					return unknown("malformed body vectors")
				}
				r := MobBodyRect{*lt.X, *lt.Y, *rb.X, *rb.Y}
				for _, v := range []int{r.Left, r.Top, r.Right, r.Bottom} {
					if v < -4096 || v > 4096 {
						return unknown("body extent exceeds reviewed bound")
					}
				}
				if !r.Nonempty() {
					continue
				}
				out.FrameCount++
				if out.FrameCount > 4096 {
					return unknown("too many explicit frame rectangles")
				}
				out.UnmirroredEnvelope = unionMobBody(out.UnmirroredEnvelope, r)
			}
		}
		next := 0
		if info := node.child("info"); info != nil {
			if link := info.child("link"); link != nil {
				next, err = strconv.Atoi(link.Value)
				if err != nil || next < 1 {
					return unknown("invalid visual link")
				}
			}
		}
		if next == 0 {
			if out.FrameCount == 0 {
				return unknown("no nonempty explicit frame body rectangles")
			}
			out.Envelope = unionMobBody(out.UnmirroredEnvelope, out.UnmirroredEnvelope.Mirror())
			return out, nil
		}
		current = next
	}
	return unknown("visual link depth exceeds reviewed bound")
}
