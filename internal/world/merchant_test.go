package world

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// merchantTestRepository is an in-memory shop store: enough of the SQL
// contract (states, revisions, stock, income) to drive the room logic.
type merchantTestRepository struct {
	domain.Repository
	mu    sync.Mutex
	shops map[int64]*domain.MerchantShop
	next  int64
}

func (r *merchantTestRepository) MerchantFor(_ context.Context, accountID int64, _ int) (domain.MerchantShop, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, shop := range r.shops {
		if shop.AccountID == accountID {
			return merchantCopy(*shop), true, nil
		}
	}
	return domain.MerchantShop{}, false, nil
}

func merchantCopy(shop domain.MerchantShop) domain.MerchantShop {
	shop.Items = append([]domain.MerchantItem(nil), shop.Items...)
	shop.Sales = append([]domain.MerchantSale(nil), shop.Sales...)
	return shop
}

func (r *merchantTestRepository) CreateMerchant(_ context.Context, q domain.MerchantCreate) (domain.MerchantShop, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, shop := range r.shops {
		if shop.AccountID == q.Session.AccountID {
			return domain.MerchantShop{}, domain.ErrAlreadyExists
		}
	}
	permit := false
	for _, item := range q.Session.Character.Items {
		permit = permit || item.InvType == 5 && item.Slot == q.PermitSlot && item.TemplateID == q.PermitItemID
	}
	if !permit {
		return domain.MerchantShop{}, domain.ErrNotFound
	}
	r.next++
	c := q.Session.Character
	shop := &domain.MerchantShop{ID: r.next, CharacterID: c.ID, AccountID: c.AccountID, ChannelID: q.Session.ChannelID, MapID: c.MapID, X: c.X, Y: c.Y, Foothold: q.Foothold, PermitItemID: q.PermitItemID, OwnerName: c.Name, Title: q.Title, State: domain.MerchantSetup, Revision: 1, OpenedAt: time.Now(), ExpiresAt: time.Now().Add(domain.MerchantDuration)}
	r.shops[shop.ID] = shop
	return merchantCopy(*shop), nil
}

func (r *merchantTestRepository) ChangeMerchant(_ context.Context, q domain.MerchantRequest) (domain.MerchantResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	shop := r.shops[q.ShopID]
	if shop == nil {
		return domain.MerchantResult{}, domain.ErrNotFound
	}
	if shop.Revision != q.ExpectedRevision {
		return domain.MerchantResult{}, domain.ErrStaleOwner
	}
	c := q.Session.Character
	c.Items = append([]domain.InventoryItem(nil), c.Items...)
	next := merchantCopy(*shop)
	result := domain.MerchantResult{}
	owner := shop.CharacterID == c.ID
	give := func(template, quantity int) {
		for i := range c.Items {
			if c.Items[i].TemplateID == template {
				c.Items[i].Quantity += quantity
				result.Inventory.Changes = append(result.Inventory.Changes, domain.InventoryChange{Mode: 1, InvType: 2, Slot: c.Items[i].Slot, Quantity: c.Items[i].Quantity})
				return
			}
		}
		item := domain.InventoryItem{ID: int64(1000 + template), TemplateID: template, InvType: 2, Slot: 9, Quantity: quantity}
		c.Items = append(c.Items, item)
		result.Inventory.Changes = append(result.Inventory.Changes, domain.InventoryChange{Mode: 0, InvType: 2, Slot: 9, Item: &item})
	}
	switch q.Kind {
	case "put":
		if !owner || next.State == domain.MerchantOpen {
			return domain.MerchantResult{}, domain.ErrForbidden
		}
		found := false
		for i := range c.Items {
			if c.Items[i].InvType == q.InvType && c.Items[i].Slot == q.Slot && c.Items[i].Quantity >= q.Bundles*q.PerBundle {
				c.Items[i].Quantity -= q.Bundles * q.PerBundle
				result.Inventory.Changes = append(result.Inventory.Changes, domain.InventoryChange{Mode: 1, InvType: q.InvType, Slot: q.Slot, Quantity: c.Items[i].Quantity})
				r.next++
				next.Items = append(next.Items, domain.MerchantItem{ID: r.next, Position: len(next.Items) + 1, Bundles: q.Bundles, PerBundle: q.PerBundle, Price: q.Price, Item: domain.InventoryItem{InvType: q.InvType, TemplateID: c.Items[i].TemplateID, Quantity: q.PerBundle}})
				found = true
			}
		}
		if !found {
			return domain.MerchantResult{}, domain.ErrNotFound
		}
	case "buy":
		if owner || next.State != domain.MerchantOpen {
			return domain.MerchantResult{}, domain.ErrForbidden
		}
		index := -1
		for i, row := range next.Items {
			if row.ID == q.ItemID {
				index = i
			}
		}
		if index < 0 || next.Items[index].Bundles < q.Bundles {
			return domain.MerchantResult{}, domain.ErrNotFound
		}
		row := next.Items[index]
		total := row.Price * int64(q.Bundles)
		if c.Mesos < total {
			return domain.MerchantResult{}, domain.ErrInsufficient
		}
		c.Mesos -= total
		next.Mesos += total
		next.Items[index].Bundles -= q.Bundles
		give(row.Item.TemplateID, q.Bundles*row.PerBundle)
		sale := domain.MerchantSale{TemplateID: row.Item.TemplateID, Bundles: q.Bundles, PerBundle: row.PerBundle, Total: total, BuyerID: c.ID, Buyer: c.Name}
		next.Sales = append(next.Sales, sale)
		result.Sale = &sale
	case "collect", "reclaim":
		if !owner || next.State == domain.MerchantOpen {
			return domain.MerchantResult{}, domain.ErrForbidden
		}
		kept := next.Items[:0:0]
		for _, row := range next.Items {
			if row.Bundles > 0 && q.Kind == "collect" {
				kept = append(kept, row)
			} else if row.Bundles > 0 {
				give(row.Item.TemplateID, row.Bundles*row.PerBundle)
			}
		}
		next.Items = kept
		c.Mesos += next.Mesos
		next.Mesos = 0
		result.Deleted = q.Kind == "reclaim"
	default:
		return domain.MerchantResult{}, domain.ErrInvalidInput
	}
	c.Revision++
	next.Revision++
	if result.Deleted {
		delete(r.shops, shop.ID)
	} else {
		*shop = next
	}
	result.Inventory.Character = c
	result.Shop = merchantCopy(next)
	return result, nil
}

func (r *merchantTestRepository) SetMerchantState(_ context.Context, id int64, from []string, to string) (domain.MerchantShop, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	shop := r.shops[id]
	if shop == nil {
		return domain.MerchantShop{}, false, domain.ErrNotFound
	}
	allowed := false
	for _, state := range from {
		allowed = allowed || state == shop.State
	}
	if !allowed {
		return domain.MerchantShop{}, false, domain.ErrStaleOwner
	}
	if to == domain.MerchantClosed && shop.Mesos == 0 && !shop.Stock() {
		delete(r.shops, id)
		return merchantCopy(*shop), false, nil
	}
	shop.State = to
	shop.Revision++
	return merchantCopy(*shop), true, nil
}

func (r *merchantTestRepository) ActiveMerchants(context.Context, int) ([]domain.MerchantShop, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var shops []domain.MerchantShop
	for _, shop := range r.shops {
		if shop.State != domain.MerchantClosed {
			shops = append(shops, merchantCopy(*shop))
		}
	}
	return shops, nil
}

func (r *merchantTestRepository) state(id int64) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if shop := r.shops[id]; shop != nil {
		return shop.State
	}
	return ""
}

func (m *memSink) take() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.pkts
	m.pkts = nil
	return out
}

// merchantFind returns the first packet with the opcode and leading body.
func merchantFind(packets [][]byte, opcode uint16, body ...byte) []byte {
	for _, p := range packets {
		if len(p) >= 2+len(body) && uint16(p[0])|uint16(p[1])<<8 == opcode && bytes.Equal(p[2:2+len(body)], body) {
			return p
		}
	}
	return nil
}

type merchantActor struct {
	live *liveCharacter
	sink *memSink
}

func merchantWorld(t *testing.T) (*Server, *merchantTestRepository, merchantActor, merchantActor) {
	t.Helper()
	repo := &merchantTestRepository{shops: map[int64]*domain.MerchantShop{}}
	s := &Server{repo: repo, merchants: newMerchantSpace(), drops: newDropSpace(), online: map[int64]domain.CharacterSession{}, scenes: map[int64]*sceneVisit{}}
	actor := func(id int64, name string, items []domain.InventoryItem) merchantActor {
		live := &liveCharacter{session: domain.CharacterSession{AccountID: id, CharacterID: id, ChannelID: 1, Character: domain.Character{ID: id, AccountID: id, Name: name, Level: 30, MapID: merchantFirstRoom, X: int(id) * 200, Y: 34, HP: 50, MaxHP: 50, Mesos: 1000, Revision: 1, Items: items}}}
		sink := &memSink{}
		s.drops.Track(1, merchantFirstRoom, id, sink)
		s.scenes[id] = &sceneVisit{live: live, sink: sink}
		return merchantActor{live, sink}
	}
	owner := actor(1, "Owner", []domain.InventoryItem{{ID: 1, TemplateID: 2000000, InvType: 2, Slot: 1, Quantity: 5}, {ID: 2, TemplateID: 5030000, InvType: 5, Slot: 1, Quantity: 1}})
	buyer := actor(2, "Buyer", []domain.InventoryItem{{ID: 3, TemplateID: 2000000, InvType: 2, Slot: 1, Quantity: 5}})
	return s, repo, owner, buyer
}

func merchantSend(t *testing.T, s *Server, a merchantActor, action byte, body func(*protocol.Writer)) bool {
	t.Helper()
	w := protocol.NewRawWriter()
	w.U8(action)
	if body != nil {
		body(w)
	}
	handled, err := s.handleMerchantInteraction(context.Background(), a.live, a.sink, protocol.NewReader(w.Packet()))
	if err != nil {
		t.Fatalf("action %d: %v", action, err)
	}
	return handled
}

func merchantOpenShop(t *testing.T, s *Server, owner merchantActor) *merchantRoom {
	t.Helper()
	merchantSend(t, s, owner, 6, func(w *protocol.Writer) { w.U8(5); w.String("shop"); w.U8(0); w.I16(1); w.I32(5030000) })
	merchantSend(t, s, owner, 21, func(w *protocol.Writer) { w.U8(2); w.I16(1); w.I16(2); w.I16(2); w.I32(300) })
	merchantSend(t, s, owner, 16, func(w *protocol.Writer) { w.U8(1) })
	room := s.merchants.byOwner[1]
	if room == nil {
		t.Fatal("no room after opening")
	}
	return room
}

func merchantEnterShop(t *testing.T, s *Server, a merchantActor, room *merchantRoom) {
	t.Helper()
	merchantSend(t, s, a, 9, func(w *protocol.Writer) { w.U32(room.serial); w.Zero(2) })
}

func TestMerchantOpenVisitBuyMaintainWithdraw(t *testing.T) {
	s, repo, owner, buyer := merchantWorld(t)
	ctx := context.Background()

	// Trade requests are not ours while nobody sits in a shop.
	if merchantSend(t, s, owner, 6, func(w *protocol.Writer) { w.U8(3); w.U8(0) }) || merchantSend(t, s, buyer, 9, func(w *protocol.Writer) { w.U32(7); w.Zero(2) }) {
		t.Fatal("a trade create/accept was routed to the merchant handler")
	}

	if err := s.handleMerchantCheck(ctx, owner.live, owner.sink); err != nil || merchantFind(owner.sink.take(), protocol.SendEntrustedShopCheck, protocol.MerchantCheckOpenTitle) == nil {
		t.Fatalf("permit check should ask for a title: %v", err)
	}
	if !merchantSend(t, s, owner, 6, func(w *protocol.Writer) { w.U8(5); w.String("shop"); w.U8(0); w.I16(1); w.I32(5030000) }) {
		t.Fatal("create not handled")
	}
	if merchantFind(owner.sink.take(), protocol.SendPlayerInteraction, 10, 5, protocol.MerchantSeats, 0) == nil || !s.tradeActive(owner.live) {
		t.Fatal("owner did not get the shop dialog in seat 0")
	}
	if merchantFind(buyer.sink.take(), protocol.SendEmployeeEnter) != nil {
		t.Fatal("a shop being set up must not stand on the map")
	}
	merchantSend(t, s, owner, 21, func(w *protocol.Writer) { w.U8(2); w.I16(1); w.I16(2); w.I16(2); w.I32(300) })
	packets := owner.sink.take()
	if merchantFind(packets, protocol.SendPlayerInteraction, 46) == nil || owner.live.session.Character.Items[0].Quantity != 1 {
		t.Fatalf("put: quantity=%d", owner.live.session.Character.Items[0].Quantity)
	}
	merchantSend(t, s, owner, 16, func(w *protocol.Writer) { w.U8(1) })
	room := s.merchants.byOwner[1]
	if room == nil || repo.state(room.shop.ID) != domain.MerchantOpen || s.tradeActive(owner.live) {
		t.Fatal("open did not publish the shop and free the owner")
	}
	if merchantFind(buyer.sink.take(), protocol.SendEmployeeEnter) == nil {
		t.Fatal("the map did not see the merchant appear")
	}
	if err := s.handleMerchantCheck(ctx, owner.live, owner.sink); err != nil || merchantFind(owner.sink.take(), protocol.SendEntrustedShopCheck, protocol.MerchantCheckElsewhere) == nil {
		t.Fatal("a second permit use must point at the open shop")
	}

	merchantEnterShop(t, s, buyer, room)
	packets = buyer.sink.take()
	if merchantFind(packets, protocol.SendPlayerInteraction, 10, 5, protocol.MerchantSeats, 1) == nil || merchantFind(packets, protocol.SendEmployeeBalloon) == nil {
		t.Fatal("visitor did not get seat 1 and the balloon update")
	}
	merchantSend(t, s, buyer, 14, func(w *protocol.Writer) { w.U32(1); w.String("hi") })
	if merchantFind(buyer.sink.take(), protocol.SendPlayerInteraction, 14, 13, 1) == nil {
		t.Fatal("chat was not echoed")
	}
	merchantSend(t, s, buyer, 22, func(w *protocol.Writer) { w.U8(0); w.I16(1); w.I32(0) })
	packets = buyer.sink.take()
	if merchantFind(packets, protocol.SendPlayerInteraction, 43, protocol.MerchantBuyOK) == nil || merchantFind(packets, protocol.SendPlayerInteraction, 46) == nil {
		t.Fatal("buy result and list refresh missing")
	}
	if buyer.live.session.Character.Mesos != 700 || buyer.live.session.Character.Items[0].Quantity != 7 {
		t.Fatalf("buyer after purchase: %+v", buyer.live.session.Character)
	}
	if merchantFind(owner.sink.take(), 0x63) == nil {
		t.Fatal("the online owner was not told about the sale")
	}
	merchantSend(t, s, buyer, 22, func(w *protocol.Writer) { w.U8(0); w.I16(1); w.I32(0) })
	merchantSend(t, s, buyer, 22, func(w *protocol.Writer) { w.U8(0); w.I16(1); w.I32(0) })
	if merchantFind(buyer.sink.take(), protocol.SendPlayerInteraction, 43, protocol.MerchantBuyFailed) == nil || buyer.live.session.Character.Mesos != 400 {
		t.Fatalf("sold-out row must refuse: mesos=%d", buyer.live.session.Character.Mesos)
	}

	// The owner walks in: visitors are sent out and nothing sells meanwhile.
	owner.sink.take()
	merchantEnterShop(t, s, owner, room)
	if merchantFind(buyer.sink.take(), protocol.SendPlayerInteraction, 18, 1, protocol.MerchantLeaveMaintenance) == nil || s.tradeActive(buyer.live) {
		t.Fatal("visitor was not sent out for maintenance")
	}
	if merchantFind(owner.sink.take(), protocol.SendPlayerInteraction, 10, 5, protocol.MerchantSeats, 0) == nil || repo.state(room.shop.ID) != domain.MerchantMaintenance {
		t.Fatal("owner did not enter maintenance")
	}
	merchantEnterShop(t, s, buyer, room)
	if merchantFind(buyer.sink.take(), protocol.SendPlayerInteraction, 10, 0, protocol.MiniRoomMaintenance) == nil {
		t.Fatal("entering a shop under maintenance must be refused")
	}
	merchantSend(t, s, owner, 30, nil)
	if merchantFind(owner.sink.take(), protocol.SendPlayerInteraction, 30, 0, 0, 0, 0) == nil || owner.live.session.Character.Mesos != 1600 {
		t.Fatalf("collect: mesos=%d", owner.live.session.Character.Mesos)
	}
	merchantSend(t, s, owner, 29, nil)
	if repo.state(room.shop.ID) != domain.MerchantOpen || s.tradeActive(owner.live) {
		t.Fatal("resume did not reopen the shop")
	}

	// Stock the shop again, then close it from inside.
	merchantEnterShop(t, s, owner, room)
	merchantSend(t, s, owner, 21, func(w *protocol.Writer) { w.U8(2); w.I16(1); w.I16(1); w.I16(1); w.I32(50) })
	owner.sink.take()
	buyer.sink.take()
	merchantSend(t, s, owner, 31, nil)
	if merchantFind(owner.sink.take(), protocol.SendPlayerInteraction, 32, protocol.MerchantWithdrawOK) == nil || owner.live.session.Character.Items[0].Quantity != 1 {
		t.Fatalf("withdraw: %+v", owner.live.session.Character.Items)
	}
	if merchantFind(buyer.sink.take(), protocol.SendEmployeeLeave) == nil || len(s.merchants.byOwner) != 0 || len(repo.shops) != 0 || s.tradeActive(owner.live) {
		t.Fatal("the closed shop is still around")
	}
}

func TestMerchantSurvivesOwnerLogoutAndRestart(t *testing.T) {
	s, repo, owner, buyer := merchantWorld(t)
	room := merchantOpenShop(t, s, owner)
	id := room.shop.ID

	// Owner logs out: the shop keeps selling.
	s.merchantLeave(owner.live)
	delete(s.scenes, 1)
	merchantEnterShop(t, s, buyer, room)
	merchantSend(t, s, buyer, 22, func(w *protocol.Writer) { w.U8(0); w.I16(2); w.I32(0) })
	if buyer.live.session.Character.Mesos != 400 || repo.shops[id].Mesos != 600 {
		t.Fatalf("offline sale: buyer=%d income=%d", buyer.live.session.Character.Mesos, repo.shops[id].Mesos)
	}

	// A new process finds the shop in the store and puts it back on the map.
	restarted := &Server{repo: repo, merchants: newMerchantSpace(), drops: newDropSpace(), online: map[int64]domain.CharacterSession{}, scenes: map[int64]*sceneVisit{}}
	repo.shops[99] = &domain.MerchantShop{ID: 99, CharacterID: 5, AccountID: 5, ChannelID: 1, MapID: merchantFirstRoom, State: domain.MerchantSetup, Mesos: 10, Revision: 1, ExpiresAt: time.Now().Add(time.Hour)}
	if err := restarted.loadMerchants(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.state(99) != domain.MerchantClosed || restarted.merchants.byOwner[5] != nil {
		t.Fatal("a shop caught in setup must go to Fredrick on restart")
	}
	arriving := &memSink{}
	restarted.sendMerchants(1, merchantFirstRoom, arriving)
	if merchantFind(arriving.take(), protocol.SendEmployeeEnter) == nil {
		t.Fatal("the restored shop was not shown to a player entering the map")
	}
	restarted.sendMerchants(2, merchantFirstRoom, arriving)
	if len(arriving.take()) != 0 {
		t.Fatal("a shop leaked into another channel")
	}

	// Business hours end: visitors are told, the rest waits at Fredrick.
	restored := restarted.merchants.byOwner[1]
	restarted.drops.Track(1, merchantFirstRoom, 2, buyer.sink)
	merchantSend(t, restarted, buyer, 9, func(w *protocol.Writer) { w.U32(restored.serial); w.Zero(2) })
	buyer.sink.take()
	restarted.expireMerchants(context.Background(), time.Now().Add(domain.MerchantDuration+time.Minute))
	packets := buyer.sink.take()
	if merchantFind(packets, protocol.SendPlayerInteraction, 18, 1, protocol.MerchantLeaveExpired) == nil || merchantFind(packets, protocol.SendEmployeeLeave) == nil {
		t.Fatal("expiry did not close the dialog and remove the merchant")
	}
	if repo.state(id) != domain.MerchantClosed || restarted.tradeActive(buyer.live) {
		t.Fatal("expired shop is not waiting at Fredrick")
	}

	// Fredrick hands everything back to the owner.
	if err := restarted.openMerchantBank(context.Background(), buyer.live, buyer.sink, 9030000); err != nil || merchantFind(buyer.sink.take(), protocol.SendStoreBank, 40) == nil {
		t.Fatalf("someone with no package should hear so: %v", err)
	}
	if err := restarted.openMerchantBank(context.Background(), owner.live, owner.sink, 9030000); err != nil || merchantFind(owner.sink.take(), protocol.SendStoreBank, 38) == nil {
		t.Fatalf("store bank did not open: %v", err)
	}
	if err := restarted.handleStoreBank(context.Background(), owner.live, owner.sink, protocol.NewReader([]byte{27})); err != nil {
		t.Fatal(err)
	}
	if merchantFind(owner.sink.take(), protocol.SendStoreBankResult, protocol.StoreBankTaken) == nil || owner.live.session.Character.Mesos != 1600 || len(repo.shops) != 1 {
		t.Fatalf("Fredrick payout: mesos=%d shops=%d", owner.live.session.Character.Mesos, len(repo.shops))
	}
}

func TestMerchantOwnerDisconnectDuringMaintenanceReopens(t *testing.T) {
	s, repo, owner, buyer := merchantWorld(t)
	room := merchantOpenShop(t, s, owner)
	merchantEnterShop(t, s, owner, room)
	if repo.state(room.shop.ID) != domain.MerchantMaintenance {
		t.Fatal("not in maintenance")
	}
	s.merchantLeave(owner.live)
	deadline := time.Now().Add(2 * time.Second)
	for repo.state(room.shop.ID) != domain.MerchantOpen {
		if time.Now().After(deadline) {
			t.Fatal("the shop stayed locked after its owner disconnected")
		}
		time.Sleep(5 * time.Millisecond)
	}
	merchantEnterShop(t, s, buyer, room)
	if !s.tradeActive(buyer.live) {
		t.Fatal("visitors cannot enter the reopened shop")
	}
	// A visitor who leaves frees the seat for the others.
	merchantSend(t, s, buyer, 18, nil)
	if s.tradeActive(buyer.live) {
		t.Fatal("leaving did not free the seat")
	}
}

// Fredrick stands on a ledge of the Free Market entrance that players cannot
// walk onto; they talk to him from the ground right below.
func TestNPCReachCoversFredrickFromTheGround(t *testing.T) {
	if !npcReach(-857, 34, -857, -176) || !npcReach(-600, 34, -857, -176) {
		t.Fatal("Fredrick cannot be reached from the ground below him")
	}
	if npcReach(-400, 34, -857, -176) || npcReach(-857, 400, -857, -176) {
		t.Fatal("an NPC far off screen is within reach")
	}
}
