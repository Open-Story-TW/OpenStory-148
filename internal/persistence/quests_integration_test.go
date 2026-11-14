package persistence

import (
	"errors"
	"fmt"
	"maple-go-server/internal/domain"
	"sync"
	"testing"
)

func TestQuestTransitionsDurableReplayAndIsolation(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	start := domain.QuestRequest{Session: a, QuestID: 1031, NPCID: 2101, Action: "start", OperationID: fmt.Sprintf("quest-start-operation-%d", a.CharacterID)}
	result, err := s.TransitionQuest(ctx, start)
	if err != nil || result.Quest.State != 1 {
		t.Fatalf("start: %+v %v", result, err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replay, e := s.TransitionQuest(ctx, start)
			if e != nil || !replay.Replayed || replay.Quest.State != 1 {
				t.Errorf("replay: %+v %v", replay, e)
			}
		}()
	}
	wg.Wait()
	a.Character = result.Character
	bad := start
	bad.Session = a
	bad.NPCID = 2100
	if _, e := s.TransitionQuest(ctx, bad); !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("altered operation: %v", e)
	}
	complete := domain.QuestRequest{Session: a, QuestID: 1031, NPCID: 2100, Action: "complete", Selection: -1, OperationID: fmt.Sprintf("quest-complete-operation-%d", a.CharacterID)}
	result, err = s.TransitionQuest(ctx, complete)
	if err != nil || result.Quest.State != 2 || result.Character.EXP != 5 {
		t.Fatalf("complete: %+v %v", result, err)
	}
	replay, err := s.TransitionQuest(ctx, complete)
	if err != nil || !replay.Replayed || replay.Character.EXP != 5 {
		t.Fatalf("complete replay: %+v %v", replay, err)
	}
	a.Character = result.Character
	states, err := s.QuestStates(ctx, a)
	if err != nil || len(states) != 1 || states[0].State != 2 || states[0].CompletedAt.IsZero() {
		t.Fatalf("reload: %+v %v", states, err)
	}
	other, err := s.QuestStates(ctx, b)
	if err != nil || len(other) != 0 {
		t.Fatalf("other character leaked: %+v %v", other, err)
	}
	start.Session = a
	start.OperationID += "-again"
	if _, err = s.TransitionQuest(ctx, start); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("completed restart: %v", err)
	}
	// Expired/unknown requirements never become an implicit forceStart route.
	start.QuestID = 1000
	start.OperationID += "-expired"
	if _, err = s.TransitionQuest(ctx, start); err == nil {
		t.Fatal("expired quest accepted")
	}
}

func TestQuestForfeitAndAtomicMobProgress(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	a.Character.MapID = 40000
	if err := s.SaveCharacter(ctx, a, a.Character); err != nil {
		t.Fatal(err)
	}
	a.Character.Revision++
	start := domain.QuestRequest{Session: a, QuestID: 1035, NPCID: 2004, Action: "start", OperationID: fmt.Sprintf("quest-mob-start-%d", a.CharacterID)}
	r, err := s.TransitionQuest(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	a.Character = r.Character
	op := fmt.Sprintf("quest-kill-operation-%d", a.CharacterID)
	value, err := s.AwardCombatKill(ctx, a, op, 0, 9300018)
	if err != nil {
		t.Fatal(err)
	}
	if q := findQuest(value.Quests, 1035); q.MobKills[9300018] != 1 {
		t.Fatalf("counter: %+v", q)
	}
	if replay, e := s.AwardCombatKill(ctx, a, op, 0, 9300018); e != nil || findQuest(replay.Quests, 1035).MobKills[9300018] != 1 {
		t.Fatalf("kill replay: %+v %v", replay, e)
	}
	if _, e := s.AwardCombatKill(ctx, a, op, 0, 9300019); !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("changed mob: %v", e)
	}
	a.Character = value
	complete := domain.QuestRequest{Session: a, QuestID: 1035, NPCID: 2002, Action: "complete", OperationID: fmt.Sprintf("quest-mob-complete-%d", a.CharacterID)}
	if _, e := s.TransitionQuest(ctx, complete); !errors.Is(e, domain.ErrInsufficient) {
		t.Fatalf("missing required item accepted: %v", e)
	}
	before := a.Character.EXP
	if loaded, e := s.QuestStates(ctx, a); e != nil || findQuest(loaded, 1035).State != 1 {
		t.Fatalf("failed transaction changed state: %+v %v", loaded, e)
	}
	forfeit := domain.QuestRequest{Session: a, QuestID: 1035, Action: "forfeit", OperationID: fmt.Sprintf("quest-forfeit-operation-%d", a.CharacterID)}
	r, err = s.TransitionQuest(ctx, forfeit)
	if err != nil || r.Quest.State != 0 || len(r.Quest.MobKills) != 0 || r.Character.EXP != before {
		t.Fatalf("forfeit: %+v %v", r, err)
	}
}

func TestRogerQuestAtomicInventoryAndRecovery(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a := sessions[0]
	a.Character.MapID = 20000
	if err := s.SaveCharacter(ctx, a, a.Character); err != nil {
		t.Fatal(err)
	}
	a.Character.Revision++
	start := domain.QuestRequest{Session: a, QuestID: 1021, NPCID: 2000, Action: "start", OperationID: fmt.Sprintf("roger-start-operation-%d", a.CharacterID)}
	if _, e := s.TransitionQuest(ctx, start); !errors.Is(e, domain.ErrForbidden) {
		t.Fatalf("native bypassed script: %v", e)
	}
	start.Script = "quest/1021.lua"
	r, err := s.TransitionQuest(ctx, start)
	if err != nil {
		t.Fatal(err)
	}
	a.Character = r.Character
	if a.Character.HP != 20 || itemQuantity(a.Character, 2010007) != 1 {
		t.Fatalf("apple acceptance: %+v", a.Character)
	}
	finish := domain.QuestRequest{Session: a, QuestID: 1021, NPCID: 2000, Action: "complete", Script: start.Script, OperationID: fmt.Sprintf("roger-finish-operation-%d", a.CharacterID)}
	if _, e := s.TransitionQuest(ctx, finish); e == nil {
		t.Fatal("unfinished apple accepted")
	}
	// Consume via shared transactional exchange to isolate quest behavior from
	// consumable effect decoding; live launcher acceptance uses UseItem.
	ex, err := s.ExecuteScriptExchange(ctx, domain.ScriptExchange{Session: a, OperationID: fmt.Sprintf("roger-eat-operation-%d", a.CharacterID), Items: []domain.ScriptItemDelta{{TemplateID: 2010007, Quantity: -1}}})
	if err != nil {
		t.Fatal(err)
	}
	a.Character = ex.Character
	a.Character.HP = 50
	finish.Session = a
	r, err = s.TransitionQuest(ctx, finish)
	if err != nil || r.Quest.State != 2 || itemQuantity(r.Character, 2010000) != 3 || itemQuantity(r.Character, 2010009) != 3 || r.Character.EXP != 10 {
		t.Fatalf("finish: %+v %v", r, err)
	}
	if replay, e := s.TransitionQuest(ctx, finish); e != nil || !replay.Replayed || itemQuantity(replay.Character, 2010000) != 3 {
		t.Fatalf("reward duplicated: %+v %v", replay, e)
	}
}
func itemQuantity(c domain.Character, id int) int {
	n := 0
	for _, i := range c.Items {
		if i.TemplateID == id {
			n += i.Quantity
		}
	}
	return n
}
