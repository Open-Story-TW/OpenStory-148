package scripting

import (
	"context"
	"fmt"
	"maple-go-server/scripts"
	"strings"
	"testing"
)

type taxiTestHost struct {
	job, mesos, coupons int64
	calls               []Command
}

func (h *taxiTestHost) Execute(_ context.Context, _ Identity, c Command) (CommandResult, error) {
	switch c.Kind {
	case "job":
		return CommandResult{Value: h.job}, nil
	case "mesos":
		return CommandResult{Value: h.mesos}, nil
	case "item_count":
		if len(c.Ints) != 1 || c.Ints[0] != 4032313 {
			return CommandResult{}, fmt.Errorf("wrong coupon query")
		}
		return CommandResult{Value: h.coupons}, nil
	case "take_taxi":
		h.calls = append(h.calls, c)
		return CommandResult{Value: map[string]any{"ok": true}}, nil
	}
	return CommandResult{}, fmt.Errorf("unexpected %s", c.Kind)
}
func taxiSession(t *testing.T, h *taxiTestHost) (*Session, Event) {
	t.Helper()
	e, err := New(Options{Host: h})
	if err != nil {
		t.Fatal(err)
	}
	src, err := scripts.Source("npc/1002007.lua")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Register("npc/1002007.lua", src); err != nil {
		t.Fatal(err)
	}
	s, event, err := e.Start(context.Background(), testIdentity, "npc/1002007.lua", StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Cancel)
	return s, event
}
func taxiReply(t *testing.T, s *Session, e Event, r Response) Event {
	t.Helper()
	r.Identity = testIdentity
	r.PromptID = e.Prompt.ID
	r.Kind = e.Prompt.Kind
	n, err := s.Respond(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func TestTaxiSequentialPaidChoicesAndCancellation(t *testing.T) {
	for _, job := range []int64{0, 100, 1000, 2000} {
		for choice, fare := range []int64{1200, 1000, 1000, 1200} {
			t.Run(fmt.Sprintf("job%d-choice%d", job, choice), func(t *testing.T) {
				if job == 0 || job == 1000 || job == 2000 {
					fare /= 10
				}
				h := &taxiTestHost{job: job, mesos: 2000}
				s, e := taxiSession(t, h)
				if e.Prompt.Kind != "menu" || len(e.Prompt.Choices) != 4 {
					t.Fatal(e)
				}
				e = taxiReply(t, s, e, Response{Choice: int64(choice)})
				if e.Prompt.Kind != "yes_no" {
					t.Fatal(e)
				}
				old := e
				e = taxiReply(t, s, e, Response{Bool: true})
				if e.Kind != "done" || len(h.calls) != 1 || h.calls[0].Ints[0] != int64(choice) || h.calls[0].Ints[1] != fare || h.calls[0].Script != "npc/1002007.lua" {
					t.Fatal(e, h.calls)
				}
				if _, err := s.Respond(context.Background(), Response{Identity: testIdentity, PromptID: old.Prompt.ID, Kind: old.Prompt.Kind, Bool: true}); err == nil || len(h.calls) != 1 {
					t.Fatal("duplicate response executed")
				}
			})
		}
	}
	for _, mode := range []string{"no", "end-menu", "end-confirm", "insufficient"} {
		t.Run(mode, func(t *testing.T) {
			h := &taxiTestHost{job: 100, mesos: 2000}
			if mode == "insufficient" {
				h.mesos = 999
			}
			s, e := taxiSession(t, h)
			if mode == "end-menu" {
				s.Cancel()
			} else {
				e = taxiReply(t, s, e, Response{Choice: 1})
				if mode == "end-confirm" {
					s.Cancel()
				} else {
					e = taxiReply(t, s, e, Response{Bool: mode != "no"})
					if e.Kind != "prompt" || e.Prompt.Kind != "say" {
						t.Fatal(e)
					}
				}
			}
			if len(h.calls) != 0 {
				t.Fatal("cancel/refusal charged", h.calls)
			}
		})
	}
}
func TestTaxiCouponPreviousPreservesSequentialLocals(t *testing.T) {
	h := &taxiTestHost{coupons: 1}
	s, e := taxiSession(t, h)
	if e.Prompt.Kind != "say" || !e.Prompt.AllowBack {
		t.Fatal(e)
	}
	e = taxiReply(t, s, e, Response{Back: true})
	if !strings.Contains(e.Prompt.Text, "Hi! I drive") || len(h.calls) != 0 {
		t.Fatal(e, h.calls)
	}
	e = taxiReply(t, s, e, Response{Bool: true})
	if !strings.Contains(e.Prompt.Text, "Taxi Coupon") {
		t.Fatal(e)
	}
	e = taxiReply(t, s, e, Response{Bool: true})
	if e.Kind != "done" || len(h.calls) != 1 || h.calls[0].Ints[0] != 2 || h.calls[0].Ints[1] != 0 {
		t.Fatal(e, h.calls)
	}
}
