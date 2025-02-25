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

func CLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {

	parser, err := kong.New(&cli, kong.Name("ocfl"),
		kong.Writers(stdout, stderr),
		kong.Description("command line tool for working with OCFL repositories"),
		kong.Vars{
			"commit_help":    commitHelp,
			"diff_help":      diffHelp,
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
	Export   exportCmd   `cmd:"" help:"${export_help}"`
	Info     InfoCmd     `cmd:"" help:"${info_help}"`
	InitRoot initRootCmd `cmd:"" help:"${init_root_help}"`
	Log      LogCmd      `cmd:"" help:"${log_help}"`
	Ls       lsCmd       `cmd:"" help:"${ls_help}"`
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

	RootLocation string `name:"root" help:"The prefix/directory of the OCFL storage root used for the command ($$${env_root})"`
	Debug        bool   `name:"debug" help:"enable debug log messages"`
}

// convert a location, which may be a local path or an 's3://' path, into
// an FS and a path.
func (g *globals) parseLocation(loc string) (ocfl.WriteFS, string, error) {
	if loc == "" {
		return nil, "", errors.New("location not set")
	}
	rl, err := url.Parse(loc)
	if err != nil {
		return nil, "", err
	}
	switch rl.Scheme {
	case "s3":
		var loadOpts []func(*config.LoadOptions) error
		bucket := rl.Host
		prefix := strings.TrimPrefix(rl.Path, "/")
		// values passed through getenv are mostly for testing.
		envKey := g.getenv(envVarAWSKey)
		envSecret := g.getenv(envVarAWSSecret)
		envRegion := g.getenv(envVarAWSRegion)
		envEndpoint := g.getenv(envVarAWSEndpoint)
		if envKey != "" && envSecret != "" {
			creds := credentials.NewStaticCredentialsProvider(envKey, envSecret, "")
			loadOpts = append(loadOpts, config.WithCredentialsProvider(creds))
		}
		if envRegion != "" {
			loadOpts = append(loadOpts, config.WithRegion(envRegion))
		}
		cfg, err := config.LoadDefaultConfig(g.ctx, loadOpts...)
		if err != nil {
			return nil, "", err
		}
		s3Opts := []func(*s3.Options){
			// Prevent "Response has no supported checksum" log messages
			// https://github.com/aws/aws-sdk-go-v2/issues/2999
			func(o *s3.Options) { o.DisableLogOutputChecksumValidationSkipped = true },
		}
		if envEndpoint != "" {
			s3Opts = append(s3Opts, func(o *s3.Options) {
				o.BaseEndpoint = aws.String(envEndpoint)
			})
		}
		if strings.EqualFold(g.getenv(envVarS3PathStyle), "true") {
			s3Opts = append(s3Opts, func(o *s3.Options) {
				o.UsePathStyle = true
			})
		}
		s3Client := s3.NewFromConfig(cfg, s3Opts...)
		fsys := &ocflS3.BucketFS{S3: s3Client, Bucket: bucket, Logger: g.logger}
		return fsys, prefix, nil
	default:
		absPath, err := filepath.Abs(loc)
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

func newLogger(l log.Level, w io.Writer) *slog.Logger {
	handl := log.New(w)
	handl.SetLevel(l)
	return slog.New(handl)
}
