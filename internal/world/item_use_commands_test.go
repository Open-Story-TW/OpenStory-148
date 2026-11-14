package world

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type itemUseWorldRepo struct {
	domain.Repository
	apply   func(domain.UseItemRequest) (domain.ItemUseResult, error)
	resolve func(domain.UseItemRequest) (domain.ItemUseResult, bool, error)
	save    func(domain.CharacterSession, domain.Character) error
}

func (r *itemUseWorldRepo) ApplyItemUse(_ context.Context, q domain.UseItemRequest) (domain.ItemUseResult, error) {
	return r.apply(q)
}
func (r *itemUseWorldRepo) ResolveItemUse(_ context.Context, q domain.UseItemRequest) (domain.ItemUseResult, bool, error) {
	return r.resolve(q)
}
func (r *itemUseWorldRepo) SaveCharacter(_ context.Context, s domain.CharacterSession, c domain.Character) error {
	if r.save != nil {
		return r.save(s, c)
	}
	return nil
}

func itemUseWorldFixture(t *testing.T) (*Server, *liveCharacter, *gameplayTestSink, *itemUseWorldRepo, domain.UseItemRequest) {
	t.Helper()
	s, live, sink, _ := gmWorldFixture(t)
	live.session.Character.MaxHP = 100
	live.session.Character.Items = []domain.InventoryItem{{ID: 222, TemplateID: 2000000, InvType: 2, Slot: 1, Quantity: 2, Revision: 1}}
	repo := &itemUseWorldRepo{}
	s.repo = repo
	sink.packets = nil
	return s, live, sink, repo, domain.UseItemRequest{ClientTick: 99, Slot: 1, TemplateID: 2000000}
}
func itemUseWorldResult(q domain.UseItemRequest) domain.ItemUseResult {
	c := cloneCombatSession(q.Session).Character
	item := c.Items[0]
	c.Items[0].Quantity--
	c.Items[0].Revision++
	if c.Items[0].Quantity == 0 {
		c.Items = nil
	}
	v, _ := content.CalculatePotionRecovery(content.Recovery{HP: 50}, content.PotionVitals{HP: c.HP, MP: c.MP, MaxHP: c.MaxHP, MaxMP: c.MaxMP}, content.PotionRecoveryModifiers{FixedHPPercent: 50}, false)
	c.HP, c.MP = v.HP, v.MP
	c.Revision++
	return domain.ItemUseResult{Character: c, SessionID: q.Session.SessionID, Generation: q.Session.Generation, ChannelID: q.Session.ChannelID, ClientTick: q.ClientTick, Slot: q.Slot, TemplateID: q.TemplateID, ItemInstanceID: item.ID, QuantityAfter: item.Quantity - 1, CommittedRevision: c.Revision, HPBefore: q.Session.Character.HP, MPBefore: q.Session.Character.MP, FlatHP: 50, FixedHPPercent: 50}
}

func TestItemUseWorldSingleApplyResolveAndAtomicPublication(t *testing.T) {
	for _, mode := range []string{"commit", "resolved", "resolved last item", "rollback", "absent proven", "unknown", "legacy", "corrupt", "replacement", "same owner new visit"} {
		t.Run(mode, func(t *testing.T) {
			s, live, sink, repo, q := itemUseWorldFixture(t)
			if mode == "resolved last item" {
				live.session.Character.Items[0].Quantity = 1
			}
			before := cloneCombatSession(live.session).Character
			applies, resolves := 0, 0
			var original domain.UseItemRequest
			repo.apply = func(req domain.UseItemRequest) (domain.ItemUseResult, error) {
				applies++
				original = req
				switch mode {
				case "rollback":
					return domain.ItemUseResult{}, domain.ErrInsufficient
				case "resolved", "resolved last item", "absent proven", "unknown", "legacy":
					return domain.ItemUseResult{}, context.DeadlineExceeded
				case "replacement":
					replacement := &liveCharacter{session: req.Session}
					replacement.session.Generation++
					seedBoundScene(t, s, replacement, &gameplayTestSink{})
				case "same owner new visit":
					seedBoundScene(t, s, live, sink)
				}
				r := itemUseWorldResult(req)
				if mode == "corrupt" {
					r.QuantityAfter = 99
				}
				return r, nil
			}
			repo.resolve = func(req domain.UseItemRequest) (domain.ItemUseResult, bool, error) {
				resolves++
				if !reflect.DeepEqual(original, req) {
					t.Fatal("resolver changed original key/snapshot")
				}
				switch mode {
				case "absent proven":
					return domain.ItemUseResult{}, false, nil
				case "unknown":
					return domain.ItemUseResult{}, false, context.DeadlineExceeded
				case "legacy":
					return domain.ItemUseResult{}, true, domain.ErrLegacyItemUseReceipt
				}
				// Real Resolve decodes the immutable bytes; empty bags lose the
				// slice allocation through omitempty and must still compare equal.
				raw, _ := json.Marshal(itemUseWorldResult(req))
				var decoded domain.ItemUseResult
				if e := json.Unmarshal(raw, &decoded); e != nil {
					t.Fatal(e)
				}
				return decoded, true, nil
			}
			err := s.handleItemUse(context.Background(), live, sink, q)
			success := mode == "commit" || mode == "resolved" || mode == "resolved last item"
			fatal := mode == "unknown" || mode == "legacy" || mode == "corrupt" || mode == "replacement" || mode == "same owner new visit"
			if (err != nil) != fatal || applies != 1 {
				t.Fatal(mode, err, applies, resolves)
			}
			if success {
				if live.session.Character.HP != 80 || live.session.Character.Revision != before.Revision+1 || len(sink.packets) != 2 || live.itemUseUncertain != nil {
					t.Fatal(live.session.Character, len(sink.packets), live.itemUseUncertain)
				}
				if !reflect.DeepEqual(sink.packets[1], protocol.InventoryVitals(live.session.Character)) {
					t.Fatal("missing complete HP/MP/max publication")
				}
			} else {
				if live.session.Character.HP != before.HP || live.session.Character.Revision != before.Revision || !reflect.DeepEqual(live.session.Character.Items, before.Items) {
					t.Fatal("noncommit altered live assets")
				}
				want := 1
				if fatal {
					want = 0
				}
				if len(sink.packets) != want {
					t.Fatal("wrong old-scene/publication frames", len(sink.packets), want)
				}
			}
			if mode == "unknown" || mode == "legacy" || mode == "corrupt" {
				if live.itemUseUncertain == nil {
					t.Fatal("uncertain receipt did not fence old aggregate")
				}
				saves := 0
				repo.save = func(domain.CharacterSession, domain.Character) error { saves++; return nil }
				live.dirty = true
				if e := s.checkpoint(context.Background(), live); !errors.Is(e, domain.ErrItemUseOutcomeUnknown) || saves != 0 {
					t.Fatal("disconnect checkpoint wrote uncertain aggregate", e, saves)
				}
				live.persistMu.Lock()
				pending := s.combatVitalsPending(live, live.session)
				replacement := live.session
				replacement.Generation++
				newPending := itemUseUncertainFor(live, replacement)
				live.persistMu.Unlock()
				if !pending || newPending {
					t.Fatal("uncertain gate owner binding")
				}
			}
		})
	}
}

func TestItemUseWorldReplayNeverRestoresOldHPOrFutureAssets(t *testing.T) {
	for _, mode := range []string{"current", "older historical receipt", "future", "stale aggregate", "mismatched item"} {
		t.Run(mode, func(t *testing.T) {
			s, live, sink, repo, q := itemUseWorldFixture(t)
			live.session.Character.Revision = 7
			repo.apply = func(req domain.UseItemRequest) (domain.ItemUseResult, error) {
				r := itemUseWorldResult(req)
				r.Character = cloneCombatSession(req.Session).Character
				r.Character.HP = 99
				r.CommittedRevision = 7
				r.Replayed = true
				switch mode {
				case "older historical receipt":
					r.CommittedRevision = 3
				case "future":
					r.CommittedRevision = 8
				case "stale aggregate":
					r.Character.Revision = 8
				case "mismatched item":
					r.Character.Items[0].Quantity = 1
				}
				return r, nil
			}
			err := s.handleItemUse(context.Background(), live, sink, q)
			valid := mode == "current" || mode == "older historical receipt"
			if (err == nil) != valid || live.session.Character.HP != 5 || live.session.Character.Items[0].Quantity != 2 {
				t.Fatal(err, live.session.Character)
			}
			if valid && (len(sink.packets) != 1 || !reflect.DeepEqual(sink.packets[0], protocol.EnableActions())) {
				t.Fatal("replay published old delta")
			}
		})
	}
}

func TestItemUseWorldMaximaClampAndNoUnsafeRepositoryFallback(t *testing.T) {
	s, live, sink, repo, q := itemUseWorldFixture(t)
	live.session.Character.HP, live.session.Character.MP = 150, 80
	live.session.Character.MaxHP, live.session.Character.MaxMP = 200, 100
	repo.apply = func(req domain.UseItemRequest) (domain.ItemUseResult, error) {
		r := itemUseWorldResult(req)
		r.Character.MaxHP, r.Character.MaxMP = 100, 30
		r.Character.HP, r.Character.MP = 100, 30
		return r, nil
	}
	if err := s.handleItemUse(context.Background(), live, sink, q); err != nil || live.session.Character.HP != 100 || live.session.Character.MP != 30 {
		t.Fatal(err, live.session.Character)
	}
	if !reflect.DeepEqual(sink.packets[1], protocol.InventoryVitals(live.session.Character)) {
		t.Fatal("red potion lost MP/max clamp")
	}
	s, live, sink, _, q = itemUseWorldFixture(t)
	s.repo = itemTestRepository{use: func(domain.UseItemRequest) (domain.Character, error) {
		t.Fatal("unsafe fallback called")
		return domain.Character{}, nil
	}}
	if err := s.handleItemUse(context.Background(), live, sink, q); !errors.Is(err, domain.ErrItemUseOutcomeUnknown) || len(sink.packets) != 0 {
		t.Fatal(err, len(sink.packets))
	}
}

func TestUnknownItemBlocksAsynchronousRecoveryAndTickBudgets(t *testing.T) {
	s, live, sink, repo, q := itemUseWorldFixture(t)
	repo.apply = func(domain.UseItemRequest) (domain.ItemUseResult, error) {
		return domain.ItemUseResult{}, context.DeadlineExceeded
	}
	repo.resolve = func(domain.UseItemRequest) (domain.ItemUseResult, bool, error) {
		return domain.ItemUseResult{}, false, context.DeadlineExceeded
	}
	if err := s.handleItemUse(context.Background(), live, sink, q); !errors.Is(err, domain.ErrItemUseOutcomeUnknown) {
		t.Fatal(err)
	}
	live.session.Character.Skills = []domain.SkillState{{ID: 1110000, Level: 1}}
	s.skills.mu.Lock()
	m := s.skills.members[live.session.CharacterID]
	m.passiveTicks = map[int]time.Time{1110000: time.Unix(1, 0)}
	s.skills.mu.Unlock()
	before := cloneCombatSession(live.session).Character
	s.tickReviewedRecoveryAt(m, time.Now())
	if !reflect.DeepEqual(before, live.session.Character) || len(sink.packets) != 0 || !m.passiveTicks[1110000].Equal(time.Unix(1, 0)) {
		t.Fatal("pending item advanced passive recovery or budget")
	}
}

// A stack gained since login sits at the end of the live bag, but the
// committed character is read back ordered by inventory and slot. Using a
// potion then must not look like a corrupt receipt and drop the connection.
func TestItemUseWorldAcceptsBagInGainOrder(t *testing.T) {
	s, live, sink, repo, q := itemUseWorldFixture(t)
	etc := domain.InventoryItem{ID: 300, TemplateID: 4000019, InvType: 4, Slot: 1, Quantity: 8, Revision: 8}
	potion := domain.InventoryItem{ID: 301, TemplateID: 2010004, InvType: 2, Slot: 2, Quantity: 15, Revision: 1}
	live.session.Character.Items = append(live.session.Character.Items, etc, potion)
	repo.apply = func(req domain.UseItemRequest) (domain.ItemUseResult, error) {
		r := itemUseWorldResult(req)
		slices.SortFunc(r.Character.Items, func(a, b domain.InventoryItem) int {
			return cmp.Or(cmp.Compare(a.InvType, b.InvType), cmp.Compare(a.Slot, b.Slot))
		})
		return r, nil
	}
	repo.resolve = func(domain.UseItemRequest) (domain.ItemUseResult, bool, error) {
		t.Fatal("a committed use needs no resolve")
		return domain.ItemUseResult{}, false, nil
	}
	if err := s.handleItemUse(context.Background(), live, sink, q); err != nil {
		t.Fatalf("item use dropped the connection: %v", err)
	}
	items := live.session.Character.Items
	if len(items) != 3 || items[0].Quantity != 1 || items[1] != potion || items[2] != etc {
		t.Fatalf("live bag after use = %+v", items)
	}
}
