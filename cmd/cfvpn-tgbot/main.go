// cfvpn-tgbot exposes `cfvpnctl derp` and `cfvpnctl rules-mode` to one Telegram group through
// @rwl_vpn_bot, by long polling. It runs only on VNM-01 (unit
// cfvpn-tgbot.service), because the commands it drives need the Tailscale
// OAuth client in /etc/cfvpn/tailscale-oauth.env.
//
//	cfvpn-tgbot                      # long-poll forever (what systemd runs)
//	cfvpn-tgbot --setup              # register the command menu for the chat, then exit
//	cfvpn-tgbot --simulate "/derp"   # run one command as if it arrived, reply in the chat, exit
//
// Configuration comes from the process environment (systemd EnvironmentFile):
// TELEGRAM_BOT_TOKEN, TELEGRAM_CHAT_ID.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/state"
	"github.com/kulinh/cf-vpn/internal/tgbot"
)

// envFiles are read only to fill gaps in the process environment, so running
// the binary by hand behaves like the systemd unit.
var envFiles = []string{"/etc/cfvpn/tgbot.env", "/etc/cfvpn/fleet-probe.env"}

func fromEnvOrFiles(key string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	for _, f := range envFiles {
		env, err := state.Load(f)
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(env[key]); v != "" {
			return v
		}
	}
	return ""
}

func main() {
	setup := flag.Bool("setup", false, "register the bot's command menu for the chat and exit")
	simulate := flag.String("simulate", "", "run this command text as if it arrived from the chat, then exit (self-test)")
	flag.Parse()

	token := fromEnvOrFiles("TELEGRAM_BOT_TOKEN")
	chatRaw := fromEnvOrFiles("TELEGRAM_CHAT_ID")
	if token == "" || chatRaw == "" {
		log.Fatalf("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required (process env or %s)", strings.Join(envFiles, ", "))
	}
	chatID, err := strconv.ParseInt(chatRaw, 10, 64)
	if err != nil {
		log.Fatalf("TELEGRAM_CHAT_ID %q is not a number: %v", chatRaw, err)
	}

	bot := &tgbot.Bot{
		Token:  token,
		ChatID: chatID,
		Logf:   log.Printf,
		Runner: tgbot.Runner{
			// Same code path as the CLI: ACL snapshots, dry-run validation,
			// If-Match write and `tailscale netcheck` all happen in there.
			ChinaMode: func(ctx context.Context, on bool, w io.Writer) error {
				return commands.RunDerpChinaMode(ctx, on, commands.DerpDeps{}, w, w)
			},
			Show: func(ctx context.Context, w io.Writer) error {
				return commands.RunDerpShow(ctx, commands.DerpDeps{}, w)
			},
			// /mode: the travel mode of the Shadowrocket .conf, written to D1
			// with the CF credentials in /etc/cfvpn/cfvpn.env.
			RulesMode: func(ctx context.Context, mode string, w io.Writer) error {
				return commands.RunRulesModeSet(ctx, mode, commands.RulesModeDeps{}, w)
			},
			RulesModeShow: func(ctx context.Context, w io.Writer) error {
				return commands.RunRulesModeShow(ctx, commands.RulesModeDeps{}, w)
			},
		},
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *setup {
		if _, err := bot.Username(ctx); err != nil {
			log.Fatal(err)
		}
		if err := bot.SetCommands(ctx); err != nil {
			log.Fatal(err)
		}
		fmt.Println("command menu registered for chat", chatID)
		return
	}

	if *simulate != "" {
		if _, err := bot.Username(ctx); err != nil {
			log.Fatal(err)
		}
		reply := bot.Dispatch(ctx, *simulate)
		if reply == "" {
			fmt.Printf("%q is not one of this bot's commands; nothing sent\n", *simulate)
			return
		}
		if err := bot.Send(ctx, reply, 0); err != nil {
			log.Fatalf("send: %v", err)
		}
		fmt.Println("sent to chat", chatID)
		fmt.Println(reply)
		return
	}

	if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
