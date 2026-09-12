package tgbot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The group is an ops channel, not an archive, so its messages are deleted
// after a day — but this bot no longer does the deleting. A separate janitor
// bot (@xiaoqie001_bot, /opt/xiaoqie_bot) owns the policy and the
// deleteMessage calls for every bot in the group.
//
// Why a hand-off rather than deleting our own messages: Telegram never
// delivers one bot's messages to another bot, so the janitor cannot discover
// what we posted; it can only delete what it is told about. All this bot does
// is drop a note per message it sent:
//
//	<SpoolDir>/<chat_id>_<message_id>.json
//	{"chat_id":-100…,"message_id":123,"source":"rwl_vpn_bot","kind":"reply","sent_at":1789300800}
//
// One file per message means no locking between the writers (sms2tele writes
// the same directory) and nothing is lost across restarts. The janitor decides
// when the message goes (24 h by default) and removes the file afterwards.
// SpoolDir empty disables the hand-off entirely.

// DefaultSpoolDir is where the janitor bot reads from.
const DefaultSpoolDir = "/var/lib/xiaoqie-janitor/spool"

type spoolEntry struct {
	ChatID    int64  `json:"chat_id"`
	MessageID int64  `json:"message_id"`
	Source    string `json:"source,omitempty"`
	Kind      string `json:"kind,omitempty"`
	SentAt    int64  `json:"sent_at"`
}

// spool records one message for the janitor to delete later. A no-op when
// SpoolDir is unset (tests, --simulate without a spool). Failures are logged,
// never fatal: a message that outlives its day is a nuisance, a lost reply is
// not.
func (b *Bot) spool(chatID, messageID int64, kind string) {
	if b.SpoolDir == "" || messageID == 0 {
		return
	}
	if err := os.MkdirAll(b.SpoolDir, 0o700); err != nil {
		b.logf("spool: create %s: %v", b.SpoolDir, err)
		return
	}
	raw, err := json.Marshal(spoolEntry{
		ChatID:    chatID,
		MessageID: messageID,
		Source:    "rwl_vpn_bot",
		Kind:      kind,
		SentAt:    time.Now().Unix(),
	})
	if err != nil {
		b.logf("spool: encode %d: %v", messageID, err)
		return
	}
	path := filepath.Join(b.SpoolDir, fmt.Sprintf("%d_%d.json", chatID, messageID))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		b.logf("spool: write %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		b.logf("spool: rename %s: %v", path, err)
		_ = os.Remove(tmp)
	}
}
