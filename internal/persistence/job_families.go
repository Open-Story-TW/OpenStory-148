package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func readJobState(ctx context.Context, q questQuerier, c *domain.Character) error {
	rows, err := q.QueryContext(ctx, `SELECT subcategory,array_to_string(sp_books,','),demon_mark FROM game_characters WHERE id=$1`, c.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return domain.ErrNotFound
	}
	var books string
	if err = rows.Scan(&c.SubJob, &books, &c.DemonMark); err != nil {
		return err
	}
	if c.SPBooks, err = parseSPBooks(books); err != nil {
		return err
	}
	return rows.Err()
}

func saveJobState(ctx context.Context, tx *sql.Tx, c domain.Character) error {
	_, err := tx.ExecContext(ctx, `UPDATE game_characters SET subcategory=$1,sp_books=$2::integer[] WHERE id=$3`, c.SubJob, formatSPBooks(c.SPBooks), c.ID)
	return err
}

func parseSPBooks(value string) (domain.SPBookTable, error) {
	var table domain.SPBookTable
	parts := strings.Split(value, ",")
	if len(parts) != len(table) {
		return table, fmt.Errorf("sp_books has %d entries", len(parts))
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return table, err
		}
		table[i] = n
	}
	return table, nil
}

func formatSPBooks(table domain.SPBookTable) string {
	parts := make([]string, len(table))
	for i, n := range table {
		parts[i] = strconv.Itoa(n)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// creationLook is one gender's Etc.wz/MakeCharInfo.img/<family> lists.
// Resistance (3000) wears an overall in Top and has no Bottom.
type creationLook struct {
	face, hair, top, bottom, shoes, weapon []int
	// cape and shield are worn from the start without being a choice.
	cape, shield int
}

var (
	explorerShoes   = []int{1072001, 1072005, 1072037, 1072038}
	explorerWeapons = []int{1302000, 1312004, 1322005}
	maleFaces       = []int{20100, 20401, 20402}
	maleHair        = []int{30000, 30027, 30030}
	femaleFaces     = []int{21700, 21201, 21002}
	femaleHair      = []int{31002, 31047, 31057}
)

var explorerLook = [2]creationLook{
	{face: maleFaces, hair: maleHair, top: []int{1040002, 1040006, 1040010}, bottom: []int{1060002, 1060006}, shoes: explorerShoes, weapon: explorerWeapons},
	{face: femaleFaces, hair: femaleHair, top: []int{1041002, 1041006, 1041010, 1041011}, bottom: []int{1061002, 1061008}, shoes: explorerShoes, weapon: explorerWeapons},
}

var creationLooks = map[int][2]creationLook{
	0:    explorerLook,
	1000: explorerLook,
	2000: {
		{face: maleFaces, hair: maleHair, top: []int{1042167}, bottom: []int{1062115}, shoes: []int{1072383}, weapon: []int{1442079}},
		{face: femaleFaces, hair: femaleHair, top: []int{1042167}, bottom: []int{1062115}, shoes: []int{1072383}, weapon: []int{1442079}},
	},
	2001: {
		{face: maleFaces, hair: maleHair, top: []int{1042180}, bottom: []int{1060138}, shoes: []int{1072418}, weapon: []int{1302132}},
		{face: femaleFaces, hair: femaleHair, top: []int{1042180}, bottom: []int{1061160}, shoes: []int{1072418}, weapon: []int{1302132}},
	},
	3000: {
		{face: maleFaces, hair: maleHair, top: []int{1050173, 1050174, 1050175}, shoes: explorerShoes, weapon: explorerWeapons},
		{face: femaleFaces, hair: femaleHair, top: []int{1051214, 1051215, 1051216}, shoes: explorerShoes, weapon: explorerWeapons},
	},
	// v148 MakeCharInfo 2003: two faces, two hair styles in eight colours, an
	// overall, one cape, shoes and a cane. The card deck is Legacy's starter
	// secondary item for the family.
	2003: {
		{face: []int{20659, 20658}, hair: hairColours(33700, 33730), top: []int{1050222}, shoes: []int{1070023}, weapon: []int{1362000}, cape: domain.PhantomStarterCape, shield: 1352100},
		{face: []int{21656, 21657}, hair: hairColours(34700, 34730), top: []int{1051272}, shoes: []int{1071035}, weapon: []int{1362000}, cape: domain.PhantomStarterCape, shield: 1352100},
	},
}

// hairColours lists the eight colours of each base style.
func hairColours(styles ...int) []int {
	var ids []int
	for _, style := range styles {
		for colour := 0; colour < 8; colour++ {
			ids = append(ids, style+colour)
		}
	}
	return ids
}

// validCreationSkin: the native Cygnus avatar UI sends skin 10 (observed
// through MCP); Legacy CreateChar forces Aran to skin 11.
func validCreationSkin(family, skin int) bool {
	return skin >= 0 && skin <= 5 || family == 1000 && skin == 10 || family == 2000 && skin == 11
}

func validCreationLook(r domain.CreateCharacterRequest) bool {
	looks, ok := creationLooks[r.Family]
	if !ok {
		return false
	}
	l := looks[r.Gender]
	bottom := r.Bottom == 0 && l.bottom == nil || oneOf(r.Bottom, l.bottom...)
	return oneOf(r.Face, l.face...) && oneOf(r.Hair, l.hair...) && oneOf(r.Top, l.top...) && bottom && oneOf(r.Shoes, l.shoes...) && oneOf(r.Weapon, l.weapon...)
}

type creationPlacement struct {
	race         domain.CreationRace
	mapID        int
	portal, x, y int
	gear         map[int]domain.Equipment
	cape, shield int
}

// creationStart resolves the family's start map spawn portal and, outside
// the Explorer starter fixture, the WZ stats of the chosen starter gear.
func creationStart(r domain.CreateCharacterRequest) (creationPlacement, error) {
	race, ok := domain.CreationFamily(r.Family, r.SubJob)
	if !ok {
		return creationPlacement{}, domain.ErrInvalidInput
	}
	start := creationPlacement{race: race, mapID: race.MapID, x: -192, y: 210}
	if race.MapID == 10000 {
		return start, nil // ent defaults: Maple Island start, portal 0
	}
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	catalog, err := content.NewCatalog(root)
	if err != nil {
		return creationPlacement{}, err
	}
	m, err := catalog.Map(race.MapID)
	if err != nil {
		return creationPlacement{}, err
	}
	p, ok := m.Spawn()
	if !ok || p.ID < 0 || p.ID > 255 {
		return creationPlacement{}, fmt.Errorf("start map %d has no spawn portal", race.MapID)
	}
	start.portal, start.x, start.y = p.ID, p.X, p.Y
	start.gear = make(map[int]domain.Equipment)
	look := creationLooks[r.Family][r.Gender&1]
	start.cape, start.shield = look.cape, look.shield
	for _, id := range []int{r.Top, r.Bottom, r.Shoes, r.Weapon, look.cape, look.shield} {
		if id == 0 {
			continue
		}
		item, err := content.ItemDefinition(root, id)
		if err != nil {
			return creationPlacement{}, err
		}
		start.gear[id] = item.Equipment
	}
	return start, nil
}

// levelGrowth is Legacy MapleCharacter.levelUp's per-level max HP/MP gain as
// (hp base, mp base, mp range); the HP range is always 5 (rand(a, a+4)).
// Jobs Legacy sends to its GameMaster branch (Phantom, Cygnus x112) use their
// family's archetype instead of the GM 50..100 roll.
func levelGrowth(job int) (hp, mp, mpRange int, ok bool) {
	if !domain.ValidJob(job) {
		return 0, 0, 0, false
	}
	switch {
	case domain.IsBeginnerJob(job):
		return 12, 10, 3, true
	case job >= 3100 && job <= 3112: // Demon Slayer: no MP growth
		return 48, 0, 1, true
	case job >= 100 && job <= 132 || job >= 1100 && job <= 1112:
		return 48, 4, 3, true
	case job >= 200 && job <= 232 || job >= 1200 && job <= 1212:
		return 10, 48, 5, true
	case job >= 3200 && job <= 3212:
		return 20, 42, 3, true
	case job >= 300 && job <= 322 || job >= 400 && job <= 434 || job >= 1300 && job <= 1312 || job >= 1400 && job <= 1412 || job >= 3300 && job <= 3312 || job >= 2300 && job <= 2312 || domain.IsPhantomJob(job):
		return 20, 14, 3, true
	case job >= 510 && job <= 512 || job >= 1510 && job <= 1512:
		return 37, 18, 5, true
	case job >= 500 && job <= 532 || job >= 3500 && job <= 3512 || job == 1500:
		return 20, 18, 5, true
	case job >= 2100 && job <= 2112:
		return 50, 4, 3, true
	case job >= 2200 && job <= 2218:
		return 12, 50, 3, true
	}
	return 0, 0, 0, false
}

// applyQAItem serves !item <id> [quantity]: QA skill setup needs job weapons,
// projectiles (207xxxx stars, 233xxxx bullets) and summoning rocks. Only
// non-cash, non-pet WZ items; equipment comes with its WZ stats, bundles
// stack through the ordinary pickup path. Audited as a gm_command.
func applyQAItem(ctx context.Context, tx *sql.Tx, c *domain.Character, r domain.GMCommandRequest, result *domain.GMCommandResult) error {
	quantity := int64(1)
	if len(r.Args) == 2 {
		quantity = r.Args[1]
	}
	if len(r.Args) < 1 || len(r.Args) > 2 || r.Args[0] < 1000000 || r.Args[0] > 4999999 || quantity < 1 || quantity > domain.QAItemMax {
		return domain.ErrInvalidInput
	}
	def, err := inventoryDefinition(int(r.Args[0]))
	if err != nil || def.Cash || def.ID/10000 == 500 {
		return domain.ErrInvalidInput
	}
	inv := domain.InventoryResult{Character: *c}
	if def.InvType == 1 {
		if quantity != 1 {
			return domain.ErrInvalidInput
		}
		slot := FreeEquipmentSlot(&inv.Character)
		if slot == 0 {
			return domain.ErrInsufficient
		}
		e := def.Equipment
		e.Slot = slot
		if err = tx.QueryRowContext(ctx, `INSERT INTO asset_equipment(character_id,item_id,slot,str,dex,int_stat,luk,hp,mp,watk,matk,wdef,mdef,acc,avoid,speed,jump,upgrade_slots) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) RETURNING id`,
			c.ID, e.ItemID, e.Slot, e.STR, e.DEX, e.INT, e.LUK, e.HP, e.MP, e.WATK, e.MATK, e.WDEF, e.MDEF, e.ACC, e.AVOID, e.Speed, e.Jump, e.UpgradeSlots).Scan(&e.ID); err != nil {
			return err
		}
		inv.Character.Equipment = append(inv.Character.Equipment, e)
		inv.Changes = []domain.InventoryChange{{Mode: 0, InvType: 1, Slot: slot, Equipment: &e}}
	} else if err = pickupBundle(ctx, tx, domain.ItemDrop{InvType: def.InvType, TemplateID: def.ID, Quantity: int(quantity)}, &inv); err != nil {
		return err
	}
	if err = commitInventoryState(ctx, tx, &inv.Character); err != nil {
		return err
	}
	*c = inv.Character
	result.Inventory = inv.Changes
	return nil
}
