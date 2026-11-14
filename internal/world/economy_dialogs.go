package world

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type economyDialog struct {
	mu       sync.Mutex
	id       string
	live     *liveCharacter
	kind     string
	npcID    int
	sequence uint64
	storage  domain.Storage
	shop     domain.Shop
}
type economyDialogSpace struct {
	mu          sync.Mutex
	byCharacter map[int64]*economyDialog
}

func newEconomyDialogSpace() *economyDialogSpace {
	return &economyDialogSpace{byCharacter: make(map[int64]*economyDialog)}
}
func newEconomyDialog(live *liveCharacter, kind string, npcID int) (*economyDialog, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	return &economyDialog{id: hex.EncodeToString(id[:]), live: live, kind: kind, npcID: npcID}, nil
}

func (s *Server) setEconomyDialog(live *liveCharacter, dialog *economyDialog) {
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	s.economyDialogs.mu.Lock()
	s.economyDialogs.byCharacter[id] = dialog
	s.economyDialogs.mu.Unlock()
}
func (s *Server) getEconomyDialog(live *liveCharacter, kind string) *economyDialog {
	if s.economyDialogs == nil {
		return nil
	}
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	s.economyDialogs.mu.Lock()
	dialog := s.economyDialogs.byCharacter[id]
	s.economyDialogs.mu.Unlock()
	if dialog == nil || dialog.live != live || dialog.kind != kind {
		return nil
	}
	return dialog
}
func (s *Server) closeEconomyDialog(live *liveCharacter) {
	if s.economyDialogs == nil {
		return
	}
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	s.economyDialogs.mu.Lock()
	if dialog := s.economyDialogs.byCharacter[id]; dialog != nil && dialog.live == live {
		delete(s.economyDialogs.byCharacter, id)
	}
	s.economyDialogs.mu.Unlock()
}

// These open functions are called only by the script host after NPC identity,
// map membership, distance and active-conversation generation are validated.
func (s *Server) openStorage(ctx context.Context, live *liveCharacter, c packetSink, npcID int) error {
	if s.economyDialogs == nil || s.tradeActive(live) {
		return domain.ErrForbidden
	}
	repo, ok := s.repo.(domain.StorageRepository)
	if !ok {
		return domain.ErrForbidden
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	session := live.session
	live.mu.Unlock()
	storage, err := repo.OpenStorage(ctx, session)
	if err != nil {
		return err
	}
	dialog, err := newEconomyDialog(live, "storage", npcID)
	if err != nil {
		return err
	}
	dialog.storage = storage
	s.setEconomyDialog(live, dialog)
	return c.WritePacket(protocol.OpenStorage(npcID, storage))
}

func (s *Server) openNPCShop(_ context.Context, live *liveCharacter, c packetSink, npcID, shopID int) error {
	if s.economyDialogs == nil || s.tradeActive(live) {
		return domain.ErrForbidden
	}
	shop, err := content.NPCShop(shopID)
	if err != nil {
		return err
	}
	// Match Java's nonexistent-item filtering, while retaining source item IDs
	// so client display indexes cannot become purchase prices or asset IDs.
	items := make([]domain.ShopItem, 0, len(shop.Items))
	seen := make(map[int]bool)
	for _, item := range shop.Items {
		if _, err := inventoryItemDefinition(item.TemplateID); err == nil {
			items = append(items, item)
			seen[item.TemplateID] = true
		}
	}
	for _, id := range content.RechargeItems() {
		if !seen[id] {
			if _, err := inventoryItemDefinition(id); err == nil {
				items = append(items, domain.ShopItem{ID: -id, TemplateID: id, Price: 0})
			}
		}
	}
	shop.Items = items
	shop.NPCID = npcID
	dialog, err := newEconomyDialog(live, "shop", npcID)
	if err != nil {
		return err
	}
	dialog.shop = shop
	s.setEconomyDialog(live, dialog)
	return c.WritePacket(protocol.OpenNPCShop(shop))
}

func inventoryItemDefinition(id int) (content.Item, error) {
	catalog, err := content.DefaultCatalog()
	if err != nil {
		return content.Item{}, err
	}
	return catalog.Item(id)
}

func (s *Server) handleStorage(ctx context.Context, live *liveCharacter, c packetSink, r *protocol.Reader) error {
	mode := r.U8()
	dialog := s.getEconomyDialog(live, "storage")
	if dialog == nil {
		if mode >= 4 && mode <= 7 {
			return c.WritePacket(protocol.StorageRejected())
		}
		return c.WritePacket(protocol.EnableActions())
	}
	dialog.mu.Lock()
	defer dialog.mu.Unlock()
	req := domain.StorageRequest{ExpectedRevision: dialog.storage.Revision}
	invType := 0
	switch mode {
	case 4:
		invType = int(r.U8())
		index := int(r.U8())
		req.Kind = "withdraw"
		for _, item := range dialog.storage.Items {
			if item.Item.InvType != invType {
				continue
			}
			if index == 0 {
				req.StorageItemID = item.ID
				break
			}
			index--
		}
	case 5:
		req.Kind = "deposit"
		req.Slot = int(r.I16())
		req.TemplateID = int(r.I32())
		req.Quantity = int(r.I16())
		invType = req.TemplateID / 1000000
	case 6:
		req.Kind = "arrange"
	case 7:
		req.Kind = "mesos"
		req.Delta = protocol.ReadMoney(r)
	case 8:
		s.closeEconomyDialog(live)
		return c.WritePacket(protocol.EnableActions())
	default:
		return c.WritePacket(protocol.EnableActions())
	}
	if r.Err() != nil || r.Remaining() != 0 {
		return protocol.ErrMalformed
	}
	repo, ok := s.repo.(domain.StorageRepository)
	if !ok {
		s.closeEconomyDialog(live)
		return c.WritePacket(protocol.StorageRejected())
	}
	dialog.sequence++
	req.OperationID = fmt.Sprintf("%s/%d", dialog.id, dialog.sequence)
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	req.Session = live.session
	live.mu.Unlock()
	result, err := repo.ChangeStorage(ctx, req)
	if err != nil {
		// Every already-sent native storage request owns dialog+236 busy.
		// EnableActions only unlocks character input; a native0x223 result is
		// required here. The repository overloads ErrInsufficient for capacity
		// and mesos, so only a revision-matched full snapshot proves capacity.
		packet := protocol.StorageRejected()
		if errors.Is(err, domain.ErrInsufficient) && req.Kind == "deposit" && len(dialog.storage.Items) >= dialog.storage.Capacity {
			packet = protocol.StorageFull()
		}
		writeErr := c.WritePacket(packet)
		if errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrNotFound) {
			return writeErr // rejected transaction; no state/receipt is replayed
		}
		// Stale authority and unknown commit outcomes must be reloaded by a
		// fresh session, never retried under another operation ID in this UI.
		s.closeEconomyDialog(live)
		if writeErr != nil {
			return writeErr
		}
		return err
	}
	s.applyInventoryResult(live, result.Inventory.Character)
	dialog.storage = result.Storage
	if len(result.Inventory.Changes) > 0 {
		if err = c.WritePacket(protocol.ModifyInventory(result.Inventory.Changes)); err != nil {
			return err
		}
	}
	if err = c.WritePacket(protocol.UpdateMesos(result.Inventory.Character.Mesos, true)); err != nil {
		return err
	}
	switch mode {
	case 4:
		return c.WritePacket(protocol.StorageItems(result.Storage, invType, false))
	case 5:
		return c.WritePacket(protocol.StorageItems(result.Storage, invType, true))
	case 6:
		return c.WritePacket(protocol.StorageArrange(result.Storage))
	case 7:
		return c.WritePacket(protocol.StorageMesos(result.Storage))
	}
	return nil
}

func (s *Server) handleNPCShop(ctx context.Context, live *liveCharacter, c packetSink, r *protocol.Reader) error {
	dialog := s.getEconomyDialog(live, "shop")
	if dialog == nil {
		return c.WritePacket(protocol.EnableActions())
	}
	dialog.mu.Lock()
	defer dialog.mu.Unlock()
	wire, err := protocol.ReadNPCShopRequest(r)
	if err != nil {
		return err
	}
	mode := wire.Mode
	req := domain.ShopRequest{ShopID: dialog.shop.ID}
	switch mode {
	case 0:
		req.Kind = "buy"
		req.TemplateID = wire.TemplateID
		req.Quantity = wire.Quantity
		req.DisplayedUnitPrice = &wire.DisplayedUnitPrice
		// Native65F577 maps the selected/category row back to its original
		// catalog index. A different row with the same template is not a fallback.
		if wire.Index < len(dialog.shop.Items) && dialog.shop.Items[wire.Index].TemplateID == req.TemplateID {
			req.EntryID = dialog.shop.Items[wire.Index].ID
		} else {
			return c.WritePacket(protocol.ShopTransaction(0x20, nil))
		}
	case 1:
		req.Kind = "sell"
		req.Slot = wire.Slot
		req.TemplateID = wire.TemplateID
		req.Quantity = wire.Quantity
		if req.Quantity == 0 || req.Quantity == -1 {
			req.Quantity = 1
		}
	case 2:
		req.Kind = "recharge"
		req.Slot = wire.Slot
		req.Quantity = 1
	case 3:
		s.closeEconomyDialog(live)
		return c.WritePacket(protocol.EnableActions())
	}
	repo, ok := s.repo.(domain.ShopRepository)
	if !ok {
		return c.WritePacket(protocol.EnableActions())
	}
	dialog.sequence++
	req.OperationID = fmt.Sprintf("%s/%d", dialog.id, dialog.sequence)
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	live.session.Lease = s.currentLease()
	req.Session = live.session
	live.mu.Unlock()
	result, err := repo.ChangeShop(ctx, req)
	if err != nil {
		return c.WritePacket(protocol.ShopTransaction(0x20, nil))
	}
	s.applyInventoryResult(live, result.Inventory.Character)
	if len(result.Inventory.Changes) > 0 {
		if err = c.WritePacket(protocol.ModifyInventory(result.Inventory.Changes)); err != nil {
			return err
		}
	}
	if err = c.WritePacket(protocol.UpdateMesos(result.Inventory.Character.Mesos, true)); err != nil {
		return err
	}
	if mode == 1 || mode == 2 {
		return c.WritePacket(protocol.ShopTransaction(8, &dialog.shop))
	}
	return c.WritePacket(protocol.ShopTransaction(0, &dialog.shop))
}
