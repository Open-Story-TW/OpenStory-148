package persistence

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"maple-go-server/internal/domain"
)

func TestCreateCharacterOpensEveryClientRace(t *testing.T) {
	root := os.Getenv("MAPLEGO_WZ_ROOT")
	usePrivateSchema(t, "jobs")
	if root == "" {
		t.Skip("family start maps and gear need MAPLEGO_WZ_ROOT")
	}
	t.Setenv("MAPLEGO_WZ_ROOT", root)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	s, err := Open(ctx, Config{DatabaseURL: os.Getenv("MAPLEGO_DATABASE_URL"), RedisAddr: os.Getenv("MAPLEGO_REDIS_ADDR"), RedisPassword: os.Getenv("MAPLEGO_REDIS_PASSWORD"), MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	suffix, err := randomToken(4)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireWorldLease(ctx, 121, "jobs-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.ReleaseWorldLease(context.Background(), lease) })
	account, err := s.Register(ctx, "jb"+suffix, "TestPassword145")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureAccount(ctx, account.ID, 0, "145145"); err != nil {
		t.Fatal(err)
	}
	login, err := s.BeginLogin(ctx, account.ID, "jobs-login-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	explorer := domain.CreateCharacterRequest{Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005}
	for i, tc := range []struct {
		name             string
		look             domain.CreateCharacterRequest
		family, sub      int
		job, mapID, wdef int
		pieces           int
	}{
		{"Res", domain.CreateCharacterRequest{Face: 20401, Hair: 30027, Top: 1050174, Shoes: 1072037, Weapon: 1312004}, 3000, 0, 3000, 931000000, -1, 3},
		{"Cyg", explorer, 1000, 0, 1000, 130030000, -1, 4},
		{"Aran", domain.CreateCharacterRequest{Face: 20100, Hair: 30000, Top: 1042167, Bottom: 1062115, Shoes: 1072383, Weapon: 1442079}, 2000, 0, 2000, 914000000, -1, 4},
		{"Evan", domain.CreateCharacterRequest{Face: 20100, Hair: 30000, Top: 1042180, Bottom: 1060138, Shoes: 1072418, Weapon: 1302132}, 2001, 0, 2001, 900090004, -1, 4},
		{"Cann", explorer, 0, 2, 0, 3000600, -1, 4},
		{"Dual", explorer, 0, 1, 0, 10000, 3, 4},
	} {
		r := tc.look
		r.LoginSession, r.Lease, r.AccountID, r.WorldID = login, lease, account.ID, 121
		r.Name, r.Family, r.SubJob, r.OperationID = fmt.Sprintf("%s%s", tc.name, suffix), tc.family, tc.sub, fmt.Sprintf("jobs-create-%s-%d", suffix, i)
		c, err := s.CreateCharacter(ctx, r)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		loaded, err := s.GetCharacter(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []domain.Character{c, loaded} {
			if v.Job != tc.job || v.SubJob != tc.sub || v.MapID != tc.mapID || len(v.Equipment) != tc.pieces || v.SPBooks != (domain.SPBookTable{}) {
				t.Fatalf("%s created %+v", tc.name, v)
			}
		}
		if tc.mapID != 10000 && c.X == -192 && c.Y == 210 {
			t.Fatalf("%s kept Maple Island coordinates", tc.name)
		}
		if tc.wdef >= 0 && equipmentAt(c, -5).WDEF != tc.wdef {
			t.Fatalf("%s explorer top %+v", tc.name, equipmentAt(c, -5))
		}
	}
	if _, err = s.CreateCharacter(ctx, domain.CreateCharacterRequest{LoginSession: login, Lease: lease, AccountID: account.ID, WorldID: 121, Name: "Bad" + suffix, Family: 2000, Face: 20100, Hair: 30000, Top: 1040010, Bottom: 1060002, Shoes: 1072005, Weapon: 1322005, OperationID: "jobs-create-bad-" + suffix}); err == nil {
		t.Fatal("explorer gear accepted for an Aran")
	}
	var books string
	if err = s.db.QueryRowContext(ctx, `UPDATE game_characters SET sp_books='{0,4,0,0,0,0,0,0,0,0}' WHERE name=$1 RETURNING array_to_string(sp_books,',')`, "Evan"+suffix).Scan(&books); err != nil || books != "0,4,0,0,0,0,0,0,0,0" {
		t.Fatalf("sp_books %q %v", books, err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE game_characters SET sp_books='{0,256,0,0,0,0,0,0,0,0}' WHERE name=$1`, "Evan"+suffix); err == nil {
		t.Fatal("sp_books accepted a byte overflow")
	}
}
