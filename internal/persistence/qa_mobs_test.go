package persistence

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func qaMobWZ(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "Mob.wz")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw := `<imgdir name="7654321.img"><imgdir name="info"><int name="maxHP" value="97"/><int name="exp" value="13"/><int name="level" value="8"/></imgdir><imgdir name="stand"><canvas name="0"><vector name="lt" x="-12" y="-22"/><vector name="rb" x="12" y="0"/></canvas></imgdir></imgdir>`
	if err := os.WriteFile(filepath.Join(dir, "7654321.img.xml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
}

func TestQAMobAuthorizationUsesWZBoundedGrantAndNoCharacterMutation(t *testing.T) {
	qaMobWZ(t)
	now := time.Now()
	c := gmPolicyCharacter()
	c.MapID = domain.QAMobMap
	before := c
	g := domain.QAGMGrant{Revision: 7, ExpiresAt: now.Add(30 * time.Second)}
	q := domain.GMCommandRequest{Name: "spawn", Args: []int64{7654321, 12}, RuntimeID: strings.Repeat("a", 32)}
	a, err := authorizeQAMobs(c, q, g, now)
	if err != nil || a.GrantRevision != 7 || !a.ExpiresAt.Equal(g.ExpiresAt) || !reflect.DeepEqual(c, before) {
		t.Fatal(a, err)
	}
	g.ExpiresAt = now.Add(time.Hour)
	a, err = authorizeQAMobs(c, q, g, now)
	if err != nil || !a.ExpiresAt.Equal(now.Add(domain.QAMobLifetime)) {
		t.Fatal(a, err)
	}
	for _, mutate := range []func(*domain.Character, *domain.GMCommandRequest){
		func(c *domain.Character, q *domain.GMCommandRequest) { c.HP = 0 },
		func(c *domain.Character, q *domain.GMCommandRequest) { c.MapID = 104010000 },
		func(c *domain.Character, q *domain.GMCommandRequest) { q.Args = []int64{7654321, 13} },
		func(c *domain.Character, q *domain.GMCommandRequest) { q.Args = []int64{7654321, 0} },
		func(c *domain.Character, q *domain.GMCommandRequest) { q.Args = []int64{7654322, 1} },
		func(c *domain.Character, q *domain.GMCommandRequest) { q.RuntimeID = "" },
		func(c *domain.Character, q *domain.GMCommandRequest) { q.RuntimeID = strings.Repeat("z", 32) },
	} {
		cc, qq := c, q
		mutate(&cc, &qq)
		if _, err = authorizeQAMobs(cc, qq, g, now); err == nil {
			t.Fatal("bad authority accepted", qq)
		}
	}
	q.Name = "clearmobs"
	q.Args = nil
	if _, err = authorizeQAMobs(c, q, g, now); err != nil {
		t.Fatal(err)
	}
}

func TestQAMobExplicitCenterAuthorizationAndAuditIdentity(t *testing.T) {
	qaMobWZ(t)
	now := time.Now()
	c := gmPolicyCharacter()
	c.MapID = domain.QAMobMap
	before := c
	g := domain.QAGMGrant{Revision: 7, ExpiresAt: now.Add(time.Minute)}
	q := domain.GMCommandRequest{Name: "spawn", Args: []int64{7654321, 2}, RuntimeID: strings.Repeat("a", 32)}
	legacyHash := gmCommandHash(q)
	seen := map[string]bool{legacyHash: true}
	for _, x := range []int64{-32000, -131, 0, 131, 32000} {
		q.Args = []int64{7654321, 2, x}
		a, err := authorizeQAMobs(c, q, g, now)
		if err != nil || a.RuntimeID != q.RuntimeID || a.GrantRevision != 7 || !a.ExpiresAt.Equal(g.ExpiresAt) || !reflect.DeepEqual(c, before) {
			t.Fatal("bounded X changed grant/character", x, a, err)
		}
		hash := gmCommandHash(q)
		if seen[hash] {
			t.Fatal("center/mode omitted from immutable audit identity", x)
		}
		seen[hash] = true
	}
	for _, args := range [][]int64{{7654321, 2, -32001}, {7654321, 2, 32001}, {7654321, 2, 0, 0}, {7654322, 2, 0}} {
		q.Args = args
		if _, err := authorizeQAMobs(c, q, g, now); err == nil {
			t.Fatal("invalid explicit command authorized", args)
		}
	}
}
