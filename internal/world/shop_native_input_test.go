package world

import (
	"context"
	"errors"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"testing"
)

type nativeShopInputRepo struct {
	domain.Repository
	requests []domain.ShopRequest
}

func (r *nativeShopInputRepo) ChangeShop(_ context.Context, q domain.ShopRequest) (domain.ShopResult, error) {
	r.requests = append(r.requests, q)
	return domain.ShopResult{}, domain.ErrForbidden
}
func TestNativeShopBuyPriceIsForwardedAndMalformedOldShapeNeverHitsStore(t *testing.T) {
	for kind := 0; kind < 4; kind++ {
		repo := &nativeShopInputRepo{}
		live := &liveCharacter{session: domain.CharacterSession{CharacterID: 7, Generation: 2, Character: domain.Character{ID: 7, HP: 50, MapID: 1000000, Mesos: 1000}}}
		s := &Server{repo: repo, economyDialogs: newEconomyDialogSpace()}
		dialog := &economyDialog{id: "native-shop-test-op", live: live, kind: "shop", shop: domain.Shop{ID: 2, Items: []domain.ShopItem{{ID: 777, TemplateID: 2000000, Price: 5}, {ID: 778, TemplateID: 2000000, Price: 6}}}}
		s.setEconomyDialog(live, dialog)
		sink := &memSink{}
		w := protocol.NewWriter(0)
		w.U8(0)
		index := 0
		if kind == 2 {
			index = 2
		}
		w.U16(uint16(index))
		w.I32(2000000)
		w.I16(2)
		if kind != 1 {
			w.I32(5)
		}
		if kind == 3 {
			w.U8(0)
		}
		err := s.handleNPCShop(context.Background(), live, sink, protocol.NewReader(w.Packet()[2:]))
		if kind == 1 || kind == 3 {
			if !errors.Is(err, protocol.ErrMalformed) {
				t.Fatalf("malformed native body err%v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if kind == 0 {
			if len(repo.requests) != 1 || repo.requests[0].EntryID != 777 || repo.requests[0].DisplayedUnitPrice == nil || *repo.requests[0].DisplayedUnitPrice != 5 || repo.requests[0].Session.Generation != 2 {
				t.Fatalf("native assertion lost%+v", repo.requests)
			}
		} else if len(repo.requests) != 0 {
			t.Fatalf("malformed/incorrect row reachedStore%+v", repo.requests)
		}
		if live.session.Character.Mesos != 1000 {
			t.Fatal("rejection changed money")
		}
	}
}
func TestNativeShopOnlyExactCloseReleasesCurrentDialog(t *testing.T) {
	for _, body := range [][]byte{{4}, {3, 0}, {3}} {
		repo := &nativeShopInputRepo{}
		live := &liveCharacter{session: domain.CharacterSession{CharacterID: 7}}
		s := &Server{repo: repo, economyDialogs: newEconomyDialogSpace()}
		dialog := &economyDialog{live: live, kind: "shop"}
		s.setEconomyDialog(live, dialog)
		err := s.handleNPCShop(context.Background(), live, &memSink{}, protocol.NewReader(body))
		if len(body) == 1 && body[0] == 3 {
			if err != nil || s.getEconomyDialog(live, "shop") != nil {
				t.Fatal("exact close failed")
			}
		} else if !errors.Is(err, protocol.ErrMalformed) || s.getEconomyDialog(live, "shop") != dialog {
			t.Fatal("malformed input closed dialog")
		}
	}
}
