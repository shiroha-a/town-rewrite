// Package townmap holds the runtime-editable town map: the placement of
// facilities on the main-screen grid. It is seeded with a legacy-faithful
// default at first boot, persisted as a single JSONB row, and editable by
// admins. Every player fetches it to render the map; only admins may change it.
package townmap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Grid dimensions of the town map (columns 1..Cols, rows 0..Rows-1 = A..L).
const (
	Cols = 16
	Rows = 12
)

// Facility is a single placed facility on the town map.
type Facility struct {
	Key   string `json:"key"`   // ビュー遷移先(プリセット)。空不可
	Img   string `json:"img"`   // gif名(拡張子なし)。空不可
	Alt   string `json:"alt"`   // 表示名(ツールチップ)
	Col   int    `json:"col"`   // 1..Cols
	Row   int    `json:"row"`   // 0..Rows-1
	Ready bool   `json:"ready"` // 有効なら遷移可能
}

// Default is the legacy-faithful initial layout, mirroring the placement that
// used to be hardcoded in TownView.vue.
func Default() []Facility {
	return []Facility{
		{Key: "kabu", Img: "kabu", Alt: "株取引場", Col: 2, Row: 3, Ready: true},
		{Key: "depart", Img: "depart", Alt: "中央デパート", Col: 8, Row: 3, Ready: true},
		{Key: "bank", Img: "bank", Alt: "銀行", Col: 6, Row: 4, Ready: true},
		{Key: "syokudou", Img: "syokudou", Alt: "セントラル食堂", Col: 9, Row: 5, Ready: true},
		{Key: "gym", Img: "gym", Alt: "ジム", Col: 11, Row: 9, Ready: true},
		{Key: "keiba", Img: "keiba", Alt: "競馬場", Col: 13, Row: 9, Ready: true},
		{Key: "jobchange", Img: "work", Alt: "職業安定所", Col: 2, Row: 6, Ready: true},
		{Key: "onsen", Img: "onsen", Alt: "温泉", Col: 4, Row: 7, Ready: true},
		{Key: "hospital", Img: "hospital", Alt: "中央病院", Col: 12, Row: 6, Ready: true},
		{Key: "school", Img: "school", Alt: "学校", Col: 10, Row: 7, Ready: true},
		{Key: "kyushitu", Img: "school", Alt: "教室", Col: 8, Row: 9, Ready: true},
		{Key: "kentiku", Img: "kentiku", Alt: "建設会社", Col: 13, Row: 4, Ready: false},
		{Key: "hanbai", Img: "hanbai", Alt: "自動販売機", Col: 4, Row: 4, Ready: true},
		{Key: "yakuba", Img: "yakuba", Alt: "役場（住民名鑑）", Col: 6, Row: 7, Ready: true},
		{Key: "prof", Img: "prof", Alt: "プロフィール", Col: 14, Row: 11, Ready: false},
	}
}

// Store is a thread-safe, DB-backed holder of the current town map.
type Store struct {
	pool       *pgxpool.Pool
	mu         sync.RWMutex
	facilities []Facility
}

// NewStore loads the map from the DB, seeding it from defaults if absent.
func NewStore(ctx context.Context, pool *pgxpool.Pool, defaults []Facility) (*Store, error) {
	s := &Store{pool: pool, facilities: defaults}
	var data []byte
	err := pool.QueryRow(ctx, `SELECT facilities FROM town_map WHERE id = 1`).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		b, _ := json.Marshal(defaults)
		if _, e := pool.Exec(ctx,
			`INSERT INTO town_map (id, facilities) VALUES (1, $1) ON CONFLICT (id) DO NOTHING`, b); e != nil {
			return nil, fmt.Errorf("seed town map: %w", e)
		}
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load town map: %w", err)
	}
	var fs []Facility
	if err := json.Unmarshal(data, &fs); err != nil {
		return nil, fmt.Errorf("parse town map: %w", err)
	}
	s.facilities = fs
	return s, nil
}

// Get returns a copy of the current facilities.
func (s *Store) Get() []Facility {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Facility, len(s.facilities))
	copy(out, s.facilities)
	return out
}

// Validate checks grid bounds, required fields, and one-facility-per-cell.
func Validate(fs []Facility) error {
	seen := make(map[[2]int]bool, len(fs))
	for i, f := range fs {
		if f.Key == "" {
			return fmt.Errorf("facility %d: key is required", i)
		}
		if f.Img == "" {
			return fmt.Errorf("facility %d (%s): img is required", i, f.Key)
		}
		if f.Col < 1 || f.Col > Cols {
			return fmt.Errorf("facility %d (%s): col %d out of range 1..%d", i, f.Key, f.Col, Cols)
		}
		if f.Row < 0 || f.Row >= Rows {
			return fmt.Errorf("facility %d (%s): row %d out of range 0..%d", i, f.Key, f.Row, Rows-1)
		}
		cell := [2]int{f.Col, f.Row}
		if seen[cell] {
			return fmt.Errorf("facility %d (%s): cell (%d,%d) already occupied", i, f.Key, f.Col, f.Row)
		}
		seen[cell] = true
	}
	return nil
}

// Set validates, persists, and applies a new map (used by the admin API).
func (s *Store) Set(ctx context.Context, fs []Facility) error {
	if err := Validate(fs); err != nil {
		return err
	}
	b, err := json.Marshal(fs)
	if err != nil {
		return fmt.Errorf("encode town map: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE town_map SET facilities = $1, updated_at = now() WHERE id = 1`, b); err != nil {
		return fmt.Errorf("save town map: %w", err)
	}
	s.mu.Lock()
	s.facilities = fs
	s.mu.Unlock()
	return nil
}
