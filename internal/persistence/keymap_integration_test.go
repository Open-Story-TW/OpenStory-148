package persistence

import (
	"errors"
	"sync"
	"testing"

	"maple-go-server/internal/domain"
)

func TestCharacterKeymapDefaultsPersistenceAndSessionIsolation(t *testing.T) {
	ctx, s, actors := inventoryFixture(t)
	a, b := actors[0], actors[1]
	mapping, err := s.ReadKeymap(ctx, a)
	if err != nil || mapping[56].Action != 53 || mapping[56].Type != 5 {
		t.Fatalf("default jump missing %+v %v", mapping[56], err)
	}
	changes := []domain.KeyChange{{Key: 56}, {Key: 57, Binding: domain.KeyBinding{Type: 5, Action: 53}}}
	if err = s.UpdateKeymap(ctx, a, changes); err != nil {
		t.Fatal(err)
	}
	mapping, err = s.ReadKeymap(ctx, a)
	if err != nil || mapping[56].Type != 0 || mapping[57].Action != 53 || mapping[29].Action != 52 {
		t.Fatalf("persisted map %+v %v", mapping, err)
	}
	other, err := s.ReadKeymap(ctx, b)
	if err != nil || other != domain.DefaultKeymap() {
		t.Fatalf("other character keymap changed %+v %v", other, err)
	}
	stale := a
	stale.Generation++
	if err = s.UpdateKeymap(ctx, stale, changes); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale map update %v", err)
	}
	if _, err = s.ReadKeymap(ctx, stale); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatalf("stale map read %v", err)
	}
	var wg sync.WaitGroup
	for key := 70; key < 74; key++ {
		wg.Add(1)
		go func(key int) {
			defer wg.Done()
			if err := s.UpdateKeymap(ctx, a, []domain.KeyChange{{Key: key, Binding: domain.KeyBinding{Type: 4, Action: int32(key)}}}); err != nil {
				t.Errorf("concurrent key update %v", err)
			}
		}(key)
	}
	wg.Wait()
	mapping, err = s.ReadKeymap(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	for key := 70; key < 74; key++ {
		if mapping[key].Action != int32(key) {
			t.Fatalf("lost nonoverlapping key change%d", key)
		}
	}
}
