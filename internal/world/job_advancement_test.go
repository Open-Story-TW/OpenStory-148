package world

import (
	"context"
	"errors"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

type jobTestRepo struct {
	domain.Repository
	calls int
	err   error
}

func (r *jobTestRepo) AdvanceWarrior(_ context.Context, request domain.JobAdvancementRequest) (domain.JobAdvancementResult, error) {
	r.calls++
	if r.err != nil {
		return domain.JobAdvancementResult{}, r.err
	}
	c := request.Session.Character
	c.Job, c.Revision = 100, c.Revision+1
	c.STR, c.DEX, c.INT, c.LUK, c.AP, c.SP = 4, 4, 4, 4, 54, 1
	c.InventorySlots = [5]int{28, 24, 24, 28, 24}
	weapon := domain.Equipment{ID: 900, ItemID: 1402001, Slot: 25}
	c.Equipment = append(c.Equipment, weapon)
	return domain.JobAdvancementResult{InventoryResult: domain.InventoryResult{Character: c, Changes: []domain.InventoryChange{{Mode: 0, InvType: 1, Slot: 25, Equipment: &weapon}}}, FromJob: 0, ToJob: 100}, nil
}

func TestJobHostGrowsNativeBagsBeforeRewardAndRechecksNPC(t *testing.T) {
	for _, distance := range []int{0, 1000} {
		repo := &jobTestRepo{}
		s := &Server{repo: repo, online: make(map[int64]domain.CharacterSession), gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{
			{channel: 1, mapID: 102000003}: {npcs: map[int32]content.Life{500: {TemplateID: 1022000}}},
		}}}
		live := &liveCharacter{session: domain.CharacterSession{AccountID: 1, CharacterID: 1, SessionID: "job-host", Generation: 1, ChannelID: 1, Character: domain.Character{ID: 1, Job: 0, Level: 10, MapID: 102000003, HP: 50, MaxHP: 50, X: distance, Revision: 1}}}
		identity := scripting.Identity{SessionID: "job-host", CharacterID: 1, Generation: 1}
		sink := &memSink{}
		host := &worldScript{server: s, live: live, sink: sink, npcID: 1022000, objectID: 500, identity: identity}
		result, err := host.Execute(context.Background(), identity, scripting.Command{Kind: "advance_warrior", Script: "npc/1022000.lua", ID: "job-host-operation"})
		if distance != 0 {
			if !errors.Is(err, domain.ErrForbidden) || repo.calls != 0 || len(sink.pkts) != 0 {
				t.Fatal("paused player could advance away from NPC")
			}
			continue
		}
		if err != nil || repo.calls != 1 || result.Value.(map[string]any)["ok"] != true || live.session.Character.Job != 100 || live.session.Character.Capacity(1) != 28 {
			t.Fatalf("job state not applied: %+v %v %+v", result, err, live.session.Character)
		}
		if len(sink.pkts) != 4 {
			t.Fatalf("unexpected job packet count: %d", len(sink.pkts))
		}
		for i, invType := range []byte{1, 4} {
			p := sink.pkts[i]
			if len(p) != 4 || p[0] != 0x2b || p[1] != 0 || p[2] != invType || p[3] != 28 {
				t.Fatalf("capacity wasn't delivered before item: %x", p)
			}
		}
		if sink.opcodes()[3] != protocol.SendUpdateStats {
			t.Fatal("missing job/AP/SP stat refresh")
		}
	}
}
