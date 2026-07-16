package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/shiroha-a/town/internal/action"
	"github.com/shiroha-a/town/internal/player"
)

// facilityMenu lists a facility's menu (e.g. 食堂 = syokudou). Public.
func (s *Server) facilityMenu(w http.ResponseWriter, r *http.Request) {
	menu, err := s.content.ListFacilityMenu(r.Context(), r.PathValue("facility"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, menu)
}

type eatReq struct {
	FoodID         int64  `json:"food_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type schoolReq struct {
	CourseID       int64  `json:"course_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// schoolAttend attends a school course (raises brain params, once per game day).
func (s *Server) schoolAttend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req schoolReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.CourseID <= 0 {
		writeError(w, http.StatusBadRequest, "course_id is required")
		return
	}
	p, err := s.actions.DoSchool(r.Context(), id, req.CourseID, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

// eat eats a food from the 食堂 menu.
func (s *Server) eat(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req eatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.FoodID <= 0 {
		writeError(w, http.StatusBadRequest, "food_id is required")
		return
	}
	p, err := s.actions.DoEat(r.Context(), id, req.FoodID, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

type facilityUseReq struct {
	MenuID         int64  `json:"menu_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

// facilityUse runs a generic facility menu action (e.g. ジムのトレーニング).
func (s *Server) facilityUse(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req facilityUseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.MenuID <= 0 {
		writeError(w, http.StatusBadRequest, "menu_id is required")
		return
	}
	p, err := s.actions.DoFacilityAction(r.Context(), id, r.PathValue("facility"), req.MenuID, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

func writeFacilityResult(w http.ResponseWriter, p *player.Player, err error) {
	if err != nil {
		var condErr *action.ConditionError
		switch {
		case errors.Is(err, player.ErrNotFound):
			writeError(w, http.StatusNotFound, "player not found")
		case errors.Is(err, action.ErrItemNotFound):
			writeError(w, http.StatusNotFound, "menu item not found")
		case errors.As(err, &condErr):
			writeError(w, http.StatusUnprocessableEntity, condErr.Message)
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, toResp(p))
}
