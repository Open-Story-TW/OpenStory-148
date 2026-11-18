package world

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"os"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

// A visit is immutable. Equality of map/sink alone does not identify a repeated
// entry into the same field. Every dependent operation pins this exact object.
type sceneVisit struct {
	owner domain.CharacterSession // Identity plus map only; no mutable asset slices.
	live  *liveCharacter
	sink  packetSink
	token string
}

func makeSceneVisit(v domain.CharacterSession, live *liveCharacter, sink packetSink) *sceneVisit {
	v.Character = domain.Character{ID: v.CharacterID, AccountID: v.AccountID, WorldID: v.Lease.WorldID, MapID: v.Character.MapID}
	return &sceneVisit{owner: v, live: live, sink: sink, token: platform.NewID()}
}

func (s *Server) sceneFor(live *liveCharacter, sink packetSink) *sceneVisit {
	if live == nil {
		return nil
	}
	live.mu.Lock()
	v, current := live.visit, live.session
	live.mu.Unlock()
	if v == nil || v.live != live || sink != nil && !sameDropSink(v.sink, sink) || !sameSceneOwner(v.owner, current) || v.owner.Character.MapID != current.Character.MapID {
		return nil
	}
	s.mu.RLock()
	registered := s.scenes[current.CharacterID] == v
	s.mu.RUnlock()
	if !registered {
		return nil
	}
	return v
}

// Caller owns the CID scene gate. No registry lock is retained on return.
func (s *Server) sceneCurrent(v *sceneVisit) bool {
	if v == nil || v.live == nil || v.sink == nil {
		return false
	}
	s.mu.RLock()
	registered := s.scenes[v.owner.CharacterID] == v
	owner, exists := s.online[v.owner.CharacterID]
	s.mu.RUnlock()
	if !registered || !exists || !sameSceneOwner(owner, v.owner) || owner.Character.MapID != v.owner.Character.MapID {
		return false
	}
	v.live.mu.Lock()
	current, visit := v.live.session, v.live.visit
	v.live.mu.Unlock()
	return visit == v && sameSceneOwner(current, v.owner) && current.Character.MapID == v.owner.Character.MapID
}

// Requires the CID gate. Never waits for the old live's persistMu or VM. The
// asynchronous script cancellation performs its own exact-live identity check.
func (s *Server) detachScene(v *sceneVisit) {
	if !s.sceneCurrent(v) {
		return
	}
	owner := v.owner
	if s.maps != nil {
		s.maps.Leave(owner.ChannelID, owner.Character.MapID, owner.CharacterID, v.sink)
	}
	if s.gameplay != nil {
		s.leaveGameplay(owner.ChannelID, owner.Character.MapID, owner.CharacterID, v.sink)
		s.gameplay.leaveCarnivalSession(v)
		s.RemoveTownPortal(owner.ChannelID, owner.CharacterID)
	}
	if s.skills != nil {
		s.leaveSkills(owner.CharacterID, v.live)
	}
	if s.drops != nil {
		s.drops.Untrack(owner.ChannelID, owner.Character.MapID, owner.CharacterID, v.sink)
	}
	s.tradeLeave(v.live)
	s.leaveBuddies(v.live)
	s.leaveParty(v.live)
	s.eventDeparted(owner.CharacterID)
	s.closeEconomyDialog(v.live)
	v.live.mu.Lock()
	if v.live.visit == v {
		invalidateMapEntryQueue(v.live.mapEntry)
		v.live.visit = nil
	}
	v.live.mu.Unlock()
	s.mu.Lock()
	if s.scenes[owner.CharacterID] == v {
		delete(s.scenes, owner.CharacterID)
		if current, ok := s.online[owner.CharacterID]; ok && sameSceneOwner(current, owner) {
			delete(s.online, owner.CharacterID)
		}
	}
	s.mu.Unlock()
	if s.scripts != nil {
		go s.cancelNPCScript(v.live)
	}
}

// Caller retains own persistMu and CID gate; all identity validation precedes
// this mutation. Registry readers never see a replacement online projection
// installed without the matching authoritative visit record.
func (s *Server) installScene(v *sceneVisit, full domain.CharacterSession) {
	v.live.mu.Lock()
	v.live.visit = v
	v.live.session = full
	v.live.mu.Unlock()
	s.mu.Lock()
	if s.scenes == nil {
		s.scenes = map[int64]*sceneVisit{}
	}
	if s.online == nil {
		s.online = map[int64]domain.CharacterSession{}
	}
	s.scenes[full.CharacterID] = v
	s.online[full.CharacterID] = full
	s.mu.Unlock()
}

func offerCriticalDisplay(live *liveCharacter, sink packetSink) error {
	live.criticalDisplayNonce.Store(0)
	if os.Getenv("MAPLEGO_CRITICAL_DISPLAY") != "1" {
		return nil
	}
	var challenge [8]byte
	if _, err := rand.Read(challenge[:]); err != nil {
		return err
	}
	nonce := binary.LittleEndian.Uint64(challenge[:])
	if nonce == 0 {
		return nil
	}
	live.criticalDisplayNonce.Store(nonce)
	return sink.WritePacket(protocol.CriticalDisplayOffer(nonce))
}

// This is the only initial scene installer. Missing durable validation fails
// closed even if a consumed handoff looks locally newer or online is empty.
func (s *Server) publishInitialScene(ctx context.Context, live *liveCharacter, sink packetSink, channel int) error {
	repo, ok := s.repo.(domain.SceneRepository)
	if !ok {
		return domain.ErrForbidden
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	live.mu.Lock()
	snapshot := cloneCombatSession(live.session)
	snapshot.Lease = s.currentLease()
	live.mu.Unlock()
	if snapshot.ChannelID != channel || snapshot.CharacterID <= 0 {
		return domain.ErrStaleOwner
	}
	unlock, err := s.lockScenePublication(snapshot.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	validate, done := context.WithTimeout(ctx, 3*time.Second)
	err = repo.ValidateSceneOwner(validate, snapshot)
	done()
	if err != nil {
		return err
	}
	s.mu.RLock()
	old := s.scenes[snapshot.CharacterID]
	_, onlineWithoutVisit := s.online[snapshot.CharacterID]
	s.mu.RUnlock()
	if old == nil && onlineWithoutVisit {
		return domain.ErrStaleOwner
	}
	if old != nil {
		if sameSceneOwner(old.owner, snapshot) {
			return domain.ErrAlreadyExists
		}
		s.detachScene(old)
	}
	if err = s.restoreJudgementLobby(ctx, &snapshot); err != nil {
		return err
	}
	if err = s.restoreCarnivalLobby(ctx, &snapshot); err != nil {
		return err
	}
	// Fetch fallible content before publishing any new local owner/field packet.
	keymap, err := s.keymapPacket(ctx, snapshot)
	if err != nil {
		return err
	}
	snapshot.Character.PlacePetsAtOwner()
	visit := makeSceneVisit(snapshot, live, sink)
	s.installScene(visit, snapshot)
	if err = sink.WritePacket(protocol.SetField(snapshot.Character, channel)); err != nil {
		return err
	}
	if limit := protocol.LongDamageCap(); limit > 0 {
		if err = sink.WritePacket(protocol.LongDamageModePacket(limit)); err != nil {
			return err
		}
	}
	if err = offerCriticalDisplay(live, sink); err != nil {
		return err
	}
	// Repeated on every map so a switch changed in the admin reaches players
	// who are already connected.
	if err = sink.WritePacket(protocol.ClientUIPacket()); err != nil {
		return err
	}
	if err = sink.WritePacket(protocol.ClientFeesPacket()); err != nil {
		return err
	}
	if err = sink.WritePacket(protocol.ClientParcelPacket()); err != nil {
		return err
	}
	for _, packet := range [][]byte{protocol.EnableCashshop(), protocol.ResetTemporaryStats(), keymap, protocol.EnableActions()} {
		if err = sink.WritePacket(packet); err != nil {
			return err
		}
	}
	if s.maps != nil {
		s.maps.Enter(channel, snapshot.Character.MapID, snapshot.Character, sink)
	}
	if err = showOwnPets(sink, snapshot.Character); err != nil {
		return err
	}
	s.tradeEnter(live, sink)
	if s.drops != nil {
		s.drops.Track(channel, snapshot.Character.MapID, snapshot.CharacterID, sink, snapshot)
	}
	s.sendMerchants(channel, snapshot.Character.MapID, sink)
	if err = s.sendInventoryDrops(ctx, snapshot, sink); err != nil {
		return err
	}
	if s.gameplay != nil {
		if err = s.enterGameplay(channel, live, sink); err != nil {
			return err
		}
	}
	if err = s.enterSkills(ctx, channel, live, sink); err != nil {
		return err
	}
	s.publishStatDetail(live, sink, time.Now(), true)
	if domain.RelogBuffsEnabled() {
		s.restoreSummons(channel, live)
	}
	if err = s.enterBuddies(ctx, live, sink); err != nil {
		return err
	}
	if err = s.enterParty(ctx, live, sink); err != nil {
		return err
	}
	if err = s.enterGuild(ctx, live, sink); err != nil {
		return err
	}
	if err = s.sendAzwanHonor(ctx, snapshot, sink); err != nil {
		return err
	}
	s.enqueueMapEntry(channel, live, sink)
	s.eventEntered(channel, live, sink)
	return nil
}

func (s *Server) leaveScene(live *liveCharacter, sink packetSink) {
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	v := s.sceneFor(live, sink)
	if v == nil {
		return
	}
	unlock, err := s.lockScenePublication(v.owner.CharacterID)
	if err != nil {
		return
	}
	defer unlock()
	s.detachScene(v)
}

// writeScenePackets scopes transitional acknowledgements without retaining the
// scene gate across the database operation which precedes/follows them.
func (s *Server) writeScenePackets(expected *sceneVisit, packets ...[]byte) error {
	if expected == nil {
		return domain.ErrStaleOwner
	}
	unlock, err := s.lockScenePublication(expected.owner.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(expected) {
		return domain.ErrStaleOwner
	}
	for _, packet := range packets {
		if err = expected.sink.WritePacket(packet); err != nil {
			return err
		}
	}
	return nil
}
