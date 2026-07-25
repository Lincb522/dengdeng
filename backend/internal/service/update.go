package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dengdeng/internal/config"
	"dengdeng/internal/version"
)

var (
	ErrUpdateDisabled = errors.New("server repository updates are not enabled")
	ErrUpdateBusy     = errors.New("an update task is already running")
	ErrUpdateAction   = errors.New("invalid update action")
)

type UpdateStatus struct {
	Enabled         bool            `json:"enabled"`
	Repository      string          `json:"repository"`
	Branch          string          `json:"branch"`
	Status          string          `json:"status"` // idle | queued | running | succeeded | failed
	Action          string          `json:"action"` // check | apply | rollback
	Stage           string          `json:"stage"`
	Message         string          `json:"message"`
	CurrentVersion  string          `json:"current_version"`
	CurrentCommit   string          `json:"current_commit"`
	TargetCommit    string          `json:"target_commit"`
	PreviousCommit  string          `json:"previous_commit"`
	UpdateAvailable bool            `json:"update_available"`
	CanRollback     bool            `json:"can_rollback"`
	RequestedBy     string          `json:"requested_by"`
	RequestedAt     string          `json:"requested_at"`
	StartedAt       string          `json:"started_at"`
	FinishedAt      string          `json:"finished_at"`
	Changes         []UpdateChange  `json:"changes"`
	History         []UpdateRelease `json:"history,omitempty"`
}

type UpdateChange struct {
	Commit      string   `json:"commit"`
	Title       string   `json:"title"`
	CommittedAt string   `json:"committed_at"`
	Details     []string `json:"details,omitempty"`
}

// UpdateRelease is one completed deployment or rollback. Unlike Changes,
// which describes the currently checked range, releases remain available
// after later update checks overwrite status.json.
type UpdateRelease struct {
	Version        string         `json:"version"`
	Commit         string         `json:"commit"`
	PreviousCommit string         `json:"previous_commit,omitempty"`
	Action         string         `json:"action"`
	Message        string         `json:"message"`
	RequestedBy    string         `json:"requested_by,omitempty"`
	FinishedAt     string         `json:"finished_at"`
	Changes        []UpdateChange `json:"changes"`
}

type updateRequest struct {
	Action      string `json:"action"`
	RequestedBy string `json:"requested_by"`
	RequestedAt string `json:"requested_at"`
}

type updateTrigger func(context.Context) error

type UpdateService struct {
	cfg     config.UpdateConfig
	mu      sync.Mutex
	trigger updateTrigger
}

func NewUpdateService(cfg *config.Config) *UpdateService {
	updateCfg := config.UpdateConfig{}
	if cfg != nil {
		updateCfg = cfg.Update
	}
	return &UpdateService{cfg: updateCfg, trigger: systemdUpdateTrigger}
}

func systemdUpdateTrigger(ctx context.Context) error {
	// Polkit grants the unprivileged service account exactly one operation:
	// start this fixed unit. Keeping sudo out of the process also lets the main
	// service retain systemd's NoNewPrivileges=true hardening.
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "--no-block", "start", "dengdeng-updater.service")
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("start updater: %s", message)
	}
	return nil
}

func (s *UpdateService) stateDirectory() string {
	dir := strings.TrimSpace(s.cfg.StateDirectory)
	if dir == "" {
		dir = "/var/lib/dengdeng/update"
	}
	return filepath.Clean(dir)
}

func (s *UpdateService) statusPath() string { return filepath.Join(s.stateDirectory(), "status.json") }
func (s *UpdateService) historyPath() string {
	return filepath.Join(s.stateDirectory(), "history.json")
}
func (s *UpdateService) requestPath() string {
	return filepath.Join(s.stateDirectory(), "request.json")
}

func (s *UpdateService) Status() (UpdateStatus, error) {
	updateCfg := config.UpdateConfig{}
	if s != nil {
		updateCfg = s.cfg
	}
	status := UpdateStatus{
		Enabled:        updateCfg.Enabled,
		Repository:     strings.TrimSpace(updateCfg.Repository),
		Branch:         strings.TrimSpace(updateCfg.Branch),
		Status:         "idle",
		Stage:          "ready",
		Message:        "等待检查更新",
		CurrentVersion: version.Version,
		CurrentCommit:  version.Commit,
		Changes:        []UpdateChange{},
	}
	if s == nil {
		return status, nil
	}
	data, err := os.ReadFile(s.statusPath())
	if errors.Is(err, os.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("read update status: %w", err)
	}
	var persisted UpdateStatus
	if err := json.Unmarshal(data, &persisted); err != nil {
		return status, fmt.Errorf("decode update status: %w", err)
	}
	persisted.Enabled = status.Enabled
	if persisted.Repository == "" {
		persisted.Repository = status.Repository
	}
	if persisted.Branch == "" {
		persisted.Branch = status.Branch
	}
	if persisted.Status == "" {
		persisted.Status = "idle"
	}
	if persisted.CurrentVersion == "" {
		persisted.CurrentVersion = version.Version
	}
	if persisted.CurrentCommit == "" || persisted.CurrentCommit == "unknown" {
		persisted.CurrentCommit = version.Commit
	}
	persisted.CanRollback = persisted.PreviousCommit != ""
	if persisted.Changes == nil {
		persisted.Changes = []UpdateChange{}
	}
	history, historyErr := s.releaseHistory(persisted)
	if historyErr != nil {
		return status, historyErr
	}
	persisted.History = history
	return persisted, nil
}

func (s *UpdateService) releaseHistory(current UpdateStatus) ([]UpdateRelease, error) {
	history := []UpdateRelease{}
	data, err := os.ReadFile(s.historyPath())
	if err == nil {
		if err := json.Unmarshal(data, &history); err != nil {
			return nil, fmt.Errorf("decode update history: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read update history: %w", err)
	}
	if history == nil {
		history = []UpdateRelease{}
	}

	// The currently deployed release is also exposed when an older updater has
	// not created history.json yet. This makes the history page useful during a
	// rolling upgrade and avoids losing the release that installs this feature.
	if current.Status == "succeeded" && (current.Action == "apply" || current.Action == "rollback") && current.CurrentCommit != "" && current.FinishedAt != "" {
		found := false
		for _, release := range history {
			if release.Commit == current.CurrentCommit && release.FinishedAt == current.FinishedAt {
				found = true
				break
			}
		}
		if !found {
			history = append([]UpdateRelease{{
				Version: current.CurrentVersion, Commit: current.CurrentCommit, PreviousCommit: current.PreviousCommit,
				Action: current.Action, Message: current.Message, RequestedBy: current.RequestedBy,
				FinishedAt: current.FinishedAt, Changes: current.Changes,
			}}, history...)
		}
	}
	if len(history) > 50 {
		history = history[:50]
	}
	for i := range history {
		if history[i].Changes == nil {
			history[i].Changes = []UpdateChange{}
		}
	}
	return history, nil
}

func (s *UpdateService) Request(ctx context.Context, action, requestedBy string) (UpdateStatus, error) {
	if s == nil || !s.cfg.Enabled {
		return UpdateStatus{}, ErrUpdateDisabled
	}
	if action != "check" && action != "apply" && action != "rollback" {
		return UpdateStatus{}, ErrUpdateAction
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	status, err := s.Status()
	if err != nil {
		return UpdateStatus{}, err
	}
	if status.Status == "queued" || status.Status == "running" {
		return status, ErrUpdateBusy
	}
	if action == "rollback" && !status.CanRollback {
		return status, fmt.Errorf("%w: no previous release is available", ErrUpdateAction)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	request := updateRequest{Action: action, RequestedBy: strings.TrimSpace(requestedBy), RequestedAt: now}
	if err := writeUpdateJSON(s.requestPath(), request); err != nil {
		return UpdateStatus{}, fmt.Errorf("write update request: %w", err)
	}
	status.Status = "queued"
	status.Action = action
	status.Stage = "queued"
	status.Message = "更新任务已进入队列"
	status.RequestedBy = request.RequestedBy
	status.RequestedAt = now
	status.StartedAt = ""
	status.FinishedAt = ""
	status.Changes = []UpdateChange{}
	if err := writeUpdateJSON(s.statusPath(), status); err != nil {
		return UpdateStatus{}, fmt.Errorf("write update status: %w", err)
	}
	if err := s.trigger(ctx); err != nil {
		status.Status = "failed"
		status.Stage = "trigger"
		status.Message = err.Error()
		status.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = writeUpdateJSON(s.statusPath(), status)
		return status, err
	}
	return status, nil
}

func writeUpdateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o640); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
