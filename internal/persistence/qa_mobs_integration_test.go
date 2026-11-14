package persistence

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestQAMobCommandsDurableAuthorizationReplayAndRevocation(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	// The schema fixture resets WZ_ROOT; install the reviewed mob afterward.
	qaMobWZ(t)
	a := actors[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET map_id=$1 WHERE id=$2`, domain.QAMobMap, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	a = grantQAActor(t, ctx, s, a)
	a.Character.MapID = domain.QAMobMap
	q := domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-spawn-%d-authorization", a.CharacterID), Name: "spawn", Args: []int64{7654321, 2}, RuntimeID: strings.Repeat("a", 32)}
	unknown := q
	unknown.OperationID += "-unknown-mob"
	unknown.Args = []int64{7654322, 2}
	denied, err := s.ExecuteGMCommand(ctx, unknown)
	if !errors.Is(err, domain.ErrForbidden) || !denied.Denied || denied.QAMobs != nil || denied.Changed {
		t.Fatalf("unknown WZ mob authorized: %+v %v", denied, err)
	}
	r, err := s.ExecuteGMCommand(ctx, q)
	if err != nil || r.QAMobs == nil || r.QAMobs.RuntimeID != q.RuntimeID || r.Changed || r.Character.Revision != a.Character.Revision || !r.QAMobs.ExpiresAt.After(time.Now()) {
		t.Fatalf("authorization %+v %v", r, err)
	}
	replay, err := s.ExecuteGMCommand(ctx, q)
	if err != nil || !replay.Replayed || replay.QAMobs == nil || !replay.QAMobs.ExpiresAt.Equal(r.QAMobs.ExpiresAt) {
		t.Fatal("replay renewed capability", replay, err)
	}
	resolved, found, err := s.ResolveGMCommand(ctx, q)
	if err != nil || !found || resolved.QAMobs == nil {
		t.Fatal("resolve", found, err)
	}
	changed := q
	changed.Args = []int64{7654321, 3}
	if _, err = s.ExecuteGMCommand(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("replay changed count", err)
	}
	clear := q
	clear.Name = "clearmobs"
	clear.Args = nil
	clear.OperationID += "-clear"
	if r, err = s.ExecuteGMCommand(ctx, clear); err != nil || r.QAMobs == nil {
		t.Fatal("clear authorization", err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE ops_qa_gm_grants SET expires_at=clock_timestamp()-interval '1 second' WHERE character_id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExecuteGMCommand(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("expired replay", err)
	}
	if _, _, err = s.ResolveGMCommand(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal("expired resolver", err)
	}
	var n int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM ops_qa_gm_commands WHERE character_id=$1 AND command IN('spawn','clearmobs') AND outcome='accepted'`, a.CharacterID).Scan(&n); err != nil || n != 2 {
		t.Fatal("durable audit", n, err)
	}
}

func TestQAMobExplicitCenterDurableReplayKeepsFullArgsAndLegacySeparate(t *testing.T) {
	ctx, s, actors := qaGMFixture(t)
	qaMobWZ(t)
	a := actors[0]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET map_id=$1 WHERE id=$2`, domain.QAMobMap, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	a = grantQAActor(t, ctx, s, a)
	a.Character.MapID = domain.QAMobMap
	for _, explicit := range []bool{false, true} {
		q := domain.GMCommandRequest{Session: a, OperationID: fmt.Sprintf("qa-center-%d-%t", a.CharacterID, explicit), Name: "spawn", Args: []int64{7654321, 2}, RuntimeID: strings.Repeat("a", 32)}
		if explicit {
			q.Args = append(q.Args, -131)
		}
		r, err := s.ExecuteGMCommand(ctx, q)
		if err != nil || r.QAMobs == nil || r.Changed || r.Character.Revision != a.Character.Revision {
			t.Fatal("authorization", explicit, r, err)
		}
		for _, resolve := range []bool{false, true} {
			var replay domain.GMCommandResult
			if resolve {
				var found bool
				replay, found, err = s.ResolveGMCommand(ctx, q)
				if !found {
					t.Fatal("lost immutable receipt")
				}
			} else {
				replay, err = s.ExecuteGMCommand(ctx, q)
			}
			if err != nil || !replay.Replayed || replay.QAMobs == nil || !replay.QAMobs.ExpiresAt.Equal(r.QAMobs.ExpiresAt) || fmt.Sprint(replay.Args) != fmt.Sprint(q.Args) {
				t.Fatal("replay renewed/lost center", replay, err)
			}
		}
		for _, args := range [][]int64{{7654321, 2}, {7654321, 2, -131}, {7654321, 2, 131}} {
			if fmt.Sprint(args) == fmt.Sprint(q.Args) {
				continue
			}
			changed := q
			changed.Args = args
			if _, err = s.ExecuteGMCommand(ctx, changed); !errors.Is(err, domain.ErrForbidden) {
				t.Fatal("same-key center/mode changed", args, err)
			}
			if _, found, err := s.ResolveGMCommand(ctx, changed); !found || !errors.Is(err, domain.ErrForbidden) {
				t.Fatal("resolver accepted changed center/mode", args, found, err)
			}
		}
	}
	var accepted int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM ops_qa_gm_commands WHERE character_id=$1 AND command='spawn' AND outcome='accepted'`, a.CharacterID).Scan(&accepted); err != nil || accepted != 2 {
		t.Fatal("audit lost old/new command distinction", accepted, err)
	}
}
