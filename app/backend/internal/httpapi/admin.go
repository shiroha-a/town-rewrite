package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/shiroha-a/town/internal/content"
	"github.com/shiroha-a/town/internal/effects"
)

// requireAdmin is an INTERIM authorization check used until MiAuth provides the
// authenticated session. The acting player is passed via the X-Acting-Player-Id
// header and must hold the admin role. This is deliberately temporary — once
// MiAuth lands, the acting identity comes from the session, not a header.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	h := r.Header.Get("X-Acting-Player-Id")
	if h == "" {
		writeError(w, http.StatusUnauthorized, "認証が必要です(X-Acting-Player-Id)")
		return false
	}
	id, err := strconv.ParseInt(h, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid X-Acting-Player-Id")
		return false
	}
	ok, err := s.players.HasRole(r.Context(), id, "admin")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if !ok {
		writeError(w, http.StatusForbidden, "権限がありません")
		return false
	}
	return true
}

func writeContentErr(w http.ResponseWriter, err error) {
	var v *content.ValidationError
	if errors.As(err, &v) {
		writeError(w, http.StatusBadRequest, v.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

type createItemReq struct {
	Name     string          `json:"name"`
	Category string          `json:"category"`
	Price    int64           `json:"price"`
	Effect   json.RawMessage `json:"effect"`
}

func (s *Server) createItem(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req createItemReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	it, err := s.content.CreateItem(r.Context(), req.Name, req.Category, req.Price, req.Effect)
	if err != nil {
		writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	items, err := s.content.ListItems(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type createJobReq struct {
	Name         string          `json:"name"`
	Requirements json.RawMessage `json:"requirements"`
	Effect       json.RawMessage `json:"effect"`
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req createJobReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	j, err := s.content.CreateJob(r.Context(), req.Name, req.Requirements, req.Effect)
	if err != nil {
		writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	jobs, err := s.content.ListJobs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

type simulateReq struct {
	Effect json.RawMessage `json:"effect"`
	State  struct {
		Money  int64 `json:"money"`
		Params map[string]struct {
			Value int `json:"value"`
			Max   int `json:"max"`
		} `json:"params"`
	} `json:"state"`
}

// simulate dry-runs an effect against a hypothetical state and returns the plan
// plus economy warnings, without persisting anything.
func (s *Server) simulate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req simulateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	state := effects.State{Money: req.State.Money, Params: map[string]effects.ParamState{}}
	for name, p := range req.State.Params {
		state.Params[name] = effects.ParamState{Value: p.Value, Max: p.Max}
	}
	res, err := content.Simulate(req.Effect, state)
	if err != nil {
		writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
