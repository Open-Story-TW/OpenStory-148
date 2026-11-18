package world

import (
	"context"
	"errors"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type amoriaState struct {
	mu            sync.Mutex
	gate          int
	gateArmed     bool
	fairy         bool
	mirrorCleared bool
	ropes         [3]int
	ropeCleared   bool
	ropeFailures  int
	platforms     uint16
	boxCleared    bool
	codesTaken    bool
	codesCleared  bool
	gatesCleared  bool
	boss          int
	fangTaken     bool
	bonusDeadline atomic.Int64
}

func newAmoriaState() *amoriaState {
	p := &amoriaState{gate: rand.IntN(3)}
	// Two separators in seven positions distribute five players over the
	// three ropes. Each six-player group gets a new solution.
	a, b := rand.IntN(7), rand.IntN(6)
	if b >= a {
		b++
	}
	a, b = min(a, b), max(a, b)
	p.ropes = [3]int{a, b - a - 1, 6 - b}
	for _, i := range rand.Perm(9)[:5] {
		p.platforms |= 1 << i
	}
	return p
}

func (s *Server) amoriaStage(ctx context.Context, actor domain.CharacterSession, member eventOccupant, npcID int) (string, error) {
	mapID := actor.Character.MapID
	if mapID == content.AmoriaExit && npcID == content.AmoriaGlitterman {
		field, err := s.gameplay.catalog.Map(content.AmoriaApproach)
		if err != nil {
			return "", err
		}
		entry, ok := field.Spawn()
		if !ok {
			return "", domain.ErrNotFound
		}
		return "", s.warpGameplay(ctx, actor.ChannelID, member.live, member.sink, field.ID, entry)
	}
	inst := s.eventOf(actor.CharacterID)
	if inst == nil || inst.amoria == nil || !time.Now().Before(inst.currentDeadline()) {
		return "目前沒有進行中的結婚組隊任務。", nil
	}
	state := inst.amoria
	state.mu.Lock()
	defer state.mu.Unlock()
	if !sameSceneOwner(actor, partySession(member.live)) || partySession(member.live).Character.MapID != mapID ||
		s.eventOf(actor.CharacterID) != inst {
		return "", domain.ErrStaleOwner
	}
	if mapID == content.AmoriaBonus && npcID == content.AmoriaBonusNPC {
		return "打開寶箱後記得拾取獎品。時間到會自動送你們出去。", nil
	}
	leader := s.partySnapshotOf(actor.CharacterID).LeaderID == actor.CharacterID
	if mapID == content.AmoriaMirror && npcID == content.AmoriaGuide && state.mirrorCleared {
		return "", s.warpAmoria(ctx, inst, member, mapID, content.AmoriaRopes, "st00", false)
	}
	if npcID == content.AmoriaGuard {
		// A member can retry the same field's NPC if their warp did not commit.
		switch {
		case mapID == content.AmoriaCodes && state.codesCleared:
			return "", s.warpAmoria(ctx, inst, member, mapID, content.AmoriaGates, "st00", false)
		case mapID == content.AmoriaGates && state.gatesCleared:
			return "", s.warpAmoria(ctx, inst, member, mapID, content.AmoriaBalrog, "st00", false)
		case mapID == content.AmoriaBalrog && state.bonusDeadline.Load() > time.Now().UnixNano():
			return "", s.warpAmoria(ctx, inst, member, mapID, content.AmoriaBonus, "st00", false)
		}
	}
	if !leader {
		return "請由隊長與我對話。", nil
	}
	if len(s.activeEventMembers(inst, mapID)) != inst.definition.PartyMin {
		return "請讓六名隊員都到這一關集合。", nil
	}
	switch {
	case mapID == content.AmoriaMirror && npcID == content.AmoriaGuide:
		if !state.gateArmed {
			state.gateArmed = true
			for i := range 3 {
				s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldObjectEffect(fmt.Sprintf("gate%d", i)))
			}
		}
		return "入口已開啟。三道門中有一道通往魔境。", nil
	case mapID == content.AmoriaMirror && npcID == content.AmoriaGlitterman:
		if !state.gateArmed || s.mapMonsterCount(inst.channel, mapID) != 0 {
			return "請從男女各自的入口進去，打倒兩邊的怪物。", nil
		}
		if !state.fairy {
			if err := s.spawnReactorMonster(mapKey{inst.channel, mapID}, content.Reactor{X: amoriaFairyX, Y: amoriaFairyY}, content.AmoriaFairy, 1); err != nil {
				return "", err
			}
			state.fairy = true
			s.broadcastInventoryPacket(inst.channel, mapID, protocol.PortalTrainingMessage("魔法蝴蝶出現了！"))
			return "打倒魔法蝴蝶，用牠掉落的翅膀鎚打破魔鏡。", nil
		}
		if state.mirrorCleared {
			return "請從魔鏡旁返回上層，找阿摩司前往下一關。", nil
		}
		used, err := s.consumeAmoriaItem(ctx, inst, member, mapID, content.AmoriaShard, 1, "mirror")
		if err != nil {
			return "", err
		}
		if !used {
			return "請從魔鏡撿起碎片，再交給我。", nil
		}
		if err := s.amoriaClear(ctx, inst, mapID, 2000); err != nil {
			return "", err
		}
		state.mirrorCleared = true
		return "魔鏡打破了。回上層找阿摩司前往下一關。", nil
	case mapID == content.AmoriaRopes && npcID == content.AmoriaGuide:
		return s.checkAmoriaPuzzle(ctx, inst, state, mapID)
	case mapID == content.AmoriaPlatforms && npcID == content.AmoriaGuide:
		return s.checkAmoriaPuzzle(ctx, inst, state, mapID)
	case mapID == content.AmoriaCodes && npcID == content.AmoriaGuard:
		if !state.codesTaken {
			used, err := s.consumeAmoriaItem(ctx, inst, member, mapID, content.AmoriaCode, 50, "codes")
			if err != nil {
				return "", err
			}
			if !used {
				return "請把 50 個邱比特愛情密碼交給我。", nil
			}
			state.codesTaken = true
		}
		if err := s.amoriaClear(ctx, inst, mapID, 8000); err != nil {
			return "", err
		}
		state.codesCleared = true
		return "", s.warpAmoria(ctx, inst, member, mapID, content.AmoriaGates, "st00", true)
	case mapID == content.AmoriaGates && npcID == content.AmoriaGuard:
		if !s.amoriaDoorsCleared(inst.channel) {
			return "請依序打開七道門，讓全隊通過。", nil
		}
		field, err := s.gameplay.catalog.Map(mapID)
		if err != nil {
			return "", err
		}
		for _, m := range s.activeEventMembers(inst, mapID) {
			c := partySession(m.live).Character
			if len(field.Areas) == 0 || !field.Areas[0].Contains(c.X, c.Y) {
				return "請在終點集合所有隊員。", nil
			}
		}
		if err := s.amoriaClear(ctx, inst, mapID, 10000); err != nil {
			return "", err
		}
		state.gatesCleared = true
		return "", s.warpAmoria(ctx, inst, member, mapID, content.AmoriaBalrog, "st00", true)
	case mapID == content.AmoriaBalrog && npcID == content.AmoriaGuard:
		return s.amoriaBalrog(ctx, inst, member)
	}
	return "請完成眼前的挑戰。", nil
}

func (s *Server) amoriaClear(ctx context.Context, inst *eventInstance, mapID int, exp int64) error {
	if err := s.awardEventStageEXP(ctx, inst, mapID, exp); err != nil {
		return err
	}
	effect := eventEffects["clear"]
	s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldScreenEffect(effect[0]))
	s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldSoundEffect(effect[1]))
	return nil
}

func (s *Server) warpAmoria(ctx context.Context, inst *eventInstance, member eventOccupant, from, to int, portal string, all bool) error {
	field, err := s.gameplay.catalog.Map(to)
	if err != nil {
		return err
	}
	entry, ok := field.Portal(portal)
	if !ok {
		return domain.ErrNotFound
	}
	move := func(player eventOccupant) error {
		s.closeEconomyDialog(player.live)
		if err := s.warpEventMember(ctx, inst, from, player, to, entry); err != nil {
			return err
		}
		// Release native exclusive requests retained across the field change.
		return player.sink.WritePacket(protocol.EnableActions())
	}
	if all {
		// Cancelling a conversation here can lock the current NPC script or a
		// peer script waiting for this stage. Both revalidate their field on use.
		for _, peer := range s.activeEventMembers(inst, from) {
			if peer.live != member.live {
				_ = move(peer)
			}
		}
	}
	return move(member)
}

func (s *Server) consumeAmoriaItem(ctx context.Context, inst *eventInstance, member eventOccupant, mapID, itemID, count int, key string) (bool, error) {
	err := s.applyEventExchange(ctx, member, "amoria-"+key+"-"+inst.id, func(current domain.CharacterSession) (domain.ScriptExchange, error) {
		if current.Character.MapID != mapID || s.eventOf(current.CharacterID) != inst {
			return domain.ScriptExchange{}, domain.ErrStaleOwner
		}
		return domain.ScriptExchange{Items: []domain.ScriptItemDelta{{TemplateID: itemID, Quantity: -count}}}, nil
	})
	if errors.Is(err, domain.ErrInsufficient) {
		return false, nil
	}
	return err == nil, err
}

func (s *Server) checkAmoriaPuzzle(ctx context.Context, inst *eventInstance, state *amoriaState, mapID int) (string, error) {
	rope := mapID == content.AmoriaRopes
	if rope && state.ropeCleared || !rope && state.boxCleared {
		return "前往右方的傳送點。", nil
	}
	field, err := s.gameplay.catalog.Map(mapID)
	if err != nil {
		return "", err
	}
	if rope && len(field.Areas) != 3 || !rope && len(field.Areas) != 9 {
		return "", domain.ErrNotFound
	}
	var counts [9]int
	for _, m := range s.activeEventMembers(inst, mapID) {
		c := partySession(m.live).Character
		for i, area := range field.Areas {
			if area.Contains(c.X, c.Y) {
				counts[i]++
				break
			}
		}
	}
	if rope && counts[0]+counts[1]+counts[2] != 5 {
		return "請讓五名隊員爬上太陽、星星和月亮下方的繩索。", nil
	}
	mask, occupied := uint16(0), 0
	for i, count := range counts {
		if count == 1 {
			mask |= 1 << i
			occupied++
		}
	}
	if !rope && occupied != 5 {
		return "請讓五名隊員分別站在五個不同的台階。", nil
	}
	if rope && [3]int{counts[0], counts[1], counts[2]} == state.ropes || !rope && mask == state.platforms {
		exp := int64(6000)
		if rope {
			exp = 4000
		}
		if err := s.amoriaClear(ctx, inst, mapID, exp); err != nil {
			return "", err
		}
		state.ropeCleared = state.ropeCleared || rope
		state.boxCleared = state.boxCleared || !rope
		s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldObjectEffect("gate"))
		return "配置正確。右方的門開啟了。", nil
	}
	failure := eventEffects["wrong"]
	s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldScreenEffect(failure[0]))
	s.broadcastInventoryPacket(inst.channel, mapID, protocol.FieldSoundEffect(failure[1]))
	if rope {
		state.ropeFailures++
		if state.ropeFailures%7 == 0 {
			_ = s.spawnReactorMonster(mapKey{inst.channel, mapID}, content.Reactor{X: 357, Y: -847}, 9400523, 1)
		}
		matching := 0
		for i, want := range state.ropes {
			if counts[i] == want {
				matching++
			}
		}
		return fmt.Sprintf("有 %d 條繩索的人數正確。", matching), nil
	}
	matching := bits.OnesCount16(mask & state.platforms)
	if matching != 0 {
		_ = s.spawnReactorMonster(mapKey{inst.channel, mapID}, content.Reactor{X: 1663, Y: 196}, 9400519+rand.IntN(4), matching)
	}
	return fmt.Sprintf("有 %d 個台階正確，請調整你們的位置。", matching), nil
}
