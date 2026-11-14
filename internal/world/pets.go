package world

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

// Pet commands follow the item-use contract: persistMu serializes the durable
// command, and the scene gate orders the self/peer packets of one commit.
// Movement and chat are field-local and never perform SQL.

func (s *Server) petRepo() (domain.PetRepository, bool) {
	repo, ok := s.repo.(domain.PetRepository)
	return repo, ok
}

func (s *Server) petSession(live *liveCharacter) domain.CharacterSession {
	live.mu.Lock()
	defer live.mu.Unlock()
	live.session.Lease = s.currentLease()
	return live.session
}

func (s *Server) handlePetPacket(ctx context.Context, live *liveCharacter, sink packetSink, op uint16, r *protocol.Reader) (err error) {
	defer func() {
		if err != nil {
			slog.Warn("pet_packet_closed_connection", "opcode", fmt.Sprintf("0x%x", op), "error", err)
		}
	}()
	switch op {
	case protocol.RecvSpawnPet:
		return s.handleSpawnPet(ctx, live, sink, r)
	case protocol.RecvPetFood:
		return s.handlePetFood(ctx, live, sink, r)
	case protocol.RecvPetCommand:
		return s.handlePetCommand(ctx, live, sink, r)
	case protocol.RecvMovePet:
		return s.handleMovePet(live, sink, r)
	case protocol.RecvPetChat:
		return s.handlePetChat(live, sink, r)
	}
	return nil
}

func petFailure(sink packetSink, err error) error {
	if errors.Is(err, domain.ErrStaleOwner) {
		return err
	}
	return sink.WritePacket(protocol.EnableActions())
}

func (s *Server) handleSpawnPet(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadSpawnPet(r)
	if err != nil {
		return err
	}
	repo, ok := s.petRepo()
	if !ok || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := s.petSession(live)
	if session.Character.MapID == domain.FreeMarketMapID {
		if err = sink.WritePacket(protocol.Notice("自由市場無法使用寵物。")); err != nil {
			return err
		}
		return sink.WritePacket(protocol.EnableActions())
	}
	call, done := context.WithTimeout(ctx, 3*time.Second)
	result, err := repo.SummonPet(call, domain.PetSummonRequest{Session: session, OperationID: "pet-summon-" + platform.NewID(), Slot: req.Slot, Lead: req.Lead})
	done()
	if err != nil {
		return petFailure(sink, err)
	}
	if err = s.publishPets(live, sink, result, nil); err != nil {
		return err
	}
	return sink.WritePacket(protocol.EnableActions())
}

func (s *Server) handlePetFood(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadPetFood(r)
	if err != nil {
		return err
	}
	repo, ok := s.petRepo()
	if !ok || s.tradeActive(live) {
		return sink.WritePacket(protocol.EnableActions())
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := s.petSession(live)
	if len(session.Character.SummonedPets()) == 0 {
		return sink.WritePacket(protocol.EnableActions())
	}
	call, done := context.WithTimeout(ctx, 3*time.Second)
	result, err := repo.FeedPet(call, domain.PetFeedRequest{Session: session, OperationID: "pet-feed-" + platform.NewID(), ClientTick: req.ClientTick, Slot: req.Slot, TemplateID: req.TemplateID, GainCloseness: rand.IntN(101) > 50})
	done()
	if err != nil {
		return petFailure(sink, err)
	}
	quantity := 0
	if result.Character != nil {
		for _, item := range result.Character.Items {
			if item.InvType == 2 && item.Slot == req.Slot {
				quantity = item.Quantity
			}
		}
	}
	err = s.publishPets(live, sink, result, func(owner int64, ch domain.PetChange) [][]byte {
		return [][]byte{protocol.PetCommandResult(owner, ch.After.Index(), 1, result.Success, true)}
	})
	if err != nil {
		return err
	}
	if !result.Success {
		if err = sink.WritePacket(protocol.Notice("您的寵物的飢餓感是滿值，如果繼續使用將會有50%的幾率減少1點親密度。")); err != nil {
			return err
		}
	}
	if err = sink.WritePacket(protocol.UpdateUseSlot(req.Slot, quantity)); err != nil {
		return err
	}
	return sink.WritePacket(protocol.EnableActions())
}

func (s *Server) handlePetCommand(ctx context.Context, live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadPetCommand(r)
	if err != nil {
		return err
	}
	session := s.petSession(live)
	pet, ok := session.Character.PetAt(req.Index)
	if !ok {
		return sink.WritePacket(protocol.EnableActions())
	}
	definition, err := content.PetDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), pet.TemplateID)
	if err != nil || req.Command < 0 || req.Command >= len(definition.Commands) {
		return sink.WritePacket(protocol.EnableActions())
	}
	command := definition.Commands[req.Command]
	success := rand.IntN(99) <= command.Probability
	result := domain.PetResult{Success: success}
	if success && command.Increase > 0 && pet.Closeness < domain.PetMaxCloseness {
		repo, ok := s.petRepo()
		if !ok {
			return sink.WritePacket(protocol.EnableActions())
		}
		live.persistMu.Lock()
		defer live.persistMu.Unlock()
		session = s.petSession(live)
		call, done := context.WithTimeout(ctx, 3*time.Second)
		result, err = repo.CommandPet(call, domain.PetCommandRequest{Session: session, OperationID: "pet-command-" + platform.NewID(), Index: req.Index, Increase: command.Increase})
		done()
		if err != nil {
			return petFailure(sink, err)
		}
	}
	response := protocol.PetCommandResult(session.CharacterID, req.Index, req.Command, success, false)
	return s.publishPets(live, sink, result, nil, response)
}

func (s *Server) handleMovePet(live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	move, err := protocol.ReadPetMove(r)
	if err != nil {
		return err
	}
	v := s.sceneFor(live, sink)
	if v == nil || s.maps == nil {
		return nil
	}
	unlock, err := s.lockScenePublication(v.owner.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(v) {
		return nil
	}
	live.mu.Lock()
	if _, ok := live.session.Character.PetAt(move.Index); !ok {
		live.mu.Unlock()
		return nil
	}
	if move.Changed {
		live.session.Character.MovePet(move.Index, move.X, move.Y)
	}
	snapshot := live.session
	live.mu.Unlock()
	s.updateOnlineOwned(snapshot, snapshot)
	s.maps.publishPets(v, snapshot.Character, false, protocol.MovePet(snapshot.CharacterID, move.Index, move.StartX, move.StartY, move.Fragments))
	return nil
}

func (s *Server) handlePetChat(live *liveCharacter, sink packetSink, r *protocol.Reader) error {
	req, err := protocol.ReadPetChat(r)
	if err != nil {
		return err
	}
	live.mu.Lock()
	_, ok := live.session.Character.PetAt(req.Index)
	allowed := ok && live.chat.take(time.Now())
	id := live.session.CharacterID
	live.mu.Unlock()
	if !allowed || s.maps == nil {
		return nil
	}
	v := s.sceneFor(live, sink)
	if v == nil {
		return nil
	}
	unlock, err := s.lockScenePublication(id)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(v) {
		return nil
	}
	s.maps.publishPets(v, domain.Character{}, true, protocol.PetChat(id, req.Index, req.Act, req.Text))
	return nil
}

// tickPets runs from the checkpoint loop: expiry is checked every pass and
// hunger once per Legacy interval while at least one pet is summoned.
func (s *Server) tickPets(ctx context.Context, live *liveCharacter, sink packetSink, now time.Time) error {
	repo, ok := s.petRepo()
	if !ok {
		return nil
	}
	live.mu.Lock()
	pets := live.session.Character.SummonedPets()
	if len(pets) == 0 {
		live.petHungerAt = time.Time{}
		live.mu.Unlock()
		return nil
	}
	if live.petHungerAt.IsZero() {
		live.petHungerAt = now.Add(domain.PetHungerInterval)
	}
	due := !now.Before(live.petHungerAt)
	if due {
		live.petHungerAt = now.Add(domain.PetHungerInterval)
	}
	live.mu.Unlock()
	expired := false
	hunger := map[int64]int{}
	for _, p := range pets {
		expired = expired || p.Expired(now)
		if due {
			definition, err := content.PetDefinition(os.Getenv("MAPLEGO_WZ_ROOT"), p.TemplateID)
			if err != nil {
				definition.Hunger = 1
			}
			hunger[p.CashID] = definition.Hunger
		}
	}
	if !due && !expired {
		return nil
	}
	live.persistMu.Lock()
	defer live.persistMu.Unlock()
	session := s.petSession(live)
	call, done := context.WithTimeout(ctx, 3*time.Second)
	result, err := repo.DecayPets(call, domain.PetDecayRequest{Session: session, OperationID: "pet-decay-" + platform.NewID(), Hunger: hunger, Now: now})
	done()
	if err != nil || len(result.Changes) == 0 {
		return err
	}
	return s.publishPets(live, sink, result, nil)
}

// publishPets installs committed pet state in the live aggregate and the map
// look, then writes self updates, peer removals/spawns and extra packets.
func (s *Server) publishPets(live *liveCharacter, sink packetSink, result domain.PetResult, perChange func(int64, domain.PetChange) [][]byte, extra ...[]byte) error {
	v := s.sceneFor(live, sink)
	if v == nil {
		return domain.ErrStaleOwner
	}
	unlock, err := s.lockScenePublication(v.owner.CharacterID)
	if err != nil {
		return err
	}
	defer unlock()
	if !s.sceneCurrent(v) {
		return domain.ErrStaleOwner
	}
	live.mu.Lock()
	if result.Character != nil {
		live.session.Character.Items = result.Character.Items
		live.session.Character.Revision = result.Character.Revision
	}
	c := &live.session.Character
	for _, ch := range result.Changes {
		next := ch.After
		c.ReplacePet(next)
		if !ch.Before.Summoned() && next.Summoned() {
			c.MovePet(next.Index(), c.X, c.Y-12)
		}
	}
	snapshot := live.session
	live.mu.Unlock()
	s.updateOnlineOwned(snapshot, snapshot)
	owner := snapshot.CharacterID
	var self, shared [][]byte
	for _, ch := range result.Changes {
		after, _ := pickPet(snapshot.Character, ch.After.CashID)
		self = append(self, protocol.UpdatePet(after))
		if ch.Before.Summoned() && (!after.Summoned() || ch.Before.Index() != after.Index()) {
			shared = append(shared, protocol.RemovePet(owner, ch.Before.Index(), ch.Reason))
		}
	}
	for _, ch := range result.Changes {
		after, _ := pickPet(snapshot.Character, ch.After.CashID)
		if after.Summoned() && (!ch.Before.Summoned() || ch.Before.Index() != after.Index()) {
			shared = append(shared, protocol.SpawnPet(owner, after))
			self = append(self, protocol.PetExcluded(owner, after))
		}
		if ch.LevelUp && after.Summoned() {
			self = append(self, protocol.PetLevelUpSelf(after.Index()))
			shared = append(shared, protocol.PetLevelUpForeign(owner, after.Index()))
		}
		if perChange != nil {
			shared = append(shared, perChange(owner, ch)...)
		}
	}
	shared = append(shared, extra...)
	for _, packet := range self {
		if packet != nil {
			if err = sink.WritePacket(packet); err != nil {
				return err
			}
		}
	}
	if s.maps != nil {
		s.maps.publishPets(v, snapshot.Character, true, shared...)
		return nil
	}
	for _, packet := range shared {
		if packet != nil {
			if err = sink.WritePacket(packet); err != nil {
				return err
			}
		}
	}
	return nil
}

func pickPet(c domain.Character, cashID int64) (domain.Pet, bool) {
	for _, p := range c.Pets {
		if p.CashID == cashID {
			return p, true
		}
	}
	return domain.Pet{}, false
}

// showOwnPets is the field-entry self announcement (Legacy MapleMap.addPlayer).
func showOwnPets(sink packetSink, c domain.Character) error {
	for _, p := range c.SummonedPets() {
		for _, packet := range [][]byte{protocol.UpdatePet(p), protocol.ShowOwnPet(c.ID, p), protocol.PetExcluded(c.ID, p)} {
			if packet == nil {
				continue
			}
			if err := sink.WritePacket(packet); err != nil {
				return err
			}
		}
	}
	return nil
}

// publishPets updates the member's look when look.ID is set, then writes the
// packets to visible peers and, when includeSelf, to the owner.
func (m *mapSpace) publishPets(v *sceneVisit, look domain.Character, includeSelf bool, packets ...[]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owner := v.owner
	room := m.rooms[mapKey{channel: owner.ChannelID, mapID: owner.Character.MapID}]
	self := room[owner.CharacterID]
	if self == nil || !sameDropSink(self.sink, v.sink) {
		return
	}
	if look.ID == owner.CharacterID {
		self.look.Pets = look.Pets
	}
	for _, packet := range packets {
		if packet == nil {
			continue
		}
		if includeSelf {
			_ = self.sink.WritePacket(packet)
		}
		for id := range self.visible {
			if peer := room[id]; peer != nil {
				_ = peer.sink.WritePacket(packet)
			}
		}
	}
}

// petAdmin is the loopback operator/QA endpoint: GET lists a character's pets,
// POST grants one to an offline character. It never edits a live session.
func (s *Server) petAdmin(w http.ResponseWriter, r *http.Request) {
	if len(s.cfg.RPCToken) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.RPCToken)) != 1 {
		debugJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	repo, ok := s.petRepo()
	if !ok {
		debugJSON(w, 503, map[string]string{"error": "pets_unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var err error
	var out any
	switch r.Method {
	case http.MethodGet:
		account, e1 := strconv.ParseInt(r.URL.Query().Get("account_id"), 10, 64)
		character, e2 := strconv.ParseInt(r.URL.Query().Get("character_id"), 10, 64)
		if e1 != nil || e2 != nil || account < 1 || character < 1 {
			err = domain.ErrInvalidInput
			break
		}
		var pets []domain.Pet
		pets, err = repo.ListPets(ctx, account, character)
		out = map[string]any{"pets": pets}
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var req domain.PetGrantRequest
		if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
			err = domain.ErrInvalidInput
			break
		}
		out, err = repo.GrantPet(ctx, req)
	default:
		w.Header().Set("Allow", "GET, POST")
		debugJSON(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	if err != nil {
		status, message := 500, "pet_admin_failed"
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			status, message = 400, "invalid_request"
		case errors.Is(err, domain.ErrNotFound):
			status, message = 404, "not_found"
		case errors.Is(err, domain.ErrAlreadyExists):
			status, message = 409, "account_must_be_offline"
		case errors.Is(err, domain.ErrInsufficient):
			status, message = 409, "cash_inventory_full"
		}
		debugJSON(w, status, map[string]string{"error": message})
		return
	}
	debugJSON(w, 200, out)
}
