package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/shiroha-a/town/internal/content"
)

// building returns the construction-company screen state: the five towns and
// their land prices, the exterior/interior catalog, every player's houses (for
// grid rendering), and the caller's own houses.
func (s *Server) building(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	st, err := s.content.Building(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type buildHouseReq struct {
	Town           int    `json:"town"`
	Row            int    `json:"row"`
	Col            int    `json:"col"`
	Exterior       string `json:"exterior"`
	InteriorRank   int    `json:"interior_rank"`
	IdempotencyKey string `json:"idempotency_key"`
}

// buildHouse builds a house for the player at the chosen plot (建設会社).
func (s *Server) buildHouse(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req buildHouseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Exterior == "" {
		writeError(w, http.StatusBadRequest, "exterior is required")
		return
	}
	p, err := s.actions.DoBuildHouse(r.Context(), id, req.Town, req.Row, req.Col, req.Exterior, req.InteriorRank, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

// adminGetPlots returns every admin-designated empty plot (管理者専用).
func (s *Server) adminGetPlots(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	plots, err := s.content.ListPlots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plots)
}

// adminUpdatePlots replaces the full set of empty plots (管理者専用).
func (s *Server) adminUpdatePlots(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var plots []content.PlotCell
	if err := json.NewDecoder(r.Body).Decode(&plots); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.content.SetPlots(r.Context(), plots); err != nil {
		writeContentErr(w, err)
		return
	}
	updated, err := s.content.ListPlots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
