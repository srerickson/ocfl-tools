package run

import (
	"fmt"
	"runtime/debug"
)

var (
	// ocfl-tools version
	Version   = "0.3.2" // may be set by -ldflags
	BuildTime string    // always set by -ldflags
)

type VersionCmd struct{}

func (cmd *VersionCmd) Run(g *globals) error {
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

	fmt.Fprintln(g.stdout, "ocfl-tools: v"+Version)
	if BuildTime != "" {
		fmt.Fprintln(g.stdout, "date:", BuildTime)
	}
	if rev := codeRev(); rev != "" {
		fmt.Fprintln(g.stdout, "commit:", rev[:8])
	}
	return nil
}
