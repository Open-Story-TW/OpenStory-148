package world

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"maple-go-server/internal/combat"
	"maple-go-server/internal/content"
	"maple-go-server/internal/protocol"
)

func TestMobPowerUpRequiresNativeOfferAndRespectsMagicCrash(t *testing.T) {
	root := t.TempDir()
	book := filepath.Join(root, "Skill.wz", "MobSkill.img.xml")
	if err := os.MkdirAll(filepath.Dir(book), 0700); err != nil {
		t.Fatal(err)
	}
	xml := `<imgdir name="MobSkill.img"><imgdir name="100"><imgdir name="level"><imgdir name="1"><int name="x" value="115"/><int name="mpCon" value="5"/><int name="interval" value="40"/><int name="time" value="30"/></imgdir></imgdir></imgdir></imgdir>`
	if err := os.WriteFile(book, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := content.NewCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(6000, 0)
	action := content.MobSkillAction{ID: content.MobSkillPowerUp, Level: 1, Action: 1}
	mob := gameplayMob{objectID: 123, hp: 100, stats: content.Mob{HP: 100, MP: 10, Skills: []content.MobSkillAction{action}}}
	owner, outsider := &gameplayMember{}, &gameplayMember{}
	request := protocol.MonsterMove{UseSkill: true}
	confirmation := protocol.MonsterMove{Action: 44, SkillData: 100 | 1<<8}
	if _, accepted := mob.powerUpFromMove(catalog, owner, confirmation, at); accepted {
		t.Fatal("accepted skill without proposal")
	}
	if offer, _ := mob.powerUpFromMove(catalog, owner, request, at); offer != action {
		t.Fatal("native controller did not receive the WZ power-up proposal")
	}
	if _, accepted := mob.powerUpFromMove(catalog, outsider, confirmation, at); accepted {
		t.Fatal("another controller consumed the proposal")
	}
	mob.powerUpFromMove(catalog, owner, request, at)
	if _, accepted := mob.powerUpFromMove(catalog, owner, confirmation, at); !accepted {
		t.Fatal("original action and rank did not match the world offer")
	}
	packet := mob.applyPowerUpLocked(at)
	st := mob.activeMobStatus(combat.MobPowerUp, at)
	if len(packet) < 2 || binary.LittleEndian.Uint16(packet) != protocol.SendApplyMonsterStatus ||
		st == nil || st.Value != 115 || st.SkillID != 100|1<<16 || mob.currentMP() != 5 {
		t.Fatalf("power-up status / MP mismatch: %v, %v, %d", packet, st, mob.currentMP())
	}
	delete(mob.statuses, combat.MobPowerUp)
	mob.statuses[combat.MobMagicCrash] = &combat.ActiveStatus{ExpiresAt: at.Add(49 * time.Second)}
	if offer, _ := mob.powerUpFromMove(catalog, owner, request, at.Add(41*time.Second)); offer.ID != 0 {
		t.Fatal("Magic Crash allowed a power-up after cooldown")
	}
	if offer, _ := mob.powerUpFromMove(catalog, owner, request, at.Add(50*time.Second)); offer != action {
		t.Fatal("power-up did not become eligible after Magic Crash expired")
	}
}
