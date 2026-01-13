package run

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/srerickson/ocfl-go"
)

const copyHelp = "Copy an object in a storage root"

type CopyCmd struct {
	ID      string `name:"id" short:"i" help:"The ID of the object to copy"`
	ObjPath string `name:"object" help:"Full path to object root. If set, --root and --id are ignored."`
	To      string `name:"to" short:"t" required:"" help:"ID for the new object (must not exist)"`
	Version string `name:"version" short:"v" default:"0" help:"Version number from source object to use as v1 state of new object. 0 or HEAD means latest."`
	Message string `name:"message" short:"m" help:"Message to include in the new object's version metadata"`
	Name    string `name:"name" short:"n" help:"Username to include in the object version metadata ($$${env_user_name})"`
	Email   string `name:"email" short:"e" help:"User email to include in the object version metadata ($$${env_user_email})"`
}

func (cmd *CopyCmd) Run(g *globals) error {
	ctx := g.ctx

	if cmd.ID == "" && cmd.ObjPath == "" {
		return errors.New("must provide --id or --object for the source object")
	}

	// Open source object
	srcObj, err := g.newObject(cmd.ID, cmd.ObjPath, ocfl.ObjectMustExist())
	if err != nil {
		return fmt.Errorf("reading source object: %w", err)
	}

	// Parse version number
	versionNum, err := parseVersionArg(cmd.Version, srcObj.Head().Num())
	if err != nil {
		return fmt.Errorf("invalid version: %w", err)
	}

	// Validate version exists
	if versionNum < 1 || versionNum > srcObj.Head().Num() {
		return fmt.Errorf("version %d is out of range (HEAD=%d)", versionNum, srcObj.Head().Num())
	}

	// Get the stage for the specified version
	stage := srcObj.VersionStage(versionNum)
	if stage == nil {
		return fmt.Errorf("could not get stage for version %d", versionNum)
	}

	// Get the root for creating the new object
	root, err := g.getRoot()
	if err != nil {
		return err
	}

	// Check that destination doesn't exist
	dstObj, err := root.NewObject(ctx, cmd.To)
	if err != nil {
		return fmt.Errorf("checking destination object: %w", err)
	}
	if dstObj.Exists() {
		return fmt.Errorf("destination object %q already exists", cmd.To)
	}

	// Set user info
	if cmd.Name == "" {
		cmd.Name = g.getenv(envVarUserName)
	}
	if cmd.Email == "" {
		cmd.Email = g.getenv(envVarUserEmail)
	}

	// Set message
	msg := cmd.Message
	if msg == "" {
		srcID := cmd.ID
		if srcID == "" {
			srcID = srcObj.ID()
		}
		msg = fmt.Sprintf("Copied from %s v%d", srcID, versionNum)
	}

	// Update (create) the destination object with the source stage
	_, err = objectUpdateOrRevert(ctx, dstObj, stage, msg, newUser(cmd.Name, cmd.Email), g.logger)
	if err != nil {
		return fmt.Errorf("creating copy: %w", err)
	}

	g.logger.Info("object copied", "from", srcObj.ID(), "from_version", versionNum, "to", cmd.To)
	return nil
}

// parseVersionArg parses a version argument string and returns the version number.
// Accepts numeric strings ("1", "2"), "0" or "HEAD" for latest, or "v1", "v2" format.
func parseVersionArg(arg string, head int) (int, error) {
	if arg == "" || arg == "0" || arg == "HEAD" || arg == "head" {
		return head, nil
	}

	// Try parsing as "vN" format
	if len(arg) > 1 && (arg[0] == 'v' || arg[0] == 'V') {
		arg = arg[1:]
	}

	num, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("cannot parse version %q: %w", arg, err)
	}

	return num, nil
}
