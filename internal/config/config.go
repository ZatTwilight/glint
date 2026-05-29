package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	WorkspaceRoots []string `json:"workspace_roots"`
}

func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}

	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(contents, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.WorkspaceRoots) == 0 {
		cfg.WorkspaceRoots = Default().WorkspaceRoots
	}

	for i, root := range cfg.WorkspaceRoots {
		expanded, err := Expand(root)
		if err != nil {
			return Config{}, err
		}
		cfg.WorkspaceRoots[i] = expanded
	}
	return cfg, nil
}

func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{WorkspaceRoots: []string{"."}}
	}
	return Config{WorkspaceRoots: []string{filepath.Join(home, "Documents", "dev")}}
}

func Path() (string, error) {
	configHome, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configHome, "agentbar", "config.json"), nil
}

func WriteExample() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err = os.Stat(path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	contents, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(contents, '\n'), 0o644)
}

func Expand(path string) (string, error) {
	if path == "~" {
		return os.UserHomeDir()
	}
	if len(path) > 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
