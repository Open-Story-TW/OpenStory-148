// Native v145 inventory/appearance layouts derived from Legacy CWvsContext and
// CField (AGPLv3), retaining the extra inventory header byte and move trailer.
package protocol

import (
	"maple-go-server/internal/domain"
	"time"
)

const RecvInventoryMove uint16 = 0x78
const SendCharacterLook uint16 = 0x143

func ReadInventoryMove(r *Reader) (domain.InventoryMoveRequest, error) {
	value := domain.InventoryMoveRequest{ClientTick: r.U32(), InvType: int(r.U8()), Source: int(r.I16()), Destination: int(r.I16()), Quantity: int(r.I16())}
	if r.Err() != nil || r.Remaining() != 0 {
		return domain.InventoryMoveRequest{}, ErrMalformed
	}
	return value, nil
}

func ModifyInventory(changes []domain.InventoryChange) []byte {
	if len(changes) == 0 || len(changes) > 255 {
		return nil
	}
	w := NewWriter(SendModifyInventory)
	w.U8(1)
	w.U8(byte(len(changes)))
	w.U8(0)
	trailer := -1
	for _, change := range changes {
		if change.InvType < 1 || change.InvType > 5 || change.Slot == 0 || change.Slot < -199 || change.Slot > 128 {
			return nil
		}
		w.U8(byte(change.Mode))
		w.U8(byte(change.InvType))
		w.I16(int16(change.Slot))
		switch change.Mode {
		case 0:
			if change.InvType == 1 && change.Equipment != nil {
				addEquipment(w, *change.Equipment)
			} else if change.Item != nil {
				addBundleItem(w, *change.Item)
			} else {
				return nil
			}
		case 1:
			if change.Quantity < 1 || change.Quantity > 30000 {
				return nil
			}
			w.I16(int16(change.Quantity))
		case 2:
			if change.Destination == 0 || change.Destination < -199 || change.Destination > 128 {
				return nil
			}
			w.I16(int16(change.Destination))
			if change.Slot < 0 {
				trailer = 1
			} else if change.Destination < 0 {
				trailer = 2
			}
		case 3:
			if change.Slot < 0 {
				trailer = 2
			}
		default:
			return nil
		}
	}
	if trailer >= 0 {
		w.U8(byte(trailer))
	}
	return w.Packet()
}

func InventoryVitals(c domain.Character) []byte {
	w := NewWriter(SendUpdateStats)
	w.U8(1)
	w.I64(0x3c00)
	for _, n := range []int{c.HP, c.WireMaxHP(), c.MP, c.WireMaxMP()} {
		if n < 0 || int64(n) > 2147483647 {
			return nil
		}
		w.I32(int32(n))
	}
	w.U8(0)
	w.U8(0)
	return w.Packet()
}

func CharacterLook(c domain.Character) []byte {
	if c.ID <= 0 {
		return nil
	}
	w := NewWriter(SendCharacterLook)
	w.I32(int32(c.ID))
	w.U8(1)
	addCharLookMode(w, c, false)
	w.Zero(3)
	w.I32(0)
	return w.Packet()
}

func DropInventoryItem(drop domain.ItemDrop) []byte {
	return dropInventoryItem(drop, false)
}

// DropMonsterLoot is the initial, animated appearance of newly materialized
// monster loot (CField.dropItemFromMapObject mode 1). The durable loot plan
// currently places the destination at the pinned monster death X/Y, so that
// same position is the throw source. Reentry/visibility refresh uses mode 2.
func DropMonsterLoot(drop domain.ItemDrop) []byte {
	if drop.PlayerDrop {
		return nil
	}
	return dropInventoryItem(drop, true)
}

func dropInventoryItem(drop domain.ItemDrop, monsterSpawn bool) []byte {
	if drop.ObjectID < 1000000000 || drop.OwnerID <= 0 || drop.X < -32768 || drop.X > 32767 || drop.Y < -32768 || drop.Y > 32767 {
		return nil
	}
	if drop.Mesos > 0 {
		if drop.Mesos > 2147483647 || drop.InvType != 0 || drop.TemplateID != 0 {
			return nil
		}
		if !monsterSpawn {
			return DropMesoOnMap(drop.ObjectID, int32(drop.Mesos), int32(drop.OwnerID), int16(drop.X), int16(drop.Y), drop.PlayerDrop)
		}
	} else if drop.TemplateID <= 0 {
		return nil
	}
	w := NewWriter(SendDropItem)
	if monsterSpawn {
		w.U8(1) // Initial throw/fall animation; not a map-entry snapshot.
	} else {
		w.U8(2)
	}
	w.I32(drop.ObjectID)
	if drop.Mesos > 0 {
		w.U8(1)
		w.I32(int32(drop.Mesos))
	} else {
		w.U8(0)
		w.I32(int32(drop.TemplateID))
	}
	w.I32(int32(drop.OwnerID))
	kind := 2
	if drop.QuestID > 0 && drop.QuestDropType >= 0 && drop.QuestDropType <= 3 {
		kind = drop.QuestDropType
		if kind < 2 && drop.QuestOwnerUntil <= time.Now().UnixMilli() {
			kind = 2
		}
	}
	w.U8(byte(kind))
	w.I16(int16(drop.X))
	w.I16(int16(drop.Y))
	w.I32(0)
	if monsterSpawn {
		w.I16(int16(drop.X)) // Original source, not the recipient's position.
		w.I16(int16(drop.Y))
		w.U16(0) // CField's animation delay; not a client/server timestamp.
	}
	if drop.Mesos == 0 {
		w.I64(maxTime)
	}
	if monsterSpawn {
		w.U16(1) // Original non-player drop / pet equipment pickup flag.
	} else {
		w.U16(0)
	}
	return w.Packet()
}

// AddEquipmentInventory encodes the ordinary positive equipment bag in a full
// character load. Call after the equipped normal/cash lists, before specials.
func AddEquipmentInventory(w *Writer, equipment []domain.Equipment) {
	for _, item := range equipment {
		if item.Slot > 0 && item.Slot <= 128 {
			w.U16(uint16(item.Slot))
			addEquipment(w, item)
		}
	}
	w.U16(0)
}
