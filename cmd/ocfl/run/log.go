package run

import (
	"context"
	"errors"
	"fmt"

	"github.com/srerickson/ocfl-go"
)

type LogCmd struct {
	objectFlags
}

func (cmd *LogCmd) Run(ctx context.Context, env *cmdEnv) error {
	obj, err := cmd.open(ctx, env, ocfl.ObjectMustExist())
	if err != nil {
		return err
	}
	for _, vnum := range obj.Head().Lineage() {
		version := obj.Version(vnum.Num())
		if version == nil {
			return errors.New("inventory is missing entry for " + vnum.String())
		}
		fmt.Fprintf(env.stdout, "%s (%s): %q", vnum.String(), version.Created(), version.Message())
		if version.User() != nil {
			fmt.Fprintf(env.stdout, " %s <%s>", version.User().Name, version.User().Address)
		}
		fmt.Fprintln(env.stdout, "")
	}
	return nil
}
