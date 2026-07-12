// Package httpapi exposes the REST API under /api/v1.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/shiroha-a/town/internal/action"
	"github.com/shiroha-a/town/internal/content"
	"github.com/shiroha-a/town/internal/player"
	"github.com/shiroha-a/town/internal/settings"
	"github.com/shiroha-a/town/internal/townmap"
)

// Server holds the API dependencies.
type Server struct {
	players  *player.Service
	actions  *action.Service
	content  *content.Service
	settings *settings.Store
	townmap  *townmap.Store
}

// NewServer builds the HTTP handler for the REST API.
func NewServer(players *player.Service, actions *action.Service, contentSvc *content.Service, st *settings.Store, tmap *townmap.Store) http.Handler {
	s := &Server{players: players, actions: actions, content: contentSvc, settings: st, townmap: tmap}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("POST /api/v1/players", s.registerPlayer)
	mux.HandleFunc("GET /api/v1/players", s.listPlayers)
	mux.HandleFunc("GET /api/v1/players/{id}", s.getPlayer)
	mux.HandleFunc("GET /api/v1/players/{id}/profile", s.playerProfile)
	mux.HandleFunc("GET /api/v1/townmap", s.townMap)
	mux.HandleFunc("GET /api/v1/items", s.shopItems)
	mux.HandleFunc("GET /api/v1/facilities/{facility}/menu", s.facilityMenu)
	mux.HandleFunc("POST /api/v1/players/{id}/eat", s.eat)
	mux.HandleFunc("POST /api/v1/players/{id}/hospital/treat", s.hospitalTreat)
	mux.HandleFunc("POST /api/v1/players/{id}/onsen/bathe", s.onsenBathe)
	mux.HandleFunc("POST /api/v1/players/{id}/facilities/{facility}/use", s.facilityUse)
	mux.HandleFunc("GET /api/v1/jobs", s.jobs)
	mux.HandleFunc("POST /api/v1/players/{id}/work", s.work)
	mux.HandleFunc("POST /api/v1/players/{id}/job", s.changeJob)
	mux.HandleFunc("POST /api/v1/players/{id}/buy", s.buy)
	mux.HandleFunc("POST /api/v1/players/{id}/use", s.use)
	mux.HandleFunc("POST /api/v1/players/{id}/bank/deposit", s.deposit)
	mux.HandleFunc("POST /api/v1/players/{id}/bank/withdraw", s.withdraw)

	// 管理者API(暫定認可: X-Acting-Player-Idヘッダのadminロール。将来MiAuthで置換)
	mux.HandleFunc("POST /api/v1/admin/items", s.createItem)
	mux.HandleFunc("GET /api/v1/admin/items", s.listItems)
	mux.HandleFunc("PUT /api/v1/admin/items/{id}", s.updateItem)
	mux.HandleFunc("DELETE /api/v1/admin/items/{id}", s.deleteItem)
	mux.HandleFunc("POST /api/v1/admin/jobs", s.createJob)
	mux.HandleFunc("GET /api/v1/admin/jobs", s.listJobs)
	mux.HandleFunc("PUT /api/v1/admin/jobs/{id}", s.updateJob)
	mux.HandleFunc("DELETE /api/v1/admin/jobs/{id}", s.deleteJob)
	mux.HandleFunc("POST /api/v1/admin/simulate", s.simulate)
	mux.HandleFunc("GET /api/v1/admin/settings", s.adminGetSettings)
	mux.HandleFunc("PUT /api/v1/admin/settings", s.adminUpdateSettings)
	mux.HandleFunc("PUT /api/v1/admin/townmap", s.adminUpdateTownMap)
	mux.HandleFunc("GET /api/v1/admin/players", s.adminListPlayers)
	mux.HandleFunc("PUT /api/v1/admin/players/{id}", s.adminUpdatePlayer)
	mux.HandleFunc("DELETE /api/v1/admin/players/{id}", s.adminDeletePlayer)
	return recoverer(mux)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// recoverer converts panics into 500 responses instead of dropping the connection.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
