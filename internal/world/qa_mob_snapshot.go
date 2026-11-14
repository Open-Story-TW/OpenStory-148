package world

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"maple-go-server/internal/domain"
)

type qaMobSnapshotSpawn struct {
	ObjectID  int32  `json:"object_id"`
	SpawnHash string `json:"spawn_hash"`
}

type qaMobSnapshotExpected struct {
	BootHash  string               `json:"boot_hash"`
	VisitHash string               `json:"visit_hash"`
	Spawns    []qaMobSnapshotSpawn `json:"spawns"`
}

type qaMobSnapshotRequest struct {
	WorldID          int                    `json:"world_id"`
	WorldEpoch       int64                  `json:"world_epoch"`
	ChannelID        int                    `json:"channel_id"` // Backend channel, not native channel_id.
	MapID            int                    `json:"map_id"`
	OwnerCharacterID int64                  `json:"owner_character_id"`
	Batch            string                 `json:"batch"`
	ObjectIDs        []int32                `json:"object_ids"`
	Expected         *qaMobSnapshotExpected `json:"expected,omitempty"`
}

type qaMobSnapshotMob struct {
	ObjectID          int32     `json:"object_id"`
	MonsterID         int       `json:"monster_id"`
	SpawnHash         string    `json:"spawn_hash"`
	HP                int64     `json:"hp"`
	MaxHP             int64     `json:"max_hp"`
	X                 int       `json:"x"`
	Y                 int       `json:"y"`
	Foothold          int       `json:"foothold"`
	ServerController  int64     `json:"server_controller_character_id"`
	Claiming          bool      `json:"claiming"`
	Reserved          bool      `json:"reserved"`
	Held              bool      `json:"held"`
	HeldUntil         time.Time `json:"held_until"`
	ExpiresAt         time.Time `json:"expires_at"`
	Defeated          bool      `json:"defeated"`
	EvidenceEligible  bool      `json:"evidence_eligible"`
	IneligibleReasons []string  `json:"ineligible_reasons"`
}

// This is a detached point-in-time runtime observation, not a durable receipt or
// proof that HP never changed between two equal observations. No player position
// is inferred from sceneVisit.owner/qaOwner.owner: those contain map-only state.
type qaMobSnapshotResponse struct {
	Complete           bool               `json:"complete"`
	RuntimeOnly        bool               `json:"runtime_only"`
	ObservedAt         time.Time          `json:"observed_at"`
	WorldID            int                `json:"world_id"`
	WorldEpoch         int64              `json:"world_epoch"`
	ServerInstanceHash string             `json:"server_instance_hash"`
	ChannelID          int                `json:"channel_id"`
	MapID              int                `json:"map_id"`
	OwnerCharacterID   int64              `json:"owner_character_id"`
	OwnerGeneration    int64              `json:"owner_generation"`
	BootHash           string             `json:"boot_hash"`
	VisitHash          string             `json:"visit_hash"`
	BatchHash          string             `json:"batch_hash"`
	EvidenceEligible   bool               `json:"evidence_eligible"`
	Mobs               []qaMobSnapshotMob `json:"mobs"`
}

func qaSnapshotHash(kind, value string) string {
	return combatEvidenceHash("qa-mob-snapshot-v1\x00" + kind + "\x00" + value)
}

func validQASnapshotHash(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 32
}

func validQASnapshotToken(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 16
}

func (q qaMobSnapshotRequest) valid() bool {
	if q.WorldID < 0 || q.WorldID > 254 || q.WorldEpoch <= 0 || q.ChannelID < 1 || q.ChannelID > 20 || q.MapID != domain.QAMobMap || q.OwnerCharacterID <= 0 || len(q.Batch) < 16 || len(q.Batch) > 256 || len(q.ObjectIDs) < 1 || len(q.ObjectIDs) > domain.QAMobMaxBatch {
		return false
	}
	ids := make(map[int32]bool, len(q.ObjectIDs))
	for _, id := range q.ObjectIDs {
		if id <= 0 || id >= 999999999 || ids[id] {
			return false
		}
		ids[id] = true
	}
	if q.Expected != nil {
		if !validQASnapshotHash(q.Expected.BootHash) || !validQASnapshotHash(q.Expected.VisitHash) || len(q.Expected.Spawns) != len(ids) {
			return false
		}
		seen := make(map[int32]bool, len(ids))
		for _, spawn := range q.Expected.Spawns {
			if !ids[spawn.ObjectID] || seen[spawn.ObjectID] || !validQASnapshotHash(spawn.SpawnHash) {
				return false
			}
			seen[spawn.ObjectID] = true
		}
	}
	return true
}

// The configured health listener is loopback-only; check the actual peer too.
// The operator token authorizes observation only, never a QA command or write.
func (s *Server) qaMobSnapshot(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || len(s.cfg.RPCToken) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.RPCToken)) != 1 {
		debugJSON(w, http.StatusUnauthorized, map[string]any{"complete": false, "error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		debugJSON(w, http.StatusMethodNotAllowed, map[string]any{"complete": false, "error": "method_not_allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req qaMobSnapshotRequest
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || !req.valid() {
		debugJSON(w, http.StatusBadRequest, map[string]any{"complete": false, "error": "invalid_request"})
		return
	}
	result, err := s.snapshotQAMobs(req)
	if err != nil {
		debugJSON(w, http.StatusConflict, map[string]any{"complete": false, "error": "snapshot_scope_unavailable"})
		return
	}
	// Neither scene gate nor map/registry/live locks span encoding or HTTP IO.
	debugJSON(w, http.StatusOK, result)
}

func qaSnapshotLeaseCurrent(lease domain.WorldLease, req qaMobSnapshotRequest, owner domain.CharacterSession, now time.Time) bool {
	return lease.WorldID == req.WorldID && lease.Epoch == req.WorldEpoch && lease.InstanceID != "" && now.Before(lease.ExpiresAt) &&
		lease.WorldID == owner.Lease.WorldID && lease.InstanceID == owner.Lease.InstanceID && lease.Epoch == owner.Lease.Epoch
}

func (s *Server) snapshotQAMobs(req qaMobSnapshotRequest) (qaMobSnapshotResponse, error) {
	if !req.valid() || s.gameplay == nil || !s.ready.Load() || req.WorldID != s.cfg.WorldID {
		return qaMobSnapshotResponse{}, domain.ErrStaleOwner
	}
	// Resolve, then release the registry lock before waiting on the CID gate.
	s.mu.RLock()
	visit := s.scenes[req.OwnerCharacterID]
	s.mu.RUnlock()
	if visit == nil {
		return qaMobSnapshotResponse{}, domain.ErrStaleOwner
	}
	unlock, err := s.lockScenePublication(req.OwnerCharacterID)
	if err != nil {
		return qaMobSnapshotResponse{}, err
	}
	defer unlock()
	if !s.sceneCurrent(visit) || visit.owner.ChannelID != req.ChannelID || visit.owner.Character.MapID != req.MapID ||
		!qaSnapshotLeaseCurrent(s.currentLease(), req, visit.owner, time.Now()) {
		return qaMobSnapshotResponse{}, domain.ErrStaleOwner
	}
	g := s.gameplay
	g.mu.Lock()
	result, err := g.copyQAMobSnapshotLocked(req, visit, time.Now().UTC())
	g.mu.Unlock()
	if err != nil {
		return qaMobSnapshotResponse{}, err // Never return the partially copied set.
	}
	if !s.ready.Load() || !s.sceneCurrent(visit) || !qaSnapshotLeaseCurrent(s.currentLease(), req, visit.owner, time.Now()) {
		return qaMobSnapshotResponse{}, domain.ErrStaleOwner
	}
	return result, nil
}

// Caller holds the exact owner CID gate and gameplay.mu. There are no nested
// registry/live/persistence locks, SQL, catalog reads, packets or RNG here.
func (g *gameplaySpace) copyQAMobSnapshotLocked(req qaMobSnapshotRequest, visit *sceneVisit, now time.Time) (qaMobSnapshotResponse, error) {
	room := g.rooms[mapKey{req.ChannelID, req.MapID}]
	if room == nil || !validQASnapshotToken(g.bootID) || !validQASnapshotToken(visit.token) {
		return qaMobSnapshotResponse{}, domain.ErrStaleOwner
	}
	m := room.members[req.OwnerCharacterID]
	if m == nil || m.visit != visit || m.live != visit.live || !sameDropSink(m.sink, visit.sink) || m.sessionID != visit.owner.SessionID || m.generation != visit.owner.Generation || m.worldID != visit.owner.Lease.WorldID {
		return qaMobSnapshotResponse{}, domain.ErrStaleOwner
	}
	out := qaMobSnapshotResponse{Complete: true, RuntimeOnly: true, ObservedAt: now, WorldID: req.WorldID, WorldEpoch: req.WorldEpoch,
		ServerInstanceHash: qaSnapshotHash("instance", visit.owner.Lease.InstanceID), ChannelID: req.ChannelID, MapID: req.MapID,
		OwnerCharacterID: req.OwnerCharacterID, OwnerGeneration: visit.owner.Generation, BootHash: qaSnapshotHash("boot", g.bootID),
		VisitHash: qaSnapshotHash("visit", visit.token), BatchHash: qaSnapshotHash("batch", req.Batch), EvidenceEligible: true,
		Mobs: make([]qaMobSnapshotMob, 0, len(req.ObjectIDs))}
	if req.Expected != nil && (!strings.EqualFold(req.Expected.BootHash, out.BootHash) || !strings.EqualFold(req.Expected.VisitHash, out.VisitHash)) {
		return qaMobSnapshotResponse{}, domain.ErrStaleOwner
	}
	ids := slices.Clone(req.ObjectIDs)
	slices.Sort(ids)
	for _, id := range ids {
		mob := room.mobs[id]
		if mob == nil || mob.objectID != id || mob.qaOwner == nil {
			return qaMobSnapshotResponse{}, domain.ErrStaleOwner
		}
		a := mob.qaOwner
		if a.retired || !now.Before(a.expires) || a.member != m || !sameSceneOwner(a.owner, visit.owner) || a.owner.Character.MapID != req.MapID || a.batch != req.Batch {
			return qaMobSnapshotResponse{}, domain.ErrStaleOwner
		}
		spawnHash := combatEvidenceHash(fmt.Sprintf("mob-%s-%d-%d-%d", g.bootID, req.ChannelID, req.MapID, id))
		if req.Expected != nil {
			for _, expected := range req.Expected.Spawns {
				if expected.ObjectID == id && !strings.EqualFold(expected.SpawnHash, spawnHash) {
					return qaMobSnapshotResponse{}, domain.ErrStaleOwner
				}
			}
		}
		row := qaMobSnapshotMob{ObjectID: id, MonsterID: mob.stats.ID, SpawnHash: spawnHash, HP: mob.hp, MaxHP: mob.stats.HP,
			X: mob.life.X, Y: mob.life.Y, Foothold: mob.life.Foothold, ServerController: mob.controller,
			Claiming: mob.claiming, Reserved: mob.reservation != nil, Held: mob.qaHeld, HeldUntil: mob.qaHeldUntil.UTC(),
			ExpiresAt: a.expires.UTC(), Defeated: mob.hp <= 0, EvidenceEligible: true, IneligibleReasons: []string{}}
		for _, condition := range []struct {
			present bool
			reason  string
		}{{row.Claiming, "claiming"}, {row.Reserved, "reserved"}, {row.Held, "held"}, {row.Defeated, "defeated"}, {row.MaxHP <= 0 || row.HP < 0 || row.HP > row.MaxHP, "invalid_hp"}} {
			if condition.present {
				row.EvidenceEligible = false
				row.IneligibleReasons = append(row.IneligibleReasons, condition.reason)
			}
		}
		out.EvidenceEligible = out.EvidenceEligible && row.EvidenceEligible
		out.Mobs = append(out.Mobs, row)
	}
	return out, nil
}
