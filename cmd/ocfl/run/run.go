package run

import (
	"context"
	_ "embed"
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
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/charmbracelet/log"
	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/backend/local"
	ocflS3 "github.com/srerickson/ocfl-go/backend/s3"
	"github.com/srerickson/ocfl-go/ocflv1"
)

const (
	envVarRoot        = "OCFL_ROOT"         // storage root location string
	envVarUserName    = "OCFL_USER_NAME"    // user name for commit
	envVarUserEmail   = "OCFL_USER_EMAIL"   // user email for commit
	envVarS3PathStyle = "OCFL_S3_PATHSTYLE" // if "true", enable path-style addressing for s3

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
	Export   exportCmd   `cmd:"export" help:"${export_help}"`
	Diff     DiffCmd     `cmd:"diff" help:"${diff_help}"`
	Info     InfoCmd     `cmd:"info" help:"${info_help}"`
	LS       lsCmd       `cmd:"ls" help:"${ls_help}"`
	Log      LogCmd      `cmd:"log" help:"${log_help}"`
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
			"info_help":      infoHelp,
			"init_root_help": initRootHelp,
			"ls_help":        lsHelp,
			"log_help":       logHelp,
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
	case "log":
		runner = &cli.Log
	case "export":
		runner = &cli.Export
	case "diff":
		runner = &cli.Diff
	case "validate":
		runner = &cli.Validate
	case "info":
		runner = &cli.Info
	default:
		kongCtx.PrintUsage(true)
		err = fmt.Errorf("unknown command: %s", kongCtx.Command())
		logger.Error(err.Error())
		return err
	}
	// fsys is nil if rootConfig is empty
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
		bucket := rl.Host
		prefix := strings.TrimPrefix(rl.Path, "/")
		// values passed through getenv are mostly for testing.
		envKey := getenv(envVarAWSKey)
		envSecret := getenv(envVarAWSSecret)
		envRegion := getenv(envVarAWSRegion)
		envEndpoint := getenv(envVarAWSEndpoint)
		if envKey != "" && envSecret != "" {
			creds := credentials.NewStaticCredentialsProvider(envKey, envSecret, "")
			loadOpts = append(loadOpts, config.WithCredentialsProvider(creds))
		}
		if envRegion != "" {
			loadOpts = append(loadOpts, config.WithRegion(envRegion))
		}
		cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return nil, "", err
		}
		var s3Opts []func(*s3.Options)
		if envEndpoint != "" {
			s3Opts = append(s3Opts, func(o *s3.Options) {
				o.BaseEndpoint = aws.String(envEndpoint)
			})
		}
		if strings.EqualFold(getenv(envVarS3PathStyle), "true") {
			s3Opts = append(s3Opts, func(o *s3.Options) {
				o.UsePathStyle = true
			})
		}
		s3Client := s3.NewFromConfig(cfg, s3Opts...)
		fsys := &ocflS3.BucketFS{S3: s3Client, Bucket: bucket, Logger: logger}
		return fsys, prefix, nil
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

func locationString(fsys ocfl.FS, dir string) string {
	switch fsys := fsys.(type) {
	case *ocflS3.BucketFS:
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
	fmt.Fprintln(stdout, "ocfl: v"+Version)
	fmt.Fprintln(stdout, "date:", BuildTime)
	if rev := codeRev(); rev != "" {
		fmt.Fprintln(stdout, "commit: ", rev[:8])
	}
}

func newLogger(l log.Level, w io.Writer) *slog.Logger {
	handl := log.New(w)
	handl.SetLevel(l)
	return slog.New(handl)
}
