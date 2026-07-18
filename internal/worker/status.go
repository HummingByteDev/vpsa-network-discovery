package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Status is the worker's self-reported condition, written atomically to
// <state>/status.json after every heartbeat. The host-side `vapn` CLI reads
// it to render `vapn status` and to health-gate updates — it is the contract
// between the container and the operator tooling.
type Status struct {
	WorkerID              string    `json:"worker_id"`
	State                 string    `json:"state"`
	SoftwareVersion       string    `json:"software_version"`
	CoordinatorURL        string    `json:"coordinator_url"`
	SnapshotVersion       string    `json:"snapshot_version"`
	LastHeartbeatAt       time.Time `json:"last_heartbeat_at"`
	Assignments           int       `json:"assignments"`
	MeasurementsSubmitted uint64    `json:"measurements_submitted"`
	LastUploadAt          time.Time `json:"last_upload_at"`
	LastUploadMillis      int64     `json:"last_upload_ms"`
	QueueDepth            int       `json:"queue_depth"`
	UpdatedAt             time.Time `json:"updated_at"`
}

func (s State) StatusPath() string { return filepath.Join(s.Dir, "status.json") }

func (s State) WriteStatus(st Status) error {
	st.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.StatusPath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.StatusPath())
}

func ReadStatus(dir string) (Status, error) {
	var st Status
	raw, err := os.ReadFile(State{Dir: dir}.StatusPath())
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(raw, &st)
	return st, err
}
