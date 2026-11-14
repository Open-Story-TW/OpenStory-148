package world

import (
	"testing"

	"maple-go-server/internal/scripting"
)

func TestScriptDestinationResolvesExactPortalName(t *testing.T) {
	gameplay, err := newGameplaySpace()
	if err != nil {
		t.Fatal(err)
	}
	m, err := gameplay.catalog.Map(20000)
	if err != nil || len(m.Portals) == 0 {
		t.Fatalf("fixture map: %+v %v", m, err)
	}
	want := m.Portals[0]
	for _, c := range []scripting.Command{
		{Kind: "warp", Ints: []int64{20000, int64(want.ID)}},
		{Kind: "warp", Ints: []int64{20000}, Text: want.Name},
	} {
		destination, portal, err := scriptDestination(gameplay.catalog, c)
		if err != nil || destination.ID != 20000 || portal != want {
			t.Fatalf("destination %+v: %+v %+v %v", c, destination, portal, err)
		}
	}
	for _, c := range []scripting.Command{
		{Kind: "warp", Ints: []int64{20000}, Text: "missing-native-portal"},
		{Kind: "warp", Ints: []int64{20000}},
		{Kind: "warp", Ints: []int64{20000, int64(want.ID)}, Text: want.Name},
		{Kind: "warp", Ints: []int64{20000, 256}},
		{Kind: "exchange", Ints: []int64{20000, 0}},
	} {
		if _, _, err := scriptDestination(gameplay.catalog, c); err == nil {
			t.Fatalf("ambiguous/unknown destination accepted: %+v", c)
		}
	}
}
