package world

import (
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestEllinReactorClickConditions(t *testing.T) {
	actor := domain.Character{X: 1057, Y: 150}
	boulder := roomReactor{def: content.Reactor{TemplateID: 3002000, X: 1057, Y: 188}}
	if _, _, ready := ellinClickEffect(boulder, actor); ready {
		t.Fatal("dilution accepted without the quest shard")
	}
	actor.Items = []domain.InventoryItem{{TemplateID: content.EllinStoneShard, Quantity: 1}}
	if in, out, ready := ellinClickEffect(boulder, actor); !ready || in != content.EllinStoneShard || out != content.EllinDilutedPoison {
		t.Fatalf("dilution exchange = (%d, %d, %t)", in, out, ready)
	}
	boulder.state = 1
	if _, _, ready := ellinClickEffect(boulder, actor); ready {
		t.Fatal("dilution accepted during the original reset delay")
	}
	basket := roomReactor{def: content.Reactor{TemplateID: 3002001, X: 408, Y: -2991}, state: 2}
	actor.X, actor.Y = 408, -2970
	if in, out, ready := ellinClickEffect(basket, actor); !ready || in != 0 || out != content.EllinPurpleStone {
		t.Fatalf("basket exchange = (%d, %d, %t)", in, out, ready)
	}
	actor.X += 146
	if _, _, ready := ellinClickEffect(basket, actor); ready {
		t.Fatal("basket clicked beyond the WZ interaction range")
	}
}
