package content

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ItemText is an item's name and description from String.wz.
type ItemText struct {
	ID   int
	Name string
	Desc string
}

type itemTexts struct {
	once   sync.Once
	byID   map[int]ItemText
	sorted []ItemText
	err    error
}

var itemTextCache sync.Map

func loadItemTexts(root string) *itemTexts {
	value, _ := itemTextCache.LoadOrStore(root, &itemTexts{})
	texts := value.(*itemTexts)
	texts.once.Do(func() {
		texts.byID = map[int]ItemText{}
		if root == "" {
			return
		}
		var collect func(n *wzNode)
		collect = func(n *wzNode) {
			if id, err := strconv.Atoi(n.Name); err == nil && n.child("name") != nil {
				// Faces and hair share the equipment file but are not items.
				if id >= 1000000 && id <= 5999999 {
					texts.byID[id] = ItemText{ID: id, Name: n.stringValue("name", ""), Desc: n.stringValue("desc", "")}
				}
				return
			}
			for i := range n.Children {
				collect(&n.Children[i])
			}
		}
		for _, name := range []string{"Eqp", "Consume", "Ins", "Etc", "Cash", "Pet"} {
			node, err := readWZ(filepath.Join(root, "String.wz", name+".img.xml"))
			if err != nil {
				texts.err = err
				continue
			}
			collect(node)
		}
		for _, text := range texts.byID {
			texts.sorted = append(texts.sorted, text)
		}
		sort.Slice(texts.sorted, func(i, j int) bool { return texts.sorted[i].ID < texts.sorted[j].ID })
	})
	return texts
}

// ItemName returns the item's text; ok is false when String.wz has none.
func ItemName(root string, id int) (ItemText, bool) {
	text, ok := loadItemTexts(root).byID[id]
	return text, ok
}

// SearchItems finds items whose name contains query, or whose ID starts with
// it when query is a number. Results are in ID order.
func SearchItems(root, query string, limit int) ([]ItemText, error) {
	texts := loadItemTexts(root)
	query = strings.ToLower(strings.TrimSpace(query))
	out := []ItemText{}
	if query == "" {
		return out, texts.err
	}
	_, numeric := strconv.Atoi(query)
	for _, text := range texts.sorted {
		if numeric == nil && strings.HasPrefix(strconv.Itoa(text.ID), query) || strings.Contains(strings.ToLower(text.Name), query) {
			out = append(out, text)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, texts.err
}

// ItemIconPath names an item's bag icon inside the game client's own .wz
// files: the archive and the canvas path within it. The server's XML dump
// carries no pixels, so icons can only come from there.
func ItemIconPath(id int) (archive, path string, ok bool) {
	switch {
	case id/10000 == 500:
		return "Item.wz", "Pet/" + strconv.Itoa(id) + ".img/info/icon", true
	case id >= 1000000 && id < 2000000:
		category, _ := equipmentCategory(id / 10000)
		if category == "" {
			return "", "", false
		}
		return "Character.wz", category + "/0" + strconv.Itoa(id) + ".img/info/icon", true
	case id >= 2000000 && id <= 5999999:
		category := map[int]string{2: "Consume", 3: "Install", 4: "Etc", 5: "Cash"}[id/1000000]
		return "Item.wz", category + "/0" + strconv.Itoa(id/10000) + ".img/0" + strconv.Itoa(id) + "/info/icon", true
	}
	return "", "", false
}
