package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsS3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/backend/local"
	"github.com/srerickson/ocfl-go/backend/s3"
	"github.com/srerickson/ocfl-go/ocflv1"
)

const (
	envVarRoot = "OCFL_ROOT"

	// keys that can be used in tests
	envVarAWSKey      = "AWS_ACCESS_KEY_ID"
	envVarAWSSecret   = "AWS_SECRET_ACCESS_KEY"
	envVarAWSEndpoint = "AWS_ENDPOINT_URL"
	envVarAWSRegion   = "AWS_REGION"
)

var (
	Version   string // set by -ldflags
	BuildTime string // set by -ldflags
)

var cli struct {
	RootConfig string `name:"root" short:"r" help:"The prefix/directory of the OCFL storage root used for the command ($$${env_root})"`
	Debug      bool   `name:"debug" help:"enable debug log messages"`

	InitRoot initRootCmd `cmd:"init-root" help:"${init_root_help}"`
	Commit   commitCmd   `cmd:"commit" help:"${commit_help}"`
	LS       lsCmd       `cmd:"ls" help:"${ls_help}"`
	Export   exportCmd   `cmd:"export" help:"${export_help}"`
	Diff     DiffCmd     `cmd:"diff" help:"${diff_help}"`
	Validate ValidateCmd `cmd:"validate" help:"${validate_help}"`
	Version  struct{}    `cmd:"version" help:"Print ocfl-tools version information"`
}

func CLI(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	ocflv1.Enable() // hopefuly this won't be necessary in the near future.
	parser, err := kong.New(&cli, kong.Name("ocfl"),
		kong.Writers(stdout, stderr),
		kong.Description("tools for working with OCFL repositories"),
		kong.Vars{
			"commit_help":    commitHelp,
			"diff_help":      diffHelp,
			"export_help":    exportHelp,
			"init_root_help": initRootHelp,
			"ls_help":        lsHelp,
			"validate_help":  validateHelp,
			"env_root":       envVarRoot,
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
	logLevel := log.WarnLevel
	if cli.Debug {
		logLevel = log.DebugLevel
	}
	logger := newLogger(logLevel, stderr)
	// root config from flag or environment
	rootConifg := cli.RootConfig
	if rootConifg == "" {
		rootConifg = getenv(envVarRoot)
	}
	// commands that don't require an existing root
	switch kongCtx.Command() {
	case "init-root":
		if err := cli.InitRoot.Run(ctx, rootConifg, stdout, logger, getenv); err != nil {
			logger.Error(err.Error())
			return err
		}
		return nil
	case "version":
		printVersion(stdout)
		return nil
	}
	// run a command on existing root
	var runner interface {
		Run(ctx context.Context, root *ocfl.Root, stdout io.Writer, logger *slog.Logger, getenv func(string) string) error
	}
	switch kongCtx.Command() {
	case "commit <path>":
		runner = &cli.Commit
	case "ls":
		runner = &cli.LS
	case "export":
		runner = &cli.Export
	case "diff":
		runner = &cli.Diff
	case "validate":
		runner = &cli.Validate
	default:
		kongCtx.PrintUsage(true)
		err = fmt.Errorf("unknown command: %s", kongCtx.Command())
		logger.Error(err.Error())
		return err
	}
	fsys, dir, err := parseLocation(ctx, rootConifg, logger, getenv)
	if err != nil {
		logger.Error("parsing OCFL root path: " + err.Error())
		return err
	}
	var root *ocfl.Root
	if fsys != nil {
		root, err = ocfl.NewRoot(ctx, fsys, dir)
		if err != nil {
			rootcnf := locationString(fsys, dir)
			logger.Error("reading OCFL storage root: " + rootcnf + ": " + err.Error())
			return err
		}
	}
	// root may be nil
	if err := runner.Run(ctx, root, stdout, logger, getenv); err != nil {
		logger.Error(err.Error())
		return err
	}
	return nil
}

// convert a location, which may be a local path or an 's3://' path, into
// an FS and a path.
func parseLocation(ctx context.Context, location string, logger *slog.Logger, getenv func(string) string) (ocfl.WriteFS, string, error) {
	if location == "" {
		return nil, "", nil
	}
	rl, err := url.Parse(location)
	if err != nil {
		return nil, "", err
	}
	switch rl.Scheme {
	case "s3":
		var loadOpts []func(*config.LoadOptions) error
		if secret := getenv(envVarAWSSecret); secret != "" {
			key := getenv(envVarAWSKey)
			creds := credentials.NewStaticCredentialsProvider(key, secret, "")
			loadOpts = append(loadOpts, config.WithCredentialsProvider(creds))
		}
		if region := getenv(envVarAWSRegion); region != "" {
			loadOpts = append(loadOpts, config.WithRegion(region))
		}
		cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return nil, "", err
		}
		var s3Opts []func(*awsS3.Options)
		// if endpoint := getenv(envVarAWSEndpoint); endpoint != "" {
		// 	s3Opts = append(s3Opts, awsS3.WithEndpointResolverV2())
		// }
		//awsS3.EndpointResolverFromURL("")
		fsys := &s3.BucketFS{
			S3:     awsS3.NewFromConfig(cfg, s3Opts...),
			Bucket: rl.Host,
			Logger: logger,
		}
		return fsys, strings.TrimPrefix(rl.Path, "/"), nil
	default:
		absPath, err := filepath.Abs(location)
		if err != nil {
			return nil, "", err
		}
		fsys, err := local.NewFS(absPath)
		if err != nil {
			return nil, "", err
		}
		return fsys, ".", nil
	}
}

func locationString(fsys ocfl.WriteFS, dir string) string {
	switch fsys := fsys.(type) {
	case *s3.BucketFS:
		return "s3://" + path.Join(fsys.Bucket, dir)
	case *local.FS:
		localDir, err := filepath.Localize(dir)
		if err != nil {
			panic(err)
		}
		return filepath.Join(fsys.Root(), localDir)
	default:
		panic(errors.New("unsupported backend type"))
	}
}

func codeRev() string {
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

func printVersion(stdout io.Writer) {
	fmt.Fprintf(stdout, "ocfl %s, built: %s", Version, BuildTime)
	if rev := codeRev(); rev != "" {
		fmt.Fprintf(stdout, ", commit: [%s]", rev[:8])
	}
	fmt.Fprintln(stdout)
}

func newLogger(l log.Level, w io.Writer) *slog.Logger {
	handl := log.New(w)
	handl.SetLevel(l)
	return slog.New(handl)
}
