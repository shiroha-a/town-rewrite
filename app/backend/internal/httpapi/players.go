package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/shiroha-a/town/internal/player"
)

type registerReq struct {
	InstanceHost string `json:"instance_host"`
	RemoteUserID string `json:"remote_user_id"`
	DisplayName  string `json:"display_name"`
}

type statusResp struct {
	Energy          int        `json:"energy"`
	EnergyMax       int        `json:"energy_max"`
	NouEnergy       int        `json:"nou_energy"`
	NouEnergyMax    int        `json:"nou_energy_max"`
	Job             string     `json:"job"`
	JobLevel        int        `json:"job_level"`
	JobExp          int        `json:"job_exp"`
	JobKaisuu       int        `json:"job_kaisuu"`
	MasteredJobs    []string   `json:"mastered_jobs"`
	Satiety         int        `json:"satiety"`
	HeightCm        int        `json:"height_cm"`
	WeightG         int        `json:"weight_g"`
	BMI             int        `json:"bmi"`
	BodyType        string     `json:"body_type"`
	DiseaseIndex    int        `json:"disease_index"`
	DiseaseName     string     `json:"disease_name"`
	Condition       string     `json:"condition"`
	WorkAvailableAt *time.Time `json:"work_available_at"`
}

type itemResp struct {
	ItemID          int64          `json:"item_id"`
	Name            string         `json:"name"`
	Quantity        int            `json:"quantity"`
	RemainingUses   int            `json:"remaining_uses"`
	Sets            int            `json:"sets"`
	Money           int64          `json:"money"`
	Params          map[string]int `json:"params"`
	IntervalMin     int            `json:"interval_min"`
	NextAvailableAt *time.Time     `json:"next_available_at"`
}

type paramsResp struct {
	Kokugo     int `json:"kokugo"`
	Suugaku    int `json:"suugaku"`
	Rika       int `json:"rika"`
	Syakai     int `json:"syakai"`
	Eigo       int `json:"eigo"`
	Ongaku     int `json:"ongaku"`
	Bijutsu    int `json:"bijutsu"`
	Looks      int `json:"looks"`
	Tairyoku   int `json:"tairyoku"`
	Kenkou     int `json:"kenkou"`
	Speed      int `json:"speed"`
	Power      int `json:"power"`
	Wanryoku   int `json:"wanryoku"`
	Kyakuryoku int `json:"kyakuryoku"`
	Love       int `json:"love"`
	Omoshirosa int `json:"omoshirosa"`
}

type playerResp struct {
	ID           int64      `json:"id"`
	InstanceHost string     `json:"instance_host"`
	RemoteUserID string     `json:"remote_user_id"`
	DisplayName  string     `json:"display_name"`
	Roles        []string   `json:"roles"`
	Money        int64      `json:"money"`
	Savings      int64      `json:"savings"`
	Status       statusResp `json:"status"`
	Params       paramsResp `json:"params"`
	Items        []itemResp `json:"items"`
	ServerNow    time.Time  `json:"server_now"`
}

func toResp(p *player.Player) playerResp {
	roles := p.Roles
	if roles == nil {
		roles = []string{}
	}
	masteredJobs := p.Status.MasteredJobs
	if masteredJobs == nil {
		masteredJobs = []string{}
	}
	items := make([]itemResp, 0, len(p.Items))
	for _, it := range p.Items {
		params := it.Params
		if params == nil {
			params = map[string]int{}
		}
		items = append(items, itemResp{
			ItemID:          it.ItemID,
			Name:            it.Name,
			Quantity:        it.Quantity,
			RemainingUses:   it.RemainingUses,
			Sets:            it.Sets,
			Money:           it.Money,
			Params:          params,
			IntervalMin:     it.IntervalMin,
			NextAvailableAt: it.NextAvailableAt,
		})
	}
	return playerResp{
		ID:           p.ID,
		InstanceHost: p.InstanceHost,
		RemoteUserID: p.RemoteUserID,
		DisplayName:  p.DisplayName,
		Roles:        roles,
		Money:        p.Money,
		Savings:      p.Savings,
		Status: statusResp{
			Energy:          p.Status.Energy,
			EnergyMax:       p.Status.EnergyMax,
			NouEnergy:       p.Status.NouEnergy,
			NouEnergyMax:    p.Status.NouEnergyMax,
			Job:             p.Status.Job,
			JobLevel:        p.Status.JobLevel,
			JobExp:          p.Status.JobExp,
			JobKaisuu:       p.Status.JobKaisuu,
			MasteredJobs:    masteredJobs,
			Satiety:         p.Status.Satiety,
			HeightCm:        p.Status.HeightCm,
			WeightG:         p.Status.WeightG,
			BMI:             p.Status.BMI,
			BodyType:        p.Status.BodyType,
			DiseaseIndex:    p.Status.DiseaseIndex,
			DiseaseName:     p.Status.DiseaseName,
			Condition:       p.Status.Condition,
			WorkAvailableAt: p.Status.WorkAvailableAt,
		},
		Params:    paramsResp(p.Params),
		Items:     items,
		ServerNow: time.Now(),
	}
}

func (s *Server) registerPlayer(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.InstanceHost == "" || req.RemoteUserID == "" {
		writeError(w, http.StatusBadRequest, "instance_host and remote_user_id are required")
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = req.RemoteUserID
	}
	p, err := s.players.Register(r.Context(), req.InstanceHost, req.RemoteUserID, req.DisplayName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toResp(p))
}

// shopItems lists the public item catalog for the store UI.
func (s *Server) shopItems(w http.ResponseWriter, r *http.Request) {
	items, err := s.content.ListShopItems(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getPlayer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	p, err := s.players.Get(r.Context(), id)
	if errors.Is(err, player.ErrNotFound) {
		writeError(w, http.StatusNotFound, "player not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toResp(p))
}
