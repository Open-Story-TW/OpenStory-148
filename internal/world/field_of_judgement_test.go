package world

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestJudgementSelectionAndOutcome(t *testing.T) {
	setup := time.Date(2026, 1, 1, 3, 50, 0, 0, time.UTC)
	p := judgementSchedule{anchor: setup}
	for _, d := range []time.Duration{-time.Minute, 8 * time.Minute, 4 * time.Hour} {
		at := setup.Add(d)
		last := p.setupAt(at)
		if last.After(at) || !at.Before(last.Add(judgementInterval)) || last.Sub(setup)%judgementInterval != 0 {
			t.Fatalf("schedule setup %v for %v", last, at)
		}
	}
	state := judgementState{setup: setup, entrants: []int64{11, 12, 13}, scores: map[int64]int{11: 25, 12: 25, 13: 100}, started: true}
	mob := &gameplayMob{hp: 1}
	room := &gameplayRoom{members: map[int64]*gameplayMember{11: {}, 12: {}}, mobs: map[int32]*gameplayMob{10: mob}}
	key := mapKey{channel: 1, mapID: content.JudgementRoomFirst}
	state.chooseJackpot(room)
	if state.jackpot != 1_000_000 || !state.lotteryDone || (state.lotteryID != 11 && state.lotteryID != 12) {
		t.Fatalf("jackpot includes a disconnected entrant: %+v", state)
	}
	now := setup.Add(judgementStart + time.Second)
	if len(state.settlement(key, room, now)) != 0 {
		t.Fatal("settled with a live enemy")
	}
	mob.hp = 0
	actions := state.settlement(key, room, now)
	if len(actions) != 1 || state.winner != 11 || !state.winnerExit.Equal(now.Add(judgementRewardTime)) {
		t.Fatalf("tie should go to earliest surviving entrant: %+v, %+v", state, actions)
	}
	for _, a := range actions {
		if a.id != 12 || !a.exit || a.award != "" {
			t.Fatalf("unexpected settlement action: %+v", a)
		}
	}
	delete(room.members, 12)
	if out := state.settlement(key, room, now.Add(time.Second)); len(out) != 1 || out[0].award != "prize" || out[0].exit {
		t.Fatalf("did not give the prize to the sole winner: %+v", out)
	}
	state.prizePlaced = true
	if out := state.settlement(key, room, state.winnerExit); len(out) != 1 || !out[0].exit {
		t.Fatalf("did not return the winner on deadline: %+v", out)
	}
}

func TestJudgementGateAndPrizeBands(t *testing.T) {
	s := &Server{}
	if s.eventAllowsEntry(domain.CharacterSession{}, content.JudgementRoomFirst) {
		t.Fatal("ordinary warp reached an event scene")
	}
	var p judgementSchedule
	for tier := range p.prizes {
		for i := range p.prizes[tier] {
			p.prizes[tier][i] = []domain.ScriptDrop{{TemplateID: 2_000_000 + tier*100 + i}}
		}
	}
	for i, roll := range []int{35, 37, 45, 70, 75, 95, 100} {
		if d := p.rollPrize(content.JudgementRoomFirst+2, roll); d.TemplateID != 2_000_100+i {
			t.Fatalf("roll %d chose the wrong tier or category: %+v", roll, d)
		}
	}
}
