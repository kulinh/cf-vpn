package tgbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTelegram implements just enough of the Bot API: getMe, getUpdates
// (queued), sendMessage (recorded), deleteMessage (recorded), setMyCommands.
type fakeTelegram struct {
	mu        sync.Mutex
	updates   []tgUpdate
	sent      []url.Values
	deleted   []string // "<chat>/<msg>"
	offsets   []string
	commands  int
	nextID    int64
	deleteErr string // when set, deleteMessage fails with this description
}

func (f *fakeTelegram) server(t *testing.T, token string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/bot"+token+"/", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		method := strings.TrimPrefix(r.URL.Path, "/bot"+token+"/")
		f.mu.Lock()
		defer f.mu.Unlock()
		write := func(result string) {
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":%s}`, result)
		}
		switch method {
		case "getMe":
			write(`{"id":1,"username":"rwl_vpn_bot"}`)
		case "setMyCommands":
			f.commands++
			write("true")
		case "sendMessage":
			f.sent = append(f.sent, r.Form)
			f.nextID++
			write(fmt.Sprintf(`{"message_id":%d}`, 100+f.nextID))
		case "deleteMessage":
			if f.deleteErr != "" {
				_, _ = fmt.Fprintf(w, `{"ok":false,"description":%q}`, f.deleteErr)
				return
			}
			f.deleted = append(f.deleted, r.Form.Get("chat_id")+"/"+r.Form.Get("message_id"))
			write("true")
		case "getUpdates":
			off := r.Form.Get("offset")
			f.offsets = append(f.offsets, off)
			if off == "-1" { // backlog probe: newest update, nothing confirmed
				if len(f.updates) == 0 {
					write("[]")
					return
				}
				b, _ := json.Marshal(f.updates[len(f.updates)-1:])
				write(string(b))
				return
			}
			// Real Telegram only returns updates with update_id >= offset and
			// drops everything below it.
			var from int64
			if off != "" {
				from, _ = strconv.ParseInt(off, 10, 64)
			}
			var out []tgUpdate
			for _, u := range f.updates {
				if u.UpdateID >= from {
					out = append(out, u)
				}
			}
			f.updates = nil
			b, _ := json.Marshal(out)
			write(string(b))
		default:
			http.Error(w, `{"ok":false,"description":"unknown method"}`, http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeTelegram) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.sent {
		out = append(out, s.Get("text"))
	}
	return out
}

type recordingRunner struct {
	mu    sync.Mutex
	calls []string
	err   error
}

// The fake runner prints what the real commands print, so the formatting
// parsers are exercised end to end.
func (r *recordingRunner) runner() Runner {
	return Runner{
		ChinaMode: func(_ context.Context, on bool, w io.Writer) error {
			r.mu.Lock()
			r.calls = append(r.calls, fmt.Sprintf("china-mode %v", on))
			r.mu.Unlock()
			if on {
				fmt.Fprint(w, sampleChinaOn)
			} else {
				fmt.Fprint(w, sampleChinaNoop)
			}
			return r.err
		},
		Show: func(_ context.Context, w io.Writer) error {
			r.mu.Lock()
			r.calls = append(r.calls, "show")
			r.mu.Unlock()
			fmt.Fprint(w, sampleDerpShow)
			return r.err
		},
		RulesMode: func(_ context.Context, mode string, w io.Writer) error {
			r.mu.Lock()
			r.calls = append(r.calls, "rules-mode "+mode)
			r.mu.Unlock()
			fmt.Fprintf(w, "rules_mode = %s — …\n", mode)
			return r.err
		},
		RulesModeShow: func(_ context.Context, w io.Writer) error {
			r.mu.Lock()
			r.calls = append(r.calls, "rules-show")
			r.mu.Unlock()
			fmt.Fprint(w, sampleRulesDefault)
			return r.err
		},
	}
}

func newTestBot(t *testing.T, f *fakeTelegram, rr *recordingRunner) *Bot {
	t.Helper()
	srv := f.server(t, "tok")
	return &Bot{Token: "tok", ChatID: -100123, Runner: rr.runner(), BaseURL: srv.URL, username: "rwl_vpn_bot"}
}

func TestParseCommand(t *testing.T) {
	for _, tc := range []struct {
		in        string
		cmd, addr string
		args      []string
	}{
		{"/china on", "china", "", []string{"on"}},
		{"/china@rwl_vpn_bot off", "china", "rwl_vpn_bot", []string{"off"}},
		{"/CHINA Status", "china", "", []string{"Status"}},
		{"/derp", "derp", "", nil},
		{"hello", "", "", nil},
		{"", "", "", nil},
		{"  /china   on  ", "china", "", []string{"on"}},
	} {
		cmd, args, addr := parseCommand(tc.in)
		if cmd != tc.cmd || addr != tc.addr || strings.Join(args, ",") != strings.Join(tc.args, ",") {
			t.Errorf("%q → (%q, %v, %q), want (%q, %v, %q)", tc.in, cmd, args, addr, tc.cmd, tc.args, tc.addr)
		}
	}
}

func TestDispatchWhitelist(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	ctx := context.Background()

	if got := b.Dispatch(ctx, "/china on"); !strings.HasPrefix(got, "✅ <b>China-mode on</b>") || !strings.Contains(got, "📶 Relay: HKG-01 52 ms · JPY-01 126 ms") {
		t.Fatalf("china on → %q", got)
	}
	if got := b.Dispatch(ctx, "/china off"); !strings.HasPrefix(got, "✅ <b>China-mode off</b>") || !strings.Contains(got, "đã off sẵn") {
		t.Fatalf("china off → %q", got)
	}
	for _, in := range []string{"/china", "/china status", "/derp", "/derp show"} {
		got := b.Dispatch(ctx, in)
		if !strings.HasPrefix(got, "ℹ️ <b>Chế độ hiện tại</b>\n🌐 China-mode: off") || strings.Contains(got, "Config") {
			t.Fatalf("%s → %q", in, got)
		}
	}
	if got := b.Dispatch(ctx, "/china maybe"); got != usage {
		t.Fatalf("bad subcommand must print usage, got %q", got)
	}
	// Commands that belong to the Worker bot in the same group: stay silent.
	for _, in := range []string{"/status", "/nodes", "/help", "/sub", "/upgrade", "plain text", "/china@other_bot on", "/adduser x y"} {
		if got := b.Dispatch(ctx, in); got != "" {
			t.Fatalf("%q must be ignored, got %q", in, got)
		}
	}
	rr.mu.Lock()
	defer rr.mu.Unlock()
	want := []string{"china-mode true", "china-mode false", "show", "show", "show", "show"}
	if strings.Join(rr.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("runner calls = %v, want %v", rr.calls, want)
	}
}

func TestDispatchMode(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	ctx := context.Background()

	for _, in := range []string{"/mode", "/mode status", "/mode@rwl_vpn_bot show"} {
		got := b.Dispatch(ctx, in)
		if !strings.HasPrefix(got, "ℹ️ <b>Chế độ hiện tại</b>\n📄 Config RWL8899: list CN (vượt GFW) · mặc định\n🌐 China-mode: off") {
			t.Fatalf("%s → %q", in, got)
		}
	}
	if got := b.Dispatch(ctx, "/mode uae"); !strings.HasPrefix(got, "✅ <b>Chế độ UAE</b>") || !strings.Contains(got, "📄 Config RWL8899 → list UAE") || !strings.Contains(got, "China-mode → off") || !strings.Contains(got, "bỏ module zalo_zalopay") {
		t.Fatalf("mode uae → %q", got)
	}
	if got := b.Dispatch(ctx, "/mode china"); !strings.HasPrefix(got, "✅ <b>Chế độ China</b>") || !strings.Contains(got, "list CN") || !strings.Contains(got, "China-mode → on · policy đã đổi") {
		t.Fatalf("mode china → %q", got)
	}
	if got := b.Dispatch(ctx, "/mode home"); !strings.HasPrefix(got, "✅ <b>Về mặc định (China)</b>") || !strings.Contains(got, "list CN") || !strings.Contains(got, "China-mode → off") {
		t.Fatalf("mode home → %q", got)
	}
	for _, in := range []string{"/mode mars", "/mode uae now"} {
		if got := b.Dispatch(ctx, in); got != usage {
			t.Fatalf("%q must print usage, got %q", in, got)
		}
	}
	rr.mu.Lock()
	defer rr.mu.Unlock()
	want := []string{
		"rules-show", "show", "rules-show", "show", "rules-show", "show",
		"rules-mode uae", "china-mode false",
		"rules-mode cn", "china-mode true",
		"rules-mode cn", "china-mode false",
	}
	if strings.Join(rr.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("runner calls = %v, want %v", rr.calls, want)
	}
}

func TestDispatchModeStopsWhenRulesWriteFails(t *testing.T) {
	f := &fakeTelegram{}
	rr := &recordingRunner{err: fmt.Errorf("d1 down")}
	b := newTestBot(t, f, rr)
	got := b.Dispatch(context.Background(), "/mode uae")
	if !strings.HasPrefix(got, "✖ <b>Chế độ UAE thất bại</b>") || !strings.Contains(got, "<code>d1 down</code>") || !strings.Contains(got, "<pre>rules_mode = uae") {
		t.Fatalf("reply = %q", got)
	}
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if strings.Join(rr.calls, "|") != "rules-mode uae" {
		t.Fatalf("china-mode must not be flipped when the D1 write failed, calls=%v", rr.calls)
	}
}

func TestHandleRejectsOtherChats(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	b.handle(context.Background(), tgUpdate{UpdateID: 1, Message: &tgMessage{MessageID: 5, Chat: tgChat{ID: -999}, Text: "/china on"}})
	rr.mu.Lock()
	calls := len(rr.calls)
	rr.mu.Unlock()
	if calls != 0 || len(f.texts()) != 0 {
		t.Fatalf("a foreign chat must be ignored entirely (calls=%d sent=%v)", calls, f.texts())
	}
}

func TestRunSkipsBacklogThenHandlesOneCommand(t *testing.T) {
	f := &fakeTelegram{updates: []tgUpdate{
		{UpdateID: 40, Message: &tgMessage{MessageID: 1, Chat: tgChat{ID: -100123}, Text: "/china on"}},
	}}
	rr := &recordingRunner{}
	srv := f.server(t, "tok")
	b := &Bot{Token: "tok", ChatID: -100123, Runner: rr.runner(), BaseURL: srv.URL, PollTimeout: 1}

	// The stale /china on above is the backlog: it must NOT run. A later
	// update must.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		seeded := len(f.offsets) > 1
		f.mu.Unlock()
		if seeded {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.mu.Lock()
	f.updates = []tgUpdate{{UpdateID: 41, Message: &tgMessage{MessageID: 2, Chat: tgChat{ID: -100123}, From: &tgUser{ID: 7, Username: "kulinh"}, Text: "/derp"}}}
	f.mu.Unlock()

	for time.Now().Before(deadline.Add(3 * time.Second)) {
		if len(f.texts()) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done

	rr.mu.Lock()
	calls := append([]string(nil), rr.calls...)
	rr.mu.Unlock()
	if strings.Join(calls, "|") != "show" {
		t.Fatalf("only the fresh command may run, got %v", calls)
	}
	texts := f.texts()
	if len(texts) != 1 || !strings.HasPrefix(texts[0], "ℹ️ <b>Chế độ hiện tại</b>") {
		t.Fatalf("replies = %v", texts)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offsets[0] != "-1" {
		t.Fatalf("first getUpdates must probe the backlog, offsets=%v", f.offsets)
	}
	if f.offsets[1] != "41" {
		t.Fatalf("polling must resume past the backlog, offsets=%v", f.offsets)
	}
	if f.sent[0].Get("parse_mode") != "HTML" {
		t.Fatalf("replies must be sent as HTML, form=%v", f.sent[0])
	}
}

func TestAckOnlyForLongCommands(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	ctx := context.Background()
	b.ack(ctx, &tgMessage{MessageID: 1, Chat: tgChat{ID: -100123}, Text: "/derp"})
	b.ack(ctx, &tgMessage{MessageID: 3, Chat: tgChat{ID: -100123}, Text: "/mode status"})
	if len(f.texts()) != 0 {
		t.Fatalf("status commands must not be announced: %v", f.texts())
	}
	b.ack(ctx, &tgMessage{MessageID: 2, Chat: tgChat{ID: -100123}, Text: "/china on"})
	if texts := f.texts(); len(texts) != 1 || !strings.Contains(texts[0], "⏳ Đang chuyển china-mode on") {
		t.Fatalf("ack = %v", texts)
	}
	b.ack(ctx, &tgMessage{MessageID: 4, Chat: tgChat{ID: -100123}, Text: "/mode uae"})
	if texts := f.texts(); len(texts) != 2 || !strings.Contains(texts[1], "⏳ Đang chuyển sang <b>Chế độ UAE</b>") {
		t.Fatalf("ack = %v", texts)
	}
}

func TestSendTruncatesAndSetsCommands(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	ctx := context.Background()
	if err := b.Send(ctx, "<b>"+strings.Repeat("x", maxTelegramText+500)+"</b>", 12); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	sent := f.sent[0]
	f.mu.Unlock()
	if len(sent.Get("text")) > maxTelegramText+20 || !strings.HasSuffix(sent.Get("text"), "(cắt bớt)") || strings.Contains(sent.Get("text"), "<b>") {
		t.Fatalf("text not truncated to plain text (%d chars)", len(sent.Get("text")))
	}
	if sent.Get("parse_mode") != "" {
		t.Fatalf("a truncated message must go out as plain text, form=%v", sent)
	}
	if sent.Get("reply_to_message_id") != "12" || sent.Get("chat_id") != "-100123" {
		t.Fatalf("send form = %v", sent)
	}
	if err := b.SetCommands(ctx); err != nil || f.commands != 1 {
		t.Fatalf("SetCommands: err=%v n=%d", err, f.commands)
	}
}

func TestRunRequiresConfig(t *testing.T) {
	if err := (&Bot{ChatID: 1}).Run(context.Background()); err == nil {
		t.Fatal("missing token must fail")
	}
	if err := (&Bot{Token: "t", ChatID: 1}).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "runner") {
		t.Fatalf("missing runner must fail, got %v", err)
	}
	half := Runner{ChinaMode: func(context.Context, bool, io.Writer) error { return nil }, Show: func(context.Context, io.Writer) error { return nil }}
	if err := (&Bot{Token: "t", ChatID: 1, Runner: half}).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "runner") {
		t.Fatalf("runner without the rules-mode functions must fail, got %v", err)
	}
}

// ---- spool hand-off to the janitor bot --------------------------------------

func readSpool(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSpoolNotesReplyAndCommandButDeletesNothing(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	b.SpoolDir = t.TempDir()

	// A handled command notes both the reply (id 101) and the command (id 7).
	b.handle(context.Background(), tgUpdate{UpdateID: 1, Message: &tgMessage{MessageID: 7, Chat: tgChat{ID: -100123}, Text: "/derp"}})

	names := readSpool(t, b.SpoolDir)
	if len(names) != 2 || names[0] != "-100123_101.json" || names[1] != "-100123_7.json" {
		t.Fatalf("spool = %v", names)
	}
	raw, _ := os.ReadFile(filepath.Join(b.SpoolDir, "-100123_101.json"))
	var e spoolEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("entry = %s (%v)", raw, err)
	}
	if e.ChatID != -100123 || e.MessageID != 101 || e.Source != "rwl_vpn_bot" || e.Kind != "reply" {
		t.Fatalf("entry = %+v", e)
	}
	if e.SentAt < time.Now().Unix()-60 {
		t.Fatalf("sent_at = %d, want about now", e.SentAt)
	}
	cmdRaw, _ := os.ReadFile(filepath.Join(b.SpoolDir, "-100123_7.json"))
	var cmd spoolEntry
	_ = json.Unmarshal(cmdRaw, &cmd)
	if cmd.Kind != "command" {
		t.Fatalf("command entry = %+v", cmd)
	}

	// The deleting is the janitor's job now: this bot must never call it.
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.deleted) != 0 {
		t.Fatalf("bot must not delete anything itself, got %v", f.deleted)
	}
}

func TestSpoolDisabledWithoutDir(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	if err := b.Send(context.Background(), "hi", 0); err != nil {
		t.Fatal(err)
	}
	// No SpoolDir: nothing written, nothing deleted, no error.
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.deleted) != 0 {
		t.Fatalf("deleted = %v", f.deleted)
	}
}

func TestSpoolSurvivesAnUnwritableDir(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	// A path under a regular file can never be created: the reply must still
	// be sent, only the hand-off is logged as lost.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	b.SpoolDir = filepath.Join(file, "spool")
	if err := b.Send(context.Background(), "hi", 0); err != nil {
		t.Fatalf("a broken spool must not fail the send: %v", err)
	}
	if texts := f.texts(); len(texts) != 1 {
		t.Fatalf("texts = %v", texts)
	}
}
