package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	ocflfs "github.com/srerickson/ocfl-go/fs"
	"github.com/srerickson/ocfl-go/extension"
)

const (
	extensionHelp    = "Manage extensions in an OCFL storage root or object"
	extensionsDir    = "extensions"
	configFileName   = "config.json"
)

type ExtensionCmd struct {
	RootExtension bool   `name:"root-extension" help:"Update storage root extension (instead of object extension)"`
	ObjPath       string `name:"object" help:"Full path to object root. Cannot be combined with --id."`
	ID            string `name:"id" short:"i" help:"The ID of the object to update. Cannot be combined with --object."`
	Name          string `name:"name" help:"Extension name. If omitted, lists all extensions."`
	Create        bool   `name:"create" help:"Create extension with default values"`
	Set           string `name:"set" help:"Field and value to set, format: 'field.path:jsonValue'"`
	Unset         string `name:"unset" help:"Field to remove from extension's config.json"`
	Remove        bool   `name:"remove" help:"Remove the extension and all its content"`
}

func (cmd *ExtensionCmd) Run(g *globals) error {
	// Validate flag combinations
	if cmd.ID != "" && cmd.ObjPath != "" {
		return errors.New("--id and --object cannot be used together")
	}
	if !cmd.RootExtension && cmd.ID == "" && cmd.ObjPath == "" {
		return errors.New("must specify --root-extension, --id, or --object")
	}
	if cmd.RootExtension && (cmd.ID != "" || cmd.ObjPath != "") {
		return errors.New("--root-extension cannot be combined with --id or --object")
	}

	// Count modification operations
	opsCount := 0
	if cmd.Set != "" {
		opsCount++
	}
	if cmd.Unset != "" {
		opsCount++
	}
	if cmd.Create {
		opsCount++
	}
	if cmd.Remove {
		opsCount++
	}

	// Validate mutually exclusive operations
	if opsCount > 1 {
		return errors.New("--set, --unset, --create, and --remove are mutually exclusive")
	}

	// If a modification operation is specified, --name is required
	if opsCount > 0 && cmd.Name == "" {
		return errors.New("--name is required when using --set, --unset, --create, or --remove")
	}

	// Determine the base filesystem and extensions directory path
	var fsys ocflfs.FS
	var extsDirPath string
	var err error

	if cmd.RootExtension {
		fsys, extsDirPath, err = cmd.getRootExtensionsPath(g)
	} else {
		fsys, extsDirPath, err = cmd.getObjectExtensionsPath(g)
	}
	if err != nil {
		return err
	}

	// If no --name provided, list all extensions
	if cmd.Name == "" {
		return cmd.listExtensions(g, fsys, extsDirPath)
	}

	extPath := path.Join(extsDirPath, cmd.Name)

	// Execute the requested operation
	switch {
	case cmd.Remove:
		return cmd.removeExtension(g, fsys, extPath)
	case cmd.Create:
		return cmd.createExtension(g, fsys, extPath)
	case cmd.Set != "":
		return cmd.setField(g, fsys, extPath)
	case cmd.Unset != "":
		return cmd.unsetField(g, fsys, extPath)
	default:
		// No modification flag - show current values
		return cmd.showExtension(g, fsys, extPath)
	}
}

func (cmd *ExtensionCmd) getRootExtensionsPath(g *globals) (ocflfs.FS, string, error) {
	root, err := g.getRoot()
	if err != nil {
		return nil, "", err
	}
	extsDirPath := path.Join(root.Path(), extensionsDir)
	return root.FS(), extsDirPath, nil
}

func (cmd *ExtensionCmd) getObjectExtensionsPath(g *globals) (ocflfs.FS, string, error) {
	obj, err := g.newObject(cmd.ID, cmd.ObjPath)
	if err != nil {
		return nil, "", err
	}
	extsDirPath := path.Join(obj.Path(), extensionsDir)
	return obj.FS(), extsDirPath, nil
}

func (cmd *ExtensionCmd) listExtensions(g *globals, fsys ocflfs.FS, extsDirPath string) error {
	entries, err := ocflfs.ReadDir(g.ctx, fsys, extsDirPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintln(g.stdout, "no extensions found")
			return nil
		}
		return fmt.Errorf("reading extensions directory: %w", err)
	}

	var extNames []string
	for _, entry := range entries {
		if entry.IsDir() {
			extNames = append(extNames, entry.Name())
		}
	}

	if len(extNames) == 0 {
		fmt.Fprintln(g.stdout, "no extensions found")
		return nil
	}

	for _, name := range extNames {
		fmt.Fprintln(g.stdout, name)
	}
	return nil
}

func (cmd *ExtensionCmd) showExtension(g *globals, fsys ocflfs.FS, extPath string) error {
	configPath := path.Join(extPath, configFileName)
	config, err := readConfig(g, fsys, configPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("extension %q not found", cmd.Name)
		}
		return fmt.Errorf("reading extension config: %w", err)
	}

	// Pretty print the config
	data, err := json.MarshalIndent(config, "", "    ")
	if err != nil {
		return fmt.Errorf("formatting config: %w", err)
	}
	fmt.Fprintln(g.stdout, string(data))
	return nil
}

func (cmd *ExtensionCmd) createExtension(g *globals, fsys ocflfs.FS, extPath string) error {
	writeFS, ok := fsys.(ocflfs.WriteFS)
	if !ok {
		return errors.New("filesystem does not support write operations")
	}

	// Get default extension from registry
	reg := extension.DefaultRegistry()
	ext, err := reg.New(cmd.Name)
	if err != nil {
		return fmt.Errorf("unknown extension %q: %w", cmd.Name, err)
	}

	// Marshal extension to JSON
	data, err := json.MarshalIndent(ext, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling extension config: %w", err)
	}
	data = append(data, '\n')

	// Write config.json
	configPath := path.Join(extPath, configFileName)
	if _, err := ocflfs.Write(g.ctx, writeFS, configPath, strings.NewReader(string(data))); err != nil {
		return fmt.Errorf("writing config.json: %w", err)
	}

	g.logger.Info("created extension", "name", cmd.Name, "path", extPath)
	return nil
}

func (cmd *ExtensionCmd) removeExtension(g *globals, fsys ocflfs.FS, extPath string) error {
	writeFS, ok := fsys.(ocflfs.WriteFS)
	if !ok {
		return errors.New("filesystem does not support write operations")
	}

	if err := ocflfs.RemoveAll(g.ctx, writeFS, extPath); err != nil {
		return fmt.Errorf("removing extension %q: %w", cmd.Name, err)
	}

	g.logger.Info("removed extension", "name", cmd.Name, "path", extPath)
	return nil
}

func (cmd *ExtensionCmd) setField(g *globals, fsys ocflfs.FS, extPath string) error {
	writeFS, ok := fsys.(ocflfs.WriteFS)
	if !ok {
		return errors.New("filesystem does not support write operations")
	}

	// Parse field:value from --set flag
	fieldPath, jsonValue, err := parseSetArg(cmd.Set)
	if err != nil {
		return err
	}

	// Parse the JSON value
	var value any
	if err := json.Unmarshal([]byte(jsonValue), &value); err != nil {
		return fmt.Errorf("invalid JSON value %q: %w", jsonValue, err)
	}

	// Read existing config or create new one
	configPath := path.Join(extPath, configFileName)
	config, err := readConfig(g, fsys, configPath)
	if err != nil {
		// If the file doesn't exist, start with an empty config with the extension name
		config = map[string]any{
			"extensionName": cmd.Name,
		}
	}

	// Set the field using dot notation
	if err := setNestedField(config, fieldPath, value); err != nil {
		return err
	}

	// Write the updated config
	if err := writeConfig(g, writeFS, configPath, config); err != nil {
		return err
	}

	g.logger.Info("set extension config field", "name", cmd.Name, "field", fieldPath)
	return nil
}

func (cmd *ExtensionCmd) unsetField(g *globals, fsys ocflfs.FS, extPath string) error {
	writeFS, ok := fsys.(ocflfs.WriteFS)
	if !ok {
		return errors.New("filesystem does not support write operations")
	}

	configPath := path.Join(extPath, configFileName)

	// Read existing config
	config, err := readConfig(g, fsys, configPath)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	// Unset the field using dot notation
	if err := unsetNestedField(config, cmd.Unset); err != nil {
		return err
	}

	// Write the updated config
	if err := writeConfig(g, writeFS, configPath, config); err != nil {
		return err
	}

	g.logger.Info("unset extension config field", "name", cmd.Name, "field", cmd.Unset)
	return nil
}

// parseSetArg parses "field.path:jsonValue" format
func parseSetArg(s string) (fieldPath, jsonValue string, err error) {
	idx := strings.Index(s, ":")
	if idx == -1 {
		return "", "", errors.New("--set format must be 'field.path:jsonValue'")
	}
	fieldPath = s[:idx]
	jsonValue = s[idx+1:]
	if fieldPath == "" {
		return "", "", errors.New("field path cannot be empty")
	}
	if jsonValue == "" {
		return "", "", errors.New("JSON value cannot be empty")
	}
	return fieldPath, jsonValue, nil
}

// readConfig reads and parses the config.json file
func readConfig(g *globals, fsys ocflfs.FS, configPath string) (map[string]any, error) {
	data, err := ocflfs.ReadAll(g.ctx, fsys, configPath)
	if err != nil {
		return nil, err
	}

	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing config.json: %w", err)
	}
	return config, nil
}

// writeConfig writes the config map to config.json
func writeConfig(g *globals, fsys ocflfs.WriteFS, configPath string, config map[string]any) error {
	data, err := json.MarshalIndent(config, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	data = append(data, '\n') // Add trailing newline

	if _, err := ocflfs.Write(g.ctx, fsys, configPath, strings.NewReader(string(data))); err != nil {
		return fmt.Errorf("writing config.json: %w", err)
	}
	return nil
}

// setNestedField sets a value at a dot-notation path in a nested map
func setNestedField(m map[string]any, fieldPath string, value any) error {
	parts := strings.Split(fieldPath, ".")
	current := m

	for i := 0; i < len(parts)-1; i++ {
		key := parts[i]
		if next, ok := current[key]; ok {
			if nextMap, ok := next.(map[string]any); ok {
				current = nextMap
			} else {
				// Intermediate value exists but is not a map, replace it
				newMap := make(map[string]any)
				current[key] = newMap
				current = newMap
			}
		} else {
			// Create intermediate map
			newMap := make(map[string]any)
			current[key] = newMap
			current = newMap
		}
	}

	current[parts[len(parts)-1]] = value
	return nil
}

// unsetNestedField removes a field at a dot-notation path in a nested map
func unsetNestedField(m map[string]any, fieldPath string) error {
	parts := strings.Split(fieldPath, ".")
	current := m

	for i := 0; i < len(parts)-1; i++ {
		key := parts[i]
		if next, ok := current[key]; ok {
			if nextMap, ok := next.(map[string]any); ok {
				current = nextMap
			} else {
				// Path doesn't exist as expected, nothing to unset
				return nil
			}
		} else {
			// Path doesn't exist, nothing to unset
			return nil
		}
	}

	delete(current, parts[len(parts)-1])
	return nil
}
