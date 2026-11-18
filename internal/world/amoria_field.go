package world

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func (s *Server) amoriaPortal(ctx context.Context, channel int, live *liveCharacter, sink packetSink, p content.Portal) (bool, error) {
	actor := partySession(live)
	mapID := actor.Character.MapID
	var to int
	var portal string
	switch {
	case mapID == content.AmoriaMirror && p.Name >= "go00" && p.Name <= "go02":
		to, portal = mapID, "st00"
	case mapID == content.AmoriaMirror && p.Script == "gendergo":
		to, portal = mapID, "male01"
		if actor.Character.Gender == 1 {
			portal = "female01"
		}
	case mapID == content.AmoriaMirror && (p.Name == "ma01" || p.Name == "fe01"):
		return true, eventPortalNotice(sink, "請從你所在一側的石門進入。")
	case mapID == content.AmoriaRopes && p.Name == "next00":
		to, portal = content.AmoriaPlatforms, "st00"
	case mapID == content.AmoriaPlatforms && p.Name == "next00":
		to, portal = content.AmoriaCodes, "st00"
	case mapID == content.AmoriaCodes && p.Name == "next00":
		return true, eventPortalNotice(sink, "請先向阿摩司交付邱比特愛情密碼。")
	case mapID == content.AmoriaGates && p.Script == "doorgo":
		if len(p.Name) != 7 || !strings.HasPrefix(p.Name, "gt0") || p.Name[3] < '0' || p.Name[3] > '6' || p.Name[4:] != "PCS" {
			return true, sink.WritePacket(protocol.EnableActions())
		}
		to = mapID
	default:
		return false, nil
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.amoria == nil || inst.channel != channel || !time.Now().Before(inst.currentDeadline()) {
		return true, sink.WritePacket(protocol.EnableActions())
	}
	state := inst.amoria
	state.mu.Lock()
	defer state.mu.Unlock()
	allowed := true
	switch {
	case mapID == content.AmoriaMirror && p.Script == "gendergo":
		allowed = state.gateArmed && (actor.Character.Gender == 0 && p.Name == "male00" ||
			actor.Character.Gender == 1 && p.Name == "female00")
	case mapID == content.AmoriaMirror:
		allowed = state.gateArmed && p.Name == fmt.Sprintf("go%02d", state.gate)
	case mapID == content.AmoriaRopes:
		allowed = state.ropeCleared
	case mapID == content.AmoriaPlatforms:
		allowed = state.boxCleared
	case mapID == content.AmoriaGates:
		allowed = s.amoriaDoorState(channel, int(p.Name[3]-'0'))
	}
	if !allowed {
		return true, eventPortalNotice(sink, "這道門目前無法通過。")
	}
	if mapID == content.AmoriaGates {
		field, err := s.gameplay.catalog.Map(mapID)
		if err != nil {
			return true, err
		}
		// PIA is the hidden routing marker. Its v148 link names the opposite
		// side of the gate at floor height. The first PIA is below the map.
		marker, ok := field.Portal(p.Name[:4] + "PIA")
		if !ok || marker.TargetMap != mapID || marker.TargetName != p.Name[:4]+"PIB" {
			return true, domain.ErrNotFound
		}
		portal = marker.TargetName
	}
	return true, s.warpAmoria(ctx, inst, eventOccupant{live, sink}, mapID, to, portal, false)
}

func (s *Server) amoriaDoorState(channel, index int) bool {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[mapKey{channel, content.AmoriaGates}]
	if room == nil {
		return false
	}
	for _, reactor := range room.reactors {
		if reactor.def.TemplateID == 6702001 && reactor.def.Index == index {
			return reactor.state == 4
		}
	}
	return false
}

func (s *Server) amoriaDoorsCleared(channel int) bool {
	g := s.gameplay
	g.mu.Lock()
	defer g.mu.Unlock()
	room := g.rooms[mapKey{channel, content.AmoriaGates}]
	if room == nil || len(room.reactors) != 7 {
		return false
	}
	for _, reactor := range room.reactors {
		if reactor.def.TemplateID != 6702001 || reactor.state != 4 {
			return false
		}
	}
	return true
}

func (s *Server) amoriaBalrog(ctx context.Context, inst *eventInstance, member eventOccupant) (string, error) {
	state := inst.amoria
	const mapID = content.AmoriaBalrog
	if state.boss == 0 {
		if err := s.spawnReactorMonster(mapKey{inst.channel, mapID}, content.Reactor{X: 674, Y: 511}, content.AmoriaFirstForm, 1); err != nil {
			return "", err
		}
		state.boss = content.AmoriaFirstForm
		if err := s.warpAmoria(ctx, inst, member, mapID, mapID, "st01", true); err != nil {
			return "", err
		}
		return "", nil
	}
	if state.boss != -1 {
		return "打倒所有巴洛古形態，再把牠掉落的獠牙交給我。", nil
	}
	if !state.fangTaken {
		used, err := s.consumeAmoriaItem(ctx, inst, member, mapID, content.AmoriaFang, 1, "fang")
		if err != nil {
			return "", err
		}
		if !used {
			return "請把變種巴洛古掉落的獠牙交給我。", nil
		}
		state.fangTaken = true
	}
	if err := s.amoriaClear(ctx, inst, mapID, 11000); err != nil {
		return "", err
	}
	state.bonusDeadline.Store(time.Now().Add(time.Minute).UnixNano())
	return "", s.warpAmoria(ctx, inst, member, mapID, content.AmoriaBonus, "st00", true)
}

func (s *Server) amoriaMobDefeated(key mapKey, actor int64, target domain.CombatTarget) {
	if key.mapID != content.AmoriaBalrog {
		return
	}
	inst := s.eventOf(actor)
	if inst == nil || inst.amoria == nil || inst.channel != key.channel {
		return
	}
	state := inst.amoria
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.boss != target.MobID {
		return
	}
	if target.MobID == content.AmoriaFinalForm {
		state.boss = -1
		return
	}
	mob, err := s.gameplay.catalog.Mob(target.MobID)
	if err != nil || len(mob.Revive) != 1 {
		return
	}
	next := mob.Revive[0]
	if err = s.spawnReactorMonster(key, content.Reactor{X: target.X - 20, Y: target.Y}, next, 1); err == nil {
		state.boss = next
	}
}

const amoriaFairyX, amoriaFairyY = 103, 783

func (s *Server) placeAmoriaFairyLoot(t *combatTicket) {
	if t.key.mapID != content.AmoriaMirror {
		return
	}
	field, err := s.gameplay.catalog.Map(content.AmoriaMirror)
	if err != nil {
		return
	}
	hall, ok := groundBelow(field, amoriaFairyX, amoriaFairyY)
	if !ok {
		return
	}
	for i := range t.request.Targets {
		target := &t.request.Targets[i]
		if !target.Defeated || target.MobID != content.AmoriaFairy {
			continue
		}
		// The fairy can fly above or below the shared hall. Keep its hammer
		// on that floor even if the sprite leaves the hall's footholds.
		y, onFloor := groundBelow(field, target.X, amoriaFairyY)
		if !onFloor || abs(y-hall) > 30 {
			target.X, y = amoriaFairyX, hall
		}
		target.Y = y
	}
}

type amoriaChestDrop struct {
	item, denominator int
}

// The Taiwanese Legacy bonus crates share these independent one-in-N rolls.
// Their three older ETC templates 4031543–4031545 are absent from v148.
var amoriaChestDrops = [...]amoriaChestDrop{
	{1032043, 25}, {1032044, 25}, {1032045, 25}, {1102099, 30}, {1102100, 30},
	{4021007, 20}, {4021008, 20}, {4020007, 5}, {4020007, 5}, {4011006, 20},
	{2040759, 50}, {2041035, 20}, {2041037, 20}, {2041039, 20}, {2041041, 20}, {2040005, 20},
	{2000005, 5}, {2022179, 15}, {2022180, 5}, {2022181, 10}, {2022182, 10},
	{2020020, 5}, {2000005, 5}, {2020010, 5}, {2020013, 5}, {2022015, 5}, {3010011, 50}, {3012005, 100},
}

func (s *Server) hitAmoriaReactor(ctx context.Context, channel int, live *liveCharacter, sink packetSink, oid int32) (bool, error) {
	actor := partySession(live)
	mapID := actor.Character.MapID
	if mapID != content.AmoriaGates && mapID != content.AmoriaBonus {
		return false, nil
	}
	inst, visit := s.eventOf(actor.CharacterID), s.sceneFor(live, sink)
	if inst == nil || inst.amoria == nil || visit == nil || !time.Now().Before(inst.currentDeadline()) ||
		actor.Character.HP <= 0 || s.tradeActive(live) {
		return true, nil
	}
	g := s.gameplay
	g.mu.Lock()
	key := mapKey{channel, mapID}
	room := g.rooms[key]
	if room == nil || room.members[actor.CharacterID] == nil || room.members[actor.CharacterID].visit != visit {
		g.mu.Unlock()
		return true, nil
	}
	r := room.reactors[oid]
	if r == nil || !content.AmoriaReactor(mapID, r.def.TemplateID) || r.gone || r.finishing || r.state == 4 ||
		abs(actor.Character.X-r.def.X) > 145 || abs(actor.Character.Y-r.def.Y) > 100 ||
		time.Since(r.lastHit) < 170*time.Millisecond {
		g.mu.Unlock()
		return true, nil
	}
	r.lastHit = time.Now()
	r.state++
	def, state := r.def, r.state
	r.finishing = state == 4 && mapID == content.AmoriaBonus
	g.mu.Unlock()
	if state < 4 || mapID == content.AmoriaGates {
		s.broadcastInventoryPacket(channel, mapID, protocol.ReactorState(oid, state, def.X, def.Y))
		return true, nil
	}
	err := s.applyEventExchange(ctx, eventOccupant{live, sink}, fmt.Sprintf("amoria-chest-%s-%d", inst.id, oid), func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != mapID || !s.sceneCurrent(visit) || s.eventOf(current.CharacterID) != inst {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		var result domain.ScriptExchange
		for _, row := range amoriaChestDrops {
			if rand.IntN(row.denominator) == 0 {
				x, y := s.gatherDropPoint(mapID, def.X, def.Y, 25*len(result.Drops))
				result.Drops = append(result.Drops, domain.ScriptDrop{TemplateID: row.item, Quantity: 1, X: x, Y: y})
			}
		}
		return result, nil
	})
	g.mu.Lock()
	r.finishing = false
	if err != nil {
		r.state--
		g.mu.Unlock()
		return true, err
	}
	r.gone = true
	g.mu.Unlock()
	s.broadcastInventoryPacket(channel, mapID, protocol.ReactorState(oid, state, def.X, def.Y))
	s.broadcastInventoryPacket(channel, mapID, protocol.ReactorLeave(oid, state, def.X, def.Y))
	return true, nil
}
