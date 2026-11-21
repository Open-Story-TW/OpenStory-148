package world

import (
	"reflect"
	"testing"
	"time"

	"maple-go-server/internal/buffstat"
	"maple-go-server/internal/domain"
)

func TestLostStatsListsEveryStatAWholeBuffResetRemoved(t *testing.T) {
	before := domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 35001002, Stat: "MONSTER_RIDING"}, {ID: 35001002, Stat: "ENHANCED_WATK"}, {ID: 35121007, Stat: "MAPLE_WARRIOR"}}}
	after := domain.Character{SkillBuffs: []domain.SkillBuff{{ID: 35121007, Stat: "MAPLE_WARRIOR"}}}
	if got := lostStats(before, after); !reflect.DeepEqual(got, []buffstat.Stat{"MONSTER_RIDING", "ENHANCED_WATK"}) {
		t.Fatal(got)
	}
}

func TestQAUseItemParseAndPublishGiantPotion(t *testing.T) {
	name, args, handled := parseGMChat("!useitem 2003516")
	if !handled || name != "useitem" || len(args) != 1 || args[0] != 2003516 {
		t.Fatalf("parseGMChat(!useitem 2003516) = (%q, %v, %v)", name, args, handled)
	}
	if !domain.QAWarpAllowed(domain.QAPharaohMap) || !domain.IsQAMobField(domain.QAPharaohMap) {
		t.Fatalf("QAPharaohMap %d should be QA warp and mob field allowed", domain.QAPharaohMap)
	}
	s := &Server{}
	sink := &gameplayTestSink{}
	exp := time.Now().Add(60 * time.Second)
	c := domain.Character{
		ID:    101,
		MapID: 180000001,
		SkillBuffs: []domain.SkillBuff{
			{ID: -2003516, Level: 1, Stat: "GIANT_POTION", Value: 300, ExpiresAt: exp},
			{ID: -2003516, Level: 1, Stat: "SPEED", Value: 20, ExpiresAt: exp},
			{ID: -2003516, Level: 1, Stat: "JUMP", Value: 20, ExpiresAt: exp},
		},
	}
	sess := domain.CharacterSession{CharacterID: 101, ChannelID: 1, Character: c}
	s.publishQAItemBuff(sess, &liveCharacter{session: sess}, sink, c, 2003516)
	if sink.count(0x2d) != 1 {
		t.Fatalf("expected 1 TemporaryStatSet (0x2d) packet, got %d (total %d)", sink.count(0x2d), len(sink.packets))
	}
}
