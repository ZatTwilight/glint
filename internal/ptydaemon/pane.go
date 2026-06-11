package ptydaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	debuglog "github.com/ZatTwilight/glint/internal/debug"
)

type PaneState struct {
	Session string    `json:"session"`
	Updated time.Time `json:"updated"`
}

func PanePath(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, string(filepath.Separator), "-")
	return filepath.Join(filepath.Dir(SocketPath()), "panes", name+".json")
}

func ReadPaneState(name string) (PaneState, error) {
	var state PaneState
	data, err := os.ReadFile(PanePath(name))
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func WritePaneState(name string, state PaneState) error {
	path := PanePath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func SwitchPane(name, target string) error {
	old, _ := ReadPaneState(name)
	debuglog.Printf("switch pane %q: %q -> %q\n", name, old.Session, target)
	if err := WritePaneState(name, PaneState{Session: strings.TrimSpace(target), Updated: time.Now()}); err != nil {
		return err
	}
	if previous := strings.TrimSpace(old.Session); previous != "" && previous != strings.TrimSpace(target) {
		if _, err := Detach(previous); err != nil {
			debuglog.Printf("switch pane %q: detach previous %q: %v\n", name, previous, err)
		}
	}
	return nil
}
