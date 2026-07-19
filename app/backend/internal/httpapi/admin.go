package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/shiroha-a/town/internal/content"
	"github.com/shiroha-a/town/internal/effects"
	"github.com/shiroha-a/town/internal/player"
	"github.com/shiroha-a/town/internal/settings"
	"github.com/shiroha-a/town/internal/townmap"
)

// townMap returns the current town map. Public: every player needs it to render
// the main screen.
func (s *Server) townMap(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.townmap.Get())
}

func (s *Server) adminUpdateTownMap(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var fs []townmap.Facility
	if err := json.NewDecoder(r.Body).Decode(&fs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.townmap.Set(r.Context(), fs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.townmap.Get())
}

func (s *Server) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.settings.Get())
}

func (s *Server) adminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var g settings.Game
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.settings.Set(r.Context(), g); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.settings.Get())
}

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
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Price       int64           `json:"price"`
	Effect      json.RawMessage `json:"effect"`
	StockMaster *int            `json:"stock_master"`
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
	it, err := s.content.CreateItem(r.Context(), req.Name, req.Category, req.Price, req.Effect, req.StockMaster)
	if err != nil {
		writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

type updateItemReq struct {
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Price       int64           `json:"price"`
	Effect      json.RawMessage `json:"effect"`
	Enabled     bool            `json:"enabled"`
	StockMaster *int            `json:"stock_master"`
}

func (s *Server) updateItem(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req updateItemReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	it, err := s.content.UpdateItem(r.Context(), id, req.Name, req.Category, req.Price, req.Effect, req.Enabled, req.StockMaster)
	if err != nil {
		writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.content.DeleteItem(r.Context(), id); err != nil {
		writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
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

type jobReq struct {
	Name          string          `json:"name"`
	Requirements  json.RawMessage `json:"requirements"`
	Effect        json.RawMessage `json:"effect"`
	Salary        int64           `json:"salary"`
	PayInterval   int             `json:"pay_interval"`
	BonusRate     int             `json:"bonus_rate"`
	RaiseRate     int             `json:"raise_rate"`
	Rank          int             `json:"rank"`
	RequireMaster string          `json:"require_master"`
	BodyCost      int             `json:"body_cost"`
	NouCost       int             `json:"nou_cost"`
	Enabled       bool            `json:"enabled"`
}

func (req jobReq) toInput() content.JobInput {
	return content.JobInput{
		Name: req.Name, Requirements: req.Requirements, Effect: req.Effect,
		Salary: req.Salary, PayInterval: req.PayInterval, BonusRate: req.BonusRate,
		RaiseRate: req.RaiseRate, Rank: req.Rank, RequireMaster: req.RequireMaster,
		BodyCost: req.BodyCost, NouCost: req.NouCost, Enabled: req.Enabled,
	}
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req jobReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	in := req.toInput()
	in.Enabled = true // 新規作成は有効で作る
	j, err := s.content.CreateJob(r.Context(), in)
	if err != nil {
		writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) updateJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req jobReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	j, err := s.content.UpdateJob(r.Context(), id, req.toInput())
	if err != nil {
		writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.content.DeleteJob(r.Context(), id); err != nil {
		writeContentErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
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

type adminPlayerSummaryResp struct {
	ID          int64    `json:"id"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Money       int64    `json:"money"`
	Job         string   `json:"job"`
	JobLevel    int      `json:"job_level"`
}

func (s *Server) adminListPlayers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	players, err := s.players.AdminList(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]adminPlayerSummaryResp, 0, len(players))
	for _, p := range players {
		roles := p.Roles
		if roles == nil {
			roles = []string{}
		}
		out = append(out, adminPlayerSummaryResp{
			ID: p.ID, DisplayName: p.DisplayName, Roles: roles, Money: p.Money, Job: p.Job, JobLevel: p.JobLevel,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type adminPlayerReq struct {
	DisplayName  string     `json:"display_name"`
	Money        int64      `json:"money"`
	IsAdmin      bool       `json:"is_admin"`
	Params       paramsResp `json:"params"`
	Energy       int        `json:"energy"`
	NouEnergy    int        `json:"nou_energy"`
	Satiety      int        `json:"satiety"`
	Job          string     `json:"job"`
	JobLevel     int        `json:"job_level"`
	JobExp       int        `json:"job_exp"`
	DiseaseIndex int        `json:"disease_index"`
	HeightCm     int        `json:"height_cm"`
	WeightG      int        `json:"weight_g"`
}

func (s *Server) adminUpdatePlayer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req adminPlayerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	err = s.players.AdminUpdate(r.Context(), id, player.AdminPlayerUpdate{
		DisplayName: req.DisplayName, Money: req.Money, IsAdmin: req.IsAdmin,
		Params: player.Params(req.Params),
		Energy: req.Energy, NouEnergy: req.NouEnergy, Satiety: req.Satiety,
		Job: req.Job, JobLevel: req.JobLevel, JobExp: req.JobExp,
		DiseaseIndex: req.DiseaseIndex, HeightCm: req.HeightCm, WeightG: req.WeightG,
	})
	if errors.Is(err, player.ErrNotFound) {
		writeError(w, http.StatusNotFound, "player not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, err := s.players.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toResp(p))
}

func (s *Server) adminDeletePlayer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.players.AdminSoftDelete(r.Context(), id); errors.Is(err, player.ErrNotFound) {
		writeError(w, http.StatusNotFound, "player not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
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
