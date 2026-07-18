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
	Enabled         bool           `json:"enabled"`
	Repository      string         `json:"repository"`
	Branch          string         `json:"branch"`
	Status          string         `json:"status"` // idle | queued | running | succeeded | failed
	Action          string         `json:"action"` // check | apply | rollback
	Stage           string         `json:"stage"`
	Message         string         `json:"message"`
	CurrentVersion  string         `json:"current_version"`
	CurrentCommit   string         `json:"current_commit"`
	TargetCommit    string         `json:"target_commit"`
	PreviousCommit  string         `json:"previous_commit"`
	UpdateAvailable bool           `json:"update_available"`
	CanRollback     bool           `json:"can_rollback"`
	RequestedBy     string         `json:"requested_by"`
	RequestedAt     string         `json:"requested_at"`
	StartedAt       string         `json:"started_at"`
	FinishedAt      string         `json:"finished_at"`
	Changes         []UpdateChange `json:"changes"`
}

type UpdateChange struct {
	Commit      string `json:"commit"`
	Title       string `json:"title"`
	CommittedAt string `json:"committed_at"`
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
	return persisted, nil
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
