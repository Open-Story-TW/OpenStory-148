package world

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type scrollWorldRepo struct {
	domain.Repository
	apply   func(domain.ScrollRequest) (domain.ScrollResult, error)
	resolve func(domain.ScrollRequest) (domain.ScrollResult, bool, error)
	save    func(domain.CharacterSession, domain.Character) error
}

func (r *scrollWorldRepo) ApplyScroll(_ context.Context, q domain.ScrollRequest) (domain.ScrollResult, error) {
	return r.apply(q)
}
func (r *scrollWorldRepo) ResolveScroll(_ context.Context, q domain.ScrollRequest) (domain.ScrollResult, bool, error) {
	return r.resolve(q)
}
func (r *scrollWorldRepo) SaveCharacter(_ context.Context, s domain.CharacterSession, c domain.Character) error {
	if r.save != nil {
		return r.save(s, c)
	}
	return nil
}

func scrollWorldFixture(t *testing.T) (*Server, *liveCharacter, *gameplayTestSink, *scrollWorldRepo) {
	t.Helper()
	s, live, sink, _ := gmWorldFixture(t)
	live.session.Character.Equipment = []domain.Equipment{{ID: 111, ItemID: 1322005, Slot: -11, WATK: 19, UpgradeSlots: 7, Enhance: 3, Potential: domain.EquipmentPotential{Present: true, RawState: 18, Options: [5]uint16{20353, 10014}, Flags: 65535}}}
	live.session.Character.Items = []domain.InventoryItem{{ID: 222, TemplateID: 2043200, InvType: 2, Slot: 2, Quantity: 2, Revision: 1}}
	r := &scrollWorldRepo{}
	s.repo = r
	sink.packets = nil
	return s, live, sink, r
}
func scrollWire() *protocol.Reader {
	w := protocol.NewRawWriter()
	w.U32(99)
	w.I16(2)
	w.I16(-11)
	w.I16(0)
	w.U8(0)
	return protocol.NewReader(w.Packet())
}
func scrollWorldResult(q domain.ScrollRequest) domain.ScrollResult {
	c := cloneCombatSession(q.Session).Character
	before, material := c.Equipment[0], c.Items[0]
	c.Items[0].Quantity--
	c.Items[0].Revision++
	change := domain.InventoryChange{Mode: 1, InvType: 2, Slot: 2, Quantity: c.Items[0].Quantity}
	if c.Items[0].Quantity == 0 {
		c.Items = c.Items[1:]
		change.Mode = 3
		change.Quantity = 0
	}
	c.Equipment[0].WATK += 2
	c.Equipment[0].UpgradeSlots--
	c.Equipment[0].ScrollLevel++
	c.Revision++
	p := domain.ScrollOutcomePlan{Version: 1, SessionID: q.Session.SessionID, Generation: q.Session.Generation, CharacterID: c.ID, AccountID: c.AccountID, WorldID: c.WorldID, ChannelID: q.Session.ChannelID, MapID: c.MapID, ClientTick: q.ClientTick, SourceSlot: q.SourceSlot, TargetSlot: q.TargetSlot, WhiteFlags: q.WhiteFlags, LegendarySpirit: q.LegendarySpirit, BeforeRevision: q.Session.Character.Revision, CommittedRevision: c.Revision, MaterialBefore: material, EquipmentBefore: before, Stats: domain.Equipment{WATK: 2}, Success: 100, SuccessRoll: 10, CurseRoll: -1, Status: 1}
	raw, sha, _ := domain.EncodeScrollPlan(p)
	return domain.ScrollResult{Inventory: domain.InventoryResult{Character: c, Changes: []domain.InventoryChange{change, {Mode: 3, InvType: 1, Slot: q.TargetSlot}, {Mode: 0, InvType: 1, Slot: q.TargetSlot, Equipment: &c.Equipment[0]}}}, Status: 1, LegendarySpirit: q.LegendarySpirit, ScrollID: material.TemplateID, EquipmentTemplateID: before.ItemID, SuccessRoll: 10, CurseRoll: -1, CommittedRevision: c.Revision, PlanJSON: raw, PlanSHA256: sha}
}

func TestScrollOutcomeSingleApplyResolveAndScenePublication(t *testing.T) {
	for _, mode := range []string{"commit", "lost reply", "last item lost reply", "known rollback", "proven absence", "unknown lookup", "corrupt stored receipt", "corrupt batch", "corrupt plan", "replacement", "same owner new visit", "other map", "legacy committed"} {
		t.Run(mode, func(t *testing.T) {
			s, live, sink, repo := scrollWorldFixture(t)
			if mode == "last item lost reply" {
				live.session.Character.Items[0].Quantity = 1
			}
			before := cloneCombatSession(live.session).Character
			applies, resolves := 0, 0
			var original domain.ScrollRequest
			repo.apply = func(q domain.ScrollRequest) (domain.ScrollResult, error) {
				applies++
				original = q
				switch mode {
				case "known rollback":
					return domain.ScrollResult{}, domain.ErrInsufficient
				case "lost reply", "last item lost reply", "proven absence", "unknown lookup":
					return domain.ScrollResult{}, context.DeadlineExceeded
				case "corrupt stored receipt":
					return domain.ScrollResult{}, fmt.Errorf("%w: %v", domain.ErrScrollOutcomeUnknown, domain.ErrForbidden)
				case "replacement":
					replacement := &liveCharacter{session: q.Session}
					replacement.session.Generation++
					seedBoundScene(t, s, replacement, &gameplayTestSink{})
				case "same owner new visit":
					seedBoundScene(t, s, live, sink)
				case "other map":
					live.session.Character.MapID++
					seedBoundScene(t, s, live, sink)
				}
				v := scrollWorldResult(q)
				if mode == "corrupt batch" {
					v.Inventory.Changes[0].Quantity = 99
				}
				if mode == "corrupt plan" {
					v.PlanSHA256 = "wrong"
				}
				if mode == "legacy committed" {
					v.PlanJSON = nil
					v.PlanSHA256 = ""
					v.CommittedRevision = 0
				}
				return v, nil
			}
			repo.resolve = func(q domain.ScrollRequest) (domain.ScrollResult, bool, error) {
				resolves++
				if !reflect.DeepEqual(q, original) {
					t.Fatal("Resolve changed original key/snapshot")
				}
				if mode == "proven absence" {
					return domain.ScrollResult{}, false, nil
				}
				if mode == "unknown lookup" || mode == "corrupt stored receipt" {
					return domain.ScrollResult{}, false, context.DeadlineExceeded
				}
				raw, _ := json.Marshal(scrollWorldResult(q))
				var v domain.ScrollResult
				if err := json.Unmarshal(raw, &v); err != nil {
					t.Fatal(err)
				}
				v.Replayed = true
				return v, true, nil
			}
			err := s.handleScroll(context.Background(), live, sink, scrollWire())
			success := mode == "commit" || mode == "lost reply" || mode == "last item lost reply" || mode == "legacy committed"
			safeReject := mode == "known rollback" || mode == "proven absence"
			if applies != 1 || (err == nil) != (success || safeReject) {
				t.Fatal(mode, err, applies, resolves)
			}
			if success {
				if live.session.Character.Equipment[0].WATK != 21 || live.session.Character.Equipment[0].ScrollLevel != 1 || live.session.Character.Equipment[0].Enhance != 3 || live.session.Character.Revision != before.Revision+1 || live.scrollUncertain != nil || sink.count(protocol.SendModifyInventory) != 1 {
					t.Fatal("commit not merged once", live.session.Character, len(sink.packets))
				}
				if len(sink.packets) != 4 || !reflect.DeepEqual(sink.packets[1], protocol.InventoryVitals(live.session.Character)) {
					t.Fatal("not one full ordered batch", len(sink.packets))
				}
			} else {
				if live.session.Character.Revision != before.Revision || !reflect.DeepEqual(live.session.Character.Items, before.Items) || live.session.Character.Equipment[0].WATK != 19 {
					t.Fatal("nonpublish mutated assets")
				}
				want := 0
				if safeReject {
					want = 1
				}
				if len(sink.packets) != want {
					t.Fatal("old scene/error published packets", mode, len(sink.packets))
				}
			}
			if mode == "unknown lookup" || mode == "corrupt stored receipt" || mode == "corrupt batch" || mode == "corrupt plan" {
				if live.scrollUncertain == nil {
					t.Fatal("fatal outcome lost fence")
				}
				saves := 0
				repo.save = func(domain.CharacterSession, domain.Character) error { saves++; return nil }
				if err := s.checkpoint(context.Background(), live); !errors.Is(err, domain.ErrScrollOutcomeUnknown) || saves != 0 {
					t.Fatal("stale disconnect checkpoint", err, saves)
				}
				live.persistMu.Lock()
				pending := s.combatVitalsPending(live, live.session)
				replacement := live.session
				replacement.Generation++
				wrong := scrollUncertainFor(live, replacement)
				live.persistMu.Unlock()
				if !pending || wrong {
					t.Fatal("fence wrong owner")
				}
			}
			wantResolve := 0
			if mode == "lost reply" || mode == "last item lost reply" || mode == "proven absence" || mode == "unknown lookup" || mode == "corrupt stored receipt" {
				wantResolve = 1
			}
			if resolves != wantResolve {
				t.Fatal("unexpected Resolve/Apply retry", resolves, wantResolve)
			}
		})
	}
}

func TestScrollOutcomeReplayNoOldHPInventoryOrEffect(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "planned", true: "legacy"}[legacy], func(t *testing.T) {
			s, live, sink, repo := scrollWorldFixture(t)
			live.session.Character.Revision = 7
			hp := live.session.Character.HP
			repo.apply = func(q domain.ScrollRequest) (domain.ScrollResult, error) {
				old := q
				old.Session.Character.Revision = 2
				v := scrollWorldResult(old)
				v.Replayed = true
				v.Inventory.Replayed = true
				v.Inventory.Changes = nil
				v.Inventory.Character = cloneCombatSession(q.Session).Character
				v.Inventory.Character.HP = 99
				if legacy {
					v.PlanJSON = nil
					v.PlanSHA256 = ""
					v.CommittedRevision = 0
				}
				return v, nil
			}
			repo.resolve = func(domain.ScrollRequest) (domain.ScrollResult, bool, error) {
				t.Fatal("ordinary old replay entered resolver")
				return domain.ScrollResult{}, false, nil
			}
			if err := s.handleScroll(context.Background(), live, sink, scrollWire()); err != nil || live.session.Character.HP != hp || live.session.Character.Items[0].Quantity != 2 || live.session.Character.Equipment[0].WATK != 19 || len(sink.packets) != 1 || !reflect.DeepEqual(sink.packets[0], protocol.EnableActions()) {
				t.Fatal("replay restored old state/effect", err, live.session.Character, len(sink.packets))
			}
		})
	}
}

func TestScrollOutcomeUnknownBlocksExistingVitalsEntrypoints(t *testing.T) {
	for _, run := range []struct {
		name string
		call func(*Server, *liveCharacter, *gameplayTestSink, time.Time) error
	}{
		{"damage", func(s *Server, l *liveCharacter, k *gameplayTestSink, _ time.Time) error {
			return s.takeDamage(context.Background(), l, k, protocol.PlayerDamage{Damage: 1})
		}},
		{"cancel", func(s *Server, l *liveCharacter, k *gameplayTestSink, _ time.Time) error {
			return s.cancelSkill(context.Background(), l, k, 1002)
		}},
		{"natural", func(s *Server, l *liveCharacter, k *gameplayTestSink, n time.Time) error {
			return s.regenerateAt(context.Background(), l, k, recoveryCommand(protocol.RecvRegeneration, 10, 10, 3), n.Add(10*time.Second))
		}},
		{"potential", func(s *Server, l *liveCharacter, k *gameplayTestSink, n time.Time) error {
			return s.regenerateAt(context.Background(), l, k, recoveryCommand(protocol.RecvPotentialRegeneration, 10, 4, 9), n.Add(4*time.Second))
		}},
		{"resync", func(s *Server, l *liveCharacter, k *gameplayTestSink, _ time.Time) error {
			return s.resyncRegeneration(l, k)
		}},
	} {
		t.Run(run.name, func(t *testing.T) {
			sink := &gameplayTestSink{}
			s, l, n := recoveryWorldFixture(t, sink)
			s.gameplay = &gameplaySpace{}
			s.repo = skillTestRepository{cancel: func(domain.CharacterSession, int) (domain.SkillResult, error) {
				t.Fatal("uncertain scroll entered skill transaction")
				return domain.SkillResult{}, nil
			}}
			q := domain.ScrollRequest{Session: cloneCombatSession(l.session), ClientTick: 9, SourceSlot: 2, TargetSlot: -11}
			l.scrollUncertain = &q
			before, dirty, budget := l.session.Character, l.dirty, s.skills.members[l.session.Character.ID].regeneration
			sink.packets = nil
			if err := run.call(s, l, sink, n); !errors.Is(err, domain.ErrScrollOutcomeUnknown) {
				t.Fatal("unknown not fatal", err)
			}
			if l.session.Character.HP != before.HP || l.session.Character.MP != before.MP || l.session.Character.Revision != before.Revision || l.dirty != dirty || budget != s.skills.members[before.ID].regeneration || len(sink.packets) != 0 {
				t.Fatal("uncertain scroll changed stale HP/budget/packets")
			}
		})
	}
}

func TestScrollOutcomeRollbackClassificationCannotHideCommittedCorruption(t *testing.T) {
	if scrollKnownRollback(fmt.Errorf("%w: %w", domain.ErrScrollOutcomeUnknown, domain.ErrForbidden)) {
		t.Fatal("committed corruption allowed continued old aggregate")
	}
	if !scrollKnownRollback(domain.ErrForbidden) {
		t.Fatal("changed-request rejection lost known rollback classification")
	}
}

type scrollApplyOnlyRepo struct {
	domain.Repository
	calls int
}

func (r *scrollApplyOnlyRepo) ApplyScroll(context.Context, domain.ScrollRequest) (domain.ScrollResult, error) {
	r.calls++
	return domain.ScrollResult{}, nil
}
func TestScrollOutcomeUnsupportedRepositoryNeverApplies(t *testing.T) {
	s, l, k, _ := scrollWorldFixture(t)
	r := &scrollApplyOnlyRepo{}
	s.repo = r
	if err := s.handleScroll(context.Background(), l, k, scrollWire()); err != nil || r.calls != 0 || len(k.packets) != 1 || !reflect.DeepEqual(k.packets[0], protocol.EnableActions()) {
		t.Fatal("unsafe compatibility fallback", err, r.calls)
	}
}
func TestScrollOutcomeFutureReplayRequiresOriginalReceiptAndNeverAppliesTwice(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			s, l, k, r := scrollWorldFixture(t)
			calls, lookups := 0, 0
			r.apply = func(q domain.ScrollRequest) (domain.ScrollResult, error) {
				calls++
				v := scrollWorldResult(q)
				v.Replayed = true
				v.Inventory.Changes = nil
				return v, nil
			}
			r.resolve = func(q domain.ScrollRequest) (domain.ScrollResult, bool, error) {
				lookups++
				if missing {
					return domain.ScrollResult{}, false, nil
				}
				v := scrollWorldResult(q)
				v.Replayed = true
				return v, true, nil
			}
			err := s.handleScroll(context.Background(), l, k, scrollWire())
			if calls != 1 || lookups != 1 {
				t.Fatal("replay reapplied", calls, lookups)
			}
			if missing {
				if err == nil || len(k.packets) != 0 || l.scrollUncertain == nil {
					t.Fatal("missing committed replay continued", err)
				}
			} else if err != nil || k.count(protocol.SendModifyInventory) != 1 || l.session.Character.Equipment[0].WATK != 21 {
				t.Fatal("original delta not recovered", err)
			}
		})
	}
}

func TestScrollOutcomeBuffExpiryClampsBothVitalsAndPeerEffectOnce(t *testing.T) {
	for _, bag := range []bool{false, true} {
		t.Run(fmt.Sprint(bag), func(t *testing.T) {
			s, l, k, r := scrollWorldFixture(t)
			l.session.Character.HP, l.session.Character.MP = 150, 120
			l.session.Character.MaxHP, l.session.Character.MaxMP = 160, 130
			if bag {
				l.session.Character.Equipment[0].Slot = 1
			}
			peerSink := &gameplayTestSink{}
			peer := cloneCombatSession(l.session)
			peer.CharacterID = 44
			peer.Character.ID = 44
			peer.AccountID = 33
			peer.Character.AccountID = 33
			peer.SessionID = "peer"
			seedBoundScene(t, s, &liveCharacter{session: peer}, peerSink)
			peerSink.packets = nil
			r.apply = func(q domain.ScrollRequest) (domain.ScrollResult, error) {
				v := scrollWorldResult(q)
				if bag {
					v.Inventory.Changes[1].Slot = 1
					v.Inventory.Changes[2].Slot = 1
				}
				v.Inventory.Character.HP, v.Inventory.Character.MaxHP = 100, 100
				v.Inventory.Character.MP, v.Inventory.Character.MaxMP = 90, 90
				return v, nil
			}
			r.resolve = func(domain.ScrollRequest) (domain.ScrollResult, bool, error) {
				t.Fatal("unexpected resolve")
				return domain.ScrollResult{}, false, nil
			}
			w := protocol.NewRawWriter()
			w.U32(99)
			w.I16(2)
			if bag {
				w.I16(1)
			} else {
				w.I16(-11)
			}
			w.I16(0)
			if bag {
				w.U8(1)
			} else {
				w.U8(0)
			}
			// Fixture result builder uses the requested target and Spirit fields below.
			if err := s.handleScroll(context.Background(), l, k, protocol.NewReader(w.Packet())); err != nil {
				t.Fatal(err)
			}
			if l.session.Character.HP != 100 || l.session.Character.MP != 90 || k.count(protocol.SendScrollEffect) != 1 || peerSink.count(protocol.SendScrollEffect) != 1 || len(k.packets) != 4 || !reflect.DeepEqual(k.packets[1], protocol.InventoryVitals(l.session.Character)) {
				t.Fatal("expiry/peer not exact ordered one-batch")
			}
		})
	}
}

func TestScrollOutcomeWhiteBindingIgnoresUnsortedLiveSlice(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, terminal := range []bool{false, true} {
			for _, lost := range []bool{false, true} {
				t.Run(fmt.Sprintf("legacy=%t/terminal=%t/lost=%t", legacy, terminal, lost), func(t *testing.T) {
					s, l, k, r := scrollWorldFixture(t)
					a := domain.InventoryItem{ID: 700, TemplateID: 2340000, InvType: 2, Slot: 10, Quantity: 5, Revision: 11}
					b := domain.InventoryItem{ID: 701, TemplateID: 2340000, InvType: 2, Slot: 8, Quantity: 2, Revision: 12}
					if terminal {
						b.Quantity = 1
					}
					// This is a normal live order after moving an earlier item to slot10;
					// SQL loads the very same assets ordered by slot8, then slot10.
					l.session.Character.Items = append(l.session.Character.Items, a, b)
					build := func(q domain.ScrollRequest) domain.ScrollResult {
						v := scrollWorldResult(q)
						v.WhiteScroll = true
						var plan domain.ScrollOutcomePlan
						_ = json.Unmarshal(v.PlanJSON, &plan)
						plan.WhiteBefore = &b
						change := domain.InventoryChange{Mode: 3, InvType: 2, Slot: 8}
						for i, item := range v.Inventory.Character.Items {
							if item.ID == b.ID {
								if terminal {
									v.Inventory.Character.Items = append(v.Inventory.Character.Items[:i], v.Inventory.Character.Items[i+1:]...)
								} else {
									v.Inventory.Character.Items[i].Quantity--
									v.Inventory.Character.Items[i].Revision++
									change.Mode = 1
									change.Quantity = 1
								}
								break
							}
						}
						v.Inventory.Changes = append(v.Inventory.Changes[:1], append([]domain.InventoryChange{change}, v.Inventory.Changes[1:]...)...)
						v.PlanJSON, v.PlanSHA256, _ = domain.EncodeScrollPlan(plan)
						if legacy {
							v.PlanJSON = nil
							v.PlanSHA256 = ""
							v.CommittedRevision = 0
						}
						return v
					}
					calls, lookups := 0, 0
					r.apply = func(q domain.ScrollRequest) (domain.ScrollResult, error) {
						calls++
						if lost {
							return domain.ScrollResult{}, context.DeadlineExceeded
						}
						return build(q), nil
					}
					r.resolve = func(q domain.ScrollRequest) (domain.ScrollResult, bool, error) {
						lookups++
						v := build(q)
						if legacy {
							v.CommittedRevision = v.Inventory.Character.Revision
						}
						v.Replayed = true
						return v, true, nil
					}
					w := protocol.NewRawWriter()
					w.U32(99)
					w.I16(2)
					w.I16(-11)
					w.I16(2)
					w.U8(0)
					if err := s.handleScroll(context.Background(), l, k, protocol.NewReader(w.Packet())); err != nil {
						t.Fatal("correct committed white falsely unknown", err)
					}
					wantLookup := 0
					if lost {
						wantLookup = 1
					}
					if calls != 1 || lookups != wantLookup || l.scrollUncertain != nil || k.count(protocol.SendModifyInventory) != 1 || k.count(protocol.SendScrollEffect) != 1 {
						t.Fatal("not one committed publication", calls, lookups)
					}
					gotA, gotB := 0, 0
					for _, i := range l.session.Character.Items {
						if i.ID == a.ID {
							if !reflect.DeepEqual(i, a) {
								t.Fatal("unconsumed white changed", i)
							}
							gotA++
						}
						if i.ID == b.ID {
							gotB++
							if terminal || i.Quantity != 1 || i.Revision != 13 {
								t.Fatal("selected white not exactly consumed", i)
							}
						}
					}
					if gotA != 1 || gotB != map[bool]int{true: 0, false: 1}[terminal] {
						t.Fatal("wrong durable white consumed", l.session.Character.Items)
					}
				})
			}
		}
	}
}

func TestScrollOutcomeWhitePlanMustMatchFullPreInputItem(t *testing.T) {
	s, l, k, r := scrollWorldFixture(t)
	white := domain.InventoryItem{ID: 701, TemplateID: 2340000, InvType: 2, Slot: 8, Quantity: 2, Revision: 12}
	l.session.Character.Items = append(l.session.Character.Items, white)
	r.apply = func(q domain.ScrollRequest) (domain.ScrollResult, error) {
		v := scrollWorldResult(q)
		v.WhiteScroll = true
		v.Inventory.Character.Items[1].Quantity--
		v.Inventory.Character.Items[1].Revision++
		change := domain.InventoryChange{Mode: 1, InvType: 2, Slot: 8, Quantity: 1}
		v.Inventory.Changes = append(v.Inventory.Changes[:1], append([]domain.InventoryChange{change}, v.Inventory.Changes[1:]...)...)
		var p domain.ScrollOutcomePlan
		_ = json.Unmarshal(v.PlanJSON, &p)
		wrong := white
		wrong.Revision++
		p.WhiteBefore = &wrong
		v.PlanJSON, v.PlanSHA256, _ = domain.EncodeScrollPlan(p)
		// Canonical result still identifies the right item/quantity; it is the
		// controller's immutable pre-input comparison that detects the ABA.
		if err := domain.ValidateScrollReceipt(q, v); err != nil {
			t.Fatal("test failed before pre-input guard", err)
		}
		return v, nil
	}
	r.resolve = func(domain.ScrollRequest) (domain.ScrollResult, bool, error) {
		t.Fatal("new apply result unexpectedly resolved")
		return domain.ScrollResult{}, false, nil
	}
	w := protocol.NewRawWriter()
	w.U32(99)
	w.I16(2)
	w.I16(-11)
	w.I16(2)
	w.U8(0)
	if err := s.handleScroll(context.Background(), l, k, protocol.NewReader(w.Packet())); !errors.Is(err, domain.ErrScrollOutcomeUnknown) || l.scrollUncertain == nil || len(k.packets) != 0 || !reflect.DeepEqual(l.session.Character.Items[1], white) {
		t.Fatal("same white ID with changed revision admitted", err)
	}
}
