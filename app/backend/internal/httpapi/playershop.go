package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/shiroha-a/town/internal/shop"
)

// listShops returns the shopping street (all open shops). Public.
func (s *Server) listShops(w http.ResponseWriter, r *http.Request) {
	list, err := s.shop.ListShops(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// getShop returns one shop's listings. Public.
func (s *Server) getShop(w http.ResponseWriter, r *http.Request) {
	ownerID, err := strconv.ParseInt(r.PathValue("ownerId"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid owner id")
		return
	}
	d, err := s.shop.GetShop(r.Context(), ownerID)
	if err != nil {
		writeShopValidation(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type shopOpenReq struct {
	Name           string `json:"name"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (s *Server) shopOpen(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req shopOpenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	p, err := s.actions.DoOpenShop(r.Context(), id, req.Name, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

type shopStockReq struct {
	ItemID   int64 `json:"item_id"`
	Quantity int   `json:"quantity"`
	Price    int64 `json:"price"`
}

// shopStock lists qty of an item from the owner's inventory at the given price.
func (s *Server) shopStock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req shopStockReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	writeShopValidation(w, s.shop.AddStock(r.Context(), id, req.ItemID, req.Quantity, req.Price))
}

func (s *Server) shopUnstock(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req shopStockReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	writeShopValidation(w, s.shop.Unstock(r.Context(), id, req.ItemID, req.Quantity))
}

func (s *Server) shopPrice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req shopStockReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	writeShopValidation(w, s.shop.SetPrice(r.Context(), id, req.ItemID, req.Price))
}

type shopBuyReq struct {
	OwnerID        int64  `json:"owner_id"`
	ItemID         int64  `json:"item_id"`
	Quantity       int    `json:"quantity"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (s *Server) shopBuy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req shopBuyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	p, err := s.actions.DoBuyFromShop(r.Context(), id, req.OwnerID, req.ItemID, req.Quantity, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

type shopOfferReq struct {
	OwnerID        int64  `json:"owner_id"`
	Amount         int64  `json:"amount"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (s *Server) shopOffer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req shopOfferReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	p, err := s.actions.DoOffer(r.Context(), id, req.OwnerID, req.Amount, req.IdempotencyKey)
	writeFacilityResult(w, p, err)
}

// writeShopValidation maps a non-player-returning shop op result: validation to
// 422, ok to 200.
func writeShopValidation(w http.ResponseWriter, err error) {
	var verr *shop.ErrValidation
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.As(err, &verr):
		writeError(w, http.StatusUnprocessableEntity, verr.Message)
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
