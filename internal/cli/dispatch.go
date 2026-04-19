package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/kulinh/cf-vpn/internal/commands"
	"github.com/kulinh/cf-vpn/internal/paths"
	"github.com/kulinh/cf-vpn/internal/state"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "usage: cfvpnctl <command>")
		return 0
	}

	switch args[0] {
	case "help":
		fmt.Fprintln(stdout, "usage: cfvpnctl <command>")
		return 0
	case "install":
		env, err := state.Load(paths.EnvFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
			return 1
		}
		in := commands.InstallInputs{
			CFAPIToken:  env["CF_API_TOKEN"],
			CFAccountID: env["CF_ACCOUNT_ID"],
			Domain:      env["DOMAIN"],
			User1Name:   env["USER1_NAME"],
		}
		if err := commands.RunInstall(context.Background(), in, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "add-user":
		if len(args) < 2 || args[1] == "" {
			fmt.Fprintln(stderr, "usage: cfvpnctl add-user <name>")
			return 2
		}
		env, err := state.Load(paths.EnvFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
			return 1
		}
		in := commands.UserInputs{Name: args[1], Domain: env["DOMAIN"]}
		if err := commands.RunAddUser(context.Background(), in, nil, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "remove-user":
		if len(args) < 2 || args[1] == "" {
			fmt.Fprintln(stderr, "usage: cfvpnctl remove-user <name>")
			return 2
		}
		in := commands.UserInputs{Name: args[1]}
		if err := commands.RunRemoveUser(context.Background(), in, nil, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "gen-sub":
		env, err := state.Load(paths.EnvFile)
		if err != nil {
			fmt.Fprintf(stderr, "cannot read env file %s: %v\n", paths.EnvFile, err)
			return 1
		}
		var name string
		if len(args) >= 2 {
			name = args[1]
		}
		in := commands.UserInputs{Name: name, Domain: env["DOMAIN"]}
		if err := commands.RunGenSub(context.Background(), in, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}
