package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// PotentialLevel is one explicit ItemOption WZ level band. Numeric and text
// attributes are both retained: an unimplemented proc must not disappear from
// the source catalog or accidentally become a supported flat stat.
type PotentialLevel struct {
	Values map[string]int
	Text   map[string]string
}

type PotentialOption struct {
	ID, OptionType, RequiredLevel, Weight int
	Description                           string
	Info                                  PotentialLevel
	Levels                                map[int]PotentialLevel
}

// PotentialCatalog is an immutable snapshot of the complete ItemOption file.
// Its WZ weight is source data, not an assertion that Java uses weighted RNG.
// Every exported lookup returns detached maps. Eligibility, rolls, option
// application and equipment level-to-band policy belong to separate contracts.
type PotentialCatalog struct {
	options map[int]PotentialOption
	ids     []int
	sha256  string
}

func LoadPotentialCatalog(root string) (*PotentialCatalog, error) {
	if root == "" {
		return nil, fmt.Errorf("potential options require an explicit WZ root")
	}
	file, err := os.Open(filepath.Join(root, "Item.wz", "ItemOption.img.xml"))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 32<<20 {
		return nil, fmt.Errorf("potential option XML exceeds 32 MiB")
	}
	return parsePotentialCatalog(data)
}

func parsePotentialCatalog(data []byte) (*PotentialCatalog, error) {
	var source wzNode
	d := xml.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(&source); err != nil {
		return nil, err
	}
	if source.XMLName.Local != "imgdir" || source.Name != "ItemOption.img" {
		return nil, fmt.Errorf("invalid potential option root")
	}
	// Reject another document or any non-whitespace content after the root.
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch v := token.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(v)) == 0 {
				continue
			}
		case xml.Comment:
			continue
		}
		return nil, fmt.Errorf("unexpected trailing potential option XML")
	}
	c := &PotentialCatalog{options: make(map[int]PotentialOption)}
	for _, entry := range source.Children {
		id, err := strconv.Atoi(entry.Name)
		if err != nil || entry.XMLName.Local != "imgdir" || id < 1 || id > 65535 {
			return nil, fmt.Errorf("invalid potential option ID %q", entry.Name)
		}
		if _, exists := c.options[id]; exists {
			return nil, fmt.Errorf("duplicate potential option %d", id)
		}
		if len(entry.Children) != 2 || entry.child("info") == nil || entry.child("level") == nil || entry.child("level").XMLName.Local != "imgdir" {
			return nil, fmt.Errorf("incomplete potential option %d", id)
		}
		info, err := parsePotentialAttributes(entry.child("info"))
		if err != nil {
			return nil, fmt.Errorf("option %d info: %w", id, err)
		}
		weight, hasWeight := info.Values["weight"]
		_, textReq := info.Text["reqLevel"]
		_, textType := info.Text["optionType"]
		_, numericDescription := info.Values["string"]
		if !hasWeight || textReq || textType || numericDescription || weight < 0 || info.Values["reqLevel"] < 0 || info.Values["optionType"] < 0 {
			return nil, fmt.Errorf("invalid potential option %d metadata", id)
		}
		o := PotentialOption{ID: id, OptionType: info.Values["optionType"], RequiredLevel: info.Values["reqLevel"],
			Weight: weight, Description: info.Text["string"], Info: info, Levels: make(map[int]PotentialLevel)}
		for _, row := range entry.child("level").Children {
			band, err := strconv.Atoi(row.Name)
			if err != nil || band < 1 || band > 255 {
				return nil, fmt.Errorf("invalid potential %d level band %q", id, row.Name)
			}
			if _, exists := o.Levels[band]; exists {
				return nil, fmt.Errorf("duplicate potential %d level band %d", id, band)
			}
			values, err := parsePotentialAttributes(&row)
			if err != nil {
				return nil, fmt.Errorf("potential %d band %d: %w", id, band, err)
			}
			o.Levels[band] = values
		}
		if len(o.Levels) == 0 {
			return nil, fmt.Errorf("potential %d has no level bands", id)
		}
		c.options[id] = o
		c.ids = append(c.ids, id)
	}
	if len(c.options) == 0 {
		return nil, fmt.Errorf("empty potential option catalog")
	}
	sort.Ints(c.ids)
	hash := sha256.Sum256(data)
	c.sha256 = hex.EncodeToString(hash[:])
	return c, nil
}

func parsePotentialAttributes(node *wzNode) (PotentialLevel, error) {
	out := PotentialLevel{Values: make(map[string]int), Text: make(map[string]string)}
	if node == nil || node.XMLName.Local != "imgdir" {
		return out, fmt.Errorf("expected potential attribute directory")
	}
	seen := make(map[string]bool)
	for _, field := range node.Children {
		if field.Name == "" || seen[field.Name] || len(field.Children) != 0 {
			return out, fmt.Errorf("invalid or duplicate potential attribute %q", field.Name)
		}
		seen[field.Name] = true
		switch field.XMLName.Local {
		case "int", "short", "long":
			value, err := strconv.ParseInt(field.Value, 10, 32)
			if err != nil {
				return out, fmt.Errorf("invalid potential integer %s", field.Name)
			}
			out.Values[field.Name] = int(value)
		case "string":
			out.Text[field.Name] = field.Value
		default:
			return out, fmt.Errorf("unsupported potential attribute kind %s", field.XMLName.Local)
		}
	}
	return out, nil
}

func clonePotentialLevel(source PotentialLevel) PotentialLevel {
	out := PotentialLevel{Values: make(map[string]int, len(source.Values)), Text: make(map[string]string, len(source.Text))}
	for key, value := range source.Values {
		out.Values[key] = value
	}
	for key, value := range source.Text {
		out.Text[key] = value
	}
	return out
}

func (c *PotentialCatalog) SourceSHA256() string { return c.sha256 }
func (c *PotentialCatalog) IDs() []int           { return append([]int(nil), c.ids...) }

func (c *PotentialCatalog) Option(id int) (PotentialOption, bool) {
	item, ok := c.options[id]
	if !ok {
		return PotentialOption{}, false
	}
	item.Info = clonePotentialLevel(item.Info)
	levels := make(map[int]PotentialLevel, len(item.Levels))
	for band, source := range item.Levels {
		levels[band] = clonePotentialLevel(source)
	}
	item.Levels = levels
	return item, true
}

func (c *PotentialCatalog) Level(id, band int) (PotentialLevel, bool) {
	item, ok := c.options[id]
	if !ok {
		return PotentialLevel{}, false
	}
	level, ok := item.Levels[band]
	if !ok {
		return PotentialLevel{}, false
	}
	return clonePotentialLevel(level), true
}
