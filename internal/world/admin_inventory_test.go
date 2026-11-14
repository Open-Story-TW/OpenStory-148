package world

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
)

type adminInventoryTestRepo struct {
	domain.Repository
	apply func(domain.AdminInventoryRequest) (domain.AdminInventoryResult, error)
	calls int
}

func (r *adminInventoryTestRepo) ApplyAdminInventory(_ context.Context, q domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
	r.calls++
	return r.apply(q)
}
func (r *adminInventoryTestRepo) AdjustAdminWallet(context.Context, domain.AdminWalletRequest) (domain.CashWallet, error) {
	return domain.CashWallet{}, errors.New("unused")
}
func (r *adminInventoryTestRepo) ReadAdminWallet(context.Context, int64) (domain.CashWallet, error) {
	return domain.CashWallet{}, errors.New("unused")
}

func adminInventoryFixture(t *testing.T) (*Server, *liveCharacter, *gameplayTestSink, *adminInventoryTestRepo) {
	t.Helper()
	s, live, sink, _ := gmWorldFixture(t)
	repo := &adminInventoryTestRepo{}
	s.repo = repo
	s.cfg.RPCToken = strings.Repeat("t", 32)
	sink.mu.Lock()
	sink.packets = nil
	sink.mu.Unlock()
	return s, live, sink, repo
}

func TestAdminInventoryAppliesToTheLiveCharacterAndItsClient(t *testing.T) {
	s, live, sink, repo := adminInventoryFixture(t)
	sword := domain.Equipment{ID: 9, ItemID: 1302000, Slot: 1, WATK: 17, UpgradeSlots: 7}
	repo.apply = func(q domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
		if q.Session == nil || q.Session.CharacterID != 4 || q.Session.SessionID != "gm-scope" || q.Session.Lease.InstanceID != "gm-test" || q.Session.Character.Revision != 1 || q.Session.Character.HP != 5 {
			t.Fatalf("the world's own session was not used: %+v", q.Session)
		}
		c := q.Session.Character
		c.Revision++
		c.Equipment = []domain.Equipment{sword}
		return domain.AdminInventoryResult{Character: c, Changes: []domain.InventoryChange{{Mode: 0, InvType: 1, Slot: 1, Equipment: &sword}}}, nil
	}
	req := domain.AdminInventoryRequest{OperationID: "admin-world-test-give", Actor: "admin@example.test", CharacterID: 4, Kind: domain.AdminGiveItem, ItemID: 1302000, Quantity: 1}
	result, err := s.applyAdminInventory(context.Background(), req)
	if err != nil || len(result.Changes) != 1 {
		t.Fatalf("apply %+v %v", result, err)
	}
	if got := live.session.Character; got.Revision != 2 || len(got.Equipment) != 1 || got.Equipment[0].ID != 9 || got.HP != 5 {
		t.Fatalf("live copy %+v", got)
	}
	if got := s.online[4].Character; got.Revision != 2 || len(got.Equipment) != 1 {
		t.Fatalf("online registry %+v", got)
	}
	if sink.count(protocol.SendModifyInventory) != 1 || sink.count(protocol.SendUpdateStats) != 0 {
		t.Fatalf("client packets: inventory=%d stats=%d", sink.count(protocol.SendModifyInventory), sink.count(protocol.SendUpdateStats))
	}

	// Mesos reach the client as a stat update.
	repo.apply = func(q domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
		c := q.Session.Character
		c.Revision++
		c.Mesos += q.Mesos
		return domain.AdminInventoryResult{Character: c}, nil
	}
	req.OperationID, req.Kind, req.Mesos = "admin-world-test-mesos", domain.AdminGiveMesos, 5_000_000_000
	defer domain.SetLongMoneyForTest(true)()
	if _, err = s.applyAdminInventory(context.Background(), req); err != nil || live.session.Character.Mesos != 5_000_000_000 || sink.count(protocol.SendUpdateStats) != 1 {
		t.Fatalf("mesos %d stats=%d %v", live.session.Character.Mesos, sink.count(protocol.SendUpdateStats), err)
	}

	// Worn equipment also sends the new maximum HP and MP.
	repo.apply = func(q domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
		c := q.Session.Character
		c.Revision++
		c.MaxHP += 100
		worn := sword
		worn.Slot = -11
		return domain.AdminInventoryResult{Character: c, Worn: true, Changes: []domain.InventoryChange{{Mode: 3, InvType: 1, Slot: -11}, {Mode: 0, InvType: 1, Slot: -11, Equipment: &worn}}}, nil
	}
	req.OperationID, req.Kind = "admin-world-test-worn", domain.AdminSetEquipment
	if _, err = s.applyAdminInventory(context.Background(), req); err != nil || live.session.Character.MaxHP != 150 || sink.count(protocol.SendModifyInventory) != 2 || sink.count(protocol.SendUpdateStats) != 2 {
		t.Fatalf("worn: max HP %d inventory=%d stats=%d %v", live.session.Character.MaxHP, sink.count(protocol.SendModifyInventory), sink.count(protocol.SendUpdateStats), err)
	}
}

func TestAdminInventoryRefusalsAndUncertainCommit(t *testing.T) {
	s, live, sink, repo := adminInventoryFixture(t)
	req := domain.AdminInventoryRequest{OperationID: "admin-world-test-refuse", Actor: "admin@example.test", CharacterID: 4, Kind: domain.AdminGiveMesos, Mesos: 1}

	// A known refusal changes nothing and is not retried.
	repo.apply = func(domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
		return domain.AdminInventoryResult{}, domain.ErrInsufficient
	}
	if _, err := s.applyAdminInventory(context.Background(), req); !errors.Is(err, domain.ErrInsufficient) || repo.calls != 1 || live.session.Character.Revision != 1 || len(sink.packets) != 0 {
		t.Fatalf("refusal: %v calls=%d", err, repo.calls)
	}

	// An unknown failure asks once more with the same operation; the replay
	// of a change the world had not seen is applied and shown once.
	repo.calls = 0
	repo.apply = func(q domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
		if repo.calls == 1 {
			return domain.AdminInventoryResult{}, errors.New("connection reset")
		}
		c := q.Session.Character
		c.Revision++
		c.Mesos = 1
		return domain.AdminInventoryResult{Character: c, Replayed: true}, nil
	}
	if _, err := s.applyAdminInventory(context.Background(), req); err != nil || repo.calls != 2 || live.session.Character.Mesos != 1 || sink.count(protocol.SendUpdateStats) != 1 {
		t.Fatalf("uncertain commit: %v calls=%d mesos=%d", err, repo.calls, live.session.Character.Mesos)
	}
	// The admin repeating that operation later shows nothing a second time.
	repo.apply = func(q domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
		return domain.AdminInventoryResult{Character: q.Session.Character, Replayed: true}, nil
	}
	if _, err := s.applyAdminInventory(context.Background(), req); err != nil || sink.count(protocol.SendUpdateStats) != 1 {
		t.Fatalf("known replay was published again: %v", err)
	}

	missing := req
	missing.CharacterID = 99
	if _, err := s.applyAdminInventory(context.Background(), missing); !errors.Is(err, errAdminNotOnline) {
		t.Fatalf("offline character: %v", err)
	}
	// Online but between maps: no skill member yet.
	s.online[98] = domain.CharacterSession{CharacterID: 98}
	missing.CharacterID = 98
	if _, err := s.applyAdminInventory(context.Background(), missing); !errors.Is(err, errAdminBusy) {
		t.Fatalf("character between maps: %v", err)
	}
}

func TestAdminCharacterEndpoints(t *testing.T) {
	s, _, _, repo := adminInventoryFixture(t)
	call := func(method, path, token, body string) (*httptest.ResponseRecorder, map[string]any) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if strings.HasSuffix(path, "/inventory") {
			s.adminInventory(w, r)
		} else {
			s.adminCharacter(w, r)
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w, out
	}
	token := s.cfg.RPCToken
	if w, _ := call(http.MethodGet, "/admin/characters?id=4", "wrong", ""); w.Code != 401 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	if w, _ := call(http.MethodPost, "/admin/characters/inventory", "wrong", "{}"); w.Code != 401 {
		t.Fatalf("wrong token on edit: %d", w.Code)
	}
	w, out := call(http.MethodGet, "/admin/characters?id=4", token, "")
	if w.Code != 200 || out["character"].(map[string]any)["id"].(float64) != 4 || out["channel"].(float64) != 1 {
		t.Fatalf("live character: %d %v", w.Code, out)
	}
	if w, out = call(http.MethodGet, "/admin/characters?id=5", token, ""); w.Code != 404 || out["error"] != "not_online" {
		t.Fatalf("offline character: %d %v", w.Code, out)
	}
	repo.apply = func(q domain.AdminInventoryRequest) (domain.AdminInventoryResult, error) {
		if q.Actor != "admin@example.test" || q.Mesos != 9007199254740993 {
			t.Fatalf("request lost on the way: %+v", q)
		}
		c := q.Session.Character
		c.Revision++
		return domain.AdminInventoryResult{Character: c}, nil
	}
	// A meso amount above 2^53 must survive the JSON between the services.
	body := `{"operation_id":"admin-world-test-http","actor":"admin@example.test","character_id":4,"kind":"give_mesos","mesos":9007199254740993}`
	if w, _ = call(http.MethodPost, "/admin/characters/inventory", token, body); w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	if w, out = call(http.MethodPost, "/admin/characters/inventory", token, `{"character_id":77,"kind":"give_mesos"}`); w.Code != 404 || out["error"] != "not_online" {
		t.Fatalf("edit of an offline character: %d %v", w.Code, out)
	}
	if w, _ = call(http.MethodPost, "/admin/characters/inventory", token, `{"character_id":4,"session":{}}`); w.Code != 400 {
		t.Fatalf("a request may not carry its own session: %d", w.Code)
	}
}
