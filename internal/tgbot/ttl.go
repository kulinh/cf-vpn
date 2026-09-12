package tgbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Message TTL: the group is an ops channel, not an archive. Every message the
// bot posts, and every command message it answered, is deleted after TTL
// (default 24 h; the bot must be a group admin to delete other people's
// messages). Telegram has no server-side TTL for bot messages and only lets a
// bot delete messages younger than 48 h, so the bot keeps its own queue.
//
// The queue is one small JSON file per message in TTLDir:
//
//	<TTLDir>/<chat_id>_<message_id>.json
//	{"chat_id":-100…,"message_id":123,"delete_at":1789300800}   (unix seconds)
//
// One file per message means no locking: scripts/fleet-probe.py drops its
// alert messages into the same directory and this bot's reaper deletes them
// too. Files survive restarts, so a queued deletion is never lost.

const (
	defaultTTL          = 24 * time.Hour
	defaultReapInterval = time.Minute
	// Telegram refuses to delete anything older than this; a file past it is
	// dropped after one last attempt so the queue cannot grow forever.
	telegramDeleteWindow = 48 * time.Hour
)

type ttlEntry struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int64 `json:"message_id"`
	DeleteAt  int64 `json:"delete_at"`
}

func (b *Bot) ttl() time.Duration {
	if b.TTL > 0 {
		return b.TTL
	}
	return defaultTTL
}

func (b *Bot) reapInterval() time.Duration {
	if b.ReapInterval > 0 {
		return b.ReapInterval
	}
	return defaultReapInterval
}

func ttlFileName(chatID, messageID int64) string {
	return fmt.Sprintf("%d_%d.json", chatID, messageID)
}

// track queues a message for deletion after TTL. A no-op when TTLDir is
// unset (tests, --simulate without a queue). Failures are logged, never
// fatal: a message that outlives its TTL is a nuisance, a lost reply is not.
func (b *Bot) track(chatID, messageID int64) {
	if b.TTLDir == "" || messageID == 0 {
		return
	}
	if err := os.MkdirAll(b.TTLDir, 0o700); err != nil {
		b.logf("ttl: create %s: %v", b.TTLDir, err)
		return
	}
	e := ttlEntry{ChatID: chatID, MessageID: messageID, DeleteAt: time.Now().Add(b.ttl()).Unix()}
	raw, _ := json.Marshal(e)
	path := filepath.Join(b.TTLDir, ttlFileName(chatID, messageID))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		b.logf("ttl: write %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		b.logf("ttl: rename %s: %v", path, err)
		_ = os.Remove(tmp)
	}
}

// deleteMessage removes one message from a chat.
func (b *Bot) deleteMessage(ctx context.Context, chatID, messageID int64) error {
	form := url.Values{"chat_id": {fmt.Sprint(chatID)}, "message_id": {fmt.Sprint(messageID)}}
	return b.call(ctx, "deleteMessage", form, nil)
}

// permanentDeleteError reports Telegram answers that will never change:
// already gone, too old, or not ours to delete.
func permanentDeleteError(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "message to delete not found") ||
		strings.Contains(s, "message can't be deleted") ||
		strings.Contains(s, "message_id_invalid") ||
		strings.Contains(s, "message not found")
}

// ReapOnce deletes every queued message whose time has come and returns how
// many were deleted. Files whose delete call fails for a transient reason
// stay for the next pass; files older than Telegram's 48 h window are
// dropped after one attempt.
func (b *Bot) ReapOnce(ctx context.Context) (int, error) {
	if b.TTLDir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(b.TTLDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	now := time.Now()
	deleted := 0
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		path := filepath.Join(b.TTLDir, de.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var e ttlEntry
		if err := json.Unmarshal(raw, &e); err != nil || e.MessageID == 0 || e.ChatID == 0 {
			b.logf("ttl: dropping unreadable %s", de.Name())
			_ = os.Remove(path)
			continue
		}
		due := time.Unix(e.DeleteAt, 0)
		if now.Before(due) {
			continue
		}
		err = b.deleteMessage(ctx, e.ChatID, e.MessageID)
		switch {
		case err == nil:
			deleted++
			_ = os.Remove(path)
		case permanentDeleteError(err):
			b.logf("ttl: message %d already gone or undeletable (%v); dropping", e.MessageID, err)
			_ = os.Remove(path)
		case now.Sub(due) > telegramDeleteWindow:
			b.logf("ttl: message %d past Telegram's 48 h window (%v); dropping", e.MessageID, err)
			_ = os.Remove(path)
		default:
			b.logf("ttl: delete %d: %v (will retry)", e.MessageID, err)
		}
		if ctx.Err() != nil {
			return deleted, ctx.Err()
		}
	}
	return deleted, nil
}

// reapLoop runs ReapOnce on a timer until ctx is cancelled.
func (b *Bot) reapLoop(ctx context.Context) {
	t := time.NewTicker(b.reapInterval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := b.ReapOnce(ctx); err != nil && ctx.Err() == nil {
				b.logf("ttl: reap: %v", err)
			} else if n > 0 {
				b.logf("ttl: deleted %d expired message(s)", n)
			}
		}
	}
}
