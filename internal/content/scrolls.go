package content

import (
	"fmt"
	"path/filepath"
	"strings"

	"maple-go-server/internal/domain"
)

type Scroll struct {
	ID, Success, Curse, TargetCategory int
	RequiredItems                      []int
	Stats                              domain.Equipment
}

func ScrollDefinition(root string, id int) (Scroll, error) {
	// Other 20480+ families change potentials, slots, flags, or random stats;
	// they require separate contracts and must not fall through as flat scrolls.
	if root == "" || id/10000 != 204 || id/100%100 >= 80 || id == 2040727 || id == 2041058 {
		return Scroll{}, fmt.Errorf("unsupported ordinary scroll %d", id)
	}
	bundle, err := readWZ(filepath.Join(root, "Item.wz", "Consume", "0204.img.xml"))
	if err != nil {
		return Scroll{}, err
	}
	node := bundle.child(fmt.Sprintf("%08d", id))
	if node == nil {
		return Scroll{}, fmt.Errorf("missing scroll %d", id)
	}
	info := node.child("info")
	if info == nil || info.child("success") == nil {
		return Scroll{}, fmt.Errorf("missing scroll rate %d", id)
	}
	s := Scroll{ID: id, Success: info.intValue("success", -1), Curse: info.intValue("cursed", 0), TargetCategory: id / 100 % 100}
	if s.Success < 0 || s.Success > 100 || s.Curse < 0 || s.Curse > 100 {
		return Scroll{}, fmt.Errorf("invalid scroll rates %d", id)
	}
	if info.intValue("cash", 0) != 0 {
		return Scroll{}, fmt.Errorf("cash scroll %d requires another handler", id)
	}
	for _, child := range info.Children {
		if strings.HasPrefix(child.Name, "inc") {
			switch child.Name {
			case "incSTR", "incDEX", "incINT", "incLUK", "incMHP", "incMMP", "incPAD", "incMAD", "incPDD", "incMDD", "incACC", "incEVA", "incSpeed", "incJump":
			default:
				return Scroll{}, fmt.Errorf("unsupported scroll effect %s", child.Name)
			}
		}
		if child.Name == "incCraft" || child.Name == "incMaxHP" || child.Name == "incMaxMP" || child.Name == "incTUC" || child.Name == "randstat" || child.Name == "recover" {
			return Scroll{}, fmt.Errorf("unsupported scroll effect %s", child.Name)
		}
	}
	s.Stats = domain.Equipment{STR: info.intValue("incSTR", 0), DEX: info.intValue("incDEX", 0), INT: info.intValue("incINT", 0), LUK: info.intValue("incLUK", 0), HP: info.intValue("incMHP", 0), MP: info.intValue("incMMP", 0), WATK: info.intValue("incPAD", 0), MATK: info.intValue("incMAD", 0), WDEF: info.intValue("incPDD", 0), MDEF: info.intValue("incMDD", 0), ACC: info.intValue("incACC", 0), AVOID: info.intValue("incEVA", 0), Speed: info.intValue("incSpeed", 0), Jump: info.intValue("incJump", 0)}
	for _, v := range []int{s.Stats.STR, s.Stats.DEX, s.Stats.INT, s.Stats.LUK, s.Stats.HP, s.Stats.MP, s.Stats.WATK, s.Stats.MATK, s.Stats.WDEF, s.Stats.MDEF, s.Stats.ACC, s.Stats.AVOID, s.Stats.Speed, s.Stats.Jump} {
		if v < 0 || v > 32767 {
			return Scroll{}, fmt.Errorf("invalid ordinary scroll stat")
		}
	}
	if req := node.child("req"); req != nil {
		for _, child := range req.Children {
			if value := req.intValue(child.Name, 0); value > 0 {
				s.RequiredItems = append(s.RequiredItems, value)
			}
		}
	}
	return s, nil
}
