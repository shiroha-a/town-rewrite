// Package content manages admin-authored, data-driven game content (items,
// jobs, and later events). All effect/condition JSON is validated through the
// effects engine before it is stored, and a simulator lets admins preview an
// effect's outcome before committing it.
package content

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shiroha-a/town/internal/effects"
	"github.com/shiroha-a/town/internal/gametime"
)

// ValidationError is a client-fixable problem with admin input (e.g. a
// malformed effect). It maps to HTTP 400.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// Item is a content item definition. Effect is the item's use-time effect.
type Item struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Category string          `json:"category"`
	Price    int64           `json:"price"`
	Effect   json.RawMessage `json:"effect"`
	Enabled  bool            `json:"enabled"`
}

// Job is a content job definition.
type Job struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Requirements json.RawMessage `json:"requirements"`
	Effect       json.RawMessage `json:"effect"`
	Enabled      bool            `json:"enabled"`
}

// Service manages content rows.
type Service struct {
	pool               *pgxpool.Pool
	loc                *time.Location
	dayBoundaryHour    int
	departDailyCount   int
	syokudouDailyCount int
}

func New(pool *pgxpool.Pool, loc *time.Location, dayBoundaryHour, departDailyCount, syokudouDailyCount int) *Service {
	if loc == nil {
		loc = time.UTC
	}
	return &Service{
		pool: pool, loc: loc, dayBoundaryHour: dayBoundaryHour,
		departDailyCount: departDailyCount, syokudouDailyCount: syokudouDailyCount,
	}
}

// dailyCountFor returns how many items to show today for a facility (0 = all).
func (s *Service) dailyCountFor(facility string) int {
	switch facility {
	case "":
		return s.departDailyCount
	case "syokudou":
		return s.syokudouDailyCount
	default:
		return 0 // ジム/温泉などは日替わりなし
	}
}

func orEmptyArray(b []byte) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

// CreateItem validates the effect and inserts a new item.
func (s *Service) CreateItem(ctx context.Context, name, category string, price int64, effect []byte) (Item, error) {
	if name == "" {
		return Item{}, &ValidationError{Message: "name is required"}
	}
	if price < 0 {
		return Item{}, &ValidationError{Message: "price must be >= 0"}
	}
	effect = orEmptyArray(effect)
	if _, err := effects.ParseEffect(effect); err != nil {
		return Item{}, &ValidationError{Message: "effect: " + err.Error()}
	}
	var it Item
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO content_items (name, category, price, effect)
		 VALUES ($1, $2, $3, $4::jsonb)
		 RETURNING id, name, COALESCE(category, ''), price, effect, enabled`,
		name, category, price, string(effect)).
		Scan(&it.ID, &it.Name, &it.Category, &it.Price, &it.Effect, &it.Enabled); err != nil {
		return Item{}, fmt.Errorf("insert item: %w", err)
	}
	return it, nil
}

// ListItems returns all items ordered by id.
func (s *Service) ListItems(ctx context.Context) ([]Item, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, COALESCE(category, ''), price, effect, enabled
		 FROM content_items ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Name, &it.Category, &it.Price, &it.Effect, &it.Enabled); err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// effectSummary parses an effect and returns its net money delta and non-zero
// per-parameter deltas (for displaying "rising parameters").
func effectSummary(effJSON []byte) (int64, map[string]int) {
	params := map[string]int{}
	eff, err := effects.ParseEffect(effJSON)
	if err != nil {
		return 0, params
	}
	for k, v := range eff.ParamSum() {
		if v != 0 {
			params[k] = v
		}
	}
	return eff.MoneySum(), params
}

// requirementSummary parses conditions into per-parameter minimums (for
// displaying "required parameters").
func requirementSummary(reqJSON []byte) map[string]int {
	c, err := effects.ParseConditions(reqJSON)
	if err != nil {
		return map[string]int{}
	}
	return c.ParamMins()
}

// ShopItem is the public view of a purchasable item, including the parameters
// its use raises (to mirror the legacy department-store table).
type ShopItem struct {
	ID              int64          `json:"id"`
	Name            string         `json:"name"`
	Category        string         `json:"category"`
	Price           int64          `json:"price"`
	Money           int64          `json:"money"`            // 使用時のお金増減
	Params          map[string]int `json:"params"`           // 使用時の上昇パラメータ
	IntervalMin     int            `json:"interval_min"`     // 使用間隔(分。0=なし)
	Durability      int            `json:"durability"`       // 1セットあたりの耐久(使用可能回数/日数)
	DurabilityUnit  string         `json:"durability_unit"`  // 'use'(回) or 'day'(日)
	PowerMultiplier int            `json:"power_multiplier"` // 温泉の回復速度倍率(0=温泉ではない)
}

// ListShopItems returns the general department-store items (facility=”).
func (s *Service) ListShopItems(ctx context.Context) ([]ShopItem, error) {
	return s.listItems(ctx, "")
}

// ListFacilityMenu returns the menu items for a facility (e.g. 'syokudou').
func (s *Service) ListFacilityMenu(ctx context.Context, facility string) ([]ShopItem, error) {
	return s.listItems(ctx, facility)
}

func (s *Service) listItems(ctx context.Context, facility string) ([]ShopItem, error) {
	query := `SELECT id, name, COALESCE(category, ''), price, effect, use_interval_min, durability, durability_unit, power_multiplier
	          FROM content_items WHERE enabled AND facility = $1`
	args := []any{facility}
	// デパート/食堂は毎日一部だけを品揃えする(旧仕様)。
	if n := s.dailyCountFor(facility); n > 0 {
		query += ` AND id IN (SELECT id FROM daily_shop_ids($1, $2, $3))`
		args = append(args, gametime.DateKey(time.Now(), s.loc, s.dayBoundaryHour), n)
	}
	query += ` ORDER BY id`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()
	items := []ShopItem{}
	for rows.Next() {
		var (
			it      ShopItem
			effJSON []byte
		)
		if err := rows.Scan(&it.ID, &it.Name, &it.Category, &it.Price, &effJSON, &it.IntervalMin, &it.Durability, &it.DurabilityUnit, &it.PowerMultiplier); err != nil {
			return nil, fmt.Errorf("scan item: %w", err)
		}
		it.Money, it.Params = effectSummary(effJSON)
		items = append(items, it)
	}
	return items, rows.Err()
}

// JobOption is the public view of a job at the job office, including the
// parameters required to take it and those working it changes.
type JobOption struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	Pay           int64          `json:"pay"`            // 基本給(1回)。salaryと同値
	Salary        int64          `json:"salary"`         // 基本給(1回)
	Rank          int            `json:"rank"`           // ランク(星)
	RequireMaster string         `json:"require_master"` // 前提マスター職(なければ空)
	Requirements  map[string]int `json:"requirements"`   // 就くための必要パラメータ
	WorkParams    map[string]int `json:"work_params"`    // 働いたときの上昇/消費パラメータ
}

// ListSelectableJobs returns enabled jobs for the job-office UI.
func (s *Service) ListSelectableJobs(ctx context.Context) ([]JobOption, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, requirements, effect, salary, rank, require_master
		 FROM content_jobs WHERE enabled ORDER BY rank, id`)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	jobs := []JobOption{}
	for rows.Next() {
		var (
			opt              JobOption
			reqJSON, effJSON []byte
			requireMaster    *string
		)
		if err := rows.Scan(&opt.ID, &opt.Name, &reqJSON, &effJSON,
			&opt.Salary, &opt.Rank, &requireMaster); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		opt.Pay = opt.Salary
		_, opt.WorkParams = effectSummary(effJSON)
		opt.Requirements = requirementSummary(reqJSON)
		if requireMaster != nil {
			opt.RequireMaster = *requireMaster
		}
		jobs = append(jobs, opt)
	}
	return jobs, rows.Err()
}

// CreateJob validates the requirements and effect, then inserts a new job.
func (s *Service) CreateJob(ctx context.Context, name string, requirements, effect []byte) (Job, error) {
	if name == "" {
		return Job{}, &ValidationError{Message: "name is required"}
	}
	requirements = orEmptyArray(requirements)
	effect = orEmptyArray(effect)
	if _, err := effects.ParseConditions(requirements); err != nil {
		return Job{}, &ValidationError{Message: "requirements: " + err.Error()}
	}
	if _, err := effects.ParseEffect(effect); err != nil {
		return Job{}, &ValidationError{Message: "effect: " + err.Error()}
	}
	var j Job
	if err := s.pool.QueryRow(ctx,
		`INSERT INTO content_jobs (name, requirements, effect)
		 VALUES ($1, $2::jsonb, $3::jsonb)
		 RETURNING id, name, requirements, effect, enabled`,
		name, string(requirements), string(effect)).
		Scan(&j.ID, &j.Name, &j.Requirements, &j.Effect, &j.Enabled); err != nil {
		return Job{}, fmt.Errorf("insert job: %w", err)
	}
	return j, nil
}

// ListJobs returns all jobs ordered by id.
func (s *Service) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, requirements, effect, enabled FROM content_jobs ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Name, &j.Requirements, &j.Effect, &j.Enabled); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// SimResult is the outcome of a dry-run effect simulation.
type SimResult struct {
	Plan     effects.Plan `json:"plan"`
	Warnings []string     `json:"warnings"`
}

// Simulate validates an effect and computes its result against a hypothetical
// state without applying anything. It surfaces economy-impact warnings so an
// admin can catch a money faucet before committing the content.
func Simulate(effectJSON []byte, state effects.State) (SimResult, error) {
	eff, err := effects.ParseEffect(orEmptyArray(effectJSON))
	if err != nil {
		return SimResult{}, &ValidationError{Message: "effect: " + err.Error()}
	}
	plan := eff.Plan(state)
	warnings := []string{}
	if plan.MoneyDelta > 0 {
		warnings = append(warnings,
			fmt.Sprintf("この効果は所持金を%d円増やします(faucet)。経済への影響に注意してください。", plan.MoneyDelta))
	}
	return SimResult{Plan: plan, Warnings: warnings}, nil
}
