package run

import (
	"context"
	"fmt"
	"runtime/debug"
)

var (
	// ocfl-tools version
	Version   = "0.4.2" // may be set by -ldflags
	BuildTime string    // always set by -ldflags
)

type VersionCmd struct{}

func (cmd *VersionCmd) Run(ctx context.Context, env *cmdEnv) error {
	codeRev := func() string {
		if info, ok := debug.ReadBuildInfo(); ok {
			revision := ""
			localmods := false
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					revision = setting.Value
				case "vcs.modified":
					localmods = setting.Value == "true"
				}
			}
			if !localmods {
				return revision
			}
		}
		return ""
	}

	fmt.Fprintln(env.stdout, "ocfl-tools: v"+Version)
	if BuildTime != "" {
		fmt.Fprintln(env.stdout, "date:", BuildTime)
	}
	if rev := codeRev(); rev != "" {
		fmt.Fprintln(env.stdout, "commit:", rev[:8])
	}
	return nil
}
