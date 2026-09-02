package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/alterfo/kb/internal/config"
)

func runConfigCmd(args []string, env config.Env, lookup config.EnvLookup, stdout, stderr io.Writer) int {
	preset := ""
	show := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "show":
			show = true
		case args[i] == "--preset":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "config: --preset requires a value")
				return 2
			}
			i++
			preset = args[i]
		case strings.HasPrefix(args[i], "--preset="):
			preset = strings.TrimPrefix(args[i], "--preset=")
		default:
			fmt.Fprintf(stderr, "config: unknown argument %q\n", args[i])
			return 2
		}
	}
	if !show {
		fmt.Fprintln(stderr, "config: expected show")
		return 2
	}
	if preset != "" {
		if err := config.ApplyPreset(&env, preset); err != nil {
			fmt.Fprintf(stderr, "config: %v\n", err)
			return 2
		}
	}
	for _, v := range config.EffectiveVars(env, lookup) {
		fmt.Fprintf(stdout, "%s=%s\n", v.Name, v.Value)
	}
	return 0
}
