package coordinator

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// KeyOverlap is how long the previous key stays valid after rotation: long
// enough for in-flight requests and worker restarts, short enough that a
// stolen old key ages out quickly.
const KeyOverlap = 10 * time.Minute

// rotateKey installs the worker's next public key. The request itself is
// signed with the *current* key (enforced by the signed middleware), which is
// the proof of possession that authorizes the rotation.
func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NextPublicKey string `json:"next_public_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.NextPublicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		problem(w, http.StatusUnprocessableEntity, "next_public_key must be base64 of 32 bytes")
		return
	}
	workerID := identity(r).ID
	if err := s.reg.RotateKey(r.Context(), workerID, ed25519.PublicKey(raw), KeyOverlap); err != nil {
		s.log.Error("key rotation failed", "worker", workerID, "error", err)
		problem(w, http.StatusInternalServerError, "rotation failed")
		return
	}
	if s.audit != nil {
		s.audit.Event(r.Context(), "auth", "worker:"+workerID, "key_rotated", workerID,
			map[string]any{"overlap_seconds": int(KeyOverlap.Seconds())})
	}
	s.log.Info("worker key rotated", "worker", workerID)
	writeJSON(w, http.StatusOK, map[string]any{
		"overlap_seconds": int(KeyOverlap.Seconds()),
	})
}

func (s *Server) adminRequestRotation(w http.ResponseWriter, r *http.Request) {
	workerID := r.PathValue("id")
	if err := s.reg.RequestRotation(r.Context(), workerID); err != nil {
		problem(w, http.StatusNotFound, "unknown worker")
		return
	}
	if s.audit != nil {
		s.audit.Event(r.Context(), "admin", "admin", "rotation_requested", workerID, nil)
	}
	w.WriteHeader(http.StatusAccepted)
}
