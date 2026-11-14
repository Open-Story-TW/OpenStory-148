package world

import "testing"

func TestLateGameplayDepartureCannotDeleteReplacementOrReassignController(t *testing.T) {
	old, current, peer := &memSink{}, &memSink{}, &memSink{}
	key := mapKey{channel: 1, mapID: 40000}
	mob := &gameplayMob{controller: 1}
	s := &Server{gameplay: &gameplaySpace{rooms: map[mapKey]*gameplayRoom{key: {members: map[int64]*gameplayMember{1: {sink: current}, 2: {sink: peer}}, mobs: map[int32]*gameplayMob{5: mob}}}}}
	s.leaveGameplay(1, 40000, 1, old)
	if s.gameplay.rooms[key].members[1] == nil || mob.controller != 1 || len(peer.pkts) != 0 {
		t.Fatal("old connection deleted replacement gameplay membership")
	}
	s.leaveGameplay(1, 40000, 1, current)
	if s.gameplay.rooms[key].members[1] != nil || mob.controller != 2 {
		t.Fatal("matching connection could not leave/reassign controller")
	}
}
