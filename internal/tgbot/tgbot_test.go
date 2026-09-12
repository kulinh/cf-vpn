package tgbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTelegram implements just enough of the Bot API: getMe, getUpdates
// (queued), sendMessage (recorded), setMyCommands.
type fakeTelegram struct {
	mu       sync.Mutex
	updates  []tgUpdate
	sent     []url.Values
	offsets  []string
	commands int
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
			write(`{"message_id":99}`)
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

func (r *recordingRunner) runner() Runner {
	return Runner{
		ChinaMode: func(_ context.Context, on bool, w io.Writer) error {
			r.mu.Lock()
			r.calls = append(r.calls, fmt.Sprintf("china-mode %v", on))
			r.mu.Unlock()
			fmt.Fprintf(w, "china-mode %v: policy updated\n--- tailscale netcheck ---\nNearest DERP: HKG-01\n", on)
			return r.err
		},
		Show: func(_ context.Context, w io.Writer) error {
			r.mu.Lock()
			r.calls = append(r.calls, "show")
			r.mu.Unlock()
			fmt.Fprint(w, "OmitDefaultRegions: false (china-mode off)\nregion 900 hkg (HKG-01)\n")
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

	if got := b.Dispatch(ctx, "/china on"); !strings.Contains(got, "✅ china-mode on") || !strings.Contains(got, "Nearest DERP") {
		t.Fatalf("china on → %q", got)
	}
	if got := b.Dispatch(ctx, "/china off"); !strings.Contains(got, "✅ china-mode off") {
		t.Fatalf("china off → %q", got)
	}
	for _, in := range []string{"/china", "/china status", "/derp", "/derp show"} {
		if got := b.Dispatch(ctx, in); !strings.Contains(got, "✅ derp show") || !strings.Contains(got, "china-mode off") {
			t.Fatalf("%s → %q", in, got)
		}
	}
	if got := b.Dispatch(ctx, "/china maybe"); !strings.Contains(got, "/china on") {
		t.Fatalf("bad subcommand must print usage, got %q", got)
	}
	// Commands that belong to the Worker bot in the same group: stay silent.
	for _, in := range []string{"/status", "/nodes", "/help", "/sub", "/upgrade", "plain text", "/china@other_bot on"} {
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

func TestDispatchReportsFailure(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{err: fmt.Errorf("boom")}
	b := newTestBot(t, f, rr)
	got := b.Dispatch(context.Background(), "/china on")
	if !strings.HasPrefix(got, "✖ china-mode on failed: boom") || !strings.Contains(got, "policy updated") {
		t.Fatalf("failure reply = %q", got)
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
	if len(texts) != 1 || !strings.Contains(texts[0], "✅ derp show") {
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
}

func TestAckOnlyForLongCommands(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	ctx := context.Background()
	b.ack(ctx, &tgMessage{MessageID: 1, Chat: tgChat{ID: -100123}, Text: "/derp"})
	if len(f.texts()) != 0 {
		t.Fatalf("show must not be announced: %v", f.texts())
	}
	b.ack(ctx, &tgMessage{MessageID: 2, Chat: tgChat{ID: -100123}, Text: "/china on"})
	if texts := f.texts(); len(texts) != 1 || !strings.Contains(texts[0], "⏳ running china-mode on") {
		t.Fatalf("ack = %v", texts)
	}
}

func TestSendTruncatesAndSetsCommands(t *testing.T) {
	f, rr := &fakeTelegram{}, &recordingRunner{}
	b := newTestBot(t, f, rr)
	ctx := context.Background()
	if err := b.Send(ctx, strings.Repeat("x", maxTelegramText+500), 12); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	sent := f.sent[0]
	f.mu.Unlock()
	if len(sent.Get("text")) > maxTelegramText+20 || !strings.HasSuffix(sent.Get("text"), "(truncated)") {
		t.Fatalf("text not truncated (%d chars)", len(sent.Get("text")))
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
}
