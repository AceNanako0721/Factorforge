package modelapi

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

const (
	maxStateBytes = 64 << 10
	maxRecentIDs  = 256
)

// State is the local budget and duplicate window. It never contains a
// credential, prompt or model output.
type State struct {
	WindowStart time.Time `json:"window_start"`
	Count       int64     `json:"count"`
	Attempts    int64     `json:"attempts"`
	IDs         []string  `json:"ids"`
}

// Reserve records one attempt before the request is sent. A failure keeps the
// reservation; the supplier failure does not refund it.
func Reserve(path, id string, access config.ModelAccess, now time.Time) error {
	if path == "" {
		return d.Fail("MODEL_STATE_INVALID", 503)
	}
	state, err := readState(path, now)
	if err != nil {
		return err
	}
	if now.Before(state.WindowStart) {
		return d.Fail("MODEL_STATE_INVALID", 503)
	}
	if access.BudgetWindowSeconds > 0 && now.Sub(state.WindowStart) >= time.Duration(access.BudgetWindowSeconds)*time.Second {
		state.WindowStart = now
		state.Count = 0
	}
	if access.MaxRequests > 0 && state.Count >= access.MaxRequests {
		return d.Fail("SEMANTIC_BUDGET_EXCEEDED", 429)
	}
	for _, seen := range state.IDs {
		if seen == id {
			return d.Fail("SEMANTIC_DUPLICATE_ID", 409)
		}
	}
	state.Count++
	state.Attempts++
	state.IDs = append(state.IDs, id)
	if len(state.IDs) > maxRecentIDs {
		state.IDs = state.IDs[len(state.IDs)-maxRecentIDs:]
	}
	return writeState(path, state)
}

func readState(path string, now time.Time) (State, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{WindowStart: now}, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxStateBytes {
		return State{}, d.Fail("MODEL_STATE_INVALID", 503)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return State{}, d.Fail("MODEL_STATE_INVALID", 503)
	}
	var state State
	if json.Unmarshal(raw, &state) != nil {
		return State{}, d.Fail("MODEL_STATE_INVALID", 503)
	}
	if state.WindowStart.IsZero() {
		state.WindowStart = now
	}
	return state, nil
}

func writeState(path string, state State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return d.Fail("MODEL_STATE_INVALID", 503)
	}
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > maxStateBytes {
		return d.Fail("MODEL_STATE_INVALID", 503)
	}
	temp, err := os.CreateTemp(dir, ".model-state-*")
	if err != nil {
		return d.Fail("MODEL_STATE_INVALID", 503)
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(raw)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return d.Fail("MODEL_STATE_INVALID", 503)
	}
	if err = os.Rename(name, path); err != nil {
		return d.Fail("MODEL_STATE_INVALID", 503)
	}
	if handle, openErr := os.Open(dir); openErr == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return nil
}
