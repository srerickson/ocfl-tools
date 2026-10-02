package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/srerickson/ocfl-go"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	fsconfig "github.com/srerickson/ocfl-go/fs/config"
)

const (
	// Environment variables for flag defaults are set with `ocflenv` tags:
	// see envResolver.

	// if "true", enable path-style addressing for s3
	envVarS3PathStyle = "OCFL_S3_PATHSTYLE"

	// s3 settings used as defaults for s3:// locations
	envVarAWSEndpoint = "AWS_ENDPOINT_URL"
	envVarAWSRegion   = "AWS_REGION"
)

func CLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	var cli cli
	env := &cmdEnv{
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		getenv: getenv,
	}
	// release local file descriptors however the command ends (success,
	// error, panic, or cancellation).
	defer env.closeAll()
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
		},
		kong.ConfigureHelp(kong.HelpOptions{
			Summary: true,
			Compact: true,
		}),
		// --help calls Exit after printing help: return from CLI instead
		// of exiting the process.
		kong.Exit(func(code int) { panic(kongExit(code)) }),
		kong.Resolvers(envResolver(getenv)),
		kong.ValueFormatter(envHelpFormatter),
		// values passed to commands' Run methods
		kong.BindTo(ctx, (*context.Context)(nil)),
		kong.Bind(env),
	)
	if err != nil {
		fmt.Fprintln(stderr, "in kong configuration:", err.Error())
		return err
	}
	kongCtx, err := parse(parser, args[1:])
	if err != nil {
		if errors.Is(err, errHelp) {
			return nil
		}
		fmt.Fprintln(stderr, err.Error())
		var parseErr *kong.ParseError
		if errors.As(err, &parseErr) {
			parseErr.Context.PrintUsage(true)
		}
		return err
	}
	logLevel := log.InfoLevel
	if cli.Debug {
		logLevel = log.DebugLevel
	}
	env.logger = newLogger(logLevel, stderr)
	env.rootLocation = cli.RootLocation
	if err := kongCtx.Run(); err != nil {
		env.logger.Error(err.Error())
		return err
	}
	return nil
}

// kongExit is the panic value used by the parser's exit function.
type kongExit int

// errHelp is returned by parse if the arguments asked for help, which has
// been printed.
var errHelp = errors.New("help requested")

// parse parses args with parser, converting a call to the parser's exit
// function into an error.
func parse(parser *kong.Kong, args []string) (kongCtx *kong.Context, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		code, ok := r.(kongExit)
		if !ok {
			panic(r)
		}
		kongCtx, err = nil, errHelp
		if code != 0 {
			err = fmt.Errorf("exit status %d", code)
		}
	}()
	return parser.Parse(args)
}

// envResolver sets flags with an `ocflenv:"NAME"` tag from the environment
// variable NAME if they aren't set on the command line. It's used instead of
// kong's `env` tag, which reads the process environment rather than the
// getenv passed to CLI.
type envResolver func(string) string

func (getenv envResolver) Validate(*kong.Application) error { return nil }

func (getenv envResolver) Resolve(_ *kong.Context, _ *kong.Path, flag *kong.Flag) (any, error) {
	name := flag.Tag.Get("ocflenv")
	if name == "" {
		return nil, nil
	}
	if val := getenv(name); val != "" {
		return val, nil
	}
	return nil, nil
}

// envHelpFormatter adds the environment variable from a flag's `ocflenv` tag
// to its help text.
func envHelpFormatter(value *kong.Value) string {
	name := value.Tag.Get("ocflenv")
	if name == "" {
		return value.Help
	}
	return value.Help + " ($" + name + ")"
}

type cli struct {
	RootLocation string `name:"root" ocflenv:"OCFL_ROOT" help:"The prefix/directory of the OCFL storage root used for the command"`
	Debug        bool   `name:"debug" help:"enable debug log messages"`

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

// cmdEnv is what commands use to interact with their environment: standard
// i/o, environment variables, logging, and storage. It's passed to every
// command's Run method.
type cmdEnv struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string
	logger *slog.Logger

	rootLocation string // from --root or $OCFL_ROOT

	closeMu sync.Mutex
	closers []io.Closer // resources opened by parseLocation
}

// parseLocation converts a location, which may be a local path or a 'file://',
// 's3://' or 'http(s)://' url, into an FS and a path within it. Local file
// systems are closed when the CLI returns.
func (env *cmdEnv) parseLocation(ctx context.Context, loc string) (ocflfs.FS, string, error) {
	if loc == "" {
		return nil, "", errors.New("location not set")
	}
	loc, err := env.withS3Defaults(loc)
	if err != nil {
		return nil, "", err
	}
	fsCfg, err := fsconfig.New(ctx, loc, fsconfig.WithLogger(env.logger))
	if err != nil {
		return nil, "", err
	}
	if closer, ok := fsCfg.FS.(io.Closer); ok {
		env.addCloser(closer)
	}
	return fsCfg.FS, fsCfg.Path, nil
}

// withS3Defaults fills in the region, endpoint, and path-style settings of
// an s3:// location from the environment (as seen through env.getenv) if they
// aren't already part of the location. Other locations are returned as-is.
// Credentials are left to the default AWS configuration.
func (env *cmdEnv) withS3Defaults(loc string) (string, error) {
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
	setDefault("region", env.getenv(envVarAWSRegion))
	setDefault("endpoint", env.getenv(envVarAWSEndpoint))
	if strings.EqualFold(env.getenv(envVarS3PathStyle), "true") {
		setDefault("path-style", "true")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// addCloser registers c to be closed by closeAll.
func (env *cmdEnv) addCloser(c io.Closer) {
	env.closeMu.Lock()
	defer env.closeMu.Unlock()
	env.closers = append(env.closers, c)
}

// closeAll closes everything registered with addCloser, in reverse order.
func (env *cmdEnv) closeAll() {
	env.closeMu.Lock()
	closers := env.closers
	env.closers = nil
	env.closeMu.Unlock()
	for i := len(closers) - 1; i >= 0; i-- {
		if err := closers[i].Close(); err != nil && env.logger != nil {
			env.logger.Warn("closing storage backend", "err", err.Error())
		}
	}
}

// mkLocalDir creates the directory for loc if loc is a local path or a file
// url. Local backends require the directory to exist, so this is needed before
// initializing a new storage root. It does nothing for s3 or http(s) locations.
//
// The returned function removes whatever mkLocalDir created, so a failed
// initialization doesn't leave empty directories behind. It never removes
// directories that already existed or have contents, and is never nil if err is nil.
func mkLocalDir(loc string) (undo func(), err error) {
	undo = func() {}
	if loc == "" {
		return nil, errors.New("location not set")
	}
	locUrl, err := url.Parse(loc)
	if err != nil {
		return nil, err
	}
	switch {
	case locUrl.Scheme == "file":
		loc = locUrl.Path
	case len(locUrl.Scheme) > 1:
		return undo, nil // s3, http(s)
	}
	// find the outermost directory that doesn't exist yet
	var created string
	for dir := filepath.Clean(loc); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(dir); err == nil || !errors.Is(err, fs.ErrNotExist) {
			break
		}
		created = dir
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if err := os.MkdirAll(loc, 0o777); err != nil {
		return nil, err
	}
	if created == "" {
		return undo, nil
	}
	return func() {
		// remove empty directories from the innermost out, up to created.
		for dir := filepath.Clean(loc); ; dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil || dir == created {
				return
			}
		}
	}, nil
}

func (env *cmdEnv) getRoot(ctx context.Context) (*ocfl.Root, error) {
	fsys, dir, err := env.parseLocation(ctx, env.rootLocation)
	if err != nil {
		return nil, err
	}
	root, err := ocfl.NewRoot(ctx, fsys, dir)
	if err != nil {
		rootcnf := locationString(fsys, dir)
		return nil, fmt.Errorf("reading OCFL storage root %s: %w", rootcnf, err)
	}
	return root, nil
}

// newObject using id (if set) or full object path. if mustExist is true
// the object's existence is checked.
func (env *cmdEnv) newObject(ctx context.Context, id, objPath string, opts ...ocfl.ObjectOption) (*ocfl.Object, error) {
	if id == "" && objPath == "" {
		err := errors.New("must provide an object ID or an object path")
		return nil, err
	}
	if id == "" {
		fsys, dir, err := env.parseLocation(ctx, objPath)
		if err != nil {
			return nil, err
		}
		obj, err := ocfl.NewObject(ctx, fsys, dir, opts...)
		if err != nil {
			return nil, fmt.Errorf("reading object at path: %q: %w", objPath, err)
		}
		return obj, nil
	}
	root, err := env.getRoot(ctx)
	if err != nil {
		return nil, err
	}
	obj, err := root.NewObject(ctx, id, opts...)
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
