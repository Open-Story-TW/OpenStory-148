package protocol

import (
	"math"
	"os"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

const RecvNPCShop uint16 = 0x6c
const SendNPCShop uint16 = 0x242
const SendShopTransaction uint16 = 0x243

func OpenNPCShop(shop domain.Shop) []byte {
	w := NewWriter(SendNPCShop)
	w.I32(0)
	w.I32(int32(shop.NPCID))
	now := time.Now()
	w.I32(int32(now.Year()*1000000 + int(now.Month())*10000 + now.Day()*100 + now.Hour()))
	addNPCShopInfo(w, shop)
	return w.Packet()
}

func ShopTransaction(code byte, shop *domain.Shop) []byte {
	w := NewWriter(SendShopTransaction)
	w.U8(code)
	if code == 8 && shop != nil {
		w.I32(0)
		w.I32(int32(shop.NPCID))
		addNPCShopInfo(w, *shop)
	} else {
		w.U8(0)
	}
	return w.Packet()
}

func addNPCShopInfo(w *Writer, shop domain.Shop) {
	w.Bool(len(shop.Ranks) > 0)
	if len(shop.Ranks) > 0 {
		w.U8(byte(len(shop.Ranks)))
		for _, rank := range shop.Ranks {
			w.I32(int32(rank.ItemID))
			w.String(rank.Name)
		}
	}
	w.U16(uint16(len(shop.Items)))
	for _, item := range shop.Items {
		w.I32(int32(item.TemplateID))
		w.I32(int32(item.Price))
		w.U8(0)
		w.I32(int32(item.RequiredItem))
		w.I32(int32(item.RequiredQuantity))
		w.I32(int32(item.Period))
		w.I32(int32(item.MinLevel))
		w.I32(int32(item.Category))
		w.I32(0)
		w.I32(0)
		// v148 CShopDlg::SetShopDlg (0x6A7ACC) reads eight int32s after the
		// token flag; the last one (0x6A7D1D) is new since v145.
		w.I32(0)
		w.String("1900010100")
		w.String("2079010100")
		w.U8(0)
		w.I32(0)
		if item.TemplateID/10000 == 207 || item.TemplateID/10000 == 233 {
			w.Zero(6)
			definition, _ := content.ItemDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), item.TemplateID)
			w.U16(uint16(math.Float64bits(definition.UnitPrice) >> 48))
			w.U16(uint16(definition.SlotMax))
		} else {
			w.U16(1)
			w.U16(1000)
		}
		w.U8(0)
		if len(shop.Ranks) > 0 {
			w.Bool(item.PointType >= 0)
			if item.PointType >= 0 {
				w.U8(byte(item.PointType))
			}
		}
		// v148 ends each item with a 48-byte block (0x4CA071: four int32s
		// and 32 bytes). All zero means no extra condition.
		w.Zero(48)
	}
}
