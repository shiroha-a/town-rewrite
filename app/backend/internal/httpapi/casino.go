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

type casinoReq struct {
	Bet            int64           `json:"bet"`
	Params         json.RawMessage `json:"params"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type casinoResp struct {
	Player playerResp `json:"player"`
	Payout int64      `json:"payout"`
	Win    bool       `json:"win"`
	Detail any        `json:"detail"`
}

func (s *Server) casinoPlay(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	game := r.PathValue("game")
	var req casinoReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	res, err := s.actions.DoCasinoPlay(r.Context(), id, game, req.Bet, req.Params, req.IdempotencyKey)
	if err != nil {
		var condErr *action.ConditionError
		switch {
		case errors.Is(err, player.ErrNotFound):
			writeError(w, http.StatusNotFound, "player not found")
		case errors.As(err, &condErr):
			writeError(w, http.StatusUnprocessableEntity, condErr.Message)
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, casinoResp{
		Player: toResp(res.Player),
		Payout: res.Payout,
		Win:    res.Win,
		Detail: res.Detail,
	})
}
