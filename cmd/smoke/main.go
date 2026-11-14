// Smoke exercises the actual encrypted TCP login -> world flow on isolated ports.
package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/persistence"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
)

func require(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func check(err error) {
	if err != nil {
		panic(err)
	}
}
func randomHex(n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	check(err)
	return hex.EncodeToString(b)
}

func waitOpcode(c *protocol.Conn, want uint16, limit int) []byte {
	for i := 0; i < limit; i++ {
		p, err := c.ReadPacket()
		check(err)
		if op(p) == want {
			return p
		}
	}
	panic(fmt.Sprintf("opcode 0x%x not received within %d packets", want, limit))
}

func op(packet []byte) uint16 {
	require(len(packet) >= 2, "short packet")
	return binary.LittleEndian.Uint16(packet)
}
func expect(c *protocol.Conn, want uint16) []byte {
	p, err := c.ReadPacket()
	check(err)
	for (op(p) == protocol.SendClientConfig || op(p) == protocol.SendClientUI) && want != op(p) {
		p, err = c.ReadPacket()
		check(err)
	}
	require(op(p) == want, fmt.Sprintf("expected opcode 0x%x received 0x%x", want, op(p)))
	return p
}
func send(c *protocol.Conn, w *protocol.Writer) { check(w.Err()); check(c.WritePacket(w.Packet())) }
func dial(ctx context.Context, address string) *protocol.Conn {
	c, err := protocol.Dial(ctx, address)
	check(err)
	c.SetTimeouts(5*time.Second, 5*time.Second)
	w := protocol.NewWriter(protocol.RecvHello)
	w.U8(6)
	w.U16(protocol.Version)
	w.U16(1)
	send(c, w)
	expect(c, protocol.SendShowMapleStory)
	return c
}
func loginPacket(c *protocol.Conn, name, password string) {
	check(c.WritePacket(protocol.LoginRequest(name, password)))
}
func authenticated(ctx context.Context, cfg platform.Config, name, password string) *protocol.Conn {
	c := dial(ctx, net.JoinHostPort(cfg.Bind, fmt.Sprint(cfg.LoginPort)))
	loginPacket(c, name, password)
	p := expect(c, protocol.SendLoginStatus)
	require(len(p) > 3 && p[2] == 0, "login failed")
	for {
		p = expect(c, protocol.SendWorldList)
		if p[2] == 255 {
			break
		}
	}
	expect(c, protocol.SendRecommended)
	return c
}
func requestRoster(c *protocol.Conn, world int) []byte {
	w := protocol.NewWriter(protocol.RecvCharList)
	w.U8(0)
	w.U8(byte(world))
	w.U8(0)
	send(c, w)
	return expect(c, protocol.SendCharacterList)
}

type credentials struct {
	Account  string
	Password string
	Pin      string
}

func seedClient(ctx context.Context, store *persistence.Store) {
	path := filepath.Join(".local", "client-test.credentials.json")
	if _, err := os.Stat(path); err == nil {
		return
	}
	cred := credentials{Account: "golocal", Password: "Go145" + randomHex(4), Pin: "145145"}
	a, err := store.Register(ctx, cred.Account, cred.Password)
	check(err)
	check(store.ConfigureAccount(ctx, a.ID, 0, cred.Pin))
	b, err := json.MarshalIndent(cred, "", "  ")
	check(err)
	check(os.WriteFile(path, append(b, '\n'), 0600))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL", err)
		os.Exit(1)
	}
}
func run() (result error) {
	defer func() {
		if p := recover(); p != nil {
			result = fmt.Errorf("%v", p)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cfg, err := platform.Load("login")
	check(err)
	store, err := persistence.Open(ctx, persistence.Config{DatabaseURL: cfg.DatabaseURL, RedisAddr: cfg.RedisAddr, RedisPassword: cfg.RedisPassword, RedisDB: cfg.RedisDB, MaxOpenConns: 4})
	check(err)
	defer store.Close()
	checks := []string{}
	pass := func(s string) { fmt.Println("PASS", s); checks = append(checks, s) }
	check(store.CachePing(ctx))
	pass("PostgreSQL and authenticated Redis connections")
	for _, port := range []int{19080, 19081, 19082, 19083} {
		client := http.Client{Timeout: 3 * time.Second}
		resp, e := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health/ready", port))
		check(e)
		resp.Body.Close()
		require(resp.StatusCode == 200, "service not ready")
	}
	pass("login, cashshop and two independent worlds are ready")
	ports := []int{cfg.LoginPort, cfg.CashshopPort}
	for _, w := range cfg.Worlds {
		for ch := 0; ch < w.ChannelCount; ch++ {
			ports = append(ports, w.ChannelBasePort+ch)
		}
	}
	for _, port := range ports {
		c := dial(ctx, net.JoinHostPort(cfg.Bind, fmt.Sprint(port)))
		c.Close()
	}
	pass(fmt.Sprintf("%d isolated TMS145.1 handshakes and encrypted version exchanges", len(ports)))
	name := "gs" + randomHex(4)
	password := "Go145" + randomHex(4)
	pin := "145145"
	c := dial(ctx, net.JoinHostPort(cfg.Bind, fmt.Sprint(cfg.LoginPort)))
	loginPacket(c, name, password)
	expect(c, protocol.SendChooseGender)
	w := protocol.NewWriter(protocol.RecvSetGender)
	w.String(name)
	w.String(pin)
	w.U8(0)
	send(c, w)
	expect(c, protocol.SendGenderSet)
	c.Close()
	pass("TCP account registration, gender and PIN setup")
	c = dial(ctx, net.JoinHostPort(cfg.Bind, fmt.Sprint(cfg.LoginPort)))
	loginPacket(c, name, password+"bad")
	p := expect(c, protocol.SendLoginStatus)
	require(len(p) > 2 && p[2] == 4, "bad password accepted")
	c.Close()
	pass("incorrect password rejected")
	c = authenticated(ctx, cfg, name, password)
	defer c.Close()
	p = requestRoster(c, 0)
	require(len(p) > 3 && p[3] == 0, "new account roster not empty")
	w = protocol.NewWriter(protocol.RecvCheckName)
	w.String(name)
	send(c, w)
	p = expect(c, protocol.SendNameResult)
	require(p[len(p)-1] == 0, "fresh character name rejected")
	w = protocol.NewWriter(protocol.RecvCreate)
	w.String(name)
	w.I32(1)
	w.I16(0)
	w.Bytes([]byte{0, 0, 6})
	for _, v := range []int32{20100, 30000, 1040010, 1060002, 1072005, 1322005} {
		w.I32(v)
	}
	send(c, w)
	p = expect(c, protocol.SendCreateResult)
	require(len(p) > 7 && p[2] == 0, "character creation failed")
	characterID := int64(binary.LittleEndian.Uint32(p[3:7]))
	chr, e := store.GetCharacter(ctx, characterID)
	check(e)
	require(chr.Name == name && chr.GM == 0 && chr.MapID == 10000 && chr.Mesos == 1000 && len(chr.Equipment) == 4, "persisted character or starter gear invalid")
	for _, item := range chr.Equipment {
		require(item.ID > 0, "non-positive equipment ID")
	}
	require(len(chr.Items) == 1 && chr.Items[0].TemplateID == 2000000 && chr.Items[0].InvType == 2 && chr.Items[0].Slot == 1 && chr.Items[0].Quantity == 5, "starter USE red potions missing")
	pass("authenticated world RPC character creation and PostgreSQL starter equipment/inventory persistence")
	p = requestRoster(c, 0)
	require(len(p) > 3 && p[3] == 1, "roster not updated after creation")
	a, e := store.Authenticate(ctx, name, password)
	check(e)
	_, e = store.ListCharacters(ctx, a.ID, 0)
	check(e)
	readsBefore, hitsBefore := store.Stats()
	_, e = store.ListCharacters(ctx, a.ID, 0)
	check(e)
	readsAfter, hitsAfter := store.Stats()
	require(readsAfter == readsBefore && hitsAfter > hitsBefore, "roster cache failed to avoid query")
	pass("Redis roster cache hit avoids PostgreSQL roster query")
	w = protocol.NewWriter(protocol.RecvSelect)
	w.I32(int32(characterID))
	send(c, w)
	p = expect(c, protocol.SendServerIP)
	require(len(p) >= 14, "short redirect packet")
	address := net.JoinHostPort(net.IP(p[4:8]).String(), fmt.Sprint(binary.LittleEndian.Uint16(p[8:10])))
	handle := binary.LittleEndian.Uint32(p[10:14])
	require(int64(handle) != characterID && handle > 0, "handoff handle must be opaque")
	wc := dial(ctx, address)
	w = protocol.NewWriter(protocol.RecvLoggedIn)
	w.I32(int32(handle))
	send(wc, w)
	field := expect(wc, protocol.SendSetField)
	require(len(field) > 700, "world entry payload incomplete")
	pass("login redirects to independent world and consumes opaque one-use handoff")
	replay := dial(ctx, address)
	w = protocol.NewWriter(protocol.RecvLoggedIn)
	w.I32(int32(handle))
	send(replay, w)
	_, e = replay.ReadPacket()
	require(e != nil, "handoff replay accepted")
	replay.Close()
	pass("handoff replay rejected")
	// Meso ledger: drop then pickup with PG-backed idempotent balance.
	w = protocol.NewWriter(protocol.RecvMesoDrop)
	w.U32(1001)
	w.I32(100)
	check(w.Err())
	check(wc.WritePacket(w.Packet()))
	dropPkt := waitOpcode(wc, protocol.SendDropItem, 128)
	require(dropPkt[7] == 1, "drop was not meso")
	objectID := int32(binary.LittleEndian.Uint32(dropPkt[3:7]))
	_ = waitOpcode(wc, protocol.SendUpdateStats, 128)
	mid, e := store.GetCharacter(ctx, characterID)
	check(e)
	require(mid.Mesos == 900, fmt.Sprintf("after drop mesos=%d", mid.Mesos))
	w = protocol.NewWriter(protocol.RecvItemPickup)
	w.I32(0)
	w.U8(0)
	w.I16(0)
	w.I16(0)
	w.I32(objectID)
	check(w.Err())
	check(wc.WritePacket(w.Packet()))
	_ = waitOpcode(wc, protocol.SendRemoveItem, 128)
	_ = waitOpcode(wc, protocol.SendUpdateStats, 128)
	after, e := store.GetCharacter(ctx, characterID)
	check(e)
	require(after.Mesos == 1000, fmt.Sprintf("after pickup mesos=%d", after.Mesos))
	check(wc.WritePacket(w.Packet()))
	time.Sleep(150 * time.Millisecond)
	replayBal, e := store.GetCharacter(ctx, characterID)
	check(e)
	require(replayBal.Mesos == 1000, "pickup replay changed mesos")
	pass("meso drop/pickup ledger persists with no double credit")
	// A real encrypted USE_ITEM request changes both the native bag and HP.
	// Retry every request (including the last stack removal) with the same tick.
	for used := 1; used <= 5; used++ {
		w = protocol.NewWriter(protocol.RecvUseItem)
		w.U32(uint32(100 + used))
		w.I16(1)
		w.I32(2000000)
		for retry := 0; retry < 2; retry++ {
			check(wc.WritePacket(w.Packet()))
			inventory := waitOpcode(wc, protocol.SendModifyInventory, 128)
			require(len(inventory) >= 9 && inventory[3] == 1 && inventory[6] == 2, "invalid USE inventory response")
			if used == 5 {
				require(len(inventory) == 9 && inventory[5] == 3, "last potion did not clear slot")
			} else {
				require(len(inventory) == 11 && inventory[5] == 1 && int(binary.LittleEndian.Uint16(inventory[9:11])) == 5-used, "wrong potion quantity after use/replay")
			}
			hp := waitOpcode(wc, protocol.SendUpdateStats, 128)
			require(len(hp) == 17 && binary.LittleEndian.Uint64(hp[3:11]) == 0x400 && binary.LittleEndian.Uint32(hp[11:15]) == uint32(chr.MaxHP), "invalid capped HP response")
		}
	}
	emptyBag, e := store.GetCharacter(ctx, characterID)
	check(e)
	require(len(emptyBag.Items) == 0 && emptyBag.HP == emptyBag.MaxHP, "potion state did not persist")
	// Unsupported effects recover the action state without a mutation.
	w = protocol.NewWriter(protocol.RecvUseItem)
	w.U32(200)
	w.I16(1)
	w.I32(2000001)
	check(wc.WritePacket(w.Packet()))
	blockedUse := waitOpcode(wc, protocol.SendUpdateStats, 128)
	require(len(blockedUse) == 13 && binary.LittleEndian.Uint64(blockedUse[3:11]) == 0, "unsupported item did not unlock actions")
	pass("USE_ITEM consumes five red potions, caps HP, replays exactly once and clears final stack")
	check(wc.WritePacket(protocol.EncodeAbsolutePlayerMove(0, 0, 1234, -56)))
	time.Sleep(150 * time.Millisecond)
	wc.Close()
	c.Close()
	deadline := time.Now().Add(5 * time.Second)
	var reconnect domain.LoginSession
	for {
		reconnect, e = store.BeginLogin(ctx, a.ID, "smoke-reconnect-"+name)
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			check(e)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The probe login is explicitly released before subsequent client verification.
	check(store.EndLogin(ctx, reconnect))
	pass("world disconnect releases account for re-login")
	saved, e := store.GetCharacter(ctx, characterID)
	check(e)
	require(saved.X == 1234 && saved.Y == -56, fmt.Sprintf("disconnect checkpoint missing move: x=%d y=%d", saved.X, saved.Y))
	require(saved.Revision > chr.Revision, "disconnect checkpoint did not advance revision")
	require(len(saved.Items) == 0 && saved.HP == saved.MaxHP, "disconnect checkpoint restored consumed potions or old HP")
	pass("RecvMove stays in memory and disconnect checkpoint persists position")

	// Dual-client same-channel AOI: spawn exchange, move relay, remove on leave.
	nameB := "gb" + randomHex(4)
	passwordB := "Go145" + randomHex(4)
	cB := dial(ctx, net.JoinHostPort(cfg.Bind, fmt.Sprint(cfg.LoginPort)))
	loginPacket(cB, nameB, passwordB)
	expect(cB, protocol.SendChooseGender)
	w = protocol.NewWriter(protocol.RecvSetGender)
	w.String(nameB)
	w.String(pin)
	w.U8(0)
	send(cB, w)
	expect(cB, protocol.SendGenderSet)
	cB.Close()
	cA := authenticated(ctx, cfg, name, password)
	defer cA.Close()
	p = requestRoster(cA, 0)
	require(len(p) > 3 && p[3] >= 1, "account A roster empty")
	w = protocol.NewWriter(protocol.RecvSelect)
	w.I32(int32(characterID))
	send(cA, w)
	p = expect(cA, protocol.SendServerIP)
	addrA := net.JoinHostPort(net.IP(p[4:8]).String(), fmt.Sprint(binary.LittleEndian.Uint16(p[8:10])))
	handleA := binary.LittleEndian.Uint32(p[10:14])
	wcA := dial(ctx, addrA)
	w = protocol.NewWriter(protocol.RecvLoggedIn)
	w.I32(int32(handleA))
	send(wcA, w)
	expect(wcA, protocol.SendSetField)
	for i := 0; i < 4; i++ {
		_, e = wcA.ReadPacket()
		check(e)
	}
	// Bring A inside default AOI of a fresh spawn at (0,0).
	check(wcA.WritePacket(protocol.EncodeAbsolutePlayerMove(0, 0, 100, 0)))
	time.Sleep(100 * time.Millisecond)
	cB = authenticated(ctx, cfg, nameB, passwordB)
	defer cB.Close()
	w = protocol.NewWriter(protocol.RecvCheckName)
	w.String(nameB)
	send(cB, w)
	expect(cB, protocol.SendNameResult)
	w = protocol.NewWriter(protocol.RecvCreate)
	w.String(nameB)
	w.I32(1)
	w.I16(0)
	w.Bytes([]byte{0, 0, 6})
	for _, v := range []int32{20100, 30000, 1040010, 1060002, 1072005, 1322005} {
		w.I32(v)
	}
	send(cB, w)
	p = expect(cB, protocol.SendCreateResult)
	require(len(p) > 7 && p[2] == 0, "character B creation failed")
	charBID := int64(binary.LittleEndian.Uint32(p[3:7]))
	w = protocol.NewWriter(protocol.RecvSelect)
	w.I32(int32(charBID))
	send(cB, w)
	p = expect(cB, protocol.SendServerIP)
	addrB := net.JoinHostPort(net.IP(p[4:8]).String(), fmt.Sprint(binary.LittleEndian.Uint16(p[8:10])))
	handleB := binary.LittleEndian.Uint32(p[10:14])
	wcB := dial(ctx, addrB)
	w = protocol.NewWriter(protocol.RecvLoggedIn)
	w.I32(int32(handleB))
	send(wcB, w)
	expect(wcB, protocol.SendSetField)
	for i := 0; i < 4; i++ {
		_, e = wcB.ReadPacket()
		check(e)
	}
	spawnA := waitOpcode(wcA, protocol.SendSpawnPlayer, 128)
	require(binary.LittleEndian.Uint32(spawnA[2:6]) == uint32(charBID), "A did not spawn B")
	spawnB := waitOpcode(wcB, protocol.SendSpawnPlayer, 128)
	require(binary.LittleEndian.Uint32(spawnB[2:6]) == uint32(characterID), "B did not spawn A")
	check(wcA.WritePacket(protocol.EncodeAbsolutePlayerMove(0, 0, 222, 33)))
	moveB := waitOpcode(wcB, protocol.SendMovePlayer, 128)
	require(binary.LittleEndian.Uint32(moveB[2:6]) == uint32(characterID), "B did not receive A move")
	wcA.Close()
	removeB := waitOpcode(wcB, protocol.SendRemovePlayer, 128)
	require(binary.LittleEndian.Uint32(removeB[2:6]) == uint32(characterID), "B did not receive A remove")
	wcB.Close()
	pass("same-map AOI spawn/move/remove broadcast between two world clients")

	check(store.VerifyPIN(ctx, a.ID, pin))
	pass("secondary password stored as verified hash")
	seedClient(ctx, store)
	receipt := map[string]any{"verified_at": time.Now().UTC(), "result": "PASS for listed checks", "checks": checks, "test_account": name, "test_character_id": characterID, "world_packet_bytes": len(field), "gui_verified": false, "capacity_5000_verified": false, "handoff_scope": "loopback development with one-use int31 client handle"}
	body, e := json.MarshalIndent(receipt, "", "  ")
	check(e)
	check(os.MkdirAll(".local", 0700))
	check(os.WriteFile(filepath.Join(".local", "verification.json"), append(body, '\n'), 0600))
	fmt.Println("Go protocol integration checks passed. GUI account file: .local/client-test.credentials.json")
	return nil
}
