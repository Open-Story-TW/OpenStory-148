package persistence

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestIronBodySPCostPersistentBuffReplayReplacementAndExpiry(t *testing.T) {
	ctx, s, sessions := inventoryFixture(t)
	a, b := sessions[0], sessions[1]
	if _, err := s.db.ExecContext(ctx, `UPDATE game_characters SET job=100,level=10,sp=1,mp=30,base_max_mp=30,max_mp=30 WHERE id=$1`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	var err error
	a.Character, err = readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil {
		t.Fatal(err)
	}
	learned, err := s.AllocateSkill(ctx, domain.SkillAllocateRequest{Session: a, SkillID: 1001003, OperationID: fmt.Sprintf("iron-body-learn-%d", a.CharacterID)})
	if err != nil || learned.Character.SP != 0 {
		t.Fatalf("iron-body normal SP %+v %v", learned, err)
	}
	a.Character = learned.Character
	q := domain.SkillCastRequest{Session: a, SkillID: 1001003, ClientLevel: 2, OperationID: fmt.Sprintf("iron-body-cast-%d", a.CharacterID)}
	if _, err = s.CastSkill(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("client level override accepted: %v", err)
	}
	q.ClientLevel = 1
	r, err := s.CastSkill(ctx, q)
	if err != nil || r.Character.MP != 22 || len(r.Character.SkillBuffs) != 1 || len(r.Character.SkillCooldowns) != 0 {
		t.Fatalf("iron-body cast %+v %v", r, err)
	}
	buff := r.Character.SkillBuffs[0]
	if buff.ID != 1001003 || buff.Stat != "WDEF" || buff.Value != 10 || time.Until(buff.ExpiresAt) < 108*time.Second || time.Until(buff.ExpiresAt) > 111*time.Second {
		t.Fatalf("wrong source effect %+v", buff)
	}
	replay, err := s.CastSkill(ctx, q)
	if err != nil || !replay.Replayed || replay.Character.MP != 22 {
		t.Fatalf("iron-body replay %+v %v", replay, err)
	}
	loaded, err := readCharacterSQL(ctx, s.db, a.CharacterID)
	if err != nil || len(loaded.SkillBuffs) != 1 || loaded.SkillBuffs[0] != buff {
		t.Fatalf("buff lost on full reload %+v %v", loaded.SkillBuffs, err)
	}
	q.Session = b
	q.OperationID += "-other"
	if _, err = s.CastSkill(ctx, q); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("unlearned actor cast accepted: %v", err)
	}
	a.Character = r.Character
	q.Session = a
	q.OperationID += "-replace"
	r, err = s.CastSkill(ctx, q)
	if err != nil || r.Character.MP != 14 || len(r.Character.SkillBuffs) != 1 || len(r.Expired) != 1 {
		t.Fatalf("replacement/cost %+v %v", r, err)
	}
	a.Character = r.Character
	if _, err = s.db.ExecContext(ctx, `UPDATE game_skill_buffs SET expires_at=clock_timestamp()-interval '1 second' WHERE character_id=$1 AND stat='WDEF'`, a.CharacterID); err != nil {
		t.Fatal(err)
	}
	r, err = s.TickSkillBuffs(ctx, a)
	if err != nil || len(r.Character.SkillBuffs) != 0 || len(r.Expired) != 1 || r.Expired[0].Stat != "WDEF" || r.Character.MP != 14 {
		t.Fatalf("expiry %+v %v", r, err)
	}
}
