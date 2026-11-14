package content

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryDefinitionRejectsUnimplementedEffects(t *testing.T) {
	apple, err := RecoveryDefinition("", 2010007)
	if err != nil || apple.HP != 30 || apple.MP != 0 {
		t.Fatalf("apple=%+v %v", apple, err)
	}
	if _, err = RecoveryDefinition("", 9999999); err == nil {
		t.Fatal("unknown recovery accepted")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "Item.wz", "Consume")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := `<imgdir name="0200.img">
<imgdir name="02000100"><imgdir name="spec"><int name="hpR" value="70"/><int name="mpR" value="20"/></imgdir></imgdir>
<imgdir name="02000101"><imgdir name="spec"><int name="hp" value="50"/><int name="speed" value="20"/></imgdir></imgdir>
<imgdir name="02000102"><imgdir name="spec"><int name="hpR" value="101"/></imgdir></imgdir>
</imgdir>`
	if err = os.WriteFile(filepath.Join(dir, "0200.img.xml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := RecoveryDefinition(root, 2000100)
	if err != nil || value.HPRate != 70 || value.MPRate != 20 {
		t.Fatalf("rates=%+v %v", value, err)
	}
	for _, id := range []int{2000101, 2000102} {
		if _, err = RecoveryDefinition(root, id); err == nil {
			t.Fatalf("accepted unsupported recovery %d", id)
		}
	}
}

func TestConsumableDefinitionReadsTimedStats(t *testing.T) {
	r, buff, err := ConsumableDefinition("", 2002000)
	if err != nil || r != (Recovery{}) || buff == nil || buff.Values["eva"] != 100 || buff.Duration.Seconds() != 180 {
		t.Fatalf("embedded pill %+v %+v %v", r, buff, err)
	}
	if _, err = RecoveryDefinition("", 2002000); err == nil {
		t.Fatal("RecoveryDefinition accepted a timed item")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "Item.wz", "Consume")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := `<imgdir name="0202.img">
<imgdir name="02020000"><imgdir name="spec"><int name="hp" value="300"/><int name="pad" value="5"/><int name="speed" value="10"/><int name="time" value="60000"/></imgdir></imgdir>
<imgdir name="02020001"><imgdir name="spec"><int name="pad" value="5"/></imgdir></imgdir>
<imgdir name="02020002"><imgdir name="spec"><int name="pad" value="5"/><int name="morph" value="1"/><int name="time" value="60000"/></imgdir></imgdir>
<imgdir name="02020003"><imgdir name="spec"><int name="time" value="60000"/></imgdir></imgdir>
</imgdir>`
	if err = os.WriteFile(filepath.Join(dir, "0202.img.xml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	r, buff, err = ConsumableDefinition(root, 2020000)
	if err != nil || r.HP != 300 || buff == nil || buff.Values["pad"] != 5 || buff.Values["speed"] != 10 || buff.Duration.Seconds() != 60 {
		t.Fatalf("hp+buff %+v %+v %v", r, buff, err)
	}
	// No time, an unsupported key, or a time without a stat stays rejected.
	for _, id := range []int{2020001, 2020002, 2020003} {
		if _, _, err = ConsumableDefinition(root, id); err == nil {
			t.Fatalf("accepted %d", id)
		}
	}
}
