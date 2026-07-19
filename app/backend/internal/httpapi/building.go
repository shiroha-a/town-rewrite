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

type sellHouseReq struct {
	HouseID        int64  `json:"house_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// sellHouse demolishes the player's house and refunds the land price (建設会社).
func (s *Server) sellHouse(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req sellHouseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.HouseID <= 0 {
		writeError(w, http.StatusBadRequest, "house_id is required")
		return
	}
	p, err := s.actions.DoSellHouse(r.Context(), id, req.HouseID, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

type rebuildHouseReq struct {
	HouseID        int64  `json:"house_id"`
	Exterior       string `json:"exterior"`
	InteriorRank   int    `json:"interior_rank"`
	IdempotencyKey string `json:"idempotency_key"`
}

// rebuildHouse rebuilds the player's house with a new exterior/interior (建設会社).
func (s *Server) rebuildHouse(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req rebuildHouseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.HouseID <= 0 || req.Exterior == "" {
		writeError(w, http.StatusBadRequest, "house_id and exterior are required")
		return
	}
	p, err := s.actions.DoRebuildHouse(r.Context(), id, req.HouseID, req.Exterior, req.InteriorRank, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

type houseCommentReq struct {
	HouseID        int64  `json:"house_id"`
	Setumei        string `json:"setumei"`
	IdempotencyKey string `json:"idempotency_key"`
}

// houseComment sets the mouse-over comment of the player's house (マイホーム設定).
func (s *Server) houseComment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req houseCommentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.HouseID <= 0 {
		writeError(w, http.StatusBadRequest, "house_id is required")
		return
	}
	p, err := s.actions.DoSetHouseComment(r.Context(), id, req.HouseID, req.Setumei, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

type saisenReq struct {
	HouseID        int64  `json:"house_id"`
	Amount         int64  `json:"amount"`
	IdempotencyKey string `json:"idempotency_key"`
}

// saisen offers money at a house's offering box (さい銭).
func (s *Server) saisen(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req saisenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.HouseID <= 0 || req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "house_id and amount are required")
		return
	}
	p, err := s.actions.DoSaisen(r.Context(), id, req.HouseID, req.Amount, req.IdempotencyKey)
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
