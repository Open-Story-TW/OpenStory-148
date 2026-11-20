package world

import (
	"context"
	"log/slog"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type revivalTransition struct {
	fromMap int
	hp      int // Nonzero for an event-specific revival.
}

// clearDeathBuffs requires live.persistMu. It commits death cancellation before
// a subsequent return can restore HP, so a rapid confirmation cannot preserve
// a buff between the incoming hit and the periodic buff worker.
func (s *Server) clearDeathBuffs(ctx context.Context, live *liveCharacter, sink packetSink, expected ...*sceneVisit) error {
	session := s.skillSession(live)
	if session.Character.HP != 0 || len(session.Character.SkillBuffs) == 0 {
		return nil
	}
	repo, ok := s.repo.(domain.SkillRepository)
	if !ok {
		return domain.ErrForbidden
	}
	step, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	r, err := repo.TickSkillBuffs(step, session)
	if err != nil {
		return err
	}
	// A death-cancellation transaction can finish after a replacement claim.
	// The transition caller supplies its pinned visit: do not mutate the old
	// aggregate or emit even a cancellation packet if that visit was removed.
	if len(expected) > 0 {
		if expected[0] == nil {
			return domain.ErrStaleOwner
		}
		unlock, e := s.lockScenePublication(session.CharacterID)
		if e != nil {
			return e
		}
		defer unlock()
		if !s.sceneCurrent(expected[0]) {
			return domain.ErrStaleOwner
		}
	}
	snap := s.mergeSkillResult(live, r)
	s.publishSkillTick(snap, sink, r, session.Character.HP, session.Character.MP)
	if snap.Character.HP != 0 || len(snap.Character.SkillBuffs) != 0 {
		return domain.ErrForbidden
	}
	return nil
}

// reviveGameplay accepts only the native ordinary death-dialog choice. The
// client target map is merely a request marker; WZ chooses the actual return.
// Wheel consumption and event-specific revival require their own transactions.
func (s *Server) reviveGameplay(ctx context.Context, channel int, live *liveCharacter, sink packetSink, req protocol.PortalRequest) error {
	live.mu.Lock()
	c := live.session.Character
	live.mu.Unlock()
	reject := func(reason string) error {
		wheel := -1
		if req.Revival != nil {
			wheel = int(req.Revival.WheelType)
		}
		slog.Info("revival_rejected", "character", c.ID, "map", c.MapID, "hp", c.HP, "reason", reason, "wheel", wheel, "target", req.TargetMap, "portal", req.Name)
		return sink.WritePacket(protocol.UpdateHPMP(c.HP, c.MP))
	}
	switch {
	case req.Revival == nil:
		return reject("not_revival")
	case req.Revival.WheelType != 0:
		return reject("wheel")
	case req.TargetMap != 0 || req.Name != "":
		return reject("target")
	case c.HP != 0 || c.MaxHP < 1:
		return reject("alive")
	}
	if content.CarnivalArenaMode(c.MapID) != 0 {
		return s.carnivalRevive(ctx, live, sink, partySession(live))
	}
	current, err := s.gameplay.catalog.Map(c.MapID)
	if err != nil || current.ReturnMap == 999999999 {
		return reject("no_return_map")
	}
	if inst := s.eventOf(c.ID); inst != nil && inst.definition.RevivalHP > 0 && inst.definition.Stage(c.MapID) {
		entry, ok := current.Spawn()
		if !ok {
			return reject("no_spawn")
		}
		s.cancelNPCScript(live)
		s.closeEconomyDialog(live)
		return s.warpGameplayTransition(ctx, channel, live, sink, c.MapID, entry, &revivalTransition{fromMap: c.MapID, hp: inst.definition.RevivalHP})
	}
	to, err := s.gameplay.catalog.Map(current.ReturnMap)
	if err != nil {
		return reject("return_map_missing")
	}
	entry, ok := to.Spawn()
	if !ok {
		return reject("no_spawn")
	}
	slog.Info("revival_accepted", "character", c.ID, "from", c.MapID, "to", to.ID)
	s.cancelNPCScript(live)
	s.closeEconomyDialog(live)
	if err = s.warpGameplayTransition(ctx, channel, live, sink, to.ID, entry, &revivalTransition{fromMap: c.MapID}); err != nil {
		slog.Warn("revival_transition_failed", "character", c.ID, "from", c.MapID, "to", to.ID, "error", err)
		return err
	}
	return nil
}
