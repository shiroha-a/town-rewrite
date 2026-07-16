// Package shop implements 店経営 (player-run shops): a player opens a shop and
// stocks it from their own inventory with a set price; other players visit and
// buy, with the sale credited to the owner. This package holds the reads and the
// non-money stock management; the money-moving open/buy/offer flows live in the
// action service (ledger + idempotency). Legacy: original_house.cgi §4.
package shop

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shiroha-a/town/internal/effects"
)

// ErrValidation wraps a user-facing validation failure.
type ErrValidation struct{ Message string }

func (e *ErrValidation) Error() string { return e.Message }

// Summary is one open shop in the shopping-street list.
type Summary struct {
	OwnerID   int64  `json:"owner_id"`
	OwnerName string `json:"owner_name"`
	Name      string `json:"name"`
	Listings  int    `json:"listings"`
}

// Listing is one item for sale in a shop.
type Listing struct {
	ItemID   int64          `json:"item_id"`
	ItemName string         `json:"item_name"`
	Category string         `json:"category"`
	Price    int64          `json:"price"`
	Stock    int            `json:"stock"`
	Money    int64          `json:"money"`  // 使用時のお金増減
	Params   map[string]int `json:"params"` // 使用効果
}

// Detail is a shop's full view.
type Detail struct {
	OwnerID   int64     `json:"owner_id"`
	OwnerName string    `json:"owner_name"`
	Name      string    `json:"name"`
	Listings  []Listing `json:"listings"`
}

// Service provides shop reads and stock management.
type Service struct {
	pool *pgxpool.Pool
}

// New builds the service.
func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// ListShops returns all open shops (with a stocked-listing count), newest first.
func (s *Service) ListShops(ctx context.Context) ([]Summary, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT sh.owner_id, p.display_name, sh.name,
		        (SELECT count(*) FROM shop_listings sl WHERE sl.owner_id = sh.owner_id AND sl.stock > 0)
		 FROM shops sh JOIN players p ON p.id = sh.owner_id
		 WHERE p.deleted_at IS NULL
		 ORDER BY sh.opened_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list shops: %w", err)
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var s Summary
		if err := rows.Scan(&s.OwnerID, &s.OwnerName, &s.Name, &s.Listings); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetShop returns a shop's listings (in-stock first).
func (s *Service) GetShop(ctx context.Context, ownerID int64) (Detail, error) {
	var d Detail
	d.OwnerID = ownerID
	err := s.pool.QueryRow(ctx,
		`SELECT sh.name, p.display_name FROM shops sh JOIN players p ON p.id = sh.owner_id WHERE sh.owner_id = $1`,
		ownerID).Scan(&d.Name, &d.OwnerName)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, &ErrValidation{Message: "その商店は存在しません。"}
	}
	if err != nil {
		return d, fmt.Errorf("shop: %w", err)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT sl.item_id, ci.name, COALESCE(ci.category, ''), sl.price, sl.stock, ci.effect
		 FROM shop_listings sl JOIN content_items ci ON ci.id = sl.item_id
		 WHERE sl.owner_id = $1 ORDER BY (sl.stock > 0) DESC, ci.name`, ownerID)
	if err != nil {
		return d, fmt.Errorf("listings: %w", err)
	}
	defer rows.Close()
	d.Listings = []Listing{}
	for rows.Next() {
		var l Listing
		var effJSON []byte
		if err := rows.Scan(&l.ItemID, &l.ItemName, &l.Category, &l.Price, &l.Stock, &effJSON); err != nil {
			return d, err
		}
		if eff, err := effects.ParseEffect(effJSON); err == nil {
			l.Money = eff.MoneySum()
			l.Params = eff.ParamSum()
		}
		d.Listings = append(d.Listings, l)
	}
	return d, rows.Err()
}

// itemDurability returns a content item's per-set durability (min 1).
func itemDurability(ctx context.Context, tx pgx.Tx, itemID int64) (int, error) {
	var d int
	if err := tx.QueryRow(ctx, `SELECT GREATEST(1, COALESCE(durability, 1)) FROM content_items WHERE id = $1`, itemID).Scan(&d); err != nil {
		return 0, err
	}
	return d, nil
}

// AddStock moves qty of an item from the owner's inventory into their shop
// listing and sets the price. Requires an open shop and sufficient inventory.
func (s *Service) AddStock(ctx context.Context, ownerID, itemID int64, qty int, price int64) error {
	if qty <= 0 || price < 0 {
		return &ErrValidation{Message: "数量と価格を正しく入力してください。"}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shops WHERE owner_id = $1)`, ownerID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return &ErrValidation{Message: "先に商店を開いてください。"}
		}
		var have int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(quantity, 0) FROM player_items WHERE player_id = $1 AND item_id = $2`, ownerID, itemID).Scan(&have); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if have < qty {
			return &ErrValidation{Message: "出品する在庫が足りません。"}
		}
		dur, err := itemDurability(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE player_items SET quantity = quantity - $3,
			   remaining_uses = GREATEST(0, remaining_uses - $4), updated_at = now()
			 WHERE player_id = $1 AND item_id = $2`, ownerID, itemID, qty, dur*qty); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO shop_listings (owner_id, item_id, price, stock) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (owner_id, item_id) DO UPDATE SET price = $3, stock = shop_listings.stock + $4`,
			ownerID, itemID, price, qty); err != nil {
			return err
		}
		return nil
	})
}

// Unstock moves qty from a listing back into the owner's inventory.
func (s *Service) Unstock(ctx context.Context, ownerID, itemID int64, qty int) error {
	if qty <= 0 {
		return &ErrValidation{Message: "数量を正しく入力してください。"}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var stock int
		if err := tx.QueryRow(ctx, `SELECT stock FROM shop_listings WHERE owner_id = $1 AND item_id = $2`, ownerID, itemID).Scan(&stock); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &ErrValidation{Message: "出品が見つかりません。"}
			}
			return err
		}
		if stock < qty {
			return &ErrValidation{Message: "撤去する在庫が足りません。"}
		}
		dur, err := itemDurability(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE shop_listings SET stock = stock - $3 WHERE owner_id = $1 AND item_id = $2`, ownerID, itemID, qty); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_items (player_id, item_id, quantity, remaining_uses) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (player_id, item_id) DO UPDATE SET quantity = player_items.quantity + $3,
			   remaining_uses = player_items.remaining_uses + $4, updated_at = now()`,
			ownerID, itemID, qty, dur*qty); err != nil {
			return err
		}
		return nil
	})
}

// SetPrice updates a listing's price.
func (s *Service) SetPrice(ctx context.Context, ownerID, itemID int64, price int64) error {
	if price < 0 {
		return &ErrValidation{Message: "価格が不正です。"}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE shop_listings SET price = $3 WHERE owner_id = $1 AND item_id = $2`, ownerID, itemID, price)
	if err != nil {
		return fmt.Errorf("set price: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return &ErrValidation{Message: "出品が見つかりません。"}
	}
	return nil
}
