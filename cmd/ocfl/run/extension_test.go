package run_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestExtensionCmd(t *testing.T) {
	// Create a temp storage root
	tmpDir := t.TempDir()

	// Initialize a storage root
	args := []string{"init-root", "--root", tmpDir}
	testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})

	t.Run("list extensions", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "0004-hashed-n-tuple-storage-layout", stdout)
		})
	})

	t.Run("show extension config", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "0004-hashed-n-tuple-storage-layout",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, `"extensionName": "0004-hashed-n-tuple-storage-layout"`, stdout)
			be.In(t, `"tupleSize"`, stdout)
		})
	})

	t.Run("show nonexistent extension", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "nonexistent",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "not found", err.Error())
		})
	})

	t.Run("create extension with defaults", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "0002-flat-direct-storage-layout",
			"--create",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "created extension", stderr)
		})

		// Verify config was created
		configPath := filepath.Join(tmpDir, "extensions", "0002-flat-direct-storage-layout", "config.json")
		data, err := os.ReadFile(configPath)
		be.NilErr(t, err)
		be.In(t, `"extensionName": "0002-flat-direct-storage-layout"`, string(data))
	})

	t.Run("create unknown extension fails", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "unknown-extension",
			"--create",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "unknown extension", err.Error())
		})
	})

	t.Run("set field on root extension", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "0004-hashed-n-tuple-storage-layout",
			"--set", "tupleSize:5",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})

		// Verify config was updated
		configPath := filepath.Join(tmpDir, "extensions", "0004-hashed-n-tuple-storage-layout", "config.json")
		data, err := os.ReadFile(configPath)
		be.NilErr(t, err)
		be.In(t, "\"tupleSize\": 5", string(data))
	})

	t.Run("set nested field", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "0004-hashed-n-tuple-storage-layout",
			"--set", `custom.nested:"value"`,
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})

		configPath := filepath.Join(tmpDir, "extensions", "0004-hashed-n-tuple-storage-layout", "config.json")
		data, err := os.ReadFile(configPath)
		be.NilErr(t, err)
		be.In(t, `"custom"`, string(data))
		be.In(t, `"nested": "value"`, string(data))
	})

	t.Run("unset field", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "0004-hashed-n-tuple-storage-layout",
			"--unset", "custom.nested",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})

		configPath := filepath.Join(tmpDir, "extensions", "0004-hashed-n-tuple-storage-layout", "config.json")
		data, err := os.ReadFile(configPath)
		be.NilErr(t, err)
		be.NotIn(t, `"nested": "value"`, string(data))
	})

	t.Run("create new custom extension with set", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "my-custom-extension",
			"--set", `myField:"myValue"`,
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})

		configPath := filepath.Join(tmpDir, "extensions", "my-custom-extension", "config.json")
		data, err := os.ReadFile(configPath)
		be.NilErr(t, err)
		be.In(t, `"extensionName": "my-custom-extension"`, string(data))
		be.In(t, `"myField": "myValue"`, string(data))
	})

	t.Run("remove extension", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "my-custom-extension",
			"--remove",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})

		extDir := filepath.Join(tmpDir, "extensions", "my-custom-extension")
		_, err := os.Stat(extDir)
		be.True(t, os.IsNotExist(err))
	})

	t.Run("error: id and object together", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--id", "obj1",
			"--object", "/some/path",
			"--name", "ext",
			"--set", "a:1",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
		})
	})

	t.Run("error: root-extension with id", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--id", "obj1",
			"--name", "ext",
			"--set", "a:1",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
		})
	})

	t.Run("error: name required for modification", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--set", "a:1",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
			be.In(t, "--name is required", err.Error())
		})
	})

	t.Run("error: multiple operations", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--root-extension",
			"--name", "ext",
			"--set", "a:1",
			"--remove",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
		})
	})

	t.Run("error: no target specified", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--name", "ext",
			"--set", "a:1",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.Nonzero(t, err)
		})
	})
}

func TestExtensionCmd_Object(t *testing.T) {
	// Create a temp storage root
	tmpDir := t.TempDir()
	contentDir := t.TempDir()

	// Create test content
	os.WriteFile(filepath.Join(contentDir, "test.txt"), []byte("test"), 0644)

	// Initialize a storage root
	args := []string{"init-root", "--root", tmpDir}
	testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})

	// Create an object
	args = []string{
		"commit",
		"--root", tmpDir,
		"--id", "test-obj",
		"--name", "Test User",
		"--message", "test",
		contentDir,
	}
	testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
		be.NilErr(t, err)
	})

	t.Run("list extensions on object (empty)", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--id", "test-obj",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "no extensions found", stdout)
		})
	})

	t.Run("add extension to object via id", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--id", "test-obj",
			"--name", "my-obj-ext",
			"--set", `objField:"objValue"`,
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "set extension config field", stderr)
		})
	})

	t.Run("list extensions on object (has extension)", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--id", "test-obj",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "my-obj-ext", stdout)
		})
	})

	t.Run("show extension on object", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--id", "test-obj",
			"--name", "my-obj-ext",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, `"objField": "objValue"`, stdout)
		})
	})

	t.Run("update extension on object via id", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--id", "test-obj",
			"--name", "my-obj-ext",
			"--set", "anotherField:42",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
		})
	})

	t.Run("remove extension from object", func(t *testing.T) {
		args := []string{
			"extension",
			"--root", tmpDir,
			"--id", "test-obj",
			"--name", "my-obj-ext",
			"--remove",
		}
		testutil.RunCLI(args, nil, func(err error, stdout, stderr string) {
			be.NilErr(t, err)
			be.In(t, "removed extension", stderr)
		})
	})
}
