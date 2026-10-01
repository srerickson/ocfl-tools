package run

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/srerickson/ocfl-go"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	fsconfig "github.com/srerickson/ocfl-go/fs/config"
)

const (
	envVarRoot      = "OCFL_ROOT"       // storage root location string
	envVarUserName  = "OCFL_USER_NAME"  // user name for commit
	envVarUserEmail = "OCFL_USER_EMAIL" // user email for commit

	// if "true", enable path-style addressing for s3
	envVarS3PathStyle = "OCFL_S3_PATHSTYLE"

	// s3 settings used as defaults for s3:// locations
	envVarAWSEndpoint = "AWS_ENDPOINT_URL"
	envVarAWSRegion   = "AWS_REGION"
)

func CLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	parser, err := kong.New(&cli, kong.Name("ocfl"),
		kong.Writers(stdout, stderr),
		kong.Description("command line tool for working with OCFL repositories"),
		kong.Vars{
			"commit_help":    commitHelp,
			"diff_help":      diffHelp,
			"delete_help":    deleteHelp,
			"export_help":    exportHelp,
			"info_help":      infoHelp,
			"init_root_help": initRootHelp,
			"ls_help":        lsHelp,
			"log_help":       logHelp,
			"stage_help":     stageHelp,
			"validate_help":  validateHelp,
			"env_root":       envVarRoot,
			"env_user_name":  envVarUserName,
			"env_user_email": envVarUserEmail,
		},
		kong.ConfigureHelp(kong.HelpOptions{
			Summary: true,
			Compact: true,
		}),
	)
	if err != nil {
		fmt.Fprintln(stderr, "in kong configuration:", err.Error())
		return err
	}
	kongCtx, err := parser.Parse(args[1:])
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		var parseErr *kong.ParseError
		if errors.As(err, &parseErr) {
			parseErr.Context.PrintUsage(true)
		}
		return err
	}
	cli.globals.ctx = ctx
	cli.globals.stdout = stdout
	cli.globals.stderr = stderr
	cli.globals.stdin = stdin
	cli.globals.getenv = getenv
	logLevel := log.InfoLevel
	if cli.Debug {
		logLevel = log.DebugLevel
	}
	cli.globals.logger = newLogger(logLevel, stderr)
	//root config from flag or environment
	if cli.globals.RootLocation == "" {
		cli.globals.RootLocation = getenv(envVarRoot)
	}
	// release local file descriptors however the command ends (success,
	// error, panic, or cancellation).
	defer cli.globals.closeAll()
	if err := kongCtx.Run(&cli.globals); err != nil {
		cli.globals.logger.Error(err.Error())
		return err
	}
	return nil
}

var cli struct {
	globals
	Commit   CommitCmd   `cmd:"" help:"${commit_help}"`
	Diff     DiffCmd     `cmd:"" help:"${diff_help}"`
	Delete   DeleteCmd   `cmd:"" help:"${delete_help}"`
	Export   ExportCmd   `cmd:"" help:"${export_help}"`
	Info     InfoCmd     `cmd:"" help:"${info_help}"`
	InitRoot InitRootCmd `cmd:"" help:"${init_root_help}"`
	Log      LogCmd      `cmd:"" help:"${log_help}"`
	Ls       LsCmd       `cmd:"" help:"${ls_help}"`
	Stage    StageCmd    `cmd:"" help:"${stage_help}"`
	Validate ValidateCmd `cmd:"" help:"${validate_help}"`
	Version  VersionCmd  `cmd:"" help:"Print ocfl-tools version information"`
}

type globals struct {
	ctx    context.Context
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	getenv func(string) string
	logger *slog.Logger

	closeMu sync.Mutex
	closers []io.Closer // resources opened by parseLocation

	RootLocation string `name:"root" help:"The prefix/directory of the OCFL storage root used for the command ($$${env_root})"`
	Debug        bool   `name:"debug" help:"enable debug log messages"`
}

// parseLocation converts a location, which may be a local path or a 'file://',
// 's3://' or 'http(s)://' url, into an FS and a path within it. Local file
// systems are closed when the CLI returns.
func (g *globals) parseLocation(loc string) (ocflfs.FS, string, error) {
	if loc == "" {
		return nil, "", errors.New("location not set")
	}
	loc, err := g.withS3Defaults(loc)
	if err != nil {
		return nil, "", err
	}
	fsCfg, err := fsconfig.New(g.ctx, loc, fsconfig.WithLogger(g.logger))
	if err != nil {
		return nil, "", err
	}
	if closer, ok := fsCfg.FS.(io.Closer); ok {
		g.addCloser(closer)
	}
	return fsCfg.FS, fsCfg.Path, nil
}

// withS3Defaults fills in the region, endpoint, and path-style settings of
// an s3:// location from the environment (as seen through g.getenv) if they
// aren't already part of the location. Other locations are returned as-is.
// Credentials are left to the default AWS configuration.
func (g *globals) withS3Defaults(loc string) (string, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return "", err
	}
	if u.Scheme != "s3" {
		return loc, nil
	}
	q := u.Query()
	setDefault := func(key, val string) {
		if val != "" && !q.Has(key) {
			q.Set(key, val)
		}
	}
	setDefault("region", g.getenv(envVarAWSRegion))
	setDefault("endpoint", g.getenv(envVarAWSEndpoint))
	if strings.EqualFold(g.getenv(envVarS3PathStyle), "true") {
		setDefault("path-style", "true")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// addCloser registers c to be closed by closeAll.
func (g *globals) addCloser(c io.Closer) {
	g.closeMu.Lock()
	defer g.closeMu.Unlock()
	g.closers = append(g.closers, c)
}

// closeAll closes everything registered with addCloser, in reverse order.
func (g *globals) closeAll() {
	g.closeMu.Lock()
	closers := g.closers
	g.closers = nil
	g.closeMu.Unlock()
	for i := len(closers) - 1; i >= 0; i-- {
		if err := closers[i].Close(); err != nil && g.logger != nil {
			g.logger.Warn("closing storage backend", "err", err.Error())
		}
	}
}

// mkLocalDir creates the directory for loc if loc is a local path or a file
// url. Local backends require the directory to exist, so this is needed before
// initializing a new storage root. It does nothing for s3 or http(s) locations.
func (g *globals) mkLocalDir(loc string) error {
	if loc == "" {
		return errors.New("location not set")
	}
	locUrl, err := url.Parse(loc)
	if err != nil {
		return err
	}
	switch {
	case locUrl.Scheme == "file":
		loc = locUrl.Path
	case len(locUrl.Scheme) > 1:
		return nil // s3, http(s)
	}
	return os.MkdirAll(loc, 0o777)
}

func (g *globals) getRoot() (*ocfl.Root, error) {
	fsys, dir, err := g.parseLocation(g.RootLocation)
	if err != nil {
		return nil, err
	}
	root, err := ocfl.NewRoot(g.ctx, fsys, dir)
	if err != nil {
		rootcnf := locationString(fsys, dir)
		return nil, fmt.Errorf("reading OCFL storage root %s: %w", rootcnf, err)
	}
	return root, nil
}

// newObject using id (if set) or full object path. if mustExist is true
// the object's existence is checked.
func (g *globals) newObject(id, objPath string, opts ...ocfl.ObjectOption) (*ocfl.Object, error) {
	if id == "" && objPath == "" {
		err := errors.New("must provide an object ID or an object path")
		return nil, err
	}
	if id == "" {
		fsys, dir, err := g.parseLocation(objPath)
		if err != nil {
			return nil, err
		}
		obj, err := ocfl.NewObject(g.ctx, fsys, dir, opts...)
		if err != nil {
			return nil, fmt.Errorf("reading object at path: %q: %w", objPath, err)
		}
		return obj, nil
	}
	root, err := g.getRoot()
	if err != nil {
		return nil, err
	}
	obj, err := root.NewObject(g.ctx, id, opts...)
	if err != nil {
		return nil, fmt.Errorf("reading object id: %q: %w", id, err)
	}
	return obj, nil
}

// locationString returns the location string for dir in fsys. The result is a
// url that parseLocation accepts, so it can be used as a --root or --object
// value.
func locationString(fsys ocflfs.FS, dir string) string {
	text, err := fsconfig.FSConfig{FS: fsys, Path: dir}.MarshalText()
	if err != nil {
		return fmt.Sprintf("%T:%s", fsys, dir)
	}
	return string(text)
}

func newLogger(l log.Level, w io.Writer) *slog.Logger {
	handl := log.New(w)
	handl.SetLevel(l)
	return slog.New(handl)
}
