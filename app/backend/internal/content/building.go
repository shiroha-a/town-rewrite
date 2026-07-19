package content

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shiroha-a/town/internal/building"
	"github.com/shiroha-a/town/internal/townmap"
)

// BuildingState is everything the build screen (建設会社/KentikuView) needs to
// render the town grids, the exterior/interior catalog, and the player's houses.
type BuildingState struct {
	Towns      []building.Town     `json:"towns"`
	Exteriors  []building.Exterior `json:"exteriors"`
	Interiors  []building.Interior `json:"interiors"`
	Plots      []PlotCell          `json:"plots"`       // 管理者が指定した空地マス
	Houses     []HouseCell         `json:"houses"`      // 全プレイヤーの家(グリッド描画用)
	MyHouses   []MyHouse           `json:"my_houses"`   // 自分の家(一覧)
	HouseCount int                 `json:"house_count"` // 自分の所有軒数
	MochiieMax int                 `json:"mochiie_max"`
	Cols       int                 `json:"cols"`
	Rows       int                 `json:"rows"`
}

// HouseCell is a house on the map (any owner), used for grid rendering.
type HouseCell struct {
	Town      int    `json:"town"`
	Row       int    `json:"row"`
	Col       int    `json:"col"`
	Exterior  string `json:"exterior"`
	OwnerName string `json:"owner_name"`
	Own       bool   `json:"own"`
}

// MyHouse is one of the player's own houses (for the owned-houses list).
type MyHouse struct {
	ID           int64  `json:"id"`
	Town         int    `json:"town"`
	Row          int    `json:"row"`
	Col          int    `json:"col"`
	Exterior     string `json:"exterior"`
	InteriorRank int    `json:"interior_rank"`
	BuiltAt      string `json:"built_at"` // RFC3339
}

// PlotCell is an admin-designated empty plot on which a house may be built.
type PlotCell struct {
	Town int `json:"town"`
	Row  int `json:"row"`
	Col  int `json:"col"`
}

// Building returns the full state of the construction screen for a player.
func (s *Service) Building(ctx context.Context, playerID int64) (*BuildingState, error) {
	st := &BuildingState{
		Towns:      building.Towns(),
		Exteriors:  building.Exteriors(),
		Interiors:  building.Interiors(),
		MochiieMax: building.MochiieMax,
		Cols:       townmap.Cols,
		Rows:       townmap.Rows,
		Houses:     []HouseCell{},
		MyHouses:   []MyHouse{},
	}

	plots, err := s.ListPlots(ctx)
	if err != nil {
		return nil, err
	}
	st.Plots = plots

	rows, err := s.pool.Query(ctx,
		`SELECT h.town, h.grid_row, h.grid_col, h.exterior, h.owner_id, COALESCE(p.display_name, '')
		 FROM player_houses h LEFT JOIN players p ON p.id = h.owner_id
		 ORDER BY h.town, h.grid_row, h.grid_col`)
	if err != nil {
		return nil, fmt.Errorf("list houses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			c       HouseCell
			ownerID int64
		)
		if err := rows.Scan(&c.Town, &c.Row, &c.Col, &c.Exterior, &ownerID, &c.OwnerName); err != nil {
			return nil, fmt.Errorf("scan house: %w", err)
		}
		c.Own = ownerID == playerID
		st.Houses = append(st.Houses, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate houses: %w", err)
	}

	mrows, err := s.pool.Query(ctx,
		`SELECT id, town, grid_row, grid_col, exterior, interior_rank, built_at
		 FROM player_houses WHERE owner_id = $1 ORDER BY built_at`, playerID)
	if err != nil {
		return nil, fmt.Errorf("list my houses: %w", err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var (
			h     MyHouse
			built time.Time
		)
		if err := mrows.Scan(&h.ID, &h.Town, &h.Row, &h.Col, &h.Exterior, &h.InteriorRank, &built); err != nil {
			return nil, fmt.Errorf("scan my house: %w", err)
		}
		h.BuiltAt = built.Format(time.RFC3339)
		st.MyHouses = append(st.MyHouses, h)
	}
	if err := mrows.Err(); err != nil {
		return nil, fmt.Errorf("iterate my houses: %w", err)
	}
	st.HouseCount = len(st.MyHouses)
	return st, nil
}

// ListPlots returns every admin-designated empty plot across all towns.
func (s *Service) ListPlots(ctx context.Context) ([]PlotCell, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT town, grid_row, grid_col FROM town_plots ORDER BY town, grid_row, grid_col`)
	if err != nil {
		return nil, fmt.Errorf("list plots: %w", err)
	}
	defer rows.Close()
	out := []PlotCell{}
	for rows.Next() {
		var c PlotCell
		if err := rows.Scan(&c.Town, &c.Row, &c.Col); err != nil {
			return nil, fmt.Errorf("scan plot: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetPlots replaces the full set of empty plots (admin editor save). Coordinates
// are validated against the grid bounds; duplicates are ignored.
func (s *Service) SetPlots(ctx context.Context, plots []PlotCell) error {
	for _, p := range plots {
		if p.Col < 1 || p.Col > townmap.Cols || p.Row < 0 || p.Row >= townmap.Rows {
			return &ValidationError{Message: fmt.Sprintf("空地の座標が範囲外です(row=%d, col=%d)", p.Row, p.Col)}
		}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM town_plots`); err != nil {
			return fmt.Errorf("clear plots: %w", err)
		}
		for _, p := range plots {
			if _, err := tx.Exec(ctx,
				`INSERT INTO town_plots (town, grid_row, grid_col) VALUES ($1, $2, $3)
				 ON CONFLICT DO NOTHING`,
				p.Town, p.Row, p.Col); err != nil {
				return fmt.Errorf("insert plot: %w", err)
			}
		}
		return nil
	})
}
