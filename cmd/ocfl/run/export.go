package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"

	"github.com/srerickson/ocfl-go"
)

type ExportCmd struct {
	objectFlags
	Version  int      `name:"version" short:"v" default:"0" help:"The number (unpadded) of the object version from which to export content"`
	Replace  bool     `name:"replace" help:"replace existing files with object contents"`
	SrcDir   string   `name:"dir" short:"d" default:"." help:"An object directory to export. Defaults to the object's logical root. Ignored if --file is set."`
	SrcFiles []string `name:"file" short:"f" help:"Object file(s) to export. Wildcards (*,?,[]) can be used to match multiple files. This flag can be repeated."`
	To       string   `name:"to" short:"t" default:"." help:"The destination directory for writing exported content. For single file exports, use '-' to print file to STDOUT or a file name."`
}

func (cmd *ExportCmd) Run(ctx context.Context, env *cmdEnv) error {
	obj, err := cmd.open(ctx, env, ocfl.ObjectMustExist())
	if err != nil {
		return err
	}
	versionFS, err := obj.VersionFS(ctx, cmd.Version)
	if err != nil {
		return err
	}
	// check destination: it doesn't need to exist, but its parent should be an
	// existing directory.
	var absTo string
	if cmd.To != "-" {
		absTo, err = filepath.Abs(cmd.To)
		if err != nil {
			return fmt.Errorf("invalid value for --to: %w", err)
		}
		parentDir := filepath.Dir(absTo)
		exists, isDir, err := stat(parentDir)
		if err != nil {
			return err
		}
		if !exists || !isDir {
			err := errors.New("not an existing directory: " + parentDir)
			return err
		}
	}
	if len(cmd.SrcFiles) < 1 {
		if cmd.To == "-" {
			err := errors.New("exporting to STDOUT requires --file flag")
			return err
		}
		subFS, err := fs.Sub(versionFS, cmd.SrcDir)
		if err != nil {
			return err
		}
		return exportFS(ctx, env.logger, absTo, subFS, cmd.Replace)
	}
	var matches []string
	for _, srcFile := range cmd.SrcFiles {
		m, err := fs.Glob(versionFS, srcFile)
		if err != nil {
			return err
		}
		matches = append(matches, m...)
	}
	if len(matches) < 1 {
		err = errors.New("no matching files in the object")
		return err
	}
	if cmd.To == "-" {
		// print first match to STDOUT
		return exportFile(versionFS, matches[0], false, env.stdout)
	}
	exists, isDir, err := stat(absTo)
	if err != nil {
		return err
	}
	// single match: we can create/overwrite destination as file
	if (!exists || !isDir) && len(matches) == 1 {
		return exportFile(versionFS, matches[0], cmd.Replace, nil, absTo)
	}
	// copy matching files into the destination, which must be an existing directory
	if !isDir {
		err = errors.New("not an existing directory: " + absTo)
		return err
	}
	for _, file := range matches {
		dstName := filepath.Join(absTo, path.Base(file))
		if err := exportFile(versionFS, file, cmd.Replace, nil, dstName); err != nil {
			return err
		}
	}
	return nil
}

func exportFile(srcFS fs.FS, srcName string, replace bool, stdout io.Writer, dstNames ...string) (err error) {
	f, err := srcFS.Open(srcName)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	if stdout != nil {
		_, err = io.Copy(stdout, f)
		return
	}
	const fileMode, dirMode fs.FileMode = 0o664, 0o775
	perm := os.O_WRONLY | os.O_CREATE
	switch {
	case replace:
		// replace file if it exists
		perm |= os.O_TRUNC
	default:
		// file must not exist
		perm |= os.O_EXCL
	}
	writers := make([]io.Writer, len(dstNames))
	for i, name := range dstNames {
		var f *os.File
		if err = os.MkdirAll(filepath.Dir(name), dirMode); err != nil {
			return
		}
		f, err = os.OpenFile(name, perm, fileMode)
		if err != nil {
			return
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}()
		writers[i] = f
	}
	_, err = io.Copy(io.MultiWriter(writers...), f)
	return
}

func stat(dir string) (exists bool, isDir bool, err error) {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	return true, info.IsDir(), nil
}

func exportFS(ctx context.Context, logger *slog.Logger, dstDir string, srcFS fs.FS, replace bool) error {
	return fs.WalkDir(srcFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		dstPath := filepath.Join(dstDir, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}
		srcFile, err := srcFS.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()
		fileFlag := os.O_CREATE | os.O_WRONLY | os.O_EXCL
		if replace {
			fileFlag = os.O_CREATE | os.O_TRUNC | os.O_WRONLY
		}
		w, err := os.OpenFile(dstPath, fileFlag, 0644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, srcFile); err != nil {
			w.Close()
			return &os.PathError{Op: "copy", Path: dstPath, Err: err}
		}
		if err := w.Close(); err != nil {
			return err
		}
		logger.Log(ctx, slog.LevelInfo, "copied", "file", dstPath)
		return nil
	})
}
