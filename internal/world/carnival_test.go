package world

import (
	"testing"
	"time"

	"maple-go-server/internal/content"
	"maple-go-server/internal/domain"
)

func TestCarnivalInvitationTaiwanHours(t *testing.T) {
	cases := []struct {
		hour, minute int
		want         bool
	}{
		{15, 45, false},
		{16, 0, false},
		{16, 15, true},
		{16, 16, false},
		{16, 45, true},
		{19, 45, true},
		{20, 15, true},
		{20, 45, false},
		{21, 15, false},
	}
	for _, tc := range cases {
		// Pass UTC so realm hours remain Taiwan hours on any host.
		now := time.Date(2012, time.August, 15, tc.hour, tc.minute, 31, 0, carnivalTaiwanTime).UTC()
		window := carnivalInvitationTime(now)
		open := !window.IsZero()
		if open != tc.want {
			t.Fatalf("Taiwan %02d:%02d open=%v, want %v", tc.hour, tc.minute, open, tc.want)
		}
		if tc.want && !window.Equal(now.Truncate(time.Minute)) {
			t.Fatalf("Taiwan %02d:%02d has incorrect start %v", tc.hour, tc.minute, window)
		}
	}
}

func TestCarnivalLegacyRequiresOptInAndSceneOwner(t *testing.T) {
	session := domain.CharacterSession{CharacterID: 123, SessionID: "current"}
	state := &carnivalState{entrants: map[int64]*carnivalPlayer{
		session.CharacterID: {owner: &sceneVisit{owner: session}, legacy: true},
	}}
	space := gameplaySpace{carnival: state}
	if space.carnivalEntryLocked(session, content.CarnivalSecondLobby) {
		t.Fatal("retired lobby should stay closed with no opt-in")
	}
	state.legacy = make(map[mapKey]*carnivalLegacyQueue)
	if !space.carnivalEntryLocked(session, content.CarnivalSecondLobby) {
		t.Fatal("registered owner could not enter enabled legacy lobby")
	}
	session.SessionID = "different"
	if space.carnivalEntryLocked(session, content.CarnivalSecondLobby) {
		t.Fatal("a different session was admitted through an old registration")
	}
}

func TestCarnivalLegacyExperienceRankBoundaries(t *testing.T) {
	for _, tc := range []struct {
		score int
		win   int64
	}{
		{1, 15000}, {100, 15000}, {101, 25000}, {250, 25000},
		{251, 35000}, {500, 35000}, {501, 48000},
	} {
		if winner, loser := carnivalLegacyExperience(tc.score, true), carnivalLegacyExperience(tc.score, false); winner != tc.win || loser != tc.win/2 {
			t.Errorf("score %d: winner=%d loser=%d, want %d and %d", tc.score, winner, loser, tc.win, tc.win/2)
		}
	}
}
