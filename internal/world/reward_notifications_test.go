package world

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type rewardPickupRepo struct {
	domain.Repository
	result domain.InventoryResult
	err    error
	calls  int
}

func (r *rewardPickupRepo) PickupItem(context.Context, domain.ItemPickupRequest) (domain.InventoryResult, error) {
	r.calls++
	return r.result, r.err
}
func (r *rewardPickupRepo) MoveInventory(context.Context, domain.InventoryMoveRequest) (domain.InventoryResult, error) {
	return domain.InventoryResult{}, errors.New("unexpected move")
}
func (r *rewardPickupRepo) ListItemDrops(context.Context, int, int, int) ([]domain.ItemDrop, error) {
	return nil, nil
}

func countRewardPacket(s *gameplayTestSink, want []byte) int {
	n := 0
	for _, p := range s.packets {
		if bytes.Equal(p, want) {
			n++
		}
	}
	return n
}

func TestPickupNotificationUsesCommittedAcquiredAmountAndNotMergedStack(t *testing.T) {
	for _, kind := range []string{"bundle", "equipment", "mesos"} {
		t.Run(kind, func(t *testing.T) {
			before := domain.Character{ID: 1, Revision: 10, MapID: 40000, HP: 50, MP: 5, MaxHP: 50, MaxMP: 5}
			after := before
			after.Revision++
			drop := domain.ItemDrop{ObjectID: 1000000010, MapID: 40000, ChannelID: 1, InvType: 2, TemplateID: 2000000, Quantity: 7}
			want := protocol.ShowItemGain(2000000, 7)
			changes := []domain.InventoryChange{{Mode: 1, InvType: 2, Slot: 1, Quantity: 100}}
			if kind == "equipment" {
				drop.InvType, drop.TemplateID, drop.Quantity = 1, 1002019, 1
				want = protocol.ShowItemGain(1002019, 1)
				changes = []domain.InventoryChange{{Mode: 0, InvType: 1, Slot: 2, Equipment: &domain.Equipment{ID: 55, ItemID: 1002019, Slot: 2}}}
			} else if kind == "mesos" {
				drop.InvType, drop.TemplateID, drop.Quantity, drop.Mesos = 0, 0, 0, 1234
				after.Mesos = 1234
				want, changes = protocol.ShowMesoGain(1234), nil
			}
			repo := &rewardPickupRepo{result: domain.InventoryResult{Character: after, Drop: &drop, Changes: changes}}
			s := &Server{repo: repo, online: make(map[int64]domain.CharacterSession), drops: newDropSpace()}
			live := &liveCharacter{session: domain.CharacterSession{CharacterID: 1, SessionID: "pickup", Generation: 1, ChannelID: 1, Character: before}}
			sink := &gameplayTestSink{}
			if handled, err := s.handleInventoryPickup(context.Background(), live, sink, drop.ObjectID); !handled || err != nil {
				t.Fatalf("pickup: handled=%v err=%v", handled, err)
			}
			if countRewardPacket(sink, want) != 1 || repo.calls != 1 {
				t.Fatalf("missing single committed gain message: %x", sink.packets)
			}
			if kind == "bundle" && countRewardPacket(sink, protocol.ShowItemGain(2000000, 100)) != 0 {
				t.Fatal("reported whole stack instead of acquired amount")
			}
			// Same durable pickup retry must not show the reward again.
			repo.result.Replayed = true
			if _, err := s.handleInventoryPickup(context.Background(), live, sink, drop.ObjectID); err != nil || countRewardPacket(sink, want) != 1 {
				t.Fatal("replayed pickup emitted another gain", err)
			}
			repo.err = domain.ErrInsufficient
			if _, err := s.handleInventoryPickup(context.Background(), live, sink, drop.ObjectID); err != nil || countRewardPacket(sink, want) != 1 {
				t.Fatal("rejected pickup emitted gain", err)
			}
		})
	}
}

func grantNotificationResult(q domain.CombatAttackRequest) domain.CombatAttackResult {
	r := fakeSharedResult(q)
	for i := range r.Spawns {
		for _, p := range q.Members {
			r.Spawns[i].Grants = append(r.Spawns[i].Grants, domain.CombatGrant{CharacterID: p.CharacterID, Experience: 3})
		}
	}
	return r
}

func TestCombatExperienceNotificationWaitsForCommitThenReachesPartyOnce(t *testing.T) {
	for _, mode := range []string{"commit", "resolve", "reject"} {
		t.Run(mode, func(t *testing.T) {
			s, lives, sinks, mob := sharedWorldFixture(t)
			var committed domain.CombatAttackResult
			calls, lookups := 0, 0
			repo := &sharedCombatRepo{parties: []domain.CombatPartyPin{{ID: 1, Revision: 1, Members: []int64{1, 2}}}}
			repo.apply = func(q domain.CombatAttackRequest) (domain.CombatAttackResult, error) {
				calls++
				committed = grantNotificationResult(q)
				if mode == "reject" {
					return domain.CombatAttackResult{}, domain.ErrInsufficient
				}
				if mode == "resolve" {
					return domain.CombatAttackResult{}, errors.New("lost commit reply")
				}
				return committed, nil
			}
			repo.outcome = func(_, _ string) (*domain.CombatAttackResult, error) {
				lookups++
				if lookups == 1 {
					return nil, errors.New("temporary lookup failure")
				}
				committed.Replayed = true // A resolved original commit still needs its first notification.
				return &committed, nil
			}
			s.repo = repo
			if err := s.settledMeleeGameplay(context.Background(), 1, lives[0], sinks[0], protocol.MeleeAttack{Tick: 400, Targets: []protocol.MeleeTarget{{ObjectID: mob.objectID, Damage: 99999}}}, domain.CombatNormal); err != nil {
				t.Fatal(err)
			}
			if mode == "resolve" {
				if sinks[0].count(protocol.SendStatusInfo) != 0 || sinks[1].count(protocol.SendStatusInfo) != 0 {
					t.Fatal("reward announced while commit remained unknown")
				}
				s.resolveCombatSettlements(context.Background(), repo)
				s.resolveCombatSettlements(context.Background(), repo)
			}
			want := 1
			if mode == "reject" {
				want = 0
			}
			for i, sink := range sinks {
				if got := countRewardPacket(sink, protocol.ShowExperienceGain(3, i == 0)); got != want {
					t.Fatalf("recipient %d exp notifications=%d want%d", i, got, want)
				}
			}
			if calls != 1 {
				t.Fatal("notification retried settlement")
			}
		})
	}
}

func TestCombatExperienceNotificationUsesGrantAcrossLevelUpAndRejectsOldVisit(t *testing.T) {
	s, lives, sinks, _ := sharedWorldFixture(t)
	key := mapKey{channel: 1, mapID: 40000}
	prior := cloneCombatSession(lives[0].session)
	prior.Character.EXP = 100
	current := prior.Character
	current.Level++
	current.EXP, current.Revision = 2, prior.Character.Revision+1
	lives[0].session.Character = current
	peer := combatLivePeer{id: prior.CharacterID, session: prior, live: lives[0], sink: sinks[0], entry: s.gameplay.rooms[key].members[prior.CharacterID]}
	ticket := &combatTicket{actorSession: prior, peers: []combatLivePeer{peer}, key: key, request: domain.CombatAttackRequest{Targets: []domain.CombatTarget{{SpawnOperationID: "level-up-mob", Contributions: []domain.CombatContribution{{CharacterID: prior.CharacterID, Damage: 10}}}}}}
	r := domain.CombatAttackResult{Characters: []domain.Character{current}, Spawns: []domain.CombatSpawnResult{{SpawnOperationID: "level-up-mob", Grants: []domain.CombatGrant{{CharacterID: prior.CharacterID, Experience: 50}}}}}
	s.publishCombatProgressLocked(ticket, r)
	s.publishCombatProgressLocked(ticket, r)
	if countRewardPacket(sinks[0], protocol.ShowExperienceGain(50, true)) != 1 {
		t.Fatal("level-up grant lost or notified twice")
	}
	// A new ticket cannot announce a replay already reflected in its snapshot.
	peer.session.Character = current
	ticket = &combatTicket{actorSession: prior, peers: []combatLivePeer{peer}, key: key}
	s.publishCombatProgressLocked(ticket, r)
	if countRewardPacket(sinks[0], protocol.ShowExperienceGain(50, true)) != 1 {
		t.Fatal("old reward replayed from equal revision")
	}
	// Same character/session and sink, new membership pointer: old visit stays silent.
	peer.session = prior
	ticket = &combatTicket{actorSession: prior, peers: []combatLivePeer{peer}, key: key}
	s.leaveGameplay(1, 40000, prior.CharacterID, sinks[0])
	if err := s.enterGameplay(1, lives[0], sinks[0]); err != nil {
		t.Fatal(err)
	}
	s.publishCombatProgressLocked(ticket, r)
	if countRewardPacket(sinks[0], protocol.ShowExperienceGain(50, true)) != 1 {
		t.Fatal("old reward crossed same-map reentry")
	}
}

func TestExperienceWhiteColorFollowsFrozenDamageNotFinalHitter(t *testing.T) {
	ticket := &combatTicket{actorSession: domain.CharacterSession{CharacterID: 3}, request: domain.CombatAttackRequest{
		Parties: []domain.CombatPartyPin{{ID: 10, Members: []int64{1, 2}}},
		Targets: []domain.CombatTarget{{SpawnOperationID: "mob", Contributions: []domain.CombatContribution{
			{CharacterID: 1, Damage: 40}, {CharacterID: 2, Damage: 35}, {CharacterID: 3, Damage: 50},
		}}},
	}}
	if got := combatWhiteExperienceRecipient(ticket, "mob"); got != 1 {
		t.Fatalf("white color belongs to top player of top party, got%d", got)
	}
	if combatWhiteExperienceRecipient(ticket, "different-mob") != 0 {
		t.Fatal("unknown spawn borrowed another reward's color")
	}
}
