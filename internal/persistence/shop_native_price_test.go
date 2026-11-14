package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maple-go-server/internal/domain"
	"testing"
)

func TestShopHashPreservesInternalReceiptAndBindsNativePrice(t *testing.T) {
	r := domain.ShopRequest{Session: domain.CharacterSession{AccountID: 1, CharacterID: 2, SessionID: "session", Generation: 3}, ShopID: 2, Kind: "buy", EntryID: 5, TemplateID: 2000000, Quantity: 2}
	old := struct {
		AccountID, CharacterID              int64
		Session                             string
		Generation                          int64
		ShopID                              int
		Kind                                string
		EntryID, TemplateID, Slot, Quantity int
	}{1, 2, "session", 3, 2, "buy", 5, 2000000, 0, 2}
	raw, _ := json.Marshal(old)
	sum := sha256.Sum256(raw)
	expected := hex.EncodeToString(sum[:])
	if shopHash(r) != expected {
		t.Fatal("nil price changed pre-existing internal receipt hash")
	}
	five := int64(5)
	r.DisplayedUnitPrice = &five
	if shopHash(r) == expected {
		t.Fatal("native price omitted from replay hash")
	}
	a := shopHash(r)
	five = 0
	if shopHash(r) == a {
		t.Fatal("changed native price replay aliases original")
	}
}
