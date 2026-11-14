package scripting

import (
	"context"
	"fmt"
	"testing"

	"maple-go-server/scripts"
)

// salonHost stands in for the world: it keeps a look and a coupon bag and
// drops style IDs listed in missing, as the WZ filter does.
type salonHost struct {
	gender, hair, face int64
	coupons            map[int64]int64
	missing            map[int64]bool
	looks              []Command
}

func (h *salonHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	switch c.Kind {
	case "gender":
		return CommandResult{Value: h.gender}, nil
	case "hair":
		return CommandResult{Value: h.hair}, nil
	case "face":
		return CommandResult{Value: h.face}, nil
	case "item_count":
		return CommandResult{Value: h.coupons[c.Ints[0]]}, nil
	case "style_filter":
		kept := []int64{}
		for _, id := range c.Ints {
			if !h.missing[id] {
				kept = append(kept, id)
			}
		}
		return CommandResult{Value: kept}, nil
	case "change_look":
		if c.ID == "" || c.Line == 0 || len(c.Ints) < 3 {
			return CommandResult{}, fmt.Errorf("bad change_look %+v", c)
		}
		h.looks = append(h.looks, c)
		if h.coupons[c.Ints[0]] == 0 {
			return CommandResult{Value: map[string]any{"ok": false, "reason": "no_coupon"}}, nil
		}
		h.coupons[c.Ints[0]]--
		style := c.Ints[2]
		if c.Ints[1] == 1 {
			h.hair = style
		} else {
			h.face = style
		}
		return CommandResult{Value: map[string]any{"ok": true, "style": style}}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected command %s", c.Kind)
}

func startSalon(t *testing.T, npcID int, host *salonHost) (*Session, Event) {
	t.Helper()
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("npc/%d.lua", npcID)
	source, err := scripts.Source(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Register(name, source); err != nil {
		t.Fatal(err)
	}
	s, event, err := e.Start(context.Background(), testIdentity, name, StartOptions{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	return s, event
}

func respond(t *testing.T, s *Session, event Event, r Response) Event {
	t.Helper()
	if event.Kind != "prompt" {
		t.Fatalf("expected a prompt, got %+v", event)
	}
	r.Identity, r.PromptID, r.Kind = testIdentity, event.Prompt.ID, event.Prompt.Kind
	next, err := s.Respond(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestNatalieVIPHaircutPreviewsAndUsesCoupon(t *testing.T) {
	host := &salonHost{gender: 0, hair: 30033, face: 20000, coupons: map[int64]int64{5150001: 1}, missing: map[int64]bool{30203: true}}
	s, event := startSalon(t, 1012103, host)
	if event.Line == 0 {
		t.Fatal("prompt event carries no source line")
	}
	event = respond(t, s, event, Response{Choice: 0})
	p := event.Prompt
	if p.Kind != "avatar" || len(p.Choices) != 11 || p.Choices[0].ID != 30033 {
		t.Fatalf("haircut preview %+v", p)
	}
	for _, c := range p.Choices {
		if c.ID%10 != 3 || c.ID == 30203 {
			t.Fatalf("style %d keeps neither colour nor WZ filter", c.ID)
		}
	}
	event = respond(t, s, event, Response{Choice: 2}) // third style: 30003
	if host.hair != 30003 || host.coupons[5150001] != 0 || len(host.looks) != 1 || host.looks[0].Ints[0] != 5150001 || host.looks[0].Ints[1] != 1 {
		t.Fatalf("look %+v hair %d", host.looks, host.hair)
	}
	if event.Prompt == nil || event.Prompt.Kind != "say" {
		t.Fatalf("no closing line %+v", event)
	}
}

func TestNatalieWithoutCouponKeepsHair(t *testing.T) {
	host := &salonHost{hair: 30030, coupons: map[int64]int64{}}
	s, event := startSalon(t, 1012103, host)
	event = respond(t, s, event, Response{Choice: 1})
	if event.Prompt.Kind != "avatar" || len(event.Prompt.Choices) != 8 || event.Prompt.Choices[7].ID != 30037 {
		t.Fatalf("dye preview %+v", event.Prompt)
	}
	respond(t, s, event, Response{Choice: 4})
	if host.hair != 30030 || host.looks[0].Ints[0] != 5151001 {
		t.Fatalf("dye without coupon changed hair: %d %+v", host.hair, host.looks)
	}
}

func TestBrittanyRandomHaircutFallsBackToEXPCoupon(t *testing.T) {
	host := &salonHost{gender: 1, hair: 31001, coupons: map[int64]int64{5150010: 1}}
	s, event := startSalon(t, 1012104, host)
	event = respond(t, s, event, Response{Choice: 0})
	if event.Prompt.Kind != "yes_no" {
		t.Fatalf("expected confirmation %+v", event.Prompt)
	}
	respond(t, s, event, Response{Bool: true})
	if len(host.looks) != 1 || host.looks[0].Ints[0] != 5150010 || len(host.looks[0].Ints) != 2+13 {
		t.Fatalf("random haircut %+v", host.looks)
	}
	for _, id := range host.looks[0].Ints[2:] {
		if id%10 != 1 || id/10000 != 3 {
			t.Fatalf("candidate %d", id)
		}
	}
}

func TestBrittanyDeclineChangesNothing(t *testing.T) {
	host := &salonHost{hair: 30000, coupons: map[int64]int64{5151000: 1}}
	s, event := startSalon(t, 1012104, host)
	event = respond(t, s, event, Response{Choice: 1})
	event = respond(t, s, event, Response{Bool: false})
	if event.Kind != "done" || len(host.looks) != 0 {
		t.Fatalf("decline %+v %+v", event, host.looks)
	}
}

func TestHeadwardRequiresLegendCouponForFaces(t *testing.T) {
	host := &salonHost{gender: 0, face: 20300, coupons: map[int64]int64{}}
	s, event := startSalon(t, 1012117, host)
	event = respond(t, s, event, Response{Choice: 3})
	if event.Prompt == nil || event.Prompt.Kind != "say" || len(host.looks) != 0 {
		t.Fatalf("face option without coupon %+v", event)
	}
	host = &salonHost{gender: 0, face: 20300, coupons: map[int64]int64{5150041: 1}}
	s, event = startSalon(t, 1012117, host)
	event = respond(t, s, event, Response{Choice: 3})
	if event.Prompt.Kind != "avatar" || event.Prompt.Choices[0].ID != 20300 {
		t.Fatalf("face preview %+v", event.Prompt)
	}
	respond(t, s, event, Response{Choice: 1})
	if host.face != 20301 || host.coupons[5150041] != 0 || host.looks[0].Ints[1] != 2 {
		t.Fatalf("face change %d %+v", host.face, host.looks)
	}
}

func TestAvatarAnswerOutsideListIsRejected(t *testing.T) {
	host := &salonHost{hair: 30030, coupons: map[int64]int64{5150001: 1}}
	s, event := startSalon(t, 1012103, host)
	event = respond(t, s, event, Response{Choice: 0})
	_, err := s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: event.Prompt.ID, Kind: "avatar", Choice: int64(len(event.Prompt.Choices))})
	if err == nil {
		t.Fatal("index past the style list accepted")
	}
}

func TestAndroidStylistExplainsMissingAndroid(t *testing.T) {
	s, event := startSalon(t, 1012123, &salonHost{})
	if event.Prompt == nil || event.Prompt.Kind != "say" {
		t.Fatalf("%+v", event)
	}
	if next := respond(t, s, event, Response{Finish: true}); next.Kind != "done" {
		t.Fatalf("%+v", next)
	}
}
