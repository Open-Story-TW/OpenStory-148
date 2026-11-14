package world

import (
	"context"
	"errors"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

func TestUnknownItemUseDoesNotReassertOldVitalsOrConsumeRecoveryBudget(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*Server, *liveCharacter, *gameplayTestSink, time.Time) error
	}{
		{"damage", func(s *Server, live *liveCharacter, sink *gameplayTestSink, _ time.Time) error {
			return s.takeDamage(context.Background(), live, sink, protocol.PlayerDamage{Damage: 1})
		}},
		{"cancel", func(s *Server, live *liveCharacter, sink *gameplayTestSink, _ time.Time) error {
			return s.cancelSkill(context.Background(), live, sink, 1002)
		}},
		{"natural", func(s *Server, live *liveCharacter, sink *gameplayTestSink, now time.Time) error {
			return s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvRegeneration, 10, 10, 3), now.Add(10*time.Second))
		}},
		{"potential", func(s *Server, live *liveCharacter, sink *gameplayTestSink, now time.Time) error {
			return s.regenerateAt(context.Background(), live, sink, recoveryCommand(protocol.RecvPotentialRegeneration, 10, 4, 9), now.Add(4*time.Second))
		}},
		{"malformed recovery", func(s *Server, live *liveCharacter, sink *gameplayTestSink, _ time.Time) error {
			return s.resyncRegeneration(live, sink)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &gameplayTestSink{}
			s, live, now := recoveryWorldFixture(t, sink)
			s.gameplay = &gameplaySpace{}
			s.repo = skillTestRepository{cancel: func(domain.CharacterSession, int) (domain.SkillResult, error) {
				t.Fatal("unknown item outcome entered a new skill transaction")
				return domain.SkillResult{}, nil
			}}
			q := domain.UseItemRequest{Session: cloneCombatSession(live.session), ClientTick: 9, Slot: 1, TemplateID: 2000000}
			live.itemUseUncertain = &q
			before, dirty := live.session.Character, live.dirty
			budget := s.skills.members[before.ID].regeneration
			sink.packets = nil
			if err := tc.run(s, live, sink, now); !errors.Is(err, domain.ErrItemUseOutcomeUnknown) {
				t.Fatal("uncertain item outcome was not fatal", err)
			}
			after := live.session.Character
			if len(sink.packets) != 0 || before.HP != after.HP || before.MP != after.MP || before.Revision != after.Revision || live.dirty != dirty || s.skills.members[before.ID].regeneration != budget {
				t.Fatal("uncertain item outcome published or mutated stale vitals")
			}
		})
	}
}
