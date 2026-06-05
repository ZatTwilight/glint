package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const glintStateDirName = "glint"

type sidebarState struct {
	CollapsedProjects map[string]bool `json:"collapsed_projects"`
}

func loadCollapsedProjects() map[string]bool {
	state, err := loadSidebarState()
	if err != nil || state.CollapsedProjects == nil {
		return map[string]bool{}
	}
	return state.CollapsedProjects
}

func saveCollapsedProjects(collapsed map[string]bool) error {
	cleaned := make(map[string]bool, len(collapsed))
	for path, isCollapsed := range collapsed {
		path = strings.TrimSpace(path)
		if path != "" && isCollapsed {
			cleaned[path] = true
		}
	}
	return saveSidebarState(sidebarState{CollapsedProjects: cleaned})
}

func loadSidebarState() (sidebarState, error) {
	path, err := sidebarStatePath()
	if err != nil {
		return sidebarState{}, err
	}
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return sidebarState{CollapsedProjects: map[string]bool{}}, nil
	}
	if err != nil {
		return sidebarState{}, err
	}
	var state sidebarState
	if err := json.Unmarshal(contents, &state); err != nil {
		return sidebarState{}, err
	}
	if state.CollapsedProjects == nil {
		state.CollapsedProjects = map[string]bool{}
	}
	return state, nil
}

func saveSidebarState(state sidebarState) error {
	path, err := sidebarStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	return os.WriteFile(path, contents, 0o644)
}

func sidebarStatePath() (string, error) {
	dir, err := uiStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sidebar.json"), nil
}

func uiStateDir() (string, error) {
	if dir := os.Getenv("GLINT_STATE_DIR"); strings.TrimSpace(dir) != "" {
		return expandUIHome(dir), nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); strings.TrimSpace(dir) != "" {
		return filepath.Join(expandUIHome(dir), glintStateDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", glintStateDirName), nil
}

func expandUIHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
