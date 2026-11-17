package content

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"sync"

	"maple-go-server/internal/domain"
)

// Item contains only WZ facts consumed by the implemented inventory rules.
// Lack of a definition is an error, never permission to equip/drop an item.
type Item struct {
	ID         int
	InvType    int
	SlotMax    int
	EquipSlots []int
	Gender     int // -1 unisex
	ReqLevel   int
	ReqJob     int
	ReqSTR     int
	ReqDEX     int
	ReqINT     int
	ReqLUK     int
	Cash       bool
	TradeBlock bool
	Quest      bool
	Overall    bool
	// Display facts for the item description; no inventory rule reads them.
	Only        bool
	ReqFame     int
	AttackSpeed int
	Price       int64
	// ProjectileAttack is a star, arrow or bullet's incPAD: its attack counts while it is shot.
	ProjectileAttack int
	UnitPrice        float64
	Equipment        domain.Equipment
	// These are immutable template facts from info/option, not generated
	// instance potential. Level is the one-based per-line native override.
	FixedPotentialOptionCount int
	FixedPotentialOptions     [5]ItemPotentialOption
}

type ItemPotentialOption struct {
	ID, Level int
}

var itemDefinitions sync.Map

func (c *Catalog) Item(id int) (Item, error) { return ItemDefinition(c.root, id) }

func ItemDefinition(root string, id int) (Item, error) {
	key := fmt.Sprintf("%s/%d", root, id)
	if value, ok := itemDefinitions.Load(key); ok {
		return value.(Item), nil
	}
	// Pets are CASH items whose WZ lives under Item.wz/Pet, one file each.
	if id/10000 == 500 {
		if root != "" {
			if _, err := readWZ(filepath.Join(root, "Item.wz", "Pet", fmt.Sprintf("%d.img.xml", id))); err != nil {
				return Item{}, err
			}
		}
		return Item{ID: id, InvType: 5, SlotMax: 1, Gender: -1, Cash: true, TradeBlock: true}, nil
	}
	if root == "" {
		item, ok := starterItem(id)
		if !ok {
			return Item{}, fmt.Errorf("item %d absent from embedded inventory content", id)
		}
		return item, nil
	}
	if id < 1000000 || id > 5999999 {
		return Item{}, fmt.Errorf("unsupported item id %d", id)
	}
	invType := id / 1000000
	definition := Item{ID: id, InvType: invType, SlotMax: 1, Gender: -1}
	var node *wzNode
	var err error
	if invType == 1 {
		category, slots := equipmentCategory(id / 10000)
		if category == "" {
			return Item{}, fmt.Errorf("unsupported equipment category %d", id/10000)
		}
		node, err = readWZ(filepath.Join(root, "Character.wz", category, fmt.Sprintf("%08d.img.xml", id)))
		definition.EquipSlots = slots
		gender := (id / 1000) % 10
		if gender < 2 {
			definition.Gender = gender
		}
		definition.Overall = id/10000 == 105
	} else {
		category := map[int]string{2: "Consume", 3: "Install", 4: "Etc", 5: "Cash"}[invType]
		bundle, e := readWZ(filepath.Join(root, "Item.wz", category, fmt.Sprintf("%04d.img.xml", id/10000)))
		if e != nil {
			return Item{}, e
		}
		node = bundle.child(fmt.Sprintf("%08d", id))
		if node == nil {
			return Item{}, fmt.Errorf("item %d missing in WZ bundle", id)
		}
	}
	if err != nil {
		return Item{}, err
	}
	info := node.child("info")
	if info == nil {
		return Item{}, fmt.Errorf("item %d has no WZ info", id)
	}
	// DumpItems.java:232-243 stores an omitted WZ slotMax as1 for EQUIP
	// and100 for all bundle inventories. Quest/trade/cash flags do not change
	// that default; explicit limits (including projectiles) remain authoritative.
	defaultSlotMax := 100
	if invType == 1 {
		defaultSlotMax = 1
	}
	definition.SlotMax = info.intValue("slotMax", defaultSlotMax)
	if definition.SlotMax < 1 || definition.SlotMax > 30000 {
		return Item{}, fmt.Errorf("invalid slotMax for %d", id)
	}
	definition.ReqLevel = info.intValue("reqLevel", 0)
	definition.ReqJob = info.intValue("reqJob", 0)
	definition.ReqSTR = info.intValue("reqSTR", 0)
	definition.ReqDEX = info.intValue("reqDEX", 0)
	definition.ReqINT = info.intValue("reqINT", 0)
	definition.ReqLUK = info.intValue("reqLUK", 0)
	definition.Cash = info.intValue("cash", 0) != 0 || invType == 5
	definition.TradeBlock = info.intValue("tradeBlock", 0) != 0 || info.intValue("accountSharable", 0) != 0 || info.intValue("onlyTradeBlock", 0) != 0 || info.intValue("equipTradeBlock", 0) != 0
	definition.Quest = info.intValue("quest", 0) != 0
	definition.Only = info.intValue("only", 0) != 0
	definition.ReqFame = info.intValue("reqPOP", 0)
	definition.AttackSpeed = info.intValue("attackSpeed", 0)
	definition.Price = int64(info.intValue("price", 0))
	definition.UnitPrice = float64(definition.Price)
	if price := info.child("unitPrice"); price != nil {
		value, e := strconv.ParseFloat(price.Value, 64)
		if e != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return Item{}, fmt.Errorf("invalid unitPrice for %d", id)
		}
		definition.UnitPrice = value
	}
	if invType != 1 {
		definition.ProjectileAttack = info.intValue("incPAD", 0)
	}
	if invType == 1 {
		definition.FixedPotentialOptionCount, definition.FixedPotentialOptions, err = readItemPotentialOptions(info)
		if err != nil {
			return Item{}, fmt.Errorf("item %d: %w", id, err)
		}
		definition.Equipment = domain.Equipment{ItemID: id, STR: info.intValue("incSTR", 0), DEX: info.intValue("incDEX", 0), INT: info.intValue("incINT", 0), LUK: info.intValue("incLUK", 0), HP: info.intValue("incMHP", 0), MP: info.intValue("incMMP", 0), WATK: info.intValue("incPAD", 0), MATK: info.intValue("incMAD", 0), WDEF: info.intValue("incPDD", 0), MDEF: info.intValue("incMDD", 0), ACC: info.intValue("incACC", 0), AVOID: info.intValue("incEVA", 0), Speed: info.intValue("incSpeed", 0), Jump: info.intValue("incJump", 0), UpgradeSlots: info.intValue("tuc", 0)}
	}
	itemDefinitions.Store(key, definition)
	return definition, nil
}

func equipmentCategory(category int) (string, []int) {
	switch category {
	case 100:
		return "Cap", []int{-1}
	case 101:
		return "Accessory", []int{-2}
	case 102:
		return "Accessory", []int{-3}
	case 103:
		return "Accessory", []int{-4}
	case 104:
		return "Coat", []int{-5}
	case 105:
		return "Longcoat", []int{-5}
	case 106:
		return "Pants", []int{-6}
	case 107:
		return "Shoes", []int{-7}
	case 108:
		return "Glove", []int{-8}
	case 109:
		return "Shield", []int{-10}
	case 110:
		return "Cape", []int{-9}
	case 111:
		return "Ring", []int{-12, -13, -15, -16}
	case 112:
		return "Accessory", []int{-17}
	case 113:
		return "Accessory", []int{-50}
	case 114:
		return "Accessory", []int{-49}
	case 115:
		return "Accessory", []int{-51}
	case 116:
		return "Accessory", []int{-52}
	case 117:
		return "Accessory", []int{-53}
	case 118:
		return "Accessory", []int{-54}
	case 134, 135:
		// Katara (134, islot Si) and secondary items take the shield slot.
		return "Weapon", []int{-10}
	case 170:
		return "Weapon", []int{-11}
	case 190, 193:
		// TamingMob: the mount occupies -18, its saddle -19 (Java MapleInventoryType).
		return "TamingMob", []int{-18}
	case 191, 194:
		return "TamingMob", []int{-19}
	default:
		if category >= 121 && category <= 159 {
			return "Weapon", []int{-11}
		}
	}
	return "", nil
}

func starterItem(id int) (Item, bool) {
	if id >= 4031332 && id <= 4031341 {
		price := int64(100 * (id - 4031331))
		return Item{ID: id, InvType: 4, SlotMax: 100, Gender: -1, Price: price, UnitPrice: float64(price), TradeBlock: true}, true
	}
	if id == 4031802 {
		return Item{ID: id, InvType: 4, SlotMax: 100, Gender: -1, Price: 1, UnitPrice: 1, Quest: true}, true
	}
	if id == 2010000 || id == 2010007 || id == 2010009 {
		price := int64(2)
		if id == 2010007 {
			price = 1
		}
		if id == 2010009 {
			price = 30
		}
		return Item{ID: id, InvType: 2, SlotMax: 300, Gender: -1, Price: price, UnitPrice: float64(price), Quest: id == 2010007}, true
	}
	if id == 2000000 {
		return Item{ID: id, InvType: 2, SlotMax: 300, Gender: -1, Price: 3, UnitPrice: 3}, true
	}
	allowed := false
	for _, value := range []int{1040002, 1040006, 1040010, 1041002, 1041006, 1041010, 1041011, 1060002, 1060006, 1061002, 1061008, 1072001, 1072005, 1072037, 1072038, 1302000, 1312004, 1322005} {
		if id == value {
			allowed = true
			break
		}
	}
	if !allowed {
		return Item{}, false
	}
	_, slots := equipmentCategory(id / 10000)
	gender := -1
	if g := (id / 1000) % 10; g < 2 {
		gender = g
	}
	equipment := domain.Equipment{ItemID: id, UpgradeSlots: 7}
	price := int64(10)
	switch id / 10000 {
	case 130, 131:
		equipment.WATK = 17
		price = 1
	case 132:
		equipment.WATK = 19
		price = 1
	case 104:
		equipment.WDEF = 3
	case 106:
		equipment.WDEF = 2
	case 107:
		equipment.WDEF = 2
		equipment.UpgradeSlots = 5
		price = 1
	}
	return Item{ID: id, InvType: 1, SlotMax: 1, EquipSlots: slots, Gender: gender, Price: price, UnitPrice: float64(price), Equipment: equipment}, true
}

func (i Item) AllowsSlot(slot int) bool {
	for _, candidate := range i.EquipSlots {
		if candidate == slot {
			return true
		}
	}
	return false
}
func (i Item) Droppable() bool { return !i.Cash && !i.TradeBlock && !i.Quest }

// Tradeable reports whether a held item may go into a player trade. cashID is
// the item's cash serial, 0 for none. A cash item passes only under the
// trade_cash_items setting.
func (i Item) Tradeable(cashID int64) bool {
	if i.TradeBlock || i.Quest {
		return false
	}
	if !i.Cash && cashID == 0 {
		return true
	}
	return domain.TradeCashAllowed(i.ID, cashID)
}
