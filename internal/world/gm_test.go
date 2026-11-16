package world

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"maple-go-server/internal/domain"
	"maple-go-server/internal/platform"
)

type gmTestRepo struct {
	domain.Repository
	execute func(domain.GMCommandRequest) (domain.GMCommandResult, error)
	resolve func(domain.GMCommandRequest) (domain.GMCommandResult, bool, error)
	grant   func(domain.QAGMGrantRequest) (domain.QAGMGrantResult, error)
}

func (r *gmTestRepo) ExecuteGMCommand(_ context.Context, q domain.GMCommandRequest) (domain.GMCommandResult, error) {
	return r.execute(q)
}
func (r *gmTestRepo) ResolveGMCommand(_ context.Context, q domain.GMCommandRequest) (domain.GMCommandResult, bool, error) {
	return r.resolve(q)
}
func (r *gmTestRepo) AuthorizeQAGM(_ context.Context, q domain.QAGMGrantRequest) (domain.QAGMGrantResult, error) {
	return r.grant(q)
}

func gmWorldFixture(t *testing.T) (*Server, *liveCharacter, *gameplayTestSink, *gmTestRepo) {
	t.Helper()
	lease := domain.WorldLease{WorldID: 1, InstanceID: "gm-test", Epoch: 2}
	actor := domain.CharacterSession{AccountID: 3, CharacterID: 4, SessionID: "gm-scope", Generation: 5, ChannelID: 1, Lease: lease, Character: domain.Character{ID: 4, AccountID: 3, WorldID: 1, MapID: 10000, Level: 10, HP: 5, MaxHP: 50, MP: 2, MaxMP: 10, Revision: 1, BaseMaxHP: 50, BaseMaxMP: 10, VitalsModel: 1}}
	live := &liveCharacter{session: actor}
	sink := &gameplayTestSink{}
	repo := &gmTestRepo{}
	s := &Server{repo: repo, skills: newSkillSpace(), online: map[int64]domain.CharacterSession{4: actor}, lease: lease}
	seedBoundScene(t, s, live, sink)
	if err := s.enterSkills(context.Background(), 1, live, sink); err != nil {
		t.Fatal(err)
	}
	return s, live, sink, repo
}

func TestGMChatStrictSelfOnlyGrammarAndWarpAllowlistBeforeStore(t *testing.T) {
	for _, text := range []string{"!level -1", "!level +10", "!level 1e2", "!level 0x10", "!heal 123", "!skill 1001 3 101", "!warp 999999999999999999999", "!!heal", "!GM"} {
		name, _, handled := parseGMChat(text)
		if !handled || name != "" {
			t.Fatalf("grammar accepted %q", text)
		}
	}
	if _, _, handled := parseGMChat("ordinary !heal speech"); handled {
		t.Fatal("ordinary speech intercepted")
	}
	s, live, sink, repo := gmWorldFixture(t)
	repo.execute = func(domain.GMCommandRequest) (domain.GMCommandResult, error) {
		t.Fatal("non-allowlisted warp reached repository")
		return domain.GMCommandResult{}, nil
	}
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!warp 910000000"); !handled || err != nil || live.session.Character.MapID != 10000 {
		t.Fatal("warp changed field", err)
	}
	// An allowlisted field still needs the store's QA grant and audit.
	called := false
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		called = q.Name == "warp" && len(q.Args) == 1 && q.Args[0] == 104000000
		return domain.GMCommandResult{Denied: true, Reason: "not_authorized"}, domain.ErrForbidden
	}
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!warp 104000000"); !handled || err != nil || !called || live.session.Character.MapID != 10000 {
		t.Fatal("denied warp changed field", err)
	}
}

func TestGMChatOrdinaryAccountDeniedWithoutRankShortcut(t *testing.T) {
	s, live, sink, repo := gmWorldFixture(t)
	called := false
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		called = true
		if q.Session.Character.GM != 0 {
			t.Fatal("host promoted ordinary rank")
		}
		return domain.GMCommandResult{Denied: true}, domain.ErrForbidden
	}
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!heal"); !handled || err != nil || !called || live.session.Character.HP != 5 || sink.count(0x1f) != 0 {
		t.Fatal("normal authority denied incorrectly", err)
	}
}

func TestGMCommitReplacementCannotPublishOrMutateNewOwner(t *testing.T) {
	s, live, sink, repo := gmWorldFixture(t)
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		newOwner := q.Session
		newOwner.Generation++
		newOwner.SessionID = "replacement"
		newOwner.Character.HP = 7
		s.mu.Lock()
		s.online[4] = newOwner
		s.mu.Unlock()
		c := q.Session.Character
		c.Revision++
		c.HP = 50
		return domain.GMCommandResult{Character: c, Changed: true}, nil
	}
	before := len(sink.packets)
	if _, err := s.handleGMChat(context.Background(), live, sink, "!heal"); !errors.Is(err, domain.ErrStaleOwner) {
		t.Fatal("replacement not fenced", err)
	}
	if s.online[4].SessionID != "replacement" || s.online[4].Character.HP != 7 || live.session.Character.HP != 5 || len(sink.packets) != before {
		t.Fatal("old result replaced owner or leaked frames")
	}
}

func TestGMUncertainCommitResolvesSameIDWithoutReexecution(t *testing.T) {
	s, live, sink, repo := gmWorldFixture(t)
	var original domain.GMCommandRequest
	calls, resolves := 0, 0
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		calls++
		original = q
		return domain.GMCommandResult{}, errors.New("unknown commit")
	}
	repo.resolve = func(q domain.GMCommandRequest) (domain.GMCommandResult, bool, error) {
		resolves++
		if q.OperationID != original.OperationID {
			t.Fatal("new op after unknown commit")
		}
		c := q.Session.Character
		c.Revision++
		c.HP = c.MaxHP
		return domain.GMCommandResult{Character: c, Changed: true, Replayed: true}, true, nil
	}
	if _, err := s.handleGMChat(context.Background(), live, sink, "!heal"); err != nil || calls != 1 || resolves != 1 || live.session.Character.HP != 50 {
		t.Fatalf("resolve calls=%d resolves=%d HP%d %v", calls, resolves, live.session.Character.HP, err)
	}
}

func TestGMCommitPreservesNewMovementAndDoesNotFakeCast(t *testing.T) {
	s, live, sink, repo := gmWorldFixture(t)
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		live.mu.Lock()
		live.session.Character.X = 80
		live.dirty = true
		live.mu.Unlock()
		c := q.Session.Character
		c.Revision++
		c.Skills = []domain.SkillState{{ID: 1002, Level: 3}}
		return domain.GMCommandResult{Character: c, Changed: true, SkillID: 1002}, nil
	}
	if _, err := s.handleGMChat(context.Background(), live, sink, "!skill 1002 3"); err != nil || live.session.Character.X != 80 || !live.dirty || sink.count(0x2d) != 0 || sink.count(0x154) != 0 {
		t.Fatal("movement lost or synthetic cast published", err)
	}
}

func TestGMBusyTransitionRejectsBeforeRepository(t *testing.T) {
	s, live, sink, repo := gmWorldFixture(t)
	live.mapEntry = &mapEntryQueue{pending: &mapEntryRequest{}}
	repo.execute = func(domain.GMCommandRequest) (domain.GMCommandResult, error) {
		t.Fatal("busy scene reached repository")
		return domain.GMCommandResult{}, nil
	}
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!heal"); !handled || err != nil || live.session.Character.HP != 5 {
		t.Fatal("busy command accepted", err)
	}
}

func TestGMUncertainCommitUnresolvedNeverClaimsSuccess(t *testing.T) {
	s, live, sink, repo := gmWorldFixture(t)
	repo.execute = func(domain.GMCommandRequest) (domain.GMCommandResult, error) {
		return domain.GMCommandResult{}, errors.New("unknown commit")
	}
	repo.resolve = func(domain.GMCommandRequest) (domain.GMCommandResult, bool, error) {
		return domain.GMCommandResult{}, false, errors.New("receipt unavailable")
	}
	before := len(sink.packets)
	if _, err := s.handleGMChat(context.Background(), live, sink, "!heal"); err == nil || live.session.Character.HP != 5 || len(sink.packets) != before {
		t.Fatal("unresolved outcome changed state or claimed success", err)
	}
}

func TestGMOperatorEndpointAuthenticationAndStrictBody(t *testing.T) {
	const token = "test-operator-key-must-be-32-characters"
	calls := 0
	repo := &gmTestRepo{grant: func(q domain.QAGMGrantRequest) (domain.QAGMGrantResult, error) {
		calls++
		if q.Actor != "local-operator" {
			t.Fatal("operator identity spoofed")
		}
		return domain.QAGMGrantResult{}, domain.ErrAlreadyExists
	}}
	s := &Server{cfg: platform.Config{RPCToken: token}, repo: repo}
	for _, tc := range []struct {
		method, auth, body string
		status             int
	}{
		{"POST", "", `{}`, 401}, {"GET", "Bearer " + token, `{}`, 405},
		{"POST", "Bearer " + token, `{"actor":"spoof"}`, 400},
		{"POST", "Bearer " + token, `{} {}`, 400},
		{"POST", "Bearer " + token, `{"action":"grant"}`, 409},
	} {
		r := httptest.NewRequest(tc.method, "/admin/gm-grants", strings.NewReader(tc.body))
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		s.gmAuthorizations(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), token) {
			t.Fatalf("HTTP status%d want%d body%s", w.Code, tc.status, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("invalid request reached grant", calls)
	}
	s.cfg.RPCToken = "short"
	r := httptest.NewRequest(http.MethodPost, "/admin/gm-grants", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer short")
	w := httptest.NewRecorder()
	s.gmAuthorizations(w, r)
	if w.Code != 401 || calls != 1 {
		t.Fatal("short operator token accepted")
	}
}

func TestGMChatCashExpiryGrammarAndFeedback(t *testing.T) {
	for _, text := range []string{"!cashexpire", "!cashexpire 5", "!cashexpire 5 -1", "!cashexpire 5 60 1", "!cashexpire 0x5 60"} {
		if name, _, handled := parseGMChat(text); !handled || name != "" {
			t.Fatalf("grammar accepted %q", text)
		}
	}
	if name, args, _ := parseGMChat("!cashexpire 1234567890123 60"); name != "cashexpire" || len(args) != 2 || args[0] != 1234567890123 || args[1] != 60 {
		t.Fatalf("parse %q %v", name, args)
	}
	s, live, sink, repo := gmWorldFixture(t)
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		if q.Name != "cashexpire" || q.Session.CharacterID != 4 || len(q.Args) != 2 || q.Args[0] != 77 || q.Args[1] != 60 {
			t.Fatalf("request %+v", q)
		}
		return domain.GMCommandResult{Character: live.session.Character, Name: q.Name, Args: q.Args, CashExpiry: &domain.GMCashExpiry{CashID: 77, TemplateID: 5072000, ExpiresAt: 1000}}, nil
	}
	revision := live.session.Character.Revision
	sink.mu.Lock()
	sent := len(sink.packets)
	sink.mu.Unlock()
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!cashexpire 77 60"); !handled || err != nil || live.session.Character.Revision != revision {
		t.Fatal("cash expiry handling", err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	// Only the feedback line: no character, inventory or stat republication.
	if len(sink.packets) != sent+1 || !strings.Contains(string(sink.packets[sent]), "locker serial 77") {
		t.Fatalf("packets %d->%d", sent, len(sink.packets))
	}
}

func TestGMChatDenialIsFollowedByItsCause(t *testing.T) {
	s, live, sink, repo := gmWorldFixture(t)
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		return domain.GMCommandResult{Denied: true, Reason: "unsupported_or_invalid_command", Detail: "skill 3201005 max level is 20"}, domain.ErrForbidden
	}
	before := sink.count(0x58)
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!skill 3201005 99"); !handled || err != nil {
		t.Fatal(err)
	}
	if sink.count(0x58)-before != 2 {
		t.Fatalf("denial plus cause lines: %d", sink.count(0x58)-before)
	}
	if name, args, _ := parseGMChat("!stats 4 4 400 60"); name != "stats" || len(args) != 4 {
		t.Fatalf("stats grammar %q %v", name, args)
	}
}

func TestGMChatHelpListsEveryCommand(t *testing.T) {
	help := ""
	for page := 1; page <= len(gmHelpPages); page++ {
		lines, ok := gmHelpLines(page)
		if !ok {
			t.Fatal("page", page)
		}
		help += strings.Join(lines, "\n") + "\n"
	}
	// One valid line per command: adding a command to the parser without
	// listing it in !help should fail here.
	samples := []string{"!heal", "!whereami", "!effstats", "!clearmobs", "!help", "!help 2", "!online", "!rates", "!level 1", "!job 1", "!warp 1", "!buffreset 1", "!hp 1",
		"!ap 1", "!sp 1", "!map 1", "!npc 1", "!cash 1", "!points 1", "!petfood 1", "!mesos 1", "!alliance 1", "!mobhold 1", "!item 1", "!debuff 1", "!skill 1 1",
		"!cashexpire 1 1", "!petexpire 1 1", "!vitals 1 1", "!spawn 1 1", "!stats 4 4 4 4", "!goto Someone", "!warphere Someone", "!dc Someone", "!notice two words",
		"!fame 1", "!hair 30000", "!face 20000", "!skin 1", "!hide 1", "!god 1", "!maxskills", "!killall", "!cleardrops",
		"!ban Someone", "!ban Someone 7", "!unban Someone", "!banip Someone", "!unbanip 1.2.3.4", "!rate exp 2", "!oxquiz open"}
	known := map[string]bool{}
	for _, text := range samples {
		name, _, _, _ := parseGMCommand(text)
		if name == "" || !strings.Contains(help, "!"+name) {
			t.Fatalf("%q is not a listed command", text)
		}
		known[name] = true
	}
	// And the other way: every listed command is one the parser accepts.
	listed := 0
	for _, page := range gmHelpPages {
		for _, e := range page.entries {
			listed++
			if !known[strings.TrimPrefix(strings.Fields(e.usage)[0], "!")] {
				t.Fatalf("help lists %q, which no sample parses", e.usage)
			}
		}
	}
	if listed != len(known) {
		t.Fatalf("%d listed, %d parsed", listed, len(known))
	}
	for _, text := range []string{"!help 1 2", "!notice", "!goto", "!dc two names", "!map x", "!ban", "!ban Someone 0", "!ban Someone x", "!rate gold 2", "!rate exp 0", "!god 2 2", "!killall 1"} {
		if name, _, _, handled := parseGMCommand(text); !handled || name != "" {
			t.Fatalf("grammar accepted %q", text)
		}
	}
	if _, _, arg, _ := parseGMCommand("!notice  server  restarts at 3 "); arg != "server  restarts at 3" {
		t.Fatalf("notice text %q", arg)
	}
	s, live, sink, repo := gmWorldFixture(t)
	want := 0
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		if q.Name != "help" || len(q.Args) != want {
			t.Fatalf("request %+v", q)
		}
		return domain.GMCommandResult{Character: live.session.Character, Name: q.Name}, nil
	}
	menu, _ := gmHelpLines(0)
	before := sink.count(0x58)
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!help"); !handled || err != nil || sink.count(0x58)-before != len(menu) {
		t.Fatal("help menu", err, sink.count(0x58)-before)
	}
	want = 1
	page, _ := gmHelpLines(5)
	before = sink.count(0x58)
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!help 5"); !handled || err != nil || sink.count(0x58)-before != len(page) {
		t.Fatal("help page", err, sink.count(0x58)-before)
	}
	if !strings.Contains(strings.Join(page, "\n"), "★!dc") || strings.Contains(strings.Join(page, "\n"), "★!online") {
		t.Fatalf("standing marks %q", page)
	}
	// A page that does not exist is answered without asking the store.
	repo.execute = func(domain.GMCommandRequest) (domain.GMCommandResult, error) {
		t.Fatal("unknown page reached the store")
		return domain.GMCommandResult{}, nil
	}
	before = sink.count(0x58)
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!help 9"); !handled || err != nil || sink.count(0x58)-before != 1 {
		t.Fatal("unknown page", err)
	}
	// Without a grant the list stays hidden behind the ordinary denial.
	repo.execute = func(domain.GMCommandRequest) (domain.GMCommandResult, error) {
		return domain.GMCommandResult{Denied: true, Reason: "not_authorized"}, domain.ErrForbidden
	}
	before = sink.count(0x58)
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!help"); !handled || err != nil || sink.count(0x58)-before != 1 {
		t.Fatal("ungranted help", err)
	}
}

func TestGMStandingCommandsCarryTextAndActAfterTheStore(t *testing.T) {
	for name, ok := range map[string]bool{"notice": true, "dc": true, "goto": true, "warphere": true, "map": true, "npc": true, "cash": true, "points": true, "warp": false, "online": false, "item": false} {
		if domain.GMStandingCommand(name) != ok {
			t.Fatal(name)
		}
	}
	for _, c := range []struct {
		name string
		args []int64
		text string
		ok   bool
	}{{"notice", nil, "hello all", true}, {"notice", nil, " ", false}, {"notice", nil, "a\nb", false}, {"notice", []int64{1}, "x", false},
		{"dc", nil, "Name", true}, {"dc", nil, "two names", false}, {"goto", nil, "", false}, {"map", []int64{100000000}, "", true}, {"map", []int64{1}, "x", false},
		{"cash", []int64{domain.GMWalletMax}, "", true}, {"cash", []int64{domain.GMWalletMax + 1}, "", false}, {"points", []int64{0}, "", false}, {"heal", nil, "", false}} {
		if domain.ValidGMStandingCommand(c.name, c.args, c.text) != c.ok {
			t.Fatalf("%s %v %q", c.name, c.args, c.text)
		}
	}
	s, live, sink, repo := gmWorldFixture(t)
	var got domain.GMCommandRequest
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		got = q
		return domain.GMCommandResult{Denied: true, Reason: "standing_grant_required", Detail: "this command needs a standing GM grant from the web admin"}, domain.ErrForbidden
	}
	before := sink.count(0x58)
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!dc Nobody"); !handled || err != nil || got.Name != "dc" || got.Text != "Nobody" || len(got.Args) != 0 {
		t.Fatalf("dc request %+v %v", got, err)
	}
	if sink.count(0x58)-before != 2 {
		t.Fatalf("denial plus reason: %d", sink.count(0x58)-before)
	}
	// Authorized: the effect runs and reports. Nobody by that name is online.
	req := domain.GMCommandRequest{Session: live.session, OperationID: "gm-native-test", Name: "dc", Text: "Nobody"}
	before = sink.count(0x58)
	s.gmStanding(live.session, live, sink, req)
	if sink.count(0x58)-before != 1 {
		t.Fatal("missing target feedback")
	}
	req.Name, req.Text = "notice", "hello all"
	before, notices := sink.count(0x58), sink.count(0x63)
	s.gmStanding(live.session, live, sink, req)
	if sink.count(0x58)-before != 1 || sink.count(0x63) < notices {
		t.Fatal("notice feedback")
	}
	lines := s.gmOnlineLines()
	if len(lines) != 2 || !strings.Contains(lines[0], "1 人") {
		t.Fatalf("online %q", lines)
	}
}

func TestGMChatEffstatsEchoesServerStatsWithoutChanges(t *testing.T) {
	for _, text := range []string{"!effstats 1", "!effstats x"} {
		if name, _, handled := parseGMChat(text); !handled || name != "" {
			t.Fatalf("grammar accepted %q", text)
		}
	}
	s, live, sink, repo := gmWorldFixture(t)
	live.session.Character.STR, live.session.Character.INT = 4, 321
	s.online[4] = live.session
	repo.execute = func(q domain.GMCommandRequest) (domain.GMCommandResult, error) {
		if q.Name != "effstats" || len(q.Args) != 0 {
			t.Fatalf("request %+v", q)
		}
		return domain.GMCommandResult{Character: live.session.Character, Name: q.Name}, nil
	}
	revision := live.session.Character.Revision
	before := sink.count(0x58)
	if handled, err := s.handleGMChat(context.Background(), live, sink, "!effstats"); !handled || err != nil || live.session.Character.Revision != revision {
		t.Fatal("effstats handling", err)
	}
	if sink.count(0x58)-before != 2 {
		t.Fatalf("effective plus equipment lines: %d", sink.count(0x58)-before)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	last := string(sink.packets[len(sink.packets)-2])
	if !strings.Contains(last, "[QA GM] Effective stats: str 4 dex ") || !strings.Contains(last, " int 321 ") || !strings.Contains(last, " matk ") {
		t.Fatalf("effective line %q", last)
	}
	if !strings.Contains(string(sink.packets[len(sink.packets)-1]), "[QA GM] Equipment only: str 4 ") {
		t.Fatal("equipment line missing")
	}
}
