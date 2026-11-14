// Movement packet layouts derived from the Legacy/OdinMS Java source (AGPLv3).
// Original OdinMS authors: Patrick Huy, Matthias Butz, Jan Christian Meyer.
package protocol

import "maple-go-server/internal/domain"

// PlayerMove is the parsed RecvMove body used for memory updates and peer relay.
// PlayerMovementPose retains only fields already decoded by the reviewed
// absolute fragment layouts. The ordinary command0 unknown short is not FH.
type PlayerMovementPose struct {
	Known                      bool
	X, Y, VelocityX, VelocityY int16
	State                      byte
	Foothold                   int16
	HasFoothold                bool
	// Only consecutive physical absolute samples may supply this pair;
	// reviewed command11 is a non-physical marker, not another sample.
	HasPreviousAbsolute  bool
	PreviousX, PreviousY int16
	// MarkerOnly carries no position or ground evidence. A world consumer may
	// retain an already known exact-visit pose, never derive one from headers.
	MarkerOnly bool
	// Clock continuity is interrupted by an unreviewed non-pose fragment, nonzero
	// absolute velocity, or position change between adjacent absolute samples.
	// A later zero-velocity pose may settle, but cannot inherit old elapsed time.
	Interrupted bool
}
type PlayerMove struct {
	Pose           PlayerMovementPose
	StartX, StartY int16
	X, Y           int
	Fragments      []byte // command count + fragments (Legacy serializeMovementList payload)
	Changed        bool
}

// ParsePlayerMove consumes a RecvMove body (opcode already read).
func ParsePlayerMove(r *Reader) (PlayerMove, error) {
	var out PlayerMove
	if r == nil {
		return out, ErrMalformed
	}
	r.Skip(1) // portal count
	r.Skip(4) // crc
	r.Skip(4) // tick count
	out.StartX = r.I16()
	out.StartY = r.I16()
	r.Skip(4)
	fragStart := r.Pos()
	x, y, ok := parseAbsolutePositionPose(r, &out.Pose)
	if r.Err() != nil {
		return out, r.Err()
	}
	out.Fragments = r.BytesBetween(fragStart, r.Pos())
	trailer := r.U8()
	for i := 0; i < int(trailer); i += 2 {
		r.U8()
	}
	r.Skip(8) // four trailing shorts from Legacy MovePlayer
	if r.Err() != nil {
		return out, r.Err()
	}
	if !ok {
		return out, nil
	}
	out.X = int(x)
	out.Y = int(y)
	out.Changed = true
	return out, nil
}

// ApplyPlayerMove updates the character's in-memory X/Y from the last absolute
// life movement. It never touches the database; callers decide when to checkpoint.
func ApplyPlayerMove(r *Reader, chr *domain.Character) (PlayerMove, error) {
	move, err := ParsePlayerMove(r)
	if err != nil || chr == nil {
		return move, err
	}
	if !move.Changed {
		return move, nil
	}
	if chr.X == move.X && chr.Y == move.Y {
		move.Changed = false
		return move, nil
	}
	chr.X = move.X
	chr.Y = move.Y
	return move, nil
}

// EncodeAbsolutePlayerMove builds a minimal RecvMove with one absolute fragment.
// Used by smoke/integration clients; production clients send their own paths.
func EncodeAbsolutePlayerMove(startX, startY, x, y int16) []byte {
	w := NewWriter(RecvMove)
	w.U8(0)
	w.I32(0)
	w.I32(0)
	w.I16(startX)
	w.I16(startY)
	w.I32(0)
	w.U8(1)
	w.U8(0)
	w.I16(x)
	w.I16(y)
	w.I16(0)
	w.I16(0)
	w.I16(0)
	w.I16(0)
	w.I16(0)
	w.U8(4)
	w.I16(30)
	w.U8(0)
	w.I16(0)
	w.I16(0)
	w.I16(0)
	w.I16(0)
	return w.Packet()
}

func parseAbsolutePosition(r *Reader) (x, y int16, ok bool) { return parseAbsolutePositionPose(r, nil) }
func parseAbsolutePositionPose(r *Reader, pose *PlayerMovementPose) (x, y int16, ok bool) {
	commands := int(r.U8())
	if commands < 0 || commands > 127 {
		r.err = ErrMalformed
		return 0, 0, false
	}
	found := false
	interrupted := false
	markerOnly := commands > 0
	for i := 0; i < commands; i++ {
		command := r.U8()
		if command == 11 {
			// Native5F173B jumps straight to fragment removal: no physics,
			// stance, duration or sample update. Do not manufacture a pair or
			// erase an interruption already proved by a real fragment.
			r.U8()
			if r.Err() != nil {
				return 0, 0, false
			}
			continue
		}
		markerOnly = false
		var previous PlayerMovementPose
		if pose != nil {
			previous = *pose
			*pose = PlayerMovementPose{}
		}
		switch command {

		case 0, 7, 14, 16, 45, 46:
			x, y = r.I16(), r.I16()
			vx, vy := r.I16(), r.I16()
			r.Skip(2) // Existing opaque unk, not claimed as foothold.
			var fh int16
			if command == 14 {
				fh = r.I16()
			}
			r.Skip(4)
			state := r.U8()
			r.Skip(2)
			if pose != nil {
				*pose = PlayerMovementPose{Known: true, X: x, Y: y, VelocityX: vx, VelocityY: vy, State: state, Foothold: fh, HasFoothold: command == 14}
			}
			found = true
		case 44:
			x, y = r.I16(), r.I16()
			vx, vy := r.I16(), r.I16()
			r.Skip(2)
			state := r.U8()
			r.Skip(2)
			if pose != nil {
				*pose = PlayerMovementPose{Known: true, X: x, Y: y, VelocityX: vx, VelocityY: vy, State: state}
			}
			found = true

		case 13:
			r.Skip(4)
			r.Skip(2)
			r.U8()
			r.Skip(2)
		case 1, 2, 15, 21, 40, 41, 42, 43:
			r.Skip(4)
			r.U8()
			r.Skip(2)
		case 18, 19:
			r.Skip(4)
			r.Skip(2)
			r.U8()
			r.Skip(2)
		case 3, 4, 5, 6, 8, 9, 10, 12:
			// Java TeleportMovement: an absolute landing position (rush,
			// teleport, jump-down), then foothold, state and duration.
			x = r.I16()
			y = r.I16()
			r.Skip(2)
			r.U8()
			r.Skip(2)
			found = true
		case 20:
			r.Skip(4)
			r.Skip(4)
			r.U8()
			r.Skip(2)
		// 17: casterMove rush (Aran 21100002, live 2026-11-02), followed by a
		// command 3 with the landing position; Java Legacy does not list it.
		case 17, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39:
			r.U8()
			r.Skip(2)
		default:
			r.err = ErrMalformed
			return 0, 0, false
		}
		if r.Err() != nil {
			return 0, 0, false
		}
		if pose != nil {
			if pose.Known {
				pose.HasPreviousAbsolute = previous.Known
				pose.PreviousX, pose.PreviousY = previous.X, previous.Y
				if pose.VelocityX != 0 || pose.VelocityY != 0 || previous.Known && (previous.X != pose.X || previous.Y != pose.Y) {
					interrupted = true
				}
			} else {
				interrupted = true
			}
			pose.Interrupted = interrupted
		}
	}
	if pose != nil {
		pose.MarkerOnly = markerOnly
	}
	return x, y, found
}
