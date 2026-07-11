package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

type onsenReq struct {
	BathID         int64  `json:"bath_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// onsenBathe accelerates the player's power recovery at the chosen bath.
func (s *Server) onsenBathe(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req onsenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.BathID <= 0 {
		writeError(w, http.StatusBadRequest, "bath_id is required")
		return
	}
	p, err := s.actions.DoOnsen(r.Context(), id, req.BathID, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}
