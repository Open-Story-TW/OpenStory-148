package world

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"maple-go-server/internal/scripting"
)

type firstJobWorldRepo struct {
	domain.Repository
	calls   int
	request domain.JobAdvancementRequest
	err     error
}

func (r *firstJobWorldRepo) AdvanceFirstJob(_ context.Context, q domain.JobAdvancementRequest) (domain.JobAdvancementResult, error) {
	r.calls++
	r.request = q
	if r.err != nil {
		return domain.JobAdvancementResult{}, r.err
	}
	c := q.Session.Character
	c.Job = q.TargetJob
	c.Revision++
	c.SP++
	return domain.JobAdvancementResult{InventoryResult: domain.InventoryResult{Character: c}, ToJob: q.TargetJob}, nil
}

func TestFirstJobHostGenericHasNoCapacityPacketsAndRefreshesPeerLook(t *testing.T) {
	for _, failure := range []bool{false, true} {
		r := &firstJobWorldRepo{}
		if failure {
			r.err = domain.ErrInsufficient
		}
		key := mapKey{channel: 1, mapID: 103000003}
		self, peer := &memSink{}, &memSink{}
		live := &liveCharacter{session: domain.CharacterSession{AccountID: 1, CharacterID: 1, SessionID: "first-job-native-session", Generation: 3, ChannelID: 1, Character: domain.Character{ID: 1, Job: 0, Level: 10, MapID: key.mapID, HP: 50, MaxHP: 50, MP: 5, MaxMP: 5, Revision: 1}}}
		s := &Server{repo: r, online: map[int64]domain.CharacterSession{}, gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{key: {npcs: map[int32]content.Life{50: {TemplateID: 1052001}}}}}, maps: newMapSpace(800)}
		s.maps.rooms[key] = map[int64]*mapMember{1: {id: 1, sink: self, visible: map[int64]struct{}{2: {}}}, 2: {id: 2, sink: peer, visible: map[int64]struct{}{1: {}}}}
		seedBoundScene(t, s, live, self)
		identity := scripting.Identity{SessionID: live.session.SessionID, CharacterID: 1, Generation: 3}
		h := &worldScript{server: s, live: live, sink: self, npcID: 1052001, objectID: 50, identity: identity}
		result, err := h.Execute(context.Background(), identity, scripting.Command{Kind: "advance_first_job", Script: "npc/1052001.lua", ID: "first-job-server-operation", Ints: []int64{400}})
		if err != nil || r.calls != 1 || r.request.TargetJob != 400 || r.request.Script != "npc/1052001.lua" {
			t.Fatalf("typed source command: %+v %v", r.request, err)
		}
		if failure {
			if result.Value.(map[string]any)["ok"] != false || live.session.Character.Job != 0 || len(self.pkts) != 0 || len(peer.pkts) != 0 {
				t.Fatal("failed advancement emitted packets or changed live state")
			}
			continue
		}
		if result.Value.(map[string]any)["ok"] != true || live.session.Character.Job != 400 || len(self.pkts) != 1 || self.opcodes()[0] != protocol.SendUpdateStats {
			t.Fatalf("generic source emitted fictitious reward/capacity: %+v %+v", result, self.opcodes())
		}
		if len(peer.pkts) != 1 || !bytes.Equal(peer.pkts[0], protocol.CharacterLook(live.session.Character)) || s.maps.rooms[key][1].look.Job != 400 {
			t.Fatal("visible peer or tracked look retained beginner job")
		}
		live.session.Character.X += 5000 // beyond npcReach
		if _, err = h.Execute(context.Background(), identity, scripting.Command{Kind: "advance_first_job", Script: "npc/1052001.lua", ID: "first-job-walked-away", Ints: []int64{400}}); !errors.Is(err, domain.ErrForbidden) || r.calls != 1 {
			t.Fatalf("NPC context was not revalidated: %v calls=%d", err, r.calls)
		}
	}
}
