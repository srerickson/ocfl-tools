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
	ocflfs "github.com/srerickson/ocfl-go/fs"
	"github.com/srerickson/ocfl-go/fs/local"
	ocflS3 "github.com/srerickson/ocfl-go/fs/s3"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/httpfs"
)

const (
	envVarRoot      = "OCFL_ROOT"       // storage root location string
	envVarUserName  = "OCFL_USER_NAME"  // user name for commit
	envVarUserEmail = "OCFL_USER_EMAIL" // user email for commit

	// if "true", enable path-style addressing for s3
	envVarS3PathStyle = "OCFL_S3_PATHSTYLE"

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

	RootLocation string `name:"root" help:"The prefix/directory of the OCFL storage root used for the command ($$${env_root})"`
	Debug        bool   `name:"debug" help:"enable debug log messages"`
}

// convert a location, which may be a local path or an 's3://' path, into
// an FS and a path.
func (g *globals) parseLocation(loc string) (ocflfs.FS, string, error) {
	if loc == "" {
		return nil, "", errors.New("location not set")
	}
	locUrl, err := url.Parse(loc)
	if err != nil {
		return nil, "", err
	}
	switch locUrl.Scheme {
	case "s3":
		var awsOpts []func(*config.LoadOptions) error
		var s3Opts []func(*s3.Options)
		bucket := locUrl.Host
		prefix := strings.TrimPrefix(locUrl.Path, "/")
		// values passed through getenv are mostly for testing.
		envKey := g.getenv(envVarAWSKey)
		envSecret := g.getenv(envVarAWSSecret)
		envRegion := g.getenv(envVarAWSRegion)
		envEndpoint := g.getenv(envVarAWSEndpoint)
		if envKey != "" && envSecret != "" {
			creds := credentials.NewStaticCredentialsProvider(envKey, envSecret, "")
			awsOpts = append(awsOpts, config.WithCredentialsProvider(creds))
		}
		if envRegion != "" {
			awsOpts = append(awsOpts, config.WithRegion(envRegion))
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
		cfg, err := config.LoadDefaultConfig(g.ctx, awsOpts...)
		if err != nil {
			return nil, "", err
		}
		s3Client := s3.NewFromConfig(cfg, s3Opts...)
		fsys := &ocflS3.BucketFS{S3: s3Client, Bucket: bucket, Logger: g.logger}
		return fsys, prefix, nil
	case "http", "https":
		fsys := httpfs.New(loc)
		return fsys, ".", nil
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

func locationString(fsys ocflfs.FS, dir string) string {
	switch fsys := fsys.(type) {
	case *httpfs.FS:
		base := fsys.URL()
		if dir == "." {
			return base
		}
		return base + "/" + path.Clean(dir)
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
