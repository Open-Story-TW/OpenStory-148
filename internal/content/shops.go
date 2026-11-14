package content

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"maple-go-server/internal/domain"
)

//go:embed npc_shops.json
var npcShopData []byte
var npcShopOnce sync.Once
var npcShopCatalog map[int]domain.Shop
var npcShopError error

func NPCShop(id int) (domain.Shop, error) {
	npcShopOnce.Do(loadNPCShops)
	if npcShopError != nil {
		return domain.Shop{}, npcShopError
	}
	shop, ok := npcShopCatalog[id]
	if !ok {
		return domain.Shop{}, fmt.Errorf("NPC shop %d absent from catalog", id)
	}
	shop.Items = append([]domain.ShopItem(nil), shop.Items...)
	return shop, nil
}

func NPCShopForNPC(npcID int) (domain.Shop, error) {
	npcShopOnce.Do(loadNPCShops)
	if npcShopError != nil {
		return domain.Shop{}, npcShopError
	}
	// The legacy query selects one shop for a bare NPC interaction. Keep the
	// choice deterministic when the source contains multiple custom rows; Lua
	// service menus can still open any explicit shop ID.
	selected := 0
	for _, shop := range npcShopCatalog {
		if shop.NPCID == npcID && (selected == 0 || shop.ID < selected) {
			selected = shop.ID
		}
	}
	if selected != 0 {
		return NPCShop(selected)
	}
	return domain.Shop{}, fmt.Errorf("NPC %d has no catalog shop", npcID)
}

func loadNPCShops() {
	var data struct {
		Version int                 `json:"version"`
		Hash    string              `json:"source_sha256"`
		Shops   [][]json.RawMessage `json:"shops"`
		Items   [][]int64           `json:"items"`
		Ranks   [][]json.RawMessage `json:"ranks"`
	}
	if err := json.Unmarshal(npcShopData, &data); err != nil {
		npcShopError = err
		return
	}
	if data.Version != 1 {
		npcShopError = fmt.Errorf("unsupported shop catalog version")
		return
	}
	npcShopCatalog = make(map[int]domain.Shop)
	for _, row := range data.Shops {
		if len(row) != 3 {
			npcShopError = fmt.Errorf("invalid shop row")
			return
		}
		var shop domain.Shop
		_ = json.Unmarshal(row[0], &shop.ID)
		_ = json.Unmarshal(row[1], &shop.NPCID)
		_ = json.Unmarshal(row[2], &shop.Name)
		shop.SourceHash = data.Hash
		npcShopCatalog[shop.ID] = shop
	}
	for _, row := range data.Items {
		if len(row) != 12 {
			npcShopError = fmt.Errorf("invalid shop item row")
			return
		}
		shop, ok := npcShopCatalog[int(row[1])]
		if !ok {
			continue
		}
		shop.Items = append(shop.Items, domain.ShopItem{ID: int(row[0]), TemplateID: int(row[2]), Price: row[3], Position: int(row[4]), RequiredItem: int(row[5]), RequiredQuantity: int(row[6]), PointType: int(row[7]), Period: int(row[8]), State: int(row[9]), Category: int(row[10]), MinLevel: int(row[11])})
		npcShopCatalog[shop.ID] = shop
	}
	for _, row := range data.Ranks {
		if len(row) != 5 {
			npcShopError = fmt.Errorf("invalid rank row")
			return
		}
		var id, itemID int
		var name string
		_ = json.Unmarshal(row[1], &id)
		_ = json.Unmarshal(row[3], &name)
		_ = json.Unmarshal(row[4], &itemID)
		shop, ok := npcShopCatalog[id]
		if ok {
			shop.Ranks = append(shop.Ranks, domain.ShopRank{ItemID: itemID, Name: name})
			npcShopCatalog[id] = shop
		}
	}
	for id, shop := range npcShopCatalog {
		sort.SliceStable(shop.Items, func(i, j int) bool {
			if shop.Items[i].Position == shop.Items[j].Position {
				return shop.Items[i].ID < shop.Items[j].ID
			}
			return shop.Items[i].Position < shop.Items[j].Position
		})
		npcShopCatalog[id] = shop
	}
}

// Java appends rechargeable projectiles to the shop's regular catalog. These
// zero-price entries advertise recharge, never a free purchase.
func RechargeItems() []int {
	return []int{2070000, 2070001, 2070002, 2070003, 2070004, 2070005, 2070006, 2070007, 2070008, 2070009, 2070010, 2070011, 2070012, 2070013, 2070019, 2070023, 2070024, 2330000, 2330001, 2330002, 2330003, 2330004, 2330005, 2330008, 2331000, 2332000}
}
