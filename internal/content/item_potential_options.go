package content

import (
	"fmt"
	"strconv"
)

// Native equipment-info loader 0x57B2E6 reads info/option children0..4;
// the IDs and per-line levels feed 0x581B67. Do not confuse this template
// array with the instance's five mutable option words or general item flags.
func readItemPotentialOptions(info *wzNode) (int, [5]ItemPotentialOption, error) {
	var out [5]ItemPotentialOption
	node := info.child("option")
	if node == nil {
		return 0, out, nil
	}
	if node.XMLName.Local != "imgdir" || len(node.Children) > len(out) {
		return 0, out, fmt.Errorf("invalid fixed potential option directory")
	}
	count := len(node.Children)
	for index := 0; index < count; index++ {
		name := strconv.Itoa(index)
		var row *wzNode
		for j := range node.Children {
			if node.Children[j].Name == name {
				if row != nil {
					return 0, out, fmt.Errorf("duplicate fixed potential position %d", index)
				}
				row = &node.Children[j]
			}
		}
		if row == nil || row.XMLName.Local != "imgdir" {
			return 0, out, fmt.Errorf("fixed potential positions must be a dense0..4 prefix")
		}
		fields, err := parsePotentialAttributes(row)
		if err != nil {
			return 0, out, err
		}
		id, level := fields.Values["option"], fields.Values["level"]
		_, textID := fields.Text["option"]
		_, textLevel := fields.Text["level"]
		if textID || textLevel || id < 0 || id > 65535 {
			return 0, out, fmt.Errorf("incomplete or invalid fixed potential position %d", index)
		}
		// Native variant helper0x404C6C and loader0x57B2E6 establish zero
		// defaults for missing option/level fields in an existing child.
		// Keep zero/negative levels: native applies only positive overrides.
		// Large positive values follow its later >=21-index fallback.
		out[index] = ItemPotentialOption{ID: id, Level: level}
	}
	return count, out, nil
}
