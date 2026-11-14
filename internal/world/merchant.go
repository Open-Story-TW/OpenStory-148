package world

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

// Hired merchants. The shop row in PostgreSQL owns the items and the income;
// a merchantRoom is only the map object and the people inside its dialog, so
// a shop survives its owner logging out and is rebuilt after a restart.

const (
	merchantFirstRoom   = 910000001 // Free Market rooms 1..22
	merchantLastRoom    = 910000022
	merchantChatKept    = 50
	merchantVisitsKept  = 100
	merchantBlacklistTo = 20
	merchantSpacing     = 60 // pixels kept clear of another stand or a portal
)

type merchantSeat struct {
	id   int64
	live *liveCharacter
	sink packetSink
	look domain.Character
}

type merchantVisit struct {
	name string
	at   time.Time
}

type merchantRoom struct {
	// mu serializes every command on one shop and is held across SQL.
	mu sync.Mutex
	// The fields below are guarded by merchantSpace.mu.
	shop    domain.MerchantShop
	serial  uint32
	seats   [protocol.MerchantSeats]*merchantSeat // seat 0 is the owner
	chat    []protocol.MerchantChatLine
	visits  []merchantVisit
	banned  map[string]bool
	spawned bool // an employee stands on the map
	gone    bool
}

type merchantSpace struct {
	mu       sync.Mutex
	byOwner  map[int64]*merchantRoom
	bySerial map[uint32]*merchantRoom
	inside   map[int64]*merchantRoom // character in a seat -> room
	next     uint32
}

func newMerchantSpace() *merchantSpace {
	return &merchantSpace{byOwner: map[int64]*merchantRoom{}, bySerial: map[uint32]*merchantRoom{}, inside: map[int64]*merchantRoom{}, next: protocol.MerchantRoomBase}
}

func (m *merchantSpace) register(shop domain.MerchantShop) *merchantRoom {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	room := &merchantRoom{shop: shop, serial: m.next, banned: map[string]bool{}, spawned: shop.State != domain.MerchantSetup}
	m.byOwner[shop.CharacterID] = room
	m.bySerial[room.serial] = room
	return room
}

func (r *merchantRoom) visitorsLocked() int {
	n := 0
	for seat := 1; seat < len(r.seats); seat++ {
		if r.seats[seat] != nil {
			n++
		}
	}
	return n
}

func (r *merchantRoom) seatOfLocked(id int64) int {
	for seat, s := range r.seats {
		if s != nil && s.id == id {
			return seat
		}
	}
	return -1
}

// sendLocked writes to every occupied seat except skip (-1: nobody skipped).
func (r *merchantRoom) sendLocked(packet []byte, skip int) {
	if packet == nil {
		return
	}
	for seat, s := range r.seats {
		if s != nil && seat != skip {
			_ = s.sink.WritePacket(packet)
		}
	}
}

func (s *Server) merchantRepo() domain.MerchantRepository {
	if s.merchants == nil {
		return nil
	}
	repo, _ := s.repo.(domain.MerchantRepository)
	return repo
}

// merchantInside reports whether live sits in a shop dialog.
func (s *Server) merchantInside(live *liveCharacter) bool {
	if s.merchants == nil {
		return false
	}
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	s.merchants.mu.Lock()
	defer s.merchants.mu.Unlock()
	room := s.merchants.inside[id]
	if room == nil {
		return false
	}
	seat := room.seatOfLocked(id)
	return seat >= 0 && room.seats[seat].live == live
}

func (s *Server) merchantSession(live *liveCharacter) domain.CharacterSession {
	live.mu.Lock()
	defer live.mu.Unlock()
	live.session.Lease = s.currentLease()
	return live.session
}

func merchantMap(mapID int) bool { return mapID >= merchantFirstRoom && mapID <= merchantLastRoom }

// merchantFoothold is the platform under a stand, or 0 when unknown.
func (s *Server) merchantFoothold(mapID, x, y int) int {
	if s.gameplay == nil || s.gameplay.catalog == nil {
		return 0
	}
	m, err := s.gameplay.catalog.Map(mapID)
	if err != nil {
		return 0
	}
	best, bestY := 0, 0
	for _, f := range m.Footholds {
		x1, y1, x2, y2 := f.X1, f.Y1, f.X2, f.Y2
		if x1 > x2 {
			x1, y1, x2, y2 = x2, y2, x1, y1
		}
		if x1 == x2 || x < x1 || x > x2 {
			continue
		}
		at := y1 + (y2-y1)*(x-x1)/(x2-x1)
		if at < y-10 {
			continue
		}
		if best == 0 || at < bestY {
			best, bestY = f.ID, at
		}
	}
	return best
}

func (s *Server) merchantBroadcast(room *merchantRoom, packet []byte) {
	s.merchants.mu.Lock()
	channel, mapID := room.shop.ChannelID, room.shop.MapID
	s.merchants.mu.Unlock()
	s.broadcastInventoryPacket(channel, mapID, packet)
}

func (s *Server) merchantBalloon(room *merchantRoom) {
	s.merchants.mu.Lock()
	var packet []byte
	if room.spawned && !room.gone {
		packet = protocol.EmployeeBalloon(room.shop, room.serial, room.visitorsLocked())
	}
	channel, mapID := room.shop.ChannelID, room.shop.MapID
	s.merchants.mu.Unlock()
	s.broadcastInventoryPacket(channel, mapID, packet)
}

// sendMerchants spawns the stands of a map on a client that just entered it.
func (s *Server) sendMerchants(channel, mapID int, sink packetSink) {
	if s.merchants == nil || sink == nil || !merchantMap(mapID) {
		return
	}
	s.merchants.mu.Lock()
	defer s.merchants.mu.Unlock()
	for _, room := range s.merchants.byOwner {
		if room.spawned && !room.gone && room.shop.ChannelID == channel && room.shop.MapID == mapID {
			_ = sink.WritePacket(protocol.EmployeeEnter(room.shop, room.serial, room.visitorsLocked()))
		}
	}
}

// merchantRemove takes a shop off the map and closes every open dialog.
func (s *Server) merchantRemove(room *merchantRoom, reason byte) {
	m := s.merchants
	m.mu.Lock()
	if room.gone {
		m.mu.Unlock()
		return
	}
	room.gone = true
	if m.byOwner[room.shop.CharacterID] == room {
		delete(m.byOwner, room.shop.CharacterID)
	}
	delete(m.bySerial, room.serial)
	for seat, occupant := range room.seats {
		if occupant == nil {
			continue
		}
		if m.inside[occupant.id] == room {
			delete(m.inside, occupant.id)
		}
		if reason != 0 {
			_ = occupant.sink.WritePacket(protocol.MerchantExpelled(byte(seat), reason))
		}
		room.seats[seat] = nil
	}
	spawned := room.spawned
	room.spawned = false
	channel, mapID, owner := room.shop.ChannelID, room.shop.MapID, room.shop.CharacterID
	m.mu.Unlock()
	if spawned {
		s.broadcastInventoryPacket(channel, mapID, protocol.EmployeeLeave(owner))
	}
}

// merchantClose moves a shop to Fredrick and removes it from the map. Caller
// holds room.mu.
func (s *Server) merchantClose(ctx context.Context, room *merchantRoom, reason byte) {
	repo := s.merchantRepo()
	s.merchants.mu.Lock()
	id := room.shop.ID
	s.merchants.mu.Unlock()
	if repo != nil {
		if _, _, err := repo.SetMerchantState(ctx, id, []string{domain.MerchantSetup, domain.MerchantOpen, domain.MerchantMaintenance}, domain.MerchantClosed); err != nil && !errors.Is(err, domain.ErrNotFound) {
			slog.Warn("merchant close", "shop", id, "error", err)
		}
	}
	s.merchantRemove(room, reason)
}

// loadMerchants rebuilds the map objects after a start. A shop that was never
// opened goes to Fredrick; one caught mid-maintenance simply reopens.
func (s *Server) loadMerchants(ctx context.Context) error {
	repo := s.merchantRepo()
	if repo == nil {
		return nil
	}
	shops, err := repo.ActiveMerchants(ctx, s.cfg.WorldID)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, shop := range shops {
		switch {
		case shop.State == domain.MerchantSetup || !shop.ExpiresAt.After(now):
			if _, _, err = repo.SetMerchantState(ctx, shop.ID, []string{shop.State}, domain.MerchantClosed); err != nil {
				return err
			}
			continue
		case shop.State == domain.MerchantMaintenance:
			updated, _, err := repo.SetMerchantState(ctx, shop.ID, []string{domain.MerchantMaintenance}, domain.MerchantOpen)
			if err != nil {
				return err
			}
			shop = updated
		}
		s.merchants.register(shop)
	}
	return nil
}

func (s *Server) runMerchants(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.expireMerchants(ctx, now)
		}
	}
}

func (s *Server) expireMerchants(ctx context.Context, now time.Time) {
	s.merchants.mu.Lock()
	var due []*merchantRoom
	for _, room := range s.merchants.byOwner {
		if !room.gone && !room.shop.ExpiresAt.After(now) {
			due = append(due, room)
		}
	}
	s.merchants.mu.Unlock()
	for _, room := range due {
		room.mu.Lock()
		closeCtx, done := context.WithTimeout(ctx, 5*time.Second)
		s.merchantClose(closeCtx, room, protocol.MerchantLeaveExpired)
		done()
		room.mu.Unlock()
	}
}

// merchantLeave frees the seat of a character that disconnected, changed
// channel or left the map. It takes no SQL lock, so any caller may use it.
func (s *Server) merchantLeave(live *liveCharacter) {
	if s.merchants == nil || live == nil {
		return
	}
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	room, seat := s.merchantVacate(id, live)
	if room == nil {
		return
	}
	if seat == 0 {
		// The owner walked away mid-setup or mid-maintenance.
		go func() {
			ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			room.mu.Lock()
			defer room.mu.Unlock()
			s.merchantOwnerOut(ctx, room)
		}()
		return
	}
	s.merchantBalloon(room)
}

// merchantVacate empties the seat of id and tells the others.
func (s *Server) merchantVacate(id int64, live *liveCharacter) (*merchantRoom, int) {
	m := s.merchants
	m.mu.Lock()
	defer m.mu.Unlock()
	room := m.inside[id]
	if room == nil {
		return nil, -1
	}
	seat := room.seatOfLocked(id)
	if seat < 0 || (live != nil && room.seats[seat].live != live) {
		return nil, -1
	}
	delete(m.inside, id)
	room.seats[seat] = nil
	if seat > 0 {
		room.sendLocked(protocol.MerchantVisitorLeft(byte(seat)), -1)
	}
	return room, seat
}

// merchantOwnerOut settles a shop whose owner left the dialog without
// choosing: an unopened shop closes, a maintained one reopens. Caller holds
// room.mu.
func (s *Server) merchantOwnerOut(ctx context.Context, room *merchantRoom) {
	s.merchants.mu.Lock()
	state, gone, occupied := room.shop.State, room.gone, room.seats[0] != nil
	s.merchants.mu.Unlock()
	if gone || occupied {
		return
	}
	switch state {
	case domain.MerchantSetup:
		s.merchantClose(ctx, room, protocol.MerchantLeaveClosed)
	case domain.MerchantMaintenance:
		_ = s.merchantOpen(ctx, room)
	}
}

// merchantOpen puts the shop on sale. Caller holds room.mu.
func (s *Server) merchantOpen(ctx context.Context, room *merchantRoom) error {
	repo := s.merchantRepo()
	if repo == nil {
		return domain.ErrForbidden
	}
	s.merchants.mu.Lock()
	id := room.shop.ID
	s.merchants.mu.Unlock()
	shop, found, err := repo.SetMerchantState(ctx, id, []string{domain.MerchantSetup, domain.MerchantMaintenance}, domain.MerchantOpen)
	if err != nil {
		return err
	}
	if !found {
		s.merchantRemove(room, protocol.MerchantLeaveClosed)
		return domain.ErrNotFound
	}
	s.merchants.mu.Lock()
	room.shop = shop
	first := !room.spawned
	room.spawned = true
	var packet []byte
	if first {
		packet = protocol.EmployeeEnter(shop, room.serial, room.visitorsLocked())
	} else {
		packet = protocol.EmployeeBalloon(shop, room.serial, room.visitorsLocked())
	}
	s.merchants.mu.Unlock()
	s.broadcastInventoryPacket(shop.ChannelID, shop.MapID, packet)
	return nil
}

// handleMerchantCheck answers the permit double-click (0x6E).
func (s *Server) handleMerchantCheck(ctx context.Context, live *liveCharacter, c packetSink) error {
	repo := s.merchantRepo()
	session := s.merchantSession(live)
	if repo == nil || !merchantMap(session.Character.MapID) || session.Character.HP <= 0 || s.tradeActive(live) || s.tradeDialogBusy(live) {
		return c.WritePacket(protocol.MerchantCheck(protocol.MerchantCheckUnable))
	}
	shop, found, err := repo.MerchantFor(ctx, session.AccountID, session.Lease.WorldID)
	switch {
	case err != nil:
		return c.WritePacket(protocol.MerchantCheck(protocol.MerchantCheckUnable))
	case found && shop.State == domain.MerchantClosed && shop.CharacterID == session.CharacterID:
		return c.WritePacket(protocol.MerchantCheck(protocol.MerchantCheckFredrick))
	case found && shop.State == domain.MerchantClosed:
		return c.WritePacket(protocol.MerchantCheck(protocol.MerchantCheckOtherChar))
	case found:
		return c.WritePacket(protocol.MerchantCheckOpenAt(shop.MapID, shop.ChannelID))
	}
	return c.WritePacket(protocol.MerchantCheck(protocol.MerchantCheckOpenTitle))
}

// handleMerchantInteraction routes a player interaction request that belongs
// to a hired merchant. handled is false for trade requests.
func (s *Server) handleMerchantInteraction(ctx context.Context, live *liveCharacter, c packetSink, r *protocol.Reader) (bool, error) {
	if s.merchants == nil {
		return false, nil
	}
	action, roomType, serial := protocol.PeekMiniRoom(r.BytesBetween(r.Pos(), r.Pos()+r.Remaining()))
	live.mu.Lock()
	id := live.session.CharacterID
	live.mu.Unlock()
	s.merchants.mu.Lock()
	room := s.merchants.inside[id]
	if room != nil {
		if seat := room.seatOfLocked(id); seat < 0 || room.seats[seat].live != live {
			room = nil
		}
	}
	var target *merchantRoom
	if room == nil && action == 9 && serial > protocol.MerchantRoomBase {
		target = s.merchants.bySerial[serial]
	}
	s.merchants.mu.Unlock()
	switch {
	case room != nil:
	case action == 6 && (roomType == protocol.MiniRoomEntrustedShop || roomType == 4):
	case action == 9 && serial > protocol.MerchantRoomBase:
	default:
		return false, nil
	}
	request, err := protocol.ReadMerchantAction(r)
	if err != nil {
		if errors.Is(err, protocol.ErrUnsupportedTradeAction) {
			return true, c.WritePacket(protocol.EnableActions())
		}
		return true, err
	}
	switch {
	case room != nil:
		return true, s.merchantCommand(ctx, room, live, c, request)
	case request.Action == 6:
		if request.RoomType != protocol.MiniRoomEntrustedShop {
			return true, c.WritePacket(protocol.MerchantEnterFailed(protocol.MiniRoomNoShopHere))
		}
		return true, s.merchantCreate(ctx, live, c, request)
	default:
		if target == nil {
			return true, c.WritePacket(protocol.MerchantEnterFailed(protocol.MiniRoomClosed))
		}
		return true, s.merchantEnter(ctx, target, live, c)
	}
}

func (s *Server) merchantCreate(ctx context.Context, live *liveCharacter, c packetSink, request protocol.MerchantAction) error {
	repo := s.merchantRepo()
	session := s.merchantSession(live)
	fail := func(code byte) error { return c.WritePacket(protocol.MerchantEnterFailed(code)) }
	if repo == nil || session.Character.HP <= 0 || s.tradeActive(live) || s.tradeDialogBusy(live) {
		return fail(protocol.MiniRoomNotAllowed)
	}
	mapID, x, y := session.Character.MapID, session.Character.X, session.Character.Y
	if !merchantMap(mapID) {
		return fail(protocol.MiniRoomFreeMarket)
	}
	if s.gameplay != nil && s.gameplay.catalog != nil {
		if m, err := s.gameplay.catalog.Map(mapID); err == nil {
			for _, portal := range m.Portals {
				if merchantNear(x, y, portal.X, portal.Y) {
					return fail(protocol.MiniRoomNearPortal)
				}
			}
		}
	}
	s.merchants.mu.Lock()
	crowded := false
	for _, other := range s.merchants.byOwner {
		if !other.gone && other.shop.ChannelID == session.ChannelID && other.shop.MapID == mapID && merchantNear(x, y, other.shop.X, other.shop.Y) {
			crowded = true
			break
		}
	}
	s.merchants.mu.Unlock()
	if crowded {
		return fail(protocol.MiniRoomNoShopHere)
	}
	live.persistMu.Lock()
	session = s.merchantSession(live)
	shop, err := repo.CreateMerchant(ctx, domain.MerchantCreate{Session: session, PermitSlot: request.Slot, PermitItemID: request.ItemID, Title: request.Title, Foothold: s.merchantFoothold(mapID, x, y)})
	live.persistMu.Unlock()
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrForbidden) {
			return fail(protocol.MiniRoomNotAllowed)
		}
		_ = fail(protocol.MiniRoomNotAllowed)
		return err
	}
	room := s.merchants.register(shop)
	s.merchants.mu.Lock()
	room.seats[0] = &merchantSeat{id: session.CharacterID, live: live, sink: c, look: session.Character}
	s.merchants.inside[session.CharacterID] = room
	packet := protocol.MerchantRoom(shop, 0, nil, nil, true, 0)
	s.merchants.mu.Unlock()
	return c.WritePacket(packet)
}

func merchantNear(ax, ay, bx, by int) bool {
	dx, dy := ax-bx, ay-by
	return dx > -merchantSpacing && dx < merchantSpacing && dy > -merchantSpacing && dy < merchantSpacing
}

func merchantVisitorsLocked(room *merchantRoom) []protocol.MerchantVisitor {
	var visitors []protocol.MerchantVisitor
	for seat := 1; seat < len(room.seats); seat++ {
		if occupant := room.seats[seat]; occupant != nil {
			visitors = append(visitors, protocol.MerchantVisitor{Seat: byte(seat), Character: occupant.look})
		}
	}
	return visitors
}

func (s *Server) merchantEnter(ctx context.Context, room *merchantRoom, live *liveCharacter, c packetSink) error {
	repo := s.merchantRepo()
	session := s.merchantSession(live)
	fail := func(code byte) error { return c.WritePacket(protocol.MerchantEnterFailed(code)) }
	if repo == nil || session.Character.HP <= 0 || s.tradeActive(live) || s.tradeDialogBusy(live) {
		return fail(protocol.MiniRoomBusy)
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	m := s.merchants
	m.mu.Lock()
	shop, gone := room.shop, room.gone
	m.mu.Unlock()
	if gone || !room.spawnedNow(m) {
		return fail(protocol.MiniRoomClosed)
	}
	if shop.ChannelID != session.ChannelID || shop.MapID != session.Character.MapID {
		return fail(protocol.MiniRoomCannotEnter)
	}
	if shop.CharacterID == session.CharacterID {
		return s.merchantMaintain(ctx, room, live, c, session)
	}
	if shop.AccountID == session.AccountID {
		return fail(protocol.MiniRoomNotAllowed)
	}
	if shop.State != domain.MerchantOpen {
		return fail(protocol.MiniRoomMaintenance)
	}
	m.mu.Lock()
	if room.banned[session.Character.Name] {
		m.mu.Unlock()
		return fail(protocol.MiniRoomCannotEnter)
	}
	seat := 0
	for n := 1; n < len(room.seats); n++ {
		if room.seats[n] == nil {
			seat = n
			break
		}
	}
	if seat == 0 {
		m.mu.Unlock()
		return fail(protocol.MiniRoomFull)
	}
	occupant := &merchantSeat{id: session.CharacterID, live: live, sink: c, look: session.Character}
	visitors := merchantVisitorsLocked(room)
	room.seats[seat] = occupant
	m.inside[session.CharacterID] = room
	visitor := protocol.MerchantVisitor{Seat: byte(seat), Character: occupant.look}
	visitors = append(visitors, visitor)
	room.visits = append(room.visits, merchantVisit{name: session.Character.Name, at: time.Now()})
	if len(room.visits) > merchantVisitsKept {
		room.visits = room.visits[len(room.visits)-merchantVisitsKept:]
	}
	packet := protocol.MerchantRoom(room.shop, byte(seat), visitors, room.chat, false, 0)
	room.sendLocked(protocol.MerchantVisitorEnter(visitor), seat)
	m.mu.Unlock()
	err := c.WritePacket(packet)
	s.merchantBalloon(room)
	return err
}

func (r *merchantRoom) spawnedNow(m *merchantSpace) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return r.spawned
}

// merchantMaintain seats the owner: visitors are sent out and sales stop
// until the owner resumes. Caller holds room.mu.
func (s *Server) merchantMaintain(ctx context.Context, room *merchantRoom, live *liveCharacter, c packetSink, session domain.CharacterSession) error {
	repo := s.merchantRepo()
	m := s.merchants
	m.mu.Lock()
	id := room.shop.ID
	m.mu.Unlock()
	shop, found, err := repo.SetMerchantState(ctx, id, []string{domain.MerchantOpen, domain.MerchantMaintenance}, domain.MerchantMaintenance)
	if err != nil || !found {
		return c.WritePacket(protocol.MerchantEnterFailed(protocol.MiniRoomCannotEnter))
	}
	m.mu.Lock()
	room.shop = shop
	for seat := 1; seat < len(room.seats); seat++ {
		if occupant := room.seats[seat]; occupant != nil {
			_ = occupant.sink.WritePacket(protocol.MerchantExpelled(byte(seat), protocol.MerchantLeaveMaintenance))
			if m.inside[occupant.id] == room {
				delete(m.inside, occupant.id)
			}
			room.seats[seat] = nil
		}
	}
	room.seats[0] = &merchantSeat{id: session.CharacterID, live: live, sink: c, look: session.Character}
	m.inside[session.CharacterID] = room
	packet := protocol.MerchantRoom(shop, 0, nil, room.chat, false, int(time.Since(shop.OpenedAt)/time.Second))
	m.mu.Unlock()
	err = c.WritePacket(packet)
	s.merchantBalloon(room)
	return err
}

// merchantChange runs one durable command of the character in the dialog and
// publishes the caller's inventory. Caller holds room.mu.
func (s *Server) merchantChange(ctx context.Context, room *merchantRoom, live *liveCharacter, c packetSink, request domain.MerchantRequest) (domain.MerchantResult, error) {
	repo := s.merchantRepo()
	if repo == nil {
		return domain.MerchantResult{}, domain.ErrForbidden
	}
	s.merchants.mu.Lock()
	request.ShopID, request.ExpectedRevision = room.shop.ID, room.shop.Revision
	s.merchants.mu.Unlock()
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	request.Session = s.merchantSession(live)
	result, err := repo.ChangeMerchant(ctx, request)
	if err != nil {
		return domain.MerchantResult{}, err
	}
	s.applyInventoryResult(live, result.Inventory.Character)
	s.merchants.mu.Lock()
	room.shop = result.Shop
	s.merchants.mu.Unlock()
	if len(result.Inventory.Changes) > 0 {
		if err = c.WritePacket(protocol.ModifyInventory(result.Inventory.Changes)); err != nil {
			return result, err
		}
	}
	return result, c.WritePacket(protocol.UpdateMesos(result.Inventory.Character.Mesos, true))
}

// merchantRefresh resends the item list to everybody in the dialog.
func (s *Server) merchantRefresh(room *merchantRoom) {
	s.merchants.mu.Lock()
	room.sendLocked(protocol.MerchantItems(room.shop), -1)
	s.merchants.mu.Unlock()
}

func merchantRejected(err error) bool {
	return errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrInsufficient) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrStaleOwner)
}

// merchantCommand handles a request of a character seated in room.
func (s *Server) merchantCommand(ctx context.Context, room *merchantRoom, live *liveCharacter, c packetSink, request protocol.MerchantAction) error {
	live.mu.Lock()
	id, name := live.session.CharacterID, live.session.Character.Name
	live.mu.Unlock()
	if request.Action == 18 {
		s.merchantLeave(live)
		return nil
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	m := s.merchants
	m.mu.Lock()
	seat := room.seatOfLocked(id)
	shop, gone := room.shop, room.gone
	m.mu.Unlock()
	if gone || seat < 0 {
		return c.WritePacket(protocol.EnableActions())
	}
	owner := seat == 0
	switch request.Action {
	case 14:
		if request.Text == "" || len(request.Text) > 200 {
			return nil
		}
		line := protocol.MerchantChatLine{Text: name + " : " + request.Text, Seat: byte(seat)}
		m.mu.Lock()
		room.chat = append(room.chat, line)
		if len(room.chat) > merchantChatKept {
			room.chat = room.chat[len(room.chat)-merchantChatKept:]
		}
		room.sendLocked(protocol.MerchantChat(line.Text, line.Seat), -1)
		m.mu.Unlock()
		return nil
	case 22:
		if owner {
			return c.WritePacket(protocol.MerchantBuyResult(protocol.MerchantBuyFailed))
		}
		return s.merchantBuy(ctx, room, live, c, shop, request)
	}
	if !owner {
		return c.WritePacket(protocol.EnableActions())
	}
	switch request.Action {
	case 21:
		_, err := s.merchantChange(ctx, room, live, c, domain.MerchantRequest{Kind: "put", InvType: request.InvType, Slot: request.Slot, Bundles: request.Bundles, PerBundle: request.PerBundle, Price: request.Price})
		s.merchantRefresh(room)
		if err != nil && !merchantRejected(err) {
			return err
		}
		if err != nil {
			return c.WritePacket(protocol.EnableActions())
		}
		return nil
	case 28:
		if request.Index < 0 || request.Index >= len(shop.Items) {
			s.merchantRefresh(room)
			return c.WritePacket(protocol.EnableActions())
		}
		_, err := s.merchantChange(ctx, room, live, c, domain.MerchantRequest{Kind: "take", ItemID: shop.Items[request.Index].ID})
		s.merchantRefresh(room)
		if err != nil && !merchantRejected(err) {
			return err
		}
		if err != nil {
			return c.WritePacket(protocol.EnableActions())
		}
		return nil
	case 30, 33:
		_, err := s.merchantChange(ctx, room, live, c, domain.MerchantRequest{Kind: "collect"})
		if err != nil && !merchantRejected(err) {
			return err
		}
		m.mu.Lock()
		left := room.shop.Mesos
		m.mu.Unlock()
		if request.Action == 30 {
			_ = c.WritePacket(protocol.MerchantArranged(left))
		} else {
			_ = c.WritePacket(protocol.MerchantMesos(left))
		}
		s.merchantRefresh(room)
		if err != nil {
			return c.WritePacket(protocol.EnableActions())
		}
		return nil
	case 16, 29:
		// The native dialog closes itself after sending either request.
		m.mu.Lock()
		if room.seats[0] != nil && room.seats[0].id == id {
			room.seats[0] = nil
			delete(m.inside, id)
		}
		empty := !room.shop.Stock() && room.shop.Mesos == 0
		m.mu.Unlock()
		if empty && shop.State == domain.MerchantSetup {
			s.merchantClose(ctx, room, 0)
			return c.WritePacket(protocol.EnableActions())
		}
		if err := s.merchantOpen(ctx, room); err != nil && !merchantRejected(err) {
			return err
		}
		return c.WritePacket(protocol.EnableActions())
	case 31:
		return s.merchantWithdraw(ctx, room, live, c)
	case 36:
		now := time.Now()
		m.mu.Lock()
		entries := make([]protocol.MerchantVisitEntry, 0, len(room.visits))
		for _, visit := range room.visits {
			entries = append(entries, protocol.MerchantVisitEntry{Name: visit.name, Seconds: int(now.Sub(visit.at) / time.Second)})
		}
		m.mu.Unlock()
		return c.WritePacket(protocol.MerchantVisitList(entries))
	case 37, 38, 39:
		m.mu.Lock()
		switch {
		case request.Action == 38 && request.Text != "" && len(request.Text) <= 32 && len(room.banned) < merchantBlacklistTo:
			room.banned[request.Text] = true
		case request.Action == 39:
			delete(room.banned, request.Text)
		}
		names := make([]string, 0, len(room.banned))
		for banned := range room.banned {
			names = append(names, banned)
		}
		m.mu.Unlock()
		if request.Action != 37 {
			return nil
		}
		return c.WritePacket(protocol.MerchantBlacklist(names))
	}
	return c.WritePacket(protocol.EnableActions())
}

func (s *Server) merchantBuy(ctx context.Context, room *merchantRoom, live *liveCharacter, c packetSink, shop domain.MerchantShop, request protocol.MerchantAction) error {
	if shop.State != domain.MerchantOpen || request.Index < 0 || request.Index >= len(shop.Items) || request.Bundles < 1 {
		s.merchantRefresh(room)
		return c.WritePacket(protocol.MerchantBuyResult(protocol.MerchantBuyFailed))
	}
	row := shop.Items[request.Index]
	total, fits := domain.MerchantTotal(row.Price, request.Bundles)
	live.mu.Lock()
	mesos := live.session.Character.Mesos
	live.mu.Unlock()
	if request.Bundles > row.Bundles {
		s.merchantRefresh(room)
		return c.WritePacket(protocol.MerchantBuyResult(protocol.MerchantBuyFailed))
	}
	if !fits || total > domain.MerchantMaxPrice() {
		return c.WritePacket(protocol.MerchantBuyResult(protocol.MerchantBuyTooDear))
	}
	if shop.Mesos > math.MaxInt64-total {
		return c.WritePacket(protocol.MerchantBuyResult(protocol.MerchantBuySellerMax))
	}
	if mesos < total {
		return c.WritePacket(protocol.MerchantBuyResult(protocol.MerchantBuyNoMesos))
	}
	result, err := s.merchantChange(ctx, room, live, c, domain.MerchantRequest{Kind: "buy", ItemID: row.ID, Bundles: request.Bundles, Price: row.Price})
	if err != nil {
		code := protocol.MerchantBuyFailed
		if errors.Is(err, domain.ErrInsufficient) {
			code = protocol.MerchantBuyNoSpace
		}
		s.merchantRefresh(room)
		writeErr := c.WritePacket(protocol.MerchantBuyResult(code))
		if merchantRejected(err) {
			return writeErr
		}
		return err
	}
	_ = c.WritePacket(protocol.MerchantBuyResult(protocol.MerchantBuyOK))
	s.merchantRefresh(room)
	if result.Sale != nil {
		s.merchantNotifyOwner(result.Shop, *result.Sale, row.Bundles-request.Bundles)
	}
	return nil
}

// merchantNotifyOwner tells an online owner about a sale (Legacy dropMessage).
func (s *Server) merchantNotifyOwner(shop domain.MerchantShop, sale domain.MerchantSale, left int) {
	s.mu.RLock()
	visit := s.scenes[shop.CharacterID]
	s.mu.RUnlock()
	if visit == nil || visit.sink == nil {
		return
	}
	_ = visit.sink.WritePacket(protocol.Notice(fmt.Sprintf("[精靈商人] %s 買走了道具 %d (%d) x %d，收入 %s 楓幣，剩餘 %d 組。", sale.Buyer, sale.TemplateID, sale.PerBundle, sale.Bundles, domain.MoneyUnits(sale.Total), left)))
}

// merchantWithdraw closes the shop from inside: everything returns to the
// owner, or waits at Fredrick when it does not fit. Caller holds room.mu.
func (s *Server) merchantWithdraw(ctx context.Context, room *merchantRoom, live *liveCharacter, c packetSink) error {
	_, err := s.merchantChange(ctx, room, live, c, domain.MerchantRequest{Kind: "reclaim"})
	code := protocol.MerchantWithdrawOK
	switch {
	case err == nil:
		s.merchantRemove(room, 0)
		return c.WritePacket(protocol.MerchantWithdrawResult(code))
	case errors.Is(err, domain.ErrInsufficient):
		code = protocol.MerchantWithdrawNoSpace
		s.merchants.mu.Lock()
		income := room.shop.Mesos
		s.merchants.mu.Unlock()
		live.mu.Lock()
		if income > domain.MaxMoney()-live.session.Character.Mesos {
			code = protocol.MerchantWithdrawMesos
		}
		live.mu.Unlock()
	case merchantRejected(err):
		code = protocol.MerchantWithdrawFailed
	default:
		_ = c.WritePacket(protocol.MerchantWithdrawResult(protocol.MerchantWithdrawFailed))
		return err
	}
	// Seat 0 is cleared without a leave frame: the result closes the dialog.
	s.merchantVacate(room.shop.CharacterID, live)
	s.merchantClose(ctx, room, protocol.MerchantLeaveClosed)
	if err = c.WritePacket(protocol.MerchantWithdrawResult(code)); err != nil {
		return err
	}
	return c.WritePacket(protocol.EnableActions())
}

// openMerchantBank is Fredrick: called by the script host after the NPC
// identity, map and distance are validated.
func (s *Server) openMerchantBank(ctx context.Context, live *liveCharacter, c packetSink, npcID int) error {
	repo := s.merchantRepo()
	if repo == nil || s.tradeActive(live) {
		return domain.ErrForbidden
	}
	session := s.merchantSession(live)
	shop, found, err := repo.MerchantFor(ctx, session.AccountID, session.Lease.WorldID)
	switch {
	case err != nil:
		return c.WritePacket(protocol.StoreBankUnable())
	case !found || (shop.State == domain.MerchantClosed && shop.CharacterID != session.CharacterID):
		return c.WritePacket(protocol.StoreBankEmpty())
	case shop.State != domain.MerchantClosed:
		return c.WritePacket(protocol.StoreBankOpenAt(shop.MapID, shop.ChannelID))
	}
	return c.WritePacket(protocol.StoreBankOpen(npcID, shop))
}

// handleStoreBank is the Fredrick dialog (0x6F): 27 take out, 28 confirmed,
// 29 close, 21 the reply to a second-password prompt this server never sends.
func (s *Server) handleStoreBank(ctx context.Context, live *liveCharacter, c packetSink, r *protocol.Reader) error {
	mode := r.U8()
	if r.Err() != nil {
		return protocol.ErrMalformed
	}
	if mode != 27 && mode != 28 {
		return c.WritePacket(protocol.EnableActions())
	}
	repo := s.merchantRepo()
	if repo == nil || s.tradeActive(live) {
		return c.WritePacket(protocol.StoreBankResult(protocol.StoreBankFeeShort))
	}
	session := s.merchantSession(live)
	shop, found, err := repo.MerchantFor(ctx, session.AccountID, session.Lease.WorldID)
	if err != nil || !found || shop.State != domain.MerchantClosed || shop.CharacterID != session.CharacterID {
		_ = c.WritePacket(protocol.StoreBankResult(protocol.StoreBankNoSpace))
		return c.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session = s.merchantSession(live)
	result, err := repo.ChangeMerchant(ctx, domain.MerchantRequest{Session: session, ShopID: shop.ID, ExpectedRevision: shop.Revision, Kind: "reclaim"})
	if err != nil {
		code := protocol.StoreBankNoSpace
		if errors.Is(err, domain.ErrInsufficient) && shop.Mesos > domain.MaxMoney()-session.Character.Mesos {
			code = protocol.StoreBankMesos
		}
		writeErr := c.WritePacket(protocol.StoreBankResult(code))
		if merchantRejected(err) {
			return writeErr
		}
		return err
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
	return c.WritePacket(protocol.StoreBankResult(protocol.StoreBankTaken))
}
