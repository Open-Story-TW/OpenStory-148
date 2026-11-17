package world

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestPirateSectionClockDoesNotRestartWithinTheSameSection(t *testing.T) {
	p := &piratePQState{}
	now := time.Unix(18000, 0)
	p.startSection(content.PiratePQShore, now)
	if got := time.Unix(0, p.deadline.Load()); !got.Equal(now.Add(4 * time.Minute)) {
		t.Fatalf("shore deadline %v", got)
	}
	p.startSection(content.PiratePQLower, now)
	p.startSection(content.PiratePQUpper, now.Add(3*time.Minute))
	if got := time.Unix(0, p.deadline.Load()); !got.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("deck deadline after crossing decks: %v", got)
	}
}

func TestPirateDoorMatchesItsWZDropBounds(t *testing.T) {
	door := content.Reactor{TemplateID: 2519000, X: 132, Y: 239}
	if !pirateDropFits(door, domain.ItemDrop{X: 97, Y: 149}) {
		t.Fatal("key inside the native door rectangle was rejected")
	}
	if pirateDropFits(door, domain.ItemDrop{X: 173, Y: 240}) {
		t.Fatal("key outside the native door rectangle was accepted")
	}
}
