package world

import (
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestDragonSoaringUsesTheCharactersJobAndQuest(t *testing.T) {
	character := domain.Character{Job: 2412, Level: 120,
		Quests: []domain.QuestState{{ID: 3758, State: 2}, {ID: 3759, State: 1}},
	}
	x := domain.ScriptExchange{Growth: &domain.ScriptGrowth{Skills: []domain.ScriptSkill{{ID: 1026, Level: 1, Master: 1}}}}
	if setDragonSoaringSkill(2081000, character, x) {
		t.Fatal("Soaring was awarded at the village elder rather than the Sky Gate NPC")
	}
	if !setDragonSoaringSkill(2085000, character, x) || x.Growth.Skills[0].ID != 20031026 {
		t.Fatalf("Phantom Soaring award: %+v", x.Growth.Skills)
	}
	character.Skills = []domain.SkillState{{ID: 1026, Level: 1}, {ID: 20031026, Level: 1, ExpiresAt: time.Now().Add(-time.Minute)}}
	if knowsSoaring(character) {
		t.Fatal("accepted the wrong class or an expired copy of Soaring")
	}
	character.Skills[1].ExpiresAt = time.Time{}
	if !knowsSoaring(character) {
		t.Fatal("rejected the character's learned Soaring")
	}
	character.Quests[1].State = 2
	if setDragonSoaringSkill(2085000, character, x) {
		t.Fatal("a completed quest could award Soaring again")
	}
}
