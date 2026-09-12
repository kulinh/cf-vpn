// Package tgbot is a small long-polling Telegram bot that exposes a fixed set
// of cf-vpn controls to one group chat.
//
// Why long polling on VNM-01 rather than a command in the panel Worker: the
// only thing it drives is `cfvpnctl derp`, which needs the Tailscale OAuth
// client in /etc/cfvpn/tailscale-oauth.env. Keeping the bot on the box keeps
// that secret on the box. It also uses a different bot (@rwl_vpn_bot) from the
// Worker's webhook bot, so the two never compete for the same update stream —
// Telegram allows either getUpdates or a webhook per bot, not both.
//
// Both bots live in the same group, so this one answers a strict whitelist and
// stays silent on everything else (including the Worker bot's commands).
package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Runner is what the bot is allowed to do. Both functions write their output
// to w; it is sent back to the chat verbatim.
type Runner struct {
	ChinaMode func(ctx context.Context, on bool, w io.Writer) error
	Show      func(ctx context.Context, w io.Writer) error
}

// Bot polls one chat for commands.
type Bot struct {
	Token   string
	ChatID  int64
	Runner  Runner
	BaseURL string // default https://api.telegram.org
	HTTP    *http.Client
	Logf    func(format string, args ...any)

	// PollTimeout is the long-poll timeout in seconds (default 50).
	PollTimeout int
	// CommandTimeout bounds one command (default 4 minutes: china-mode waits
	// for the DERP map to settle and then runs netcheck).
	CommandTimeout time.Duration

	username string
	mu       sync.Mutex // one command at a time; two concurrent ACL writes would collide
}

const (
	maxTelegramText = 4000 // Telegram's limit is 4096; leave room for the prefix
	usage           = "cf-vpn control bot\n\n" +
		"/china status — show the DERP regions and whether china-mode is on\n" +
		"/china on — use ONLY our own relays (before flying to China)\n" +
		"/china off — public relays plus ours (normal, back home)\n" +
		"/derp — same as /china status\n\n" +
		"Note: with china-mode on, SIN-01 can only relay through JPY-01."
)

func (b *Bot) base() string {
	if b.BaseURL == "" {
		return "https://api.telegram.org"
	}
	return strings.TrimRight(b.BaseURL, "/")
}

func (b *Bot) client() *http.Client {
	if b.HTTP == nil {
		// Must outlast the long poll.
		b.HTTP = &http.Client{Timeout: 90 * time.Second}
	}
	return b.HTTP
}

func (b *Bot) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
	}
}

func (b *Bot) pollTimeout() int {
	if b.PollTimeout > 0 {
		return b.PollTimeout
	}
	return 50
}

func (b *Bot) commandTimeout() time.Duration {
	if b.CommandTimeout > 0 {
		return b.CommandTimeout
	}
	return 4 * time.Minute
}

func (b *Bot) call(ctx context.Context, method string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base()+"/bot"+b.Token+"/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.client().Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d: unreadable response", method, resp.StatusCode)
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s: %s", method, envelope.Description)
	}
	if out != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

// Send posts a message to the configured chat. replyTo may be 0.
func (b *Bot) Send(ctx context.Context, text string, replyTo int64) error {
	if len(text) > maxTelegramText {
		text = text[:maxTelegramText] + "\n… (truncated)"
	}
	form := url.Values{
		"chat_id":                  {fmt.Sprint(b.ChatID)},
		"text":                     {text},
		"disable_web_page_preview": {"true"},
	}
	if replyTo != 0 {
		form.Set("reply_to_message_id", fmt.Sprint(replyTo))
		// A reply to a deleted message would otherwise fail the whole send.
		form.Set("allow_sending_without_reply", "true")
	}
	return b.call(ctx, "sendMessage", form, nil)
}

type tgChat struct {
	ID int64 `json:"id"`
}

type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	Chat      tgChat  `json:"chat"`
	From      *tgUser `json:"from"`
	Text      string  `json:"text"`
}

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

// Username fetches and caches the bot's own @name, used to ignore commands
// explicitly addressed to another bot in the same group.
func (b *Bot) Username(ctx context.Context) (string, error) {
	if b.username != "" {
		return b.username, nil
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := b.call(ctx, "getMe", url.Values{}, &me); err != nil {
		return "", err
	}
	b.username = me.Username
	return b.username, nil
}

// SetCommands registers the bot's command menu for this chat only, so the
// Worker bot's menu in the same group is untouched.
func (b *Bot) SetCommands(ctx context.Context) error {
	cmds, _ := json.Marshal([]map[string]string{
		{"command": "china", "description": "DERP china-mode: status | on | off"},
		{"command": "derp", "description": "Show DERP regions and china-mode"},
	})
	scope, _ := json.Marshal(map[string]any{"type": "chat", "chat_id": b.ChatID})
	return b.call(ctx, "setMyCommands", url.Values{"commands": {string(cmds)}, "scope": {string(scope)}}, nil)
}

// parseCommand splits "/china@bot on" into ("china", ["on"], "bot").
func parseCommand(text string) (cmd string, args []string, addressed string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", nil, ""
	}
	cmd = strings.TrimPrefix(fields[0], "/")
	if at := strings.Index(cmd, "@"); at >= 0 {
		addressed = cmd[at+1:]
		cmd = cmd[:at]
	}
	return strings.ToLower(cmd), fields[1:], addressed
}

// Dispatch runs one command and returns the text to send back. An empty reply
// means "not for us, stay silent".
func (b *Bot) Dispatch(ctx context.Context, text string) string {
	cmd, args, addressed := parseCommand(text)
	if cmd == "" {
		return ""
	}
	if addressed != "" && b.username != "" && !strings.EqualFold(addressed, b.username) {
		return "" // addressed to the other bot in this group
	}

	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch cmd {
	case "derp":
		if sub != "" && sub != "show" && sub != "status" {
			return usage
		}
		return b.run(ctx, "derp show", func(ctx context.Context, w io.Writer) error { return b.Runner.Show(ctx, w) })
	case "china":
		switch sub {
		case "", "status", "show":
			return b.run(ctx, "derp show", func(ctx context.Context, w io.Writer) error { return b.Runner.Show(ctx, w) })
		case "on", "off":
			on := sub == "on"
			if len(args) > 1 {
				return usage
			}
			return b.run(ctx, "china-mode "+sub, func(ctx context.Context, w io.Writer) error { return b.Runner.ChinaMode(ctx, on, w) })
		default:
			return usage
		}
	}
	return "" // any other slash command belongs to the other bot
}

func (b *Bot) run(ctx context.Context, label string, fn func(context.Context, io.Writer) error) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, b.commandTimeout())
	defer cancel()
	var out bytes.Buffer
	start := time.Now()
	err := fn(ctx, &out)
	took := time.Since(start).Round(time.Second)
	body := strings.TrimRight(out.String(), "\n")
	if err != nil {
		b.logf("%s failed after %s: %v", label, took, err)
		if body != "" {
			return fmt.Sprintf("✖ %s failed: %v\n\n%s", label, err, body)
		}
		return fmt.Sprintf("✖ %s failed: %v", label, err)
	}
	b.logf("%s ok in %s", label, took)
	if body == "" {
		body = "(no output)"
	}
	return fmt.Sprintf("✅ %s (%s)\n\n%s", label, took, body)
}

// handle processes one update: authorization, dispatch, reply.
func (b *Bot) handle(ctx context.Context, u tgUpdate) {
	if u.Message == nil || u.Message.Text == "" {
		return
	}
	m := u.Message
	if m.Chat.ID != b.ChatID {
		b.logf("ignoring message from chat %d (only %d is allowed)", m.Chat.ID, b.ChatID)
		return
	}
	cmd, _, _ := parseCommand(m.Text)
	if cmd == "" {
		return
	}
	reply := b.Dispatch(ctx, m.Text)
	if reply == "" {
		return
	}
	who := "unknown"
	if m.From != nil {
		who = fmt.Sprintf("%d/@%s", m.From.ID, m.From.Username)
	}
	b.logf("command %q from %s", m.Text, who)
	// Long commands: tell the chat something is happening first.
	if strings.HasPrefix(reply, "✅ china-mode") || strings.HasPrefix(reply, "✖ china-mode") {
		_ = b.Send(ctx, reply, m.MessageID)
		return
	}
	if err := b.Send(ctx, reply, m.MessageID); err != nil {
		b.logf("send reply: %v", err)
	}
}

// Ack posts the "working on it" line for commands that take a while. Called by
// Run before dispatch so the group is not left guessing.
func (b *Bot) ack(ctx context.Context, m *tgMessage) {
	cmd, args, _ := parseCommand(m.Text)
	if cmd != "china" || len(args) == 0 {
		return
	}
	switch strings.ToLower(args[0]) {
	case "on", "off":
		_ = b.Send(ctx, "⏳ running china-mode "+strings.ToLower(args[0])+" (flips the policy, waits for the DERP map, then runs netcheck)…", m.MessageID)
	}
}

// skipBacklog returns the offset just past the newest pending update, so a
// restart never replays a command queued while the bot was down.
func (b *Bot) skipBacklog(ctx context.Context) (int64, error) {
	var ups []tgUpdate
	form := url.Values{"offset": {"-1"}, "limit": {"1"}, "timeout": {"0"}, "allowed_updates": {`["message"]`}}
	if err := b.call(ctx, "getUpdates", form, &ups); err != nil {
		return 0, err
	}
	if len(ups) == 0 {
		return 0, nil
	}
	return ups[len(ups)-1].UpdateID + 1, nil
}

// Run long-polls until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	if b.Token == "" || b.ChatID == 0 {
		return fmt.Errorf("tgbot: TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
	}
	if b.Runner.ChinaMode == nil || b.Runner.Show == nil {
		return fmt.Errorf("tgbot: runner is incomplete")
	}
	name, err := b.Username(ctx)
	if err != nil {
		return err
	}
	offset, err := b.skipBacklog(ctx)
	if err != nil {
		return err
	}
	b.logf("tgbot @%s polling chat %d (skipping backlog up to %d)", name, b.ChatID, offset)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var ups []tgUpdate
		form := url.Values{
			"timeout":         {fmt.Sprint(b.pollTimeout())},
			"allowed_updates": {`["message"]`},
		}
		if offset > 0 {
			form.Set("offset", fmt.Sprint(offset))
		}
		if err := b.call(ctx, "getUpdates", form, &ups); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			b.logf("getUpdates: %v (retrying in 5s)", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		for _, u := range ups {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.Message != nil && u.Message.Chat.ID == b.ChatID {
				b.ack(ctx, u.Message)
			}
			b.handle(ctx, u)
		}
	}
}
