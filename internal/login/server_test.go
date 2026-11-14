package login

import (
	"context"
	"errors"
	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
	"maple-go-server/internal/protocol"
	"net"
	"testing"
	"time"
)

func creationPacket(job int32, sub int16, gender, count byte, items ...int32) []byte {
	w := protocol.NewWriter(protocol.RecvCreate)
	w.String("GoTester")
	w.I32(job)
	w.I16(sub)
	w.U8(gender)
	w.U8(0)
	w.U8(count)
	if len(items) == 0 {
		items = []int32{20100, 30000, 1040010, 1060002, 1072005, 1322005}
	}
	for _, n := range items {
		w.I32(n)
	}
	return w.Packet()
}

func TestParseOnlySupportedAuthenticatedCreation(t *testing.T) {
	resistance := []int32{20100, 30000, 1050173, 1072005, 1322005}
	for _, tc := range []struct {
		name          string
		job           int32
		sub           int16
		gender, count byte
		items         []int32
		family        int
		ok            bool
	}{
		{"normal", 1, 0, 0, 6, nil, 0, true},
		{"cygnus", 2, 0, 0, 6, nil, 1000, true},
		{"cannoneer", 1, 2, 0, 6, nil, 0, true},
		{"resistance-overall", 0, 0, 0, 5, resistance, 3000, true},
		{"resistance-six-slots", 0, 0, 0, 6, nil, 0, false},
		{"mercedes-blocked-by-client", 5, 0, 0, 5, resistance, 0, false},
		{"demon-blocked-by-client", 6, 0, 0, 7, append(resistance, 1, 2), 0, false},
		{"unknown-sub-job", 1, 3, 0, 6, nil, 0, false},
		{"gender-mismatch", 1, 0, 1, 6, nil, 0, false},
		{"invalid-count", 1, 0, 0, 5, nil, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := protocol.NewReader(creationPacket(tc.job, tc.sub, tc.gender, tc.count, tc.items...))
			r.U16()
			req, err := parseCreate(r, domain.Account{ID: 42, Gender: 0}, 1)
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
			if tc.ok && (req.AccountID != 42 || req.WorldID != 1 || req.Face != 20100 || req.Weapon != 1322005 || req.Family != tc.family || req.SubJob != int(tc.sub)) {
				t.Fatalf("unexpected creation %+v", req)
			}
			if tc.ok && tc.family == 3000 && (req.Top != 1050173 || req.Bottom != 0 || req.Shoes != 1072005) {
				t.Fatalf("resistance slots %+v", req)
			}
		})
	}
	r := protocol.NewReader(creationPacket(1, 0, 0, 6)[:12])
	r.U16()
	if _, err := parseCreate(r, domain.Account{ID: 42, Gender: 0}, 0); err == nil {
		t.Fatal("truncated creation accepted")
	}
}

func TestUnauthenticatedCharacterOperationsFailBeforeRepository(t *testing.T) {
	s := &Server{}
	for _, op := range []uint16{protocol.RecvCharList, protocol.RecvCheckName, protocol.RecvCreate, protocol.RecvSelect, protocol.RecvRedisplay} {
		if err := s.dispatch(context.Background(), nil, &state{}, op, protocol.NewReader(nil), &net.TCPAddr{}); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("opcode %x err=%v", op, err)
		}
	}
}

type staleLoginRepository struct {
	domain.Repository
	touches int
	err     error
}

func (r *staleLoginRepository) TouchLogin(context.Context, domain.LoginSession) error {
	r.touches++
	return r.err
}

func TestReplacedOrExpiredLoginCannotUseCharacterOperations(t *testing.T) {
	for _, op := range []uint16{protocol.RecvCharList, protocol.RecvCheckName, protocol.RecvCreate, protocol.RecvSelect, protocol.RecvRedisplay} {
		repo := &staleLoginRepository{err: domain.ErrStaleOwner}
		s := &Server{repo: repo}
		st := &state{account: domain.Account{ID: 42}, session: &domain.LoginSession{AccountID: 42, SessionID: "replaced-session-0001", Generation: 1}}
		err := s.dispatch(context.Background(), nil, st, op, protocol.NewReader(nil), &net.TCPAddr{})
		if !errors.Is(err, domain.ErrStaleOwner) || repo.touches != 1 {
			t.Fatalf("opcode %x: err=%v touches=%d", op, err, repo.touches)
		}
	}
}

func TestRedirectedLoginCannotCreateMoreCharacters(t *testing.T) {
	repo := &staleLoginRepository{}
	s := &Server{repo: repo}
	st := &state{redirected: true, session: &domain.LoginSession{AccountID: 42, SessionID: "redirected-session-0001", Generation: 1}}
	if err := s.dispatch(context.Background(), nil, st, protocol.RecvCreate, protocol.NewReader(nil), &net.TCPAddr{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err=%v", err)
	}
	if repo.touches != 0 {
		t.Fatal("redirected socket should be rejected before touching session")
	}
}

type observedDeadlineConn struct {
	net.Conn
	deadlines chan<- time.Time
}

func (c observedDeadlineConn) SetReadDeadline(deadline time.Time) error {
	select {
	case c.deadlines <- deadline:
	default:
	}
	return c.Conn.SetReadDeadline(deadline)
}

func TestIdleNativeLoginSocketReceivesGUIFriendlyDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	deadlines := make(chan time.Time, 2)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{cfg: platform.Config{Worlds: []platform.WorldConfig{{ID: 0}}}}
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		s.handle(ctx, observedDeadlineConn{Conn: conn, deadlines: deadlines})
	}()
	dialCtx, dialCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dialCancel()
	client, err := protocol.Dial(dialCtx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case deadline := <-deadlines:
		remaining := time.Until(deadline)
		if remaining < 14*time.Minute || remaining > 15*time.Minute {
			t.Fatalf("idle read deadline=%s; expected approximately15minutes", remaining)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native login socket did not receive a read deadline")
	}
	client.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("closed login connection did not clean up")
	}
}
