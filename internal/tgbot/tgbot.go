// Package tgbot is a small long-polling Telegram bot that exposes a fixed set
// of cf-vpn controls to one group chat.
//
// Why long polling on VNM-01 rather than a command in the panel Worker: the
// things it drives (`cfvpnctl derp`, `cfvpnctl rules-mode`) need secrets that
// live on the box (the Tailscale OAuth client, the account CF token). It also
// uses a different bot (@rwl_vpn_bot) from the Worker's webhook bot, so the
// two never compete for the same update stream — Telegram allows either
// getUpdates or a webhook per bot, not both.
//
// Both bots live in the same group, so this one answers a strict whitelist and
// stays silent on everything else (including the Worker bot's commands).
// Everything it posts is short Telegram HTML (see format.go); keeping the
// group tidy is the janitor bot's job, this one only notes what it sent (see
// spool.go).
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

// Runner is what the bot is allowed to do. Every function writes its output
// to w; the bot parses that output into the message it posts.
type Runner struct {
	ChinaMode func(ctx context.Context, on bool, w io.Writer) error
	Show      func(ctx context.Context, w io.Writer) error
	// RulesMode sets the fleet-wide travel mode of the Shadowrocket .conf
	// (cfvpnctl rules-mode set cn|uae|none); RulesModeShow prints it.
	RulesMode     func(ctx context.Context, mode string, w io.Writer) error
	RulesModeShow func(ctx context.Context, w io.Writer) error
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

	// SpoolDir is where the janitor bot (@xiaoqie001_bot) reads the messages
	// it should delete later; empty disables the hand-off. This bot never
	// deletes anything itself.
	SpoolDir string

	username string
	mu       sync.Mutex // one command at a time; two concurrent ACL writes would collide
}

const maxTelegramText = 4000 // Telegram's limit is 4096; leave room for the prefix

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

// Send posts an HTML message to the configured chat and hands it to the
// janitor for later deletion. replyTo may be 0. Text over Telegram's limit,
// or HTML Telegram refuses to parse, is sent again as plain text so a reply
// is never lost to formatting.
func (b *Bot) Send(ctx context.Context, text string, replyTo int64) error {
	id, err := b.sendMessage(ctx, text, replyTo, true)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "can't parse entities") {
		id, err = b.sendMessage(ctx, plainText(text), replyTo, false)
	}
	if err != nil {
		return err
	}
	b.spool(b.ChatID, id, "reply")
	return nil
}

func (b *Bot) sendMessage(ctx context.Context, text string, replyTo int64, asHTML bool) (int64, error) {
	if len(text) > maxTelegramText {
		text = plainText(text)
		asHTML = false
		if len(text) > maxTelegramText {
			text = text[:maxTelegramText] + "\n… (cắt bớt)"
		}
	}
	form := url.Values{
		"chat_id":                  {fmt.Sprint(b.ChatID)},
		"text":                     {text},
		"disable_web_page_preview": {"true"},
	}
	if asHTML {
		form.Set("parse_mode", "HTML")
	}
	if replyTo != 0 {
		form.Set("reply_to_message_id", fmt.Sprint(replyTo))
		// A reply to a deleted message would otherwise fail the whole send.
		form.Set("allow_sending_without_reply", "true")
	}
	var msg struct {
		MessageID int64 `json:"message_id"`
	}
	if err := b.call(ctx, "sendMessage", form, &msg); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
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
		{"command": "mode", "description": "Chế độ đi lại: status | china | uae | home"},
		{"command": "china", "description": "DERP china-mode: status | on | off"},
		{"command": "derp", "description": "Trạng thái DERP và china-mode"},
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

// Dispatch runs one command and returns the HTML to send back. An empty reply
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
	if len(args) > 1 {
		if cmd == "mode" || cmd == "china" || cmd == "derp" {
			return usage
		}
		return ""
	}
	switch cmd {
	case "mode":
		switch sub {
		case "", "status", "show":
			return b.status(ctx, true)
		case "china", "uae", "home":
			// One trip = one command: the config's inlined list and the DERP
			// policy always move together.
			rules, chinaOn := "cn", false
			switch sub {
			case "china":
				chinaOn = true
			case "uae":
				rules = "uae"
			}
			outs, took, err := b.exec(ctx,
				func(ctx context.Context, w io.Writer) error { return b.Runner.RulesMode(ctx, rules, w) },
				func(ctx context.Context, w io.Writer) error { return b.Runner.ChinaMode(ctx, chinaOn, w) },
			)
			if err != nil {
				b.logf("mode %s failed after %s: %v", sub, took, err)
				return fmtFailure(modeTitle(sub), took, err, outs...)
			}
			b.logf("mode %s ok in %s", sub, took)
			return fmtMode(sub, took, outs[0], outs[1])
		default:
			return usage
		}
	case "derp":
		if sub != "" && sub != "show" && sub != "status" {
			return usage
		}
		return b.status(ctx, false)
	case "china":
		switch sub {
		case "", "status", "show":
			return b.status(ctx, false)
		case "on", "off":
			on := sub == "on"
			outs, took, err := b.exec(ctx, func(ctx context.Context, w io.Writer) error { return b.Runner.ChinaMode(ctx, on, w) })
			if err != nil {
				b.logf("china-mode %s failed after %s: %v", sub, took, err)
				return fmtFailure("China-mode "+sub, took, err, outs...)
			}
			b.logf("china-mode %s ok in %s", sub, took)
			return fmtChina(sub, took, outs[0])
		default:
			return usage
		}
	}
	return "" // any other slash command belongs to the other bot
}

// status answers /mode status (rules + DERP) and /derp, /china status (DERP).
func (b *Bot) status(ctx context.Context, withRules bool) string {
	var fns []func(context.Context, io.Writer) error
	if withRules {
		fns = append(fns, func(ctx context.Context, w io.Writer) error { return b.Runner.RulesModeShow(ctx, w) })
	}
	fns = append(fns, func(ctx context.Context, w io.Writer) error { return b.Runner.Show(ctx, w) })
	outs, took, err := b.exec(ctx, fns...)
	if err != nil {
		b.logf("status failed after %s: %v", took, err)
		return fmtFailure("Xem trạng thái", took, err, outs...)
	}
	b.logf("status ok in %s", took)
	if withRules {
		return fmtStatus(outs[0], outs[1])
	}
	return fmtStatus("", outs[0])
}

// exec runs the steps in order under the command lock, stopping at the first
// error, and returns each step's output (the failed step's partial output
// included) plus the elapsed time.
func (b *Bot) exec(ctx context.Context, steps ...func(context.Context, io.Writer) error) ([]string, time.Duration, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, b.commandTimeout())
	defer cancel()
	start := time.Now()
	var outs []string
	for _, step := range steps {
		var buf bytes.Buffer
		err := step(ctx, &buf)
		outs = append(outs, buf.String())
		if err != nil {
			return outs, time.Since(start), err
		}
	}
	return outs, time.Since(start), nil
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
	// The command goes the same way as the reply: the janitor deletes both a
	// day later, so the group stays clean.
	b.spool(m.Chat.ID, m.MessageID, "command")
	if err := b.Send(ctx, reply, m.MessageID); err != nil {
		b.logf("send reply: %v", err)
	}
}

// ack posts the "working on it" line for commands that take a while. Called
// by Run before dispatch so the group is not left guessing.
func (b *Bot) ack(ctx context.Context, m *tgMessage) {
	cmd, args, _ := parseCommand(m.Text)
	if len(args) != 1 {
		return
	}
	sub := strings.ToLower(args[0])
	switch {
	case cmd == "china" && (sub == "on" || sub == "off"):
		_ = b.Send(ctx, fmtAck("china", sub), m.MessageID)
	case cmd == "mode" && (sub == "china" || sub == "uae" || sub == "home"):
		_ = b.Send(ctx, fmtAck("mode", sub), m.MessageID)
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
	if b.Runner.ChinaMode == nil || b.Runner.Show == nil || b.Runner.RulesMode == nil || b.Runner.RulesModeShow == nil {
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
	if b.SpoolDir == "" {
		b.logf("spool: disabled — messages stay in the group until deleted by hand")
	} else {
		b.logf("spool: handing sent messages to the janitor bot via %s", b.SpoolDir)
	}

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
