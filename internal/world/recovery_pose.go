package world

import (
	"maple-go-server/internal/domain"
	"maple-go-server/internal/protocol"
	"time"
)

// stable denotes a final zero-velocity settled candidate. HP interval checks
// remain separate; it does not assert a native physics-frame timestamp.
type recoveryPose struct {
	visit                      *sceneVisit
	known, stable, grounded    bool
	x, y, previousX, previousY int
	velocityX, velocityY       int16
	state                      byte
	foothold                   int16
}

func (p recoveryPose) allowsHP(visit *sceneVisit, c domain.Character) bool {
	// Native9156B3 compares current/previous position;450167 accepts action2/3
	// and55 (plus131, which is outside the byte stance representation). Riding
	// alternatives9/10 remain unsupported until actual riding-state proof exists.
	action := int(p.state >> 1)
	return p.visit == visit && p.known && p.stable && p.grounded && p.x == c.X && p.y == c.Y && p.velocityX == 0 && p.velocityY == 0 && (action == 2 || action == 3 || action == 55)
}

// Called only after ApplyPlayerMove succeeds, while retaining the same CID
// scene gate. Content ground geometry was prepared outside that gate. There is
// no g.mu acquisition and no registry lock across packet publication.
func (s *Server) observeRecoveryMovement(v *sceneVisit, snapshot domain.CharacterSession, move protocol.PlayerMove, ground func(int, int, int) bool, now time.Time) {
	if s.skills == nil || v == nil {
		return
	}
	s.skills.mu.Lock()
	defer s.skills.mu.Unlock()
	m := s.skills.members[snapshot.CharacterID]
	if m == nil || m.live != v.live || !sameDropSink(m.sink, v.sink) || m.sessionID != snapshot.SessionID || m.generation != snapshot.Generation || m.channel != snapshot.ChannelID || m.mapID != snapshot.Character.MapID {
		return
	}
	before := m.recoveryPose
	if move.Pose.MarkerOnly && before.visit == v && before.known && before.x == snapshot.Character.X && before.y == snapshot.Character.Y {
		// A pure command11 carries no new physics. Retain only proof already
		// bound to this exact visit and authoritative position; never use its
		// header to establish a missing pose or restart a recovery clock.
		return
	}
	p := recoveryPose{visit: v, x: snapshot.Character.X, y: snapshot.Character.Y, previousX: before.x, previousY: before.y, state: move.Pose.State, velocityX: move.Pose.VelocityX, velocityY: move.Pose.VelocityY, foothold: move.Pose.Foothold}
	p.known = move.Pose.Known && int(move.Pose.X) == p.x && int(move.Pose.Y) == p.y
	// This is a cadence-bound settled candidate, not a reconstruction of
	// native per-frame previous/current positions. A final reviewed zero-V
	// sample may settle after motion even if the last two wire XY differ.
	// Both HP sources must then wait their full interval from receipt below.
	p.stable = p.known && p.velocityX == 0 && p.velocityY == 0
	if move.Pose.HasPreviousAbsolute {
		p.previousX, p.previousY = int(move.Pose.PreviousX), int(move.Pose.PreviousY)
	}
	if p.known && ground != nil {
		fh := 0
		if move.Pose.HasFoothold {
			fh = int(move.Pose.Foothold)
			if fh <= 0 {
				p.known = false
			}
		}
		if p.known {
			p.grounded = ground(p.x, p.y, fh)
		}
	}
	m.recoveryPose = p
	newSettlement := p.allowsHP(v, snapshot.Character) && (!before.allowsHP(v, snapshot.Character) || move.Pose.Interrupted)
	if !p.allowsHP(v, snapshot.Character) || newSettlement {
		// Reset only the two HP source clocks. Preserve accepted native tick
		// watermarks so movement cannot reopen a replay; MP cadence is independent.
		// A newly settled candidate cannot accrue time from before receipt,
		// including a zero-V stop after motion/barrier within this packet.
		for source := 0; source < 2; source++ {
			m.regeneration[source][0].at = now
		}
	}
}
