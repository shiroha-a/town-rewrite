// Package action applies player actions (work, purchases, item use) through the
// data-driven effect engine. Every action is server-authoritative and atomic:
// condition check, status changes, money movement and the action log all commit
// together, and a client idempotency key makes retries safe.
package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shiroha-a/town/internal/cleague"
	"github.com/shiroha-a/town/internal/condition"
	"github.com/shiroha-a/town/internal/effects"
	"github.com/shiroha-a/town/internal/event"
	"github.com/shiroha-a/town/internal/gametime"
	"github.com/shiroha-a/town/internal/greeting"
	"github.com/shiroha-a/town/internal/keiba"
	"github.com/shiroha-a/town/internal/ledger"
	"github.com/shiroha-a/town/internal/player"
	"github.com/shiroha-a/town/internal/rng"
	"github.com/shiroha-a/town/internal/settings"
	"github.com/shiroha-a/town/internal/stock"
)

// statusColumns whitelists the parameter -> column mapping. Effect params are
// validated by the effects package, and this map is the only place a param name
// reaches SQL, so column names are never attacker-controlled. Column names match
// the param names (romaji), so the map is identity over effects.AllParams.
var statusColumns = func() map[string]string {
	m := make(map[string]string, len(effects.AllParams))
	for _, p := range effects.AllParams {
		m[p] = p
	}
	return m
}()

// detailedParamMax is an overflow guard for the detailed parameters (国語/体力/…),
// which the legacy left effectively unbounded (青天井). The columns are BIGINT
// (migration 0036); this ceiling sits far below int64/BIGINT max so that
// clamp arithmetic and the derived power-max computation cannot overflow, while
// staying astronomically beyond any reachable gameplay value. It is not a
// gameplay cap. energy/nou_energy use their own *_max columns.
const detailedParamMax = 1_000_000_000_000_000 // 1e15

// satietyMax is the full 満腹度. Eating fills to this; food is blocked at full.
const satietyMax = 100

// fillSatiety sets 満腹度 to full and restarts its decay clock.
func fillSatiety(ctx context.Context, tx pgx.Tx, playerID int64) error {
	if _, err := tx.Exec(ctx,
		`UPDATE player_status SET satiety = $1, satiety_updated_at = now(), updated_at = now()
		 WHERE player_id = $2`, satietyMax, playerID); err != nil {
		return fmt.Errorf("fill satiety: %w", err)
	}
	return nil
}

// addWeightFromItem adds the item's calorie_g to the player's weight in grams
// (design 17.3, 飲食で体重増)。calorie_gはcontent_itemsから直接読み、0で下限クリップする。
func addWeightFromItem(ctx context.Context, tx pgx.Tx, playerID, itemID int64) error {
	if _, err := tx.Exec(ctx,
		`UPDATE player_status ps
		 SET weight_g = GREATEST(0, ps.weight_g + ci.calorie_g), updated_at = now()
		 FROM content_items ci
		 WHERE ps.player_id = $1 AND ci.id = $2`, playerID, itemID); err != nil {
		return fmt.Errorf("add weight: %w", err)
	}
	return nil
}

// ConditionError means the player did not meet an action's requirements.
type ConditionError struct{ Message string }

func (e *ConditionError) Error() string { return e.Message }

// ErrItemNotFound is returned when an item does not exist or is disabled.
var ErrItemNotFound = errors.New("item not found")

// zaikoAdjust divides the master stock to derive the shop-front daily stock
// (旧 zaiko_tyousetuti=2). remaining = max(1, ceil(stock_master / zaikoAdjust)).
const zaikoAdjust = 2

// Service applies actions.
type Service struct {
	pool            *pgxpool.Pool
	ledger          *ledger.Repo
	players         *player.Service
	rng             *rng.Rand
	loc             *time.Location
	dayBoundaryHour int
	settings        *settings.Store
}

func New(pool *pgxpool.Pool, led *ledger.Repo, players *player.Service, r *rng.Rand, loc *time.Location, dayBoundaryHour int, st *settings.Store) *Service {
	if loc == nil {
		loc = time.UTC
	}
	return &Service{
		pool: pool, ledger: led, players: players, rng: r, loc: loc,
		dayBoundaryHour: dayBoundaryHour, settings: st,
	}
}

// dailyMenuCond appends the "in today's rotation" filter to a load query when the
// facility rotates daily (depart/syokudou). Returns the extra SQL and args.
func (s *Service) dailyMenuCond(facility string, nextArg int) (string, []any) {
	cfg := s.settings.Get()
	var n int
	switch facility {
	case "":
		n = cfg.DepartDailyCount
	case "syokudou":
		n = cfg.SyokudouDailyCount
	}
	if n <= 0 {
		return "", nil
	}
	cond := fmt.Sprintf(" AND id IN (SELECT id FROM daily_shop_ids($%d, $%d, $%d))", nextArg, nextArg+1, nextArg+2)
	return cond, []any{facility, gametime.DateKey(time.Now(), s.loc, s.dayBoundaryHour), n}
}

// gameDate returns the current game day (日境界 AM5:00 等) for stock partitioning.
func (s *Service) gameDate(now time.Time) time.Time {
	return gametime.Date(now, s.loc, s.dayBoundaryHour)
}

// consumeStock lazily creates today's shop-front stock for an item and decrements
// it by qty. Items with stock_master = NULL (services etc.) are treated as
// unlimited and skipped. Insufficient stock yields a ConditionError (売り切れ).
func (s *Service) consumeStock(ctx context.Context, tx pgx.Tx, facility string, itemID int64, qty int) error {
	var stockMaster *int
	if err := tx.QueryRow(ctx,
		`SELECT stock_master FROM content_items WHERE id = $1`, itemID).Scan(&stockMaster); err != nil {
		return fmt.Errorf("read stock_master: %w", err)
	}
	if stockMaster == nil {
		return nil // 在庫無制限
	}
	date := s.gameDate(time.Now())
	if _, err := tx.Exec(ctx,
		`INSERT INTO shop_daily_stock (facility, item_id, game_date, remaining)
		 VALUES ($1, $2, $3, GREATEST(1, CEIL($4::numeric / $5)::int))
		 ON CONFLICT (facility, item_id, game_date) DO NOTHING`,
		facility, itemID, date, *stockMaster, zaikoAdjust); err != nil {
		return fmt.Errorf("init stock: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE shop_daily_stock SET remaining = remaining - $4
		 WHERE facility = $1 AND item_id = $2 AND game_date = $3 AND remaining >= $4`,
		facility, itemID, date, qty)
	if err != nil {
		return fmt.Errorf("consume stock: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return &ConditionError{Message: "売り切れです。"}
	}
	return nil
}

// runAction wraps the common per-action transaction: it claims the idempotency
// key first (a duplicate short-circuits to a no-op), reads the player state,
// then runs body. On success it returns the refreshed player aggregate.
func (s *Service) runAction(ctx context.Context, playerID int64, actionType, idemKey string, body func(ctx context.Context, tx pgx.Tx, state effects.State) error) (*player.Player, error) {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		duplicate, err := s.claimIdempotency(ctx, tx, playerID, actionType, idemKey)
		if err != nil {
			return err
		}
		if duplicate {
			return nil // 既に実行済み: no-op
		}
		state, err := s.readState(ctx, tx, playerID)
		if err != nil {
			return err
		}
		return body(ctx, tx, state)
	})
	if err != nil {
		return nil, err
	}
	return s.players.Get(ctx, playerID)
}

// WorkResult summarizes a single work action for the result screen (design 17.5).
type WorkResult struct {
	ExpGained  int      // 今回の経験値増減
	NewLevel   int      // 到達レベル
	LeveledUp  bool     // レベルが上がったか(昇給発生)
	ThisSalary int64    // 昇給後の給料(1回あたり)
	Pay        int64    // 今回支給された給料(支払間隔到達時のみ>0)
	PayEvery   int      // 支払間隔(N回出勤ごと)
	Bonus      int64    // レベルアップ時ボーナス
	Mastered   []string // 今回新たにマスターした職業
}

// DoWork works the player's current job (design 17.5 do_work). Students cannot
// work. Working is gated by the work interval, ability requirements, power floor
// and BMI condition; it then earns condition-based experience (which may level
// the player up), pays salary on the pay interval, grants a level-up bonus,
// records job mastery at level 15, and spends power and weight. It returns a
// WorkResult so the UI can show the legacy-style salary/raise/bonus messages.
func (s *Service) DoWork(ctx context.Context, playerID int64, idempotencyKey string) (*player.Player, *WorkResult, error) {
	job, err := s.currentJob(ctx, playerID)
	if err != nil {
		return nil, nil, err
	}
	if job == "学生" {
		return nil, nil, &ConditionError{Message: "学生は働けません。職業安定所で仕事を探してください。"}
	}
	econ, err := s.loadJobEconomy(ctx, job)
	if err != nil {
		return nil, nil, err
	}
	var result WorkResult
	p, err := s.runAction(ctx, playerID, "work", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		// 1. 就労間隔(前回出勤からwork_interval_min分は再出勤不可)。
		var nextAt *time.Time
		if err := tx.QueryRow(ctx,
			`SELECT next_available_at FROM player_facility_cooldowns WHERE player_id = $1 AND facility = 'work'`,
			playerID).Scan(&nextAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read work cooldown: %w", err)
		}
		if !s.settings.Get().DebugNoCooldown && nextAt != nil && time.Now().Before(*nextAt) {
			remain := int(time.Until(*nextAt).Minutes()) + 1
			return &ConditionError{Message: fmt.Sprintf("まだ働けません。あと約%d分お待ちください。", remain)}
		}
		// 2. 能力値必要条件・パワー下限。
		if ok, failed := econ.conds.Check(state); !ok {
			return &ConditionError{Message: conditionMessage(failed)}
		}
		if state.Params["energy"].Value < econ.bodyCost {
			return &ConditionError{Message: "身体パワーが足りません。"}
		}
		if state.Params["nou_energy"].Value < econ.nouCost {
			return &ConditionError{Message: "頭脳パワーが足りません。"}
		}
		// 2b. 体格(BMI)条件。身長は求職時に判定するためここでは体型のみ。
		var heightCm, weightG, diseaseIndex int
		if err := tx.QueryRow(ctx,
			`SELECT height_cm, weight_g, disease_index FROM player_status WHERE player_id = $1`,
			playerID).Scan(&heightCm, &weightG, &diseaseIndex); err != nil {
			return fmt.Errorf("read body: %w", err)
		}
		bmi := condition.BMI(heightCm, weightG)
		if (econ.bmiMin != nil && bmi < *econ.bmiMin) || (econ.bmiMax != nil && bmi > *econ.bmiMax) {
			return &ConditionError{Message: "体型がこの仕事の条件に合いません。"}
		}
		// 3. コンディション→経験値基礎値(重病・最悪は就労不可)。
		cond := condition.Compute(condition.Input{
			Energy: state.Params["energy"].Value, EnergyMax: state.Params["energy"].Max,
			NouEnergy: state.Params["nou_energy"].Value, NouEnergyMax: state.Params["nou_energy"].Max,
			Kenkou: state.Params["kenkou"].Value, Satiety: state.Params["satiety"].Value,
			BMI: bmi, DiseaseIndex: diseaseIndex,
		})
		base, canWork := condition.WorkExpBase(cond)
		if !canWork {
			return &ConditionError{Message: "今日は体調が悪すぎて働けません。病院で治療しましょう。"}
		}
		// 4. 経験値 = 基礎値 + rand(1..5)(RNGシード可能)。
		randed := base + 1 + s.rng.IntN(5)
		// 5. exp加算(下限0)とレベル(=floor(exp/100))・レベルアップ判定。
		var oldExp, kaisuu int
		var mastered []string
		if err := tx.QueryRow(ctx,
			`SELECT job_exp, job_kaisuu, mastered_jobs FROM player_status WHERE player_id = $1`,
			playerID).Scan(&oldExp, &kaisuu, &mastered); err != nil {
			return fmt.Errorf("read job progress: %w", err)
		}
		oldLevel := oldExp / 100
		newExp := oldExp + randed
		if newExp < 0 {
			newExp = 0
		}
		newLevel := newExp / 100
		leveledUp := newLevel > oldLevel
		// 6. 今回の給料(レベル×昇給係数で増額)。
		thisSalary := econ.salary + econ.salary*int64(newLevel)*int64(econ.raiseRate)/100
		// 7. 勤務回数を進め、支払間隔ごとにまとめて支給。
		payInterval := econ.payInterval
		if payInterval <= 0 {
			payInterval = 1
		}
		kaisuu++
		var pay int64
		if kaisuu%payInterval == 0 {
			pay = thisSalary * int64(payInterval)
		}
		// 8. レベルアップ時ボーナス(今回給料 × bonus_rate%)。
		var bonus int64
		if leveledUp {
			bonus = thisSalary * int64(econ.bonusRate) / 100
		}
		// 給料・ボーナスは台帳経由(system:payroll_source が原資、zero-sum維持)。
		if total := pay + bonus; total > 0 {
			if err := s.ledger.PostTx(ctx, tx, "work", "", []ledger.Entry{
				{Account: ledger.SystemAccount("payroll_source"), Delta: -total},
				{Account: ledger.PlayerAccount(playerID), Delta: total},
			}); err != nil {
				return fmt.Errorf("pay salary: %w", err)
			}
		}
		// 9. レベル15到達&未マスターならマスター認定。
		masteredNow := newLevel >= 15 && !contains(mastered, job)
		// 10. パワー消費 = 基礎コスト + floor(現在値 × 0.0125)。11. 体重減少 = body_cost×10。
		energySpend := econ.bodyCost + state.Params["energy"].Value/80
		nouSpend := econ.nouCost + state.Params["nou_energy"].Value/80
		if _, err := tx.Exec(ctx, `
			UPDATE player_status SET
				job_exp = $1, job_level = $2, job_kaisuu = $3,
				energy = GREATEST(0, energy - $4),
				nou_energy = GREATEST(0, nou_energy - $5),
				weight_g = GREATEST(0, weight_g - $6),
				mastered_jobs = CASE WHEN $7 THEN array_append(mastered_jobs, $8) ELSE mastered_jobs END,
				updated_at = now()
			WHERE player_id = $9`,
			newExp, newLevel, kaisuu, energySpend, nouSpend, econ.bodyCost*10,
			masteredNow, job, playerID); err != nil {
			return fmt.Errorf("apply work: %w", err)
		}
		// 就労クールタイムを設定。
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_facility_cooldowns (player_id, facility, next_available_at)
			 VALUES ($1, 'work', now() + make_interval(mins => $2))
			 ON CONFLICT (player_id, facility)
			 DO UPDATE SET next_available_at = now() + make_interval(mins => $2)`,
			playerID, s.settings.Get().WorkIntervalMin); err != nil {
			return fmt.Errorf("set work cooldown: %w", err)
		}
		// 結果画面用にサマリを控える。
		result = WorkResult{
			ExpGained: randed, NewLevel: newLevel, LeveledUp: leveledUp,
			ThisSalary: thisSalary, Pay: pay, PayEvery: payInterval, Bonus: bonus,
		}
		if masteredNow {
			result.Mastered = []string{job}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return p, &result, nil
}

// contains reports whether v is in s.
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// DoChangeJob changes the player's job at the job office (職業安定所). The target
// job's take-requirements are checked before switching; the job level resets.
func (s *Service) DoChangeJob(ctx context.Context, playerID int64, jobName, idempotencyKey string) (*player.Player, error) {
	if jobName == "学生" {
		return nil, &ConditionError{Message: "その仕事には就けません。"}
	}
	econ, err := s.loadJobEconomy(ctx, jobName)
	if err != nil {
		return nil, &ConditionError{Message: "そのような仕事はありません。"}
	}
	return s.runAction(ctx, playerID, "job_change", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		if ok, failed := econ.conds.Check(state); !ok {
			return &ConditionError{Message: conditionMessage(failed)}
		}
		var oldJob string
		var mastered []string
		var heightCm int
		if err := tx.QueryRow(ctx,
			`SELECT job, mastered_jobs, height_cm FROM player_status WHERE player_id = $1`,
			playerID).Scan(&oldJob, &mastered, &heightCm); err != nil {
			return fmt.Errorf("read job: %w", err)
		}
		// 身長条件(求職時)。
		if econ.heightMin != nil && heightCm < *econ.heightMin {
			return &ConditionError{Message: "身長がこの仕事の条件に足りません。"}
		}
		// 前提マスター職の条件。
		if econ.requireMaster != nil && *econ.requireMaster != "" && !contains(mastered, *econ.requireMaster) {
			return &ConditionError{Message: fmt.Sprintf("この仕事に就くには「%s」をマスターする必要があります。", *econ.requireMaster)}
		}
		// 転職: レベル・経験値・勤務回数をリセット。
		if _, err := tx.Exec(ctx,
			`UPDATE player_status SET job = $1, job_level = 0, job_exp = 0, job_kaisuu = 0, updated_at = now() WHERE player_id = $2`,
			jobName, playerID); err != nil {
			return fmt.Errorf("change job: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO status_history (player_id, field, old_value, new_value, reason)
			 VALUES ($1, 'job', $2, $3, 'job_change')`, playerID, oldJob, jobName); err != nil {
			return fmt.Errorf("insert history: %w", err)
		}
		return nil
	})
}

func (s *Service) currentJob(ctx context.Context, playerID int64) (string, error) {
	var job string
	err := s.pool.QueryRow(ctx,
		`SELECT job FROM player_status WHERE player_id = $1`, playerID).Scan(&job)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", player.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read job: %w", err)
	}
	return job, nil
}

// DoDeposit moves cash into the player's bank savings.
func (s *Service) DoDeposit(ctx context.Context, playerID, amount int64, idempotencyKey string) (*player.Player, error) {
	if amount <= 0 {
		return nil, &ConditionError{Message: "金額が正しくありません。"}
	}
	return s.runAction(ctx, playerID, "deposit", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		if state.Money < amount {
			return &ConditionError{Message: "お金が足りません。"}
		}
		// 冪等性は action_log(player_id, key) が保証するため台帳refは不要。
		return s.ledger.PostTx(ctx, tx, "deposit", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -amount},
			{Account: ledger.SavingsAccount(playerID), Delta: amount},
		})
	})
}

// DoWithdraw moves savings back into cash.
func (s *Service) DoWithdraw(ctx context.Context, playerID, amount int64, idempotencyKey string) (*player.Player, error) {
	if amount <= 0 {
		return nil, &ConditionError{Message: "金額が正しくありません。"}
	}
	return s.runAction(ctx, playerID, "withdraw", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var savings int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(delta), 0) FROM ledger_entry WHERE account = $1`,
			ledger.SavingsAccount(playerID)).Scan(&savings); err != nil {
			return fmt.Errorf("read savings: %w", err)
		}
		if savings < amount {
			return &ConditionError{Message: "貯金が足りません。"}
		}
		return s.ledger.PostTx(ctx, tx, "withdraw", "", []ledger.Entry{
			{Account: ledger.SavingsAccount(playerID), Delta: -amount},
			{Account: ledger.PlayerAccount(playerID), Delta: amount},
		})
	})
}

// transferLimit caps a single bank transfer; the excess is donated away (寄付),
// matching the legacy 振り込み. It is also the per-day cap per recipient.
const transferLimit = 1_000_000

// DoTransfer sends money from the player's savings to another member's savings,
// the recipient identified by display name. Amount above transferLimit is
// donated (removed from circulation). Students/day-laborers cannot send, self-
// transfer is rejected, and the daily total to one recipient is capped.
func (s *Service) DoTransfer(ctx context.Context, fromID int64, toName string, amount int64, idempotencyKey string) (*player.Player, error) {
	if amount <= 0 {
		return nil, &ConditionError{Message: "0円、マイナスの金額は振り込めません。"}
	}
	toName = strings.TrimSpace(toName)
	if toName == "" {
		return nil, &ConditionError{Message: "振込先の名前を入力してください。"}
	}
	return s.runAction(ctx, fromID, "transfer", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var job string
		if err := tx.QueryRow(ctx, `SELECT job FROM player_status WHERE player_id = $1`, fromID).Scan(&job); err != nil {
			return fmt.Errorf("read job: %w", err)
		}
		if job == "学生" || job == "日払いバイト" {
			return &ConditionError{Message: "学生・日払いバイト中は送金できません。"}
		}
		// 相手をメンバー名で逆引き。同名が複数いる場合は特定できないため断る。
		var cnt, toID int64
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*), COALESCE(MIN(id), 0) FROM players WHERE display_name = $1`,
			toName).Scan(&cnt, &toID); err != nil {
			return fmt.Errorf("lookup recipient: %w", err)
		}
		if cnt == 0 {
			return &ConditionError{Message: "その名前の参加者が見つかりません。"}
		}
		if cnt > 1 {
			return &ConditionError{Message: "同じ名前の参加者が複数います。振込できません。"}
		}
		if toID == fromID {
			return &ConditionError{Message: "自分宛に振り込むことはできません。"}
		}
		var savings int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(delta), 0) FROM ledger_entry WHERE account = $1`,
			ledger.SavingsAccount(fromID)).Scan(&savings); err != nil {
			return fmt.Errorf("read savings: %w", err)
		}
		if savings < amount {
			return &ConditionError{Message: "普通口座のお金が足りません。"}
		}
		// 上限超過分は寄付として自口座から引かれ、相手には届かない。
		sent, donation := amount, int64(0)
		if sent > transferLimit {
			donation = sent - transferLimit
			sent = transferLimit
		}
		// 同一相手への当日送金合計(相手に届いた額)が上限を超えないか。
		dayStart := gametime.Date(time.Now(), s.loc, s.dayBoundaryHour).Add(time.Duration(s.dayBoundaryHour) * time.Hour)
		var todaySum int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount), 0) FROM transfer_log WHERE from_id = $1 AND to_id = $2 AND created_at >= $3`,
			fromID, toID, dayStart).Scan(&todaySum); err != nil {
			return err
		}
		if todaySum+sent > transferLimit {
			return &ConditionError{Message: fmt.Sprintf("今日この相手への送金は合計%d円までです。", transferLimit)}
		}
		if err := s.ledger.PostTx(ctx, tx, "transfer", "", []ledger.Entry{
			{Account: ledger.SavingsAccount(fromID), Delta: -sent},
			{Account: ledger.SavingsAccount(toID), Delta: sent},
		}); err != nil {
			return fmt.Errorf("transfer: %w", err)
		}
		if donation > 0 {
			if err := s.ledger.PostTx(ctx, tx, "transfer_donation", "", []ledger.Entry{
				{Account: ledger.SavingsAccount(fromID), Delta: -donation},
				{Account: ledger.SystemAccount("donation"), Delta: donation},
			}); err != nil {
				return fmt.Errorf("donation: %w", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO transfer_log (from_id, to_id, amount) VALUES ($1, $2, $3)`,
			fromID, toID, sent); err != nil {
			return err
		}
		return nil
	})
}

// StatementEntry is one line of a savings-account passbook (入出金明細).
type StatementEntry struct {
	At      time.Time `json:"at"`
	Label   string    `json:"label"`
	Amount  int64     `json:"amount"`  // 符号付き(入金+/出金-)
	Balance int64     `json:"balance"` // その取引直後の口座残高
}

// bankStatementLimit caps how many recent passbook lines are returned.
const bankStatementLimit = 30

// BankStatement returns the player's recent savings-account movements, newest
// first, each labelled from the ledger reason (預け入れ/引き出し/利息/おさい銭…).
func (s *Service) BankStatement(ctx context.Context, playerID int64) ([]StatementEntry, error) {
	// running=そのentry時点の口座残高(累積和)。全履歴で累積してから最新N件を返す。
	rows, err := s.pool.Query(ctx,
		`SELECT created_at, reason, delta, running
		 FROM (
		   SELECT e.id, t.created_at, t.reason, e.delta,
		          SUM(e.delta) OVER (ORDER BY e.id) AS running
		   FROM ledger_entry e
		   JOIN ledger_tx t ON t.id = e.tx_id
		   WHERE e.account = $1
		 ) x
		 ORDER BY id DESC
		 LIMIT $2`, ledger.SavingsAccount(playerID), bankStatementLimit)
	if err != nil {
		return nil, fmt.Errorf("query statement: %w", err)
	}
	defer rows.Close()
	out := []StatementEntry{}
	for rows.Next() {
		var e StatementEntry
		var reason string
		if err := rows.Scan(&e.At, &reason, &e.Amount, &e.Balance); err != nil {
			return nil, fmt.Errorf("scan statement: %w", err)
		}
		e.Label = statementLabel(reason, e.Amount)
		out = append(out, e)
	}
	return out, rows.Err()
}

// statementLabel maps a ledger reason (and the entry sign) to a Japanese
// passbook label. Transfers share one reason for both parties, so the sign of
// the amount distinguishes send (out) from receive (in).
func statementLabel(reason string, amount int64) string {
	switch {
	case reason == "deposit":
		return "預け入れ"
	case reason == "withdraw":
		return "引き出し"
	case strings.HasPrefix(reason, "interest:"):
		return "利息"
	case reason == "offer":
		return "おさい銭"
	case reason == "transfer":
		if amount < 0 {
			return "振込(送金)"
		}
		return "振込(入金)"
	case reason == "transfer_donation":
		return "寄付"
	default:
		return "取引"
	}
}

// DoHospitalTreat cures the player's current disease at the hospital: it reads
// the disease index server-side, charges the fee for the resulting disease name,
// and resets the disease index to the healthy baseline (50). A healthy player
// may buy a preventive 「元気」 shot. Money is moved through the ledger to
// system:hospital_sink. (design 17.4)
func (s *Service) DoHospitalTreat(ctx context.Context, playerID int64, idempotencyKey string) (*player.Player, error) {
	return s.runAction(ctx, playerID, "hospital", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var index int
		if err := tx.QueryRow(ctx,
			`SELECT disease_index FROM player_status WHERE player_id = $1`, playerID).Scan(&index); err != nil {
			return fmt.Errorf("read disease index: %w", err)
		}
		// 治療費はサーバ側で病名から決定する(旧のクライアント任せを是正)。
		fee := condition.TreatmentFee(condition.DiseaseName(index))
		if state.Money < fee {
			return &ConditionError{Message: "お金が足りません。"}
		}
		if err := s.ledger.PostTx(ctx, tx, "hospital", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -fee},
			{Account: ledger.SystemAccount("hospital_sink"), Delta: fee},
		}); err != nil {
			return fmt.Errorf("pay treatment: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE player_status SET disease_index = 50, disease_evaled_at = now(), updated_at = now()
			 WHERE player_id = $1`, playerID); err != nil {
			return fmt.Errorf("cure disease: %w", err)
		}
		return nil
	})
}

// DoOnsen bathes at the hot spring. The legacy onsen does not restore a fixed
// amount: it accelerates the player's natural power recovery by the bath's
// multiplier over the elapsed time and clamps to the maximum (design 17 / legacy
// basic.cgi onsen). It charges the fee, advances the recovery timestamps so the
// worker does not double-recover, and has no cooldown. Bathing while both power
// bars are already full is rejected to avoid wasting money.
func (s *Service) DoOnsen(ctx context.Context, playerID, bathID int64, idempotencyKey string) (*player.Player, error) {
	price, multiplier, err := s.loadOnsenBath(ctx, bathID)
	if err != nil {
		return nil, err
	}
	return s.runAction(ctx, playerID, "onsen", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		if state.Money < price {
			return &ConditionError{Message: "お金が足りません。"}
		}
		energy := state.Params["energy"]
		nou := state.Params["nou_energy"]
		if energy.Value >= energy.Max && nou.Value >= nou.Max {
			return &ConditionError{Message: "すでに元気いっぱいです。"}
		}
		if err := s.ledger.PostTx(ctx, tx, "onsen", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -price},
			{Account: ledger.SystemAccount("onsen_sink"), Delta: price},
		}); err != nil {
			return fmt.Errorf("pay: %w", err)
		}
		// gain = floor(経過秒 / 回復秒 * 倍率) + 1、上限クランプ。回復時刻を現在に進める。
		cfg := s.settings.Get()
		energyRecSec, nouRecSec := cfg.EnergyRecoverySec, cfg.NouRecoverySec
		if energyRecSec <= 0 {
			energyRecSec = 60
		}
		if nouRecSec <= 0 {
			nouRecSec = 60
		}
		if _, err := tx.Exec(ctx, `
			UPDATE player_status SET
				energy = LEAST(energy_max,
					energy + FLOOR(EXTRACT(EPOCH FROM (now() - energy_recovered_at)) / $2 * $3)::int + 1),
				energy_recovered_at = now(),
				nou_energy = LEAST(nou_energy_max,
					nou_energy + FLOOR(EXTRACT(EPOCH FROM (now() - nou_recovered_at)) / $4 * $3)::int + 1),
				nou_recovered_at = now(),
				updated_at = now()
			WHERE player_id = $1`,
			playerID, energyRecSec, multiplier, nouRecSec); err != nil {
			return fmt.Errorf("onsen recover: %w", err)
		}
		return nil
	})
}

func (s *Service) loadOnsenBath(ctx context.Context, bathID int64) (price int64, multiplier int, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT price, power_multiplier FROM content_items
		 WHERE id = $1 AND enabled AND facility = 'onsen'`, bathID).Scan(&price, &multiplier)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, ErrItemNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("load onsen bath: %w", err)
	}
	if multiplier <= 0 {
		multiplier = 1
	}
	return price, multiplier, nil
}

// DoBuy purchases `sets` sets of a department-store item (facility=”). It
// enforces the shop stock, the per-item ownership cap (max_sets), charges the
// total price out of circulation and adds the durability to the inventory.
// remaining_uses accumulates durability×sets ('use'なら残回数, 'day'なら残日数)。
func (s *Service) DoBuy(ctx context.Context, playerID, itemID int64, sets int, idempotencyKey string) (*player.Player, error) {
	if sets <= 0 {
		sets = 1
	}
	price, durability, maxSets, err := s.loadItemBuy(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if sets > maxSets {
		return nil, &ConditionError{Message: fmt.Sprintf("一度に購入できるのは%dセットまでです。", maxSets)}
	}
	total := price * int64(sets)
	add := durability * sets
	return s.runAction(ctx, playerID, "buy", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		if state.Money < total {
			return &ConditionError{Message: "お金が足りません。"}
		}
		// 所持上限: 追加後の残量が max_sets×durability を超えないこと。
		var current int
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(remaining_uses, 0) FROM player_items WHERE player_id = $1 AND item_id = $2`,
			playerID, itemID).Scan(&current); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read remaining_uses: %w", err)
		}
		if current+add > maxSets*durability {
			return &ConditionError{Message: fmt.Sprintf("これ以上は持てません(最大%dセット)。", maxSets)}
		}
		// 在庫を減らす(デパート品は facility='')。
		if err := s.consumeStock(ctx, tx, "", itemID, sets); err != nil {
			return err
		}
		if err := s.ledger.PostTx(ctx, tx, "buy", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -total},
			{Account: ledger.SystemAccount("shop_sink"), Delta: total},
		}); err != nil {
			return fmt.Errorf("pay: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_items (player_id, item_id, quantity, remaining_uses)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (player_id, item_id)
			 DO UPDATE SET quantity = player_items.quantity + $3,
			               remaining_uses = player_items.remaining_uses + $4,
			               updated_at = now()`,
			playerID, itemID, sets, add); err != nil {
			return fmt.Errorf("grant item: %w", err)
		}
		return nil
	})
}

// DoUse consumes one unit of a held item and applies its effect. Using an item
// is blocked while the item's use interval (クールタイム) has not elapsed.
// NOTE: レガシーと異なり、売却はクールタイム中でも可能とする(売却実装時は間隔チェック不要)。
func (s *Service) DoUse(ctx context.Context, playerID, itemID int64, idempotencyKey string) (*player.Player, error) {
	eff, intervalMin, fillsSatiety, durUnit, err := s.loadItemUse(ctx, itemID)
	if err != nil {
		return nil, err
	}
	return s.runAction(ctx, playerID, "use", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		if !s.settings.Get().DebugNoCooldown && fillsSatiety && state.Params["satiety"].Value >= satietyMax {
			return &ConditionError{Message: "お腹がいっぱいです。今は食べられません。"}
		}
		if !s.settings.Get().DebugNoCooldown && intervalMin > 0 {
			var lastUsed *time.Time
			err := tx.QueryRow(ctx,
				`SELECT last_used_at FROM player_items WHERE player_id = $1 AND item_id = $2`,
				playerID, itemID).Scan(&lastUsed)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("read last_used_at: %w", err)
			}
			if lastUsed != nil {
				next := lastUsed.Add(time.Duration(intervalMin) * time.Minute)
				if time.Now().Before(next) {
					remain := int(time.Until(next).Minutes()) + 1
					return &ConditionError{Message: fmt.Sprintf("まだ使用できません。あと約%d分お待ちください。", remain)}
				}
			}
		}
		// 'use'単位は使用ごとに残回数-1。'day'単位は日数経過で減るため使用では減らさない。
		var affected int64
		if durUnit == "day" {
			tag, err := tx.Exec(ctx,
				`UPDATE player_items SET last_used_at = now(), updated_at = now()
				 WHERE player_id = $1 AND item_id = $2 AND remaining_uses > 0`, playerID, itemID)
			if err != nil {
				return fmt.Errorf("use item: %w", err)
			}
			affected = tag.RowsAffected()
		} else {
			tag, err := tx.Exec(ctx,
				`UPDATE player_items pi
				 SET remaining_uses = pi.remaining_uses - 1,
				     quantity = CEIL((pi.remaining_uses - 1)::numeric / ci.durability),
				     last_used_at = now(), updated_at = now()
				 FROM content_items ci
				 WHERE pi.player_id = $1 AND pi.item_id = $2 AND pi.item_id = ci.id AND pi.remaining_uses > 0`,
				playerID, itemID)
			if err != nil {
				return fmt.Errorf("consume item: %w", err)
			}
			affected = tag.RowsAffected()
		}
		if affected == 0 {
			return &ConditionError{Message: "そのアイテムを持っていません。"}
		}
		if err := s.applyEffect(ctx, tx, playerID, "use", eff, state); err != nil {
			return err
		}
		// アイテムにカロリーがあれば(高級弁当など)体重が増える。
		if err := addWeightFromItem(ctx, tx, playerID, itemID); err != nil {
			return err
		}
		// 残量が尽きたら持ち物から削除する。
		if _, err := tx.Exec(ctx,
			`DELETE FROM player_items WHERE player_id = $1 AND item_id = $2 AND remaining_uses <= 0`,
			playerID, itemID); err != nil {
			return fmt.Errorf("drop empty item: %w", err)
		}
		if fillsSatiety {
			return fillSatiety(ctx, tx, playerID)
		}
		return nil
	})
}

// DoEat eats a food from the 食堂 menu: it charges the price and applies the
// food's param effect. Eating fills 満腹度 to full and is blocked while already
// full (満腹). 食事の間隔は満腹→時間経過で下がることで自然に生じる。
func (s *Service) DoEat(ctx context.Context, playerID, foodID int64, idempotencyKey string) (*player.Player, error) {
	price, eff, err := s.loadFood(ctx, foodID)
	if err != nil {
		return nil, err
	}
	return s.runAction(ctx, playerID, "eat", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		if !s.settings.Get().DebugNoCooldown && state.Params["satiety"].Value >= satietyMax {
			return &ConditionError{Message: "お腹がいっぱいです。今は食べられません。"}
		}
		if state.Money < price {
			return &ConditionError{Message: "お金が足りません。"}
		}
		// 食堂メニューの在庫を1食ぶん減らす(売り切れなら食事不可)。
		if err := s.consumeStock(ctx, tx, "syokudou", foodID, 1); err != nil {
			return err
		}
		if err := s.ledger.PostTx(ctx, tx, "eat", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -price},
			{Account: ledger.SystemAccount("shop_sink"), Delta: price},
		}); err != nil {
			return fmt.Errorf("pay: %w", err)
		}
		if err := s.applyEffect(ctx, tx, playerID, "eat", eff, state); err != nil {
			return err
		}
		// 食事のカロリーぶん体重が増える。
		if err := addWeightFromItem(ctx, tx, playerID, foodID); err != nil {
			return err
		}
		return fillSatiety(ctx, tx, playerID)
	})
}

func (s *Service) loadFood(ctx context.Context, foodID int64) (int64, effects.Effect, error) {
	var (
		price   int64
		effJSON []byte
	)
	cond, extra := s.dailyMenuCond("syokudou", 2)
	args := append([]any{foodID}, extra...)
	err := s.pool.QueryRow(ctx,
		`SELECT price, effect FROM content_items
		 WHERE id = $1 AND enabled AND facility = 'syokudou'`+cond, args...).Scan(&price, &effJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, effects.Effect{}, ErrItemNotFound
	}
	if err != nil {
		return 0, effects.Effect{}, fmt.Errorf("load food: %w", err)
	}
	eff, err := effects.ParseEffect(effJSON)
	if err != nil {
		return 0, effects.Effect{}, fmt.Errorf("food %d effect: %w", foodID, err)
	}
	return price, eff, nil
}

// DoFacilityAction runs a generic facility menu action (e.g. ジムのトレーニング):
// it enforces a per-(player,facility) cooldown, checks power, charges the price,
// applies the effect (param上昇 + power消費), and advances the cooldown by the
// menu item's interval.
func (s *Service) DoFacilityAction(ctx context.Context, playerID int64, facility string, menuID int64, idempotencyKey string) (*player.Player, error) {
	price, eff, intervalMin, err := s.loadFacilityMenuItem(ctx, facility, menuID)
	if err != nil {
		return nil, err
	}
	return s.runAction(ctx, playerID, facility, idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var nextAt *time.Time
		if err := tx.QueryRow(ctx,
			`SELECT next_available_at FROM player_facility_cooldowns WHERE player_id = $1 AND facility = $2`,
			playerID, facility).Scan(&nextAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read cooldown: %w", err)
		}
		if !s.settings.Get().DebugNoCooldown && nextAt != nil && time.Now().Before(*nextAt) {
			remain := int(time.Until(*nextAt).Minutes()) + 1
			return &ConditionError{Message: fmt.Sprintf("まだ利用できません。あと約%d分お待ちください。", remain)}
		}
		if param, short := eff.InsufficientParam(state); short {
			return &ConditionError{Message: paramShortMessage(param)}
		}
		if state.Money < price {
			return &ConditionError{Message: "お金が足りません。"}
		}
		if err := s.ledger.PostTx(ctx, tx, facility, "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -price},
			{Account: ledger.SystemAccount(facility + "_sink"), Delta: price},
		}); err != nil {
			return fmt.Errorf("pay: %w", err)
		}
		if err := s.applyEffect(ctx, tx, playerID, facility, eff, state); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_facility_cooldowns (player_id, facility, next_available_at)
			 VALUES ($1, $2, now() + make_interval(mins => $3))
			 ON CONFLICT (player_id, facility)
			 DO UPDATE SET next_available_at = now() + make_interval(mins => $3)`,
			playerID, facility, intervalMin); err != nil {
			return fmt.Errorf("set cooldown: %w", err)
		}
		return nil
	})
}

func (s *Service) loadFacilityMenuItem(ctx context.Context, facility string, menuID int64) (int64, effects.Effect, int, error) {
	var (
		price       int64
		effJSON     []byte
		intervalMin int
	)
	err := s.pool.QueryRow(ctx,
		`SELECT price, effect, use_interval_min FROM content_items
		 WHERE id = $1 AND enabled AND facility = $2`, menuID, facility).
		Scan(&price, &effJSON, &intervalMin)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, effects.Effect{}, 0, ErrItemNotFound
	}
	if err != nil {
		return 0, effects.Effect{}, 0, fmt.Errorf("load facility item: %w", err)
	}
	eff, err := effects.ParseEffect(effJSON)
	if err != nil {
		return 0, effects.Effect{}, 0, fmt.Errorf("facility item %d effect: %w", menuID, err)
	}
	return price, eff, intervalMin, nil
}

// nextDayBoundary returns the wall-clock instant of the next game-day rollover
// (used for once-per-game-day facilities like the school). The game day rolls
// over at dayBoundaryHour local time.
func (s *Service) nextDayBoundary(now time.Time) time.Time {
	start := gametime.Date(now, s.loc, s.dayBoundaryHour) // 当日ゲーム日の 00:00(loc)
	return start.AddDate(0, 0, 1).Add(time.Duration(s.dayBoundaryHour) * time.Hour)
}

// DoSchool attends a school course: raises brain parameters once per game day,
// consuming money and brain power (nou_energy). Legacy: command.pl sub do_school.
// The daily limit reuses player_facility_cooldowns by setting next_available_at
// to the next game-day boundary.
func (s *Service) DoSchool(ctx context.Context, playerID, courseID int64, idempotencyKey string) (*player.Player, error) {
	price, eff, _, err := s.loadFacilityMenuItem(ctx, "school", courseID)
	if err != nil {
		return nil, err
	}
	return s.runAction(ctx, playerID, "school", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		// 1日1回(ゲーム日境界でリセット)。前回受講時に次境界をcooldownへ入れているため、
		// それが未来なら本日分は受講済み。
		var nextAt *time.Time
		if err := tx.QueryRow(ctx,
			`SELECT next_available_at FROM player_facility_cooldowns WHERE player_id = $1 AND facility = 'school'`,
			playerID).Scan(&nextAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read school cooldown: %w", err)
		}
		if !s.settings.Get().DebugNoCooldown && nextAt != nil && time.Now().Before(*nextAt) {
			return &ConditionError{Message: "今日の受講は終了しています。受講できるのは1日1回です。"}
		}
		if param, short := eff.InsufficientParam(state); short {
			return &ConditionError{Message: paramShortMessage(param)}
		}
		if state.Money < price {
			return &ConditionError{Message: "お金が足りません。"}
		}
		if err := s.ledger.PostTx(ctx, tx, "school", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -price},
			{Account: ledger.SystemAccount("school_sink"), Delta: price},
		}); err != nil {
			return fmt.Errorf("pay school: %w", err)
		}
		if err := s.applyEffect(ctx, tx, playerID, "school", eff, state); err != nil {
			return err
		}
		next := s.nextDayBoundary(time.Now())
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_facility_cooldowns (player_id, facility, next_available_at)
			 VALUES ($1, 'school', $2)
			 ON CONFLICT (player_id, facility) DO UPDATE SET next_available_at = $2`,
			playerID, next); err != nil {
			return fmt.Errorf("set school cooldown: %w", err)
		}
		return nil
	})
}

// ErrBadStock is returned for an unknown symbol or non-positive quantity.
var ErrBadStock = errors.New("invalid stock symbol or quantity")

// DoBuyStock buys qty shares of a symbol at the current shared price. Money moves
// through the ledger; there are no fees (legacy). Per-symbol cap is 200 shares.
// Note: legacy also gated on bank>=cost (a likely bug); we gate on money only.
func (s *Service) DoBuyStock(ctx context.Context, playerID int64, symbol string, qty int, idempotencyKey string) (*player.Player, error) {
	if !stock.ValidSymbol(symbol) || qty <= 0 {
		return nil, ErrBadStock
	}
	return s.runAction(ctx, playerID, "stock_buy", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var price int64
		if err := tx.QueryRow(ctx, `SELECT price FROM stock_price WHERE symbol = $1`, symbol).Scan(&price); err != nil {
			return fmt.Errorf("read price: %w", err)
		}
		var shares int
		var costTotal int64
		err := tx.QueryRow(ctx,
			`SELECT shares, cost_total FROM player_stock WHERE player_id = $1 AND symbol = $2`,
			playerID, symbol).Scan(&shares, &costTotal)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read holding: %w", err)
		}
		if shares+qty > stock.MaxShares {
			return &ConditionError{Message: fmt.Sprintf("この銘柄の保有上限(%d株)を超えています。", stock.MaxShares)}
		}
		cost := price * int64(qty)
		if state.Money < cost {
			return &ConditionError{Message: "持ち金が足りません。"}
		}
		// refは空。冪等性はrunActionのidempotencyKeyが担う(refに銘柄名を使うと
		// 別取引が同一refで衝突しno-opになるため)。
		if err := s.ledger.PostTx(ctx, tx, "stock_buy", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -cost},
			{Account: ledger.SystemAccount("stock_market"), Delta: cost},
		}); err != nil {
			return fmt.Errorf("pay: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_stock (player_id, symbol, shares, cost_total, inv_total, ret_total)
			 VALUES ($1, $2, $3, $4, $4, 0)
			 ON CONFLICT (player_id, symbol) DO UPDATE SET
			   shares = player_stock.shares + $3,
			   cost_total = player_stock.cost_total + $4,
			   inv_total = player_stock.inv_total + $4`,
			playerID, symbol, qty, cost); err != nil {
			return fmt.Errorf("upsert holding: %w", err)
		}
		return s.logStockTrade(ctx, tx, playerID,
			fmt.Sprintf("%s株を%d株購入(@%d円 計%d円)", symbol, qty, price, cost))
	})
}

// DoSellStock sells qty shares of a symbol at the current price. Cost basis is
// reduced proportionally; when the position hits zero the cost basis clears.
func (s *Service) DoSellStock(ctx context.Context, playerID int64, symbol string, qty int, idempotencyKey string) (*player.Player, error) {
	if !stock.ValidSymbol(symbol) || qty <= 0 {
		return nil, ErrBadStock
	}
	return s.runAction(ctx, playerID, "stock_sell", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var price int64
		if err := tx.QueryRow(ctx, `SELECT price FROM stock_price WHERE symbol = $1`, symbol).Scan(&price); err != nil {
			return fmt.Errorf("read price: %w", err)
		}
		var shares int
		var costTotal int64
		err := tx.QueryRow(ctx,
			`SELECT shares, cost_total FROM player_stock WHERE player_id = $1 AND symbol = $2`,
			playerID, symbol).Scan(&shares, &costTotal)
		if errors.Is(err, pgx.ErrNoRows) || shares < qty {
			return &ConditionError{Message: "保有株数が足りません。"}
		}
		if err != nil {
			return fmt.Errorf("read holding: %w", err)
		}
		proceeds := price * int64(qty)
		costReduce := costTotal * int64(qty) / int64(shares) // 平均原価ぶんを比例で減らす
		newShares := shares - qty
		newCost := costTotal - costReduce
		if newShares == 0 {
			newCost = 0
		}
		if err := s.ledger.PostTx(ctx, tx, "stock_sell", "", []ledger.Entry{
			{Account: ledger.SystemAccount("stock_market"), Delta: -proceeds},
			{Account: ledger.PlayerAccount(playerID), Delta: proceeds},
		}); err != nil {
			return fmt.Errorf("payout: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE player_stock SET shares = $3, cost_total = $4, ret_total = ret_total + $5
			 WHERE player_id = $1 AND symbol = $2`,
			playerID, symbol, newShares, newCost, proceeds); err != nil {
			return fmt.Errorf("update holding: %w", err)
		}
		return s.logStockTrade(ctx, tx, playerID,
			fmt.Sprintf("%s株を%d株売却(@%d円 計%d円)", symbol, qty, price, proceeds))
	})
}

// DoSettleStock liquidates all holdings at current prices, pays the player, and
// clears their positions (legacy command=del 精算).
func (s *Service) DoSettleStock(ctx context.Context, playerID int64, idempotencyKey string) (*player.Player, error) {
	return s.runAction(ctx, playerID, "stock_settle", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var total int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(sp.price * ps.shares), 0)
			 FROM player_stock ps JOIN stock_price sp ON sp.symbol = ps.symbol
			 WHERE ps.player_id = $1 AND ps.shares > 0`, playerID).Scan(&total); err != nil {
			return fmt.Errorf("sum holdings: %w", err)
		}
		if total > 0 {
			if err := s.ledger.PostTx(ctx, tx, "stock_settle", "", []ledger.Entry{
				{Account: ledger.SystemAccount("stock_market"), Delta: -total},
				{Account: ledger.PlayerAccount(playerID), Delta: total},
			}); err != nil {
				return fmt.Errorf("payout: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM player_stock WHERE player_id = $1`, playerID); err != nil {
			return fmt.Errorf("clear holdings: %w", err)
		}
		return s.logStockTrade(ctx, tx, playerID, fmt.Sprintf("%d円の精算を行いました。", total))
	})
}

// logStockTrade appends a trade message and trims the player's history.
func (s *Service) logStockTrade(ctx context.Context, tx pgx.Tx, playerID int64, msg string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO stock_trade_log (player_id, message) VALUES ($1, $2)`, playerID, msg); err != nil {
		return fmt.Errorf("log trade: %w", err)
	}
	_, err := tx.Exec(ctx,
		`DELETE FROM stock_trade_log WHERE player_id = $1 AND id NOT IN (
		   SELECT id FROM stock_trade_log WHERE player_id = $1 ORDER BY id DESC LIMIT $2)`,
		playerID, stock.TradeLogKeep)
	return err
}

// KeibaBetResult is the outcome of a horse-race bet, returned alongside the
// updated player.
type KeibaBetResult struct {
	WinnerIndex int           `json:"winner_index"`
	WinnerName  string        `json:"winner_name"`
	WinnerOdds  int           `json:"winner_odds"`
	Payout      int64         `json:"payout"`   // 払戻金
	Invested    int64         `json:"invested"` // 総購入額
	Steps       [][]int       `json:"steps"`    // 各馬の歩幅列(アニメ用)
	Lineup      []keiba.Horse `json:"lineup"`
}

// ErrBadBet is returned for a malformed or stale bet.
var ErrBadBet = errors.New("invalid or stale keiba bet")

// DoKeibaBet places bets on the player's pending race, runs the server-side
// simulation, pays out winnings and updates the ranking. bets holds tickets per
// horse index (length keiba.Runners). Legacy: keiba.cgi command=start.
func (s *Service) DoKeibaBet(ctx context.Context, playerID, raceID int64, bets []int, idempotencyKey string) (*player.Player, *KeibaBetResult, error) {
	if len(bets) != keiba.Runners {
		return nil, nil, ErrBadBet
	}
	total, horsesBet := 0, 0
	for _, t := range bets {
		if t < 0 {
			return nil, nil, ErrBadBet
		}
		total += t
		if t > 0 {
			horsesBet++
		}
	}

	var result *KeibaBetResult
	p, err := s.runAction(ctx, playerID, "keiba_bet", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		storedRace, lineup, err := keiba.LoadRace(ctx, tx, playerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "レース情報がありません。画面を開き直してください。"}
		}
		if err != nil {
			return fmt.Errorf("load race: %w", err)
		}
		if storedRace != raceID {
			return &ConditionError{Message: "レース情報が古くなっています。画面を開き直してください。"}
		}
		if total == 0 {
			return &ConditionError{Message: "馬券が購入されていません。"}
		}
		if horsesBet > keiba.MaxHorsesBet {
			return &ConditionError{Message: fmt.Sprintf("%d頭までしか賭けられません。", keiba.MaxHorsesBet)}
		}
		if total > keiba.MaxTickets {
			return &ConditionError{Message: fmt.Sprintf("一度に購入できる馬券は%d枚までです。", keiba.MaxTickets)}
		}
		cost := int64(total) * keiba.TicketPrice
		if state.Money < cost {
			return &ConditionError{Message: "お金が足りません。"}
		}
		if err := s.ledger.PostTx(ctx, tx, "keiba_bet", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -cost},
			{Account: ledger.SystemAccount("keiba"), Delta: cost},
		}); err != nil {
			return fmt.Errorf("pay: %w", err)
		}

		race := keiba.Simulate(lineup, s.rng)
		win := race.WinnerIndex
		var payout int64
		if bets[win] > 0 {
			payout = int64(lineup[win].Odds) * int64(bets[win]) * keiba.TicketPrice
		}
		if payout > 0 {
			if err := s.ledger.PostTx(ctx, tx, "keiba_payout", "", []ledger.Entry{
				{Account: ledger.SystemAccount("keiba"), Delta: -payout},
				{Account: ledger.PlayerAccount(playerID), Delta: payout},
			}); err != nil {
				return fmt.Errorf("payout: %w", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO keiba_ranking (player_id, invested, won, last_played)
			 VALUES ($1, $2, $3, now())
			 ON CONFLICT (player_id) DO UPDATE SET
			   invested = keiba_ranking.invested + $2,
			   won = keiba_ranking.won + $3,
			   last_played = now()`,
			playerID, cost, payout); err != nil {
			return fmt.Errorf("rank: %w", err)
		}
		result = &KeibaBetResult{
			WinnerIndex: win,
			WinnerName:  lineup[win].Name,
			WinnerOdds:  lineup[win].Odds,
			Payout:      payout,
			Invested:    cost,
			Steps:       race.Steps,
			Lineup:      lineup,
		}
		return nil
	})
	return p, result, err
}

// GreetResult is the outcome of posting a greeting (reward/janken/fine).
type GreetResult struct {
	Reward   int64  `json:"reward"`    // 得た報酬(宣伝は負、管理人は0)
	Jackpot  bool   `json:"jackpot"`   // 大当たり(base==7)
	Janken   string `json:"janken"`    // 勝ち/負け/あいこ/""
	JankenPC string `json:"janken_pc"` // PCの手 グー/チョキ/パー/""
	Fine     bool   `json:"fine"`      // NG罰金
}

// ErrBadGreet is returned for invalid greeting input.
var ErrBadGreet = errors.New("invalid greeting")

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var jankenHands = map[string]int{"gu": 0, "choki": 1, "pa": 2}
var jankenNames = []string{"グー", "チョキ", "パー"}

// DoGreet posts a greeting to the town board and applies its game effects:
// a random reward for normal posts (jackpot on base==7, doubled/halved by a
// rock-paper-scissors result), a 20000 charge for 宣伝, and a 30000 fine for NG
// words. Legacy: event.pl sub aisatu (command=aisatu_do).
func (s *Service) DoGreet(ctx context.Context, playerID int64, category, body, color, janken, idempotencyKey string) (*player.Player, *GreetResult, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > greeting.MaxBodyRunes {
		return nil, nil, ErrBadGreet
	}
	if color == "" {
		color = "#333333"
	}
	if !colorRe.MatchString(color) {
		return nil, nil, ErrBadGreet
	}
	// NGワードは本文をマスクし罰金フラグを立てる。
	fine := false
	for _, ng := range greeting.NGWords {
		if strings.Contains(body, ng) {
			body = strings.ReplaceAll(body, ng, "NG")
			fine = true
		}
	}

	var result *GreetResult
	p, err := s.runAction(ctx, playerID, "greeting", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var name string
		if err := tx.QueryRow(ctx, `SELECT display_name FROM players WHERE id = $1`, playerID).Scan(&name); err != nil {
			return fmt.Errorf("player: %w", err)
		}
		res := &GreetResult{Fine: fine}

		switch category {
		case greeting.CatAdmin:
			var isAdmin bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM player_roles WHERE player_id = $1 AND role = 'admin')`, playerID).Scan(&isAdmin); err != nil {
				return fmt.Errorf("role: %w", err)
			}
			if !isAdmin {
				return &ConditionError{Message: "管理人枠は管理者のみ投稿できます。"}
			}
			color = "#ff0000"
		case greeting.CatAd:
			if state.Money < greeting.AdRevCost {
				return &ConditionError{Message: "宣伝費の2万円が足りません。"}
			}
			if err := s.ledger.PostTx(ctx, tx, "greeting_ad", "", []ledger.Entry{
				{Account: ledger.PlayerAccount(playerID), Delta: -greeting.AdRevCost},
				{Account: ledger.SystemAccount("chat"), Delta: greeting.AdRevCost},
			}); err != nil {
				return fmt.Errorf("ad charge: %w", err)
			}
			res.Reward = -greeting.AdRevCost
			color = "#0000ff"
		default:
			// ジャンケン(手が指定されていれば)で報酬倍率を決める。
			mult := 1.0
			if janken != "" && janken != "none" {
				pc := s.rng.IntN(3)
				res.JankenPC = jankenNames[pc]
				me := jankenHands[janken]
				if janken == "omakase" {
					me = s.rng.IntN(3)
				}
				switch {
				case me == pc:
					res.Janken = "あいこ"
				case (me+1)%3 == pc:
					res.Janken = "勝ち"
					mult = 2.0
				default:
					res.Janken = "負け"
					mult = 0.5
				}
			}
			base := int64(s.rng.IntN(10) + 1)
			var gain int64
			if base == 7 {
				gain = base + int64(s.rng.IntN(10000)+5000)
				res.Jackpot = true
			} else {
				gain = base + int64(s.rng.IntN(2000)+1000)
			}
			gain = int64(float64(gain) * mult)
			if gain > 0 {
				if err := s.ledger.PostTx(ctx, tx, "greeting_reward", "", []ledger.Entry{
					{Account: ledger.SystemAccount("chat"), Delta: -gain},
					{Account: ledger.PlayerAccount(playerID), Delta: gain},
				}); err != nil {
					return fmt.Errorf("reward: %w", err)
				}
			}
			res.Reward = gain
		}

		if fine {
			if err := s.ledger.PostTx(ctx, tx, "greeting_fine", "", []ledger.Entry{
				{Account: ledger.PlayerAccount(playerID), Delta: -greeting.NGFine},
				{Account: ledger.SystemAccount("chat"), Delta: greeting.NGFine},
			}); err != nil {
				return fmt.Errorf("fine: %w", err)
			}
			res.Reward -= greeting.NGFine
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO greetings (user_id, user_name, category, body, color, janken)
			 VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))`,
			playerID, name, category, body, color, res.Janken); err != nil {
			return fmt.Errorf("insert greeting: %w", err)
		}
		// 最新Keep件に切り詰める。
		if _, err := tx.Exec(ctx,
			`DELETE FROM greetings WHERE id NOT IN (SELECT id FROM greetings ORDER BY id DESC LIMIT $1)`,
			greeting.Keep); err != nil {
			return fmt.Errorf("trim greetings: %w", err)
		}
		result = res
		return nil
	})
	return p, result, err
}

// eventRollInterval rate-limits random-event rolls per player (anti-spam).
const eventRollInterval = 15 * time.Second

// DoEventRoll rolls for a random event (legacy event.pl event_happen), applying
// its money/parameter/disease/weight effects. Rolls are rate-limited to one per
// eventRollInterval per player. A nil result means no event fired (rate-limited
// or the 11/12 no-fire outcome).
func (s *Service) DoEventRoll(ctx context.Context, playerID int64, idempotencyKey string) (*player.Player, *event.Outcome, error) {
	var result *event.Outcome
	p, err := s.runAction(ctx, playerID, "event_roll", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		if !s.settings.Get().DebugNoCooldown {
			var nextAt *time.Time
			if err := tx.QueryRow(ctx,
				`SELECT next_available_at FROM player_facility_cooldowns WHERE player_id = $1 AND facility = 'event_roll'`,
				playerID).Scan(&nextAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("read event cooldown: %w", err)
			}
			if nextAt != nil && time.Now().Before(*nextAt) {
				return nil // レート制限中: 抽選しない
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO player_facility_cooldowns (player_id, facility, next_available_at)
				 VALUES ($1, 'event_roll', now() + make_interval(secs => $2))
				 ON CONFLICT (player_id, facility) DO UPDATE SET next_available_at = now() + make_interval(secs => $2)`,
				playerID, eventRollInterval.Seconds()); err != nil {
				return fmt.Errorf("set event cooldown: %w", err)
			}
		}

		occurred, o := event.Roll(s.rng, state.Money, state.Params["speed"].Value)
		if !occurred {
			return nil
		}
		if err := s.applyEventOutcome(ctx, tx, playerID, state, o); err != nil {
			return err
		}
		result = &o
		return nil
	})
	return p, result, err
}

// applyEventOutcome persists one event's effects: money via the ledger, params
// via the effect engine, disease/weight directly, and the special charity /
// confiscate side effects.
func (s *Service) applyEventOutcome(ctx context.Context, tx pgx.Tx, playerID int64, state effects.State, o event.Outcome) error {
	switch o.Special {
	case "charity":
		// ログイン中に関係なく、自分以外のランダムなプレイヤーの貯金へ1万円贈る。
		var recipient int64
		err := tx.QueryRow(ctx,
			`SELECT id FROM players WHERE id <> $1 AND deleted_at IS NULL ORDER BY random() LIMIT 1`, playerID).Scan(&recipient)
		if errors.Is(err, pgx.ErrNoRows) {
			o.MoneyDelta = 0 // 相手がいなければ何もしない
		} else if err != nil {
			return fmt.Errorf("charity recipient: %w", err)
		} else if state.Money >= 10000 {
			if err := s.ledger.PostTx(ctx, tx, "event_charity", "", []ledger.Entry{
				{Account: ledger.PlayerAccount(playerID), Delta: -10000},
				{Account: ledger.SavingsAccount(recipient), Delta: 10000},
			}); err != nil {
				return fmt.Errorf("charity: %w", err)
			}
		} else {
			o.MoneyDelta = 0 // 持ち金不足なら贈らない
		}
	case "confiscate":
		// 所持品からランダムに1個(数量-1)。無ければ何もしない。
		if _, err := tx.Exec(ctx,
			`UPDATE player_items SET quantity = quantity - 1
			 WHERE (player_id, item_id) = (
			   SELECT player_id, item_id FROM player_items
			   WHERE player_id = $1 AND quantity > 0 ORDER BY random() LIMIT 1)`, playerID); err != nil {
			return fmt.Errorf("confiscate: %w", err)
		}
	default:
		if o.MoneyDelta != 0 {
			if err := s.ledger.PostTx(ctx, tx, "event_money", "", []ledger.Entry{
				{Account: ledger.PlayerAccount(playerID), Delta: o.MoneyDelta},
				{Account: ledger.SystemAccount("event"), Delta: -o.MoneyDelta},
			}); err != nil {
				return fmt.Errorf("event money: %w", err)
			}
		}
	}

	if len(o.Params) > 0 {
		eff := effects.Effect{Ops: make([]effects.Op, 0, len(o.Params))}
		for k, v := range o.Params {
			eff.Ops = append(eff.Ops, effects.Op{Kind: "add_param", Param: k, Amount: int64(v)})
		}
		if err := s.applyEffect(ctx, tx, playerID, "event", eff, state); err != nil {
			return err
		}
	}
	if o.DiseaseDelta != 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE player_status SET disease_index = GREATEST(-200, disease_index + $2) WHERE player_id = $1`,
			playerID, o.DiseaseDelta); err != nil {
			return fmt.Errorf("event disease: %w", err)
		}
	}
	if o.WeightG != 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE player_status SET weight_g = GREATEST(1000, weight_g + $2) WHERE player_id = $1`,
			playerID, o.WeightG); err != nil {
			return fmt.Errorf("event weight: %w", err)
		}
	}
	return nil
}

const (
	shopSetupFee             = 500000 // 商店の開設費(簡略化した建築費)
	offerDailyPairLimit      = 20000  // 同一相手へのさい銭 上限/日
	offerDailyRecipientLimit = 100000 // 相手が受け取れるさい銭 合計上限/日
)

// DoOpenShop opens a shop for the player, charging the setup fee. Legacy: 建設会社
// を簡略化(建物・内装・地価は省略し固定の開設費)。
func (s *Service) DoOpenShop(ctx context.Context, playerID int64, name, idempotencyKey string) (*player.Player, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "商店"
	}
	return s.runAction(ctx, playerID, "shop_open", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shops WHERE owner_id = $1)`, playerID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return &ConditionError{Message: "すでに商店を開いています。"}
		}
		if state.Money < shopSetupFee {
			return &ConditionError{Message: fmt.Sprintf("開設費(%d円)が足りません。", shopSetupFee)}
		}
		if err := s.ledger.PostTx(ctx, tx, "shop_open", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -shopSetupFee},
			{Account: ledger.SystemAccount("shop_setup"), Delta: shopSetupFee},
		}); err != nil {
			return fmt.Errorf("fee: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO shops (owner_id, name) VALUES ($1, $2)`, playerID, name); err != nil {
			return fmt.Errorf("open shop: %w", err)
		}
		return nil
	})
}

// DoBuyFromShop buys qty of a listed item from another player's shop. Payment
// goes to the owner's savings and the item transfers to the buyer. Legacy:
// buy_syouhin(個人店)— 売上が家主に入る中核。
func (s *Service) DoBuyFromShop(ctx context.Context, buyerID, ownerID, itemID int64, qty int, idempotencyKey string) (*player.Player, error) {
	if qty <= 0 {
		qty = 1
	}
	if buyerID == ownerID {
		return nil, &ConditionError{Message: "自分の店では買えません。"}
	}
	return s.runAction(ctx, buyerID, "shop_buy", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var price int64
		var stock int
		err := tx.QueryRow(ctx, `SELECT price, stock FROM shop_listings WHERE owner_id = $1 AND item_id = $2`, ownerID, itemID).Scan(&price, &stock)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その商品はありません。"}
		}
		if err != nil {
			return fmt.Errorf("listing: %w", err)
		}
		if stock < qty {
			return &ConditionError{Message: "在庫が足りません。"}
		}
		var durability, maxSets int
		if err := tx.QueryRow(ctx, `SELECT GREATEST(1, durability), max_sets FROM content_items WHERE id = $1`, itemID).Scan(&durability, &maxSets); err != nil {
			return fmt.Errorf("item: %w", err)
		}
		cost := price * int64(qty)
		if state.Money < cost {
			return &ConditionError{Message: "お金が足りません。"}
		}
		add := durability * qty
		var current int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(remaining_uses, 0) FROM player_items WHERE player_id = $1 AND item_id = $2`, buyerID, itemID).Scan(&current); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if maxSets > 0 && current+add > maxSets*durability {
			return &ConditionError{Message: fmt.Sprintf("これ以上は持てません(最大%dセット)。", maxSets)}
		}
		// 代金は店主の貯金(普通口座)へ。
		if err := s.ledger.PostTx(ctx, tx, "shop_buy", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(buyerID), Delta: -cost},
			{Account: ledger.SavingsAccount(ownerID), Delta: cost},
		}); err != nil {
			return fmt.Errorf("pay: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE shop_listings SET stock = stock - $3 WHERE owner_id = $1 AND item_id = $2`, ownerID, itemID, qty); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_items (player_id, item_id, quantity, remaining_uses) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (player_id, item_id) DO UPDATE SET quantity = player_items.quantity + $3,
			   remaining_uses = player_items.remaining_uses + $4, updated_at = now()`,
			buyerID, itemID, qty, add); err != nil {
			return err
		}
		return nil
	})
}

// DoOffer gives an offering (さい銭) to another player's savings, enforcing the
// daily per-pair and per-recipient limits. Legacy: saisensuru.
func (s *Service) DoOffer(ctx context.Context, fromID, toID, amount int64, idempotencyKey string) (*player.Player, error) {
	if amount <= 0 {
		return nil, &ConditionError{Message: "金額が不正です。"}
	}
	if fromID == toID {
		return nil, &ConditionError{Message: "自分にさい銭はできません。"}
	}
	return s.runAction(ctx, fromID, "offer", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		if state.Money < amount {
			return &ConditionError{Message: "お金が足りません。"}
		}
		dayStart := gametime.Date(time.Now(), s.loc, s.dayBoundaryHour).Add(time.Duration(s.dayBoundaryHour) * time.Hour)
		var pairSum, recipSum int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM offering_log WHERE from_id = $1 AND to_id = $2 AND created_at >= $3`, fromID, toID, dayStart).Scan(&pairSum); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM offering_log WHERE to_id = $1 AND created_at >= $2`, toID, dayStart).Scan(&recipSum); err != nil {
			return err
		}
		if pairSum+amount > offerDailyPairLimit {
			return &ConditionError{Message: fmt.Sprintf("同じ相手へは1日%d円までです。", offerDailyPairLimit)}
		}
		if recipSum+amount > offerDailyRecipientLimit {
			return &ConditionError{Message: fmt.Sprintf("その相手が受け取れるのは1日%d円までです。", offerDailyRecipientLimit)}
		}
		if err := s.ledger.PostTx(ctx, tx, "offer", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(fromID), Delta: -amount},
			{Account: ledger.SavingsAccount(toID), Delta: amount},
		}); err != nil {
			return fmt.Errorf("offer: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO offering_log (from_id, to_id, amount) VALUES ($1, $2, $3)`, fromID, toID, amount); err != nil {
			return err
		}
		return nil
	})
}

// cleagueMatchInterval rate-limits battles per player (legacy 疲労クールタイム).
const cleagueMatchInterval = 5 * time.Minute

// DoSetCharacterName creates or renames the player's battle character (free).
func (s *Service) DoSetCharacterName(ctx context.Context, playerID int64, name, idempotencyKey string) (*player.Player, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 30 {
		return nil, &ConditionError{Message: "キャラ名は1〜30文字で入力してください。"}
	}
	return s.runAction(ctx, playerID, "cleague_name", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO battle_characters (owner_id, name, abilities) VALUES ($1, $2, '{}')
			 ON CONFLICT (owner_id) DO UPDATE SET name = $2`, playerID, name)
		return err
	})
}

// DoGrowCharacter feeds the player's parameters and money into their character:
// each ability transfers the input value from the owner (owner-param > input
// required) and costs input×10000 yen. Legacy: make_chara.
func (s *Service) DoGrowCharacter(ctx context.Context, playerID int64, inputs map[string]int, idempotencyKey string) (*player.Player, error) {
	for k, v := range inputs {
		if !cleague.IsAbility(k) || v < 0 {
			return nil, &ConditionError{Message: "入力が不正です。"}
		}
	}
	return s.runAction(ctx, playerID, "cleague_grow", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var abJSON []byte
		if err := tx.QueryRow(ctx, `SELECT abilities FROM battle_characters WHERE owner_id = $1`, playerID).Scan(&abJSON); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &ConditionError{Message: "先にキャラを作成してください。"}
			}
			return fmt.Errorf("character: %w", err)
		}
		abilities := map[string]int{}
		_ = json.Unmarshal(abJSON, &abilities)

		var totalUnits int
		eff := effects.Effect{}
		for k, v := range inputs {
			if v <= 0 {
				continue
			}
			if state.Params[k].Value <= v {
				return &ConditionError{Message: fmt.Sprintf("%sが足りません(本人の値より小さい値を入力)。", k)}
			}
			totalUnits += v
			abilities[k] += v
			eff.Ops = append(eff.Ops, effects.Op{Kind: "add_param", Param: k, Amount: int64(-v)})
		}
		if totalUnits == 0 {
			return &ConditionError{Message: "育成する能力を入力してください。"}
		}
		cost := int64(totalUnits) * 10000
		if state.Money < cost {
			return &ConditionError{Message: "お金が足りません。"}
		}
		if err := s.ledger.PostTx(ctx, tx, "cleague_grow", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -cost},
			{Account: ledger.SystemAccount("cleague"), Delta: cost},
		}); err != nil {
			return fmt.Errorf("pay: %w", err)
		}
		if err := s.applyEffect(ctx, tx, playerID, "cleague", eff, state); err != nil {
			return err
		}
		newAb, _ := json.Marshal(abilities)
		if _, err := tx.Exec(ctx, `UPDATE battle_characters SET abilities = $2 WHERE owner_id = $1`, playerID, newAb); err != nil {
			return fmt.Errorf("update character: %w", err)
		}
		return nil
	})
}

// DoBattle fights the player's character against an opponent's, records the
// result for both, and returns the round-by-round outcome. Legacy: c_league/battle.
func (s *Service) DoBattle(ctx context.Context, playerID, opponentID int64, idempotencyKey string) (*player.Player, *cleague.BattleResult, error) {
	if playerID == opponentID {
		return nil, nil, &ConditionError{Message: "自分とは対戦できません。"}
	}
	var result *cleague.BattleResult
	p, err := s.runAction(ctx, playerID, "cleague_battle", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		mine, err := loadAbilities(ctx, tx, playerID)
		if err != nil {
			return err
		}
		if mine == nil {
			return &ConditionError{Message: "先にキャラを作成してください。"}
		}
		opp, err := loadAbilities(ctx, tx, opponentID)
		if err != nil {
			return err
		}
		if opp == nil {
			return &ConditionError{Message: "相手のキャラがいません。"}
		}
		if !s.settings.Get().DebugNoCooldown {
			var last *time.Time
			if err := tx.QueryRow(ctx, `SELECT last_match_at FROM battle_characters WHERE owner_id = $1`, playerID).Scan(&last); err != nil {
				return err
			}
			if last != nil && time.Since(*last) < cleagueMatchInterval {
				return &ConditionError{Message: "キャラが疲れています。少し休ませてください。"}
			}
		}
		res := cleague.Battle(mine, opp, s.rng)
		result = &res
		// 戦績更新。
		switch res.Winner {
		case "a":
			if err := bumpRecord(ctx, tx, playerID, "wins"); err != nil {
				return err
			}
			if err := bumpRecord(ctx, tx, opponentID, "losses"); err != nil {
				return err
			}
		case "b":
			if err := bumpRecord(ctx, tx, playerID, "losses"); err != nil {
				return err
			}
			if err := bumpRecord(ctx, tx, opponentID, "wins"); err != nil {
				return err
			}
		default:
			if err := bumpRecord(ctx, tx, playerID, "draws"); err != nil {
				return err
			}
			if err := bumpRecord(ctx, tx, opponentID, "draws"); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE battle_characters SET last_match_at = now() WHERE owner_id = $1`, playerID)
		return err
	})
	return p, result, err
}

func loadAbilities(ctx context.Context, tx pgx.Tx, ownerID int64) (map[string]int, error) {
	var abJSON []byte
	err := tx.QueryRow(ctx, `SELECT abilities FROM battle_characters WHERE owner_id = $1`, ownerID).Scan(&abJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("abilities: %w", err)
	}
	ab := map[string]int{}
	_ = json.Unmarshal(abJSON, &ab)
	return ab, nil
}

func bumpRecord(ctx context.Context, tx pgx.Tx, ownerID int64, col string) error {
	// col は "wins"/"losses"/"draws" の固定値のみ(内部呼び出し)。
	_, err := tx.Exec(ctx, `UPDATE battle_characters SET `+col+` = `+col+` + 1 WHERE owner_id = $1`, ownerID)
	return err
}

// jobEconomy holds a job's take-requirements and pay/level rules (design 17.5).
type jobEconomy struct {
	conds         effects.Conditions
	salary        int64
	payInterval   int
	bonusRate     int
	raiseRate     int
	bodyCost      int
	nouCost       int
	bmiMin        *int
	bmiMax        *int
	heightMin     *int
	requireMaster *string
}

func (s *Service) loadJobEconomy(ctx context.Context, name string) (jobEconomy, error) {
	var (
		reqJSON []byte
		e       jobEconomy
	)
	err := s.pool.QueryRow(ctx,
		`SELECT requirements, salary, pay_interval, bonus_rate, raise_rate,
		        body_cost, nou_cost, bmi_min, bmi_max, height_min, require_master
		 FROM content_jobs WHERE name = $1 AND enabled`, name).
		Scan(&reqJSON, &e.salary, &e.payInterval, &e.bonusRate, &e.raiseRate,
			&e.bodyCost, &e.nouCost, &e.bmiMin, &e.bmiMax, &e.heightMin, &e.requireMaster)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobEconomy{}, fmt.Errorf("job %q not found", name)
	}
	if err != nil {
		return jobEconomy{}, fmt.Errorf("load job: %w", err)
	}
	conds, err := effects.ParseConditions(reqJSON)
	if err != nil {
		return jobEconomy{}, fmt.Errorf("job %q requirements: %w", name, err)
	}
	e.conds = conds
	return e, nil
}

// loadItemBuy returns a purchasable department-store item's price, durability
// and per-item ownership cap (max_sets). Only facility=” items are sellable at
// the department store; 食堂(syokudou)は DoEat、ジム/温泉は DoFacilityAction 経由。
func (s *Service) loadItemBuy(ctx context.Context, itemID int64) (price int64, durability, maxSets int, err error) {
	cond, extra := s.dailyMenuCond("", 2)
	args := append([]any{itemID}, extra...)
	err = s.pool.QueryRow(ctx,
		`SELECT price, durability, max_sets FROM content_items
		 WHERE id = $1 AND enabled AND facility = ''`+cond, args...).Scan(&price, &durability, &maxSets)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, 0, ErrItemNotFound
	}
	if err != nil {
		return 0, 0, 0, fmt.Errorf("load item buy: %w", err)
	}
	return price, durability, maxSets, nil
}

// loadItemUse returns an item's use-effect, its use interval (minutes), whether
// using it fills 満腹度 (food items), and its durability unit ('use'/'day').
func (s *Service) loadItemUse(ctx context.Context, itemID int64) (effects.Effect, int, bool, string, error) {
	var (
		effJSON      []byte
		intervalMin  int
		fillsSatiety bool
		durUnit      string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT effect, use_interval_min, fills_satiety, durability_unit
		 FROM content_items WHERE id = $1 AND enabled`,
		itemID).Scan(&effJSON, &intervalMin, &fillsSatiety, &durUnit)
	if errors.Is(err, pgx.ErrNoRows) {
		return effects.Effect{}, 0, false, "", ErrItemNotFound
	}
	if err != nil {
		return effects.Effect{}, 0, false, "", fmt.Errorf("load item: %w", err)
	}
	eff, err := effects.ParseEffect(effJSON)
	if err != nil {
		return effects.Effect{}, 0, false, "", fmt.Errorf("item %d effect: %w", itemID, err)
	}
	return eff, intervalMin, fillsSatiety, durUnit, nil
}

// claimIdempotency inserts the action log row. It returns duplicate=true when
// the idempotency key was already used.
func (s *Service) claimIdempotency(ctx context.Context, tx pgx.Tx, playerID int64, actionType, key string) (bool, error) {
	if key == "" {
		if _, err := tx.Exec(ctx,
			`INSERT INTO action_log (player_id, action_type) VALUES ($1, $2)`,
			playerID, actionType); err != nil {
			return false, fmt.Errorf("action_log insert: %w", err)
		}
		return false, nil
	}
	var id int64
	err := tx.QueryRow(ctx,
		`INSERT INTO action_log (player_id, action_type, idempotency_key)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (player_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		 RETURNING id`, playerID, actionType, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("action_log claim: %w", err)
	}
	return false, nil
}

func (s *Service) readState(ctx context.Context, tx pgx.Tx, playerID int64) (effects.State, error) {
	var (
		energy, energyMax, nou, nouMax                              int
		satiety                                                     int
		kokugo, suugaku, rika, syakai, eigo, ongaku, bijutsu        int
		looks, tairyoku, kenkou, speed, power, wanryoku, kyakuryoku int
		love, omoshirosa                                            int
	)
	err := tx.QueryRow(ctx,
		`SELECT energy, energy_max, nou_energy, nou_energy_max, satiety,
		        kokugo, suugaku, rika, syakai, eigo, ongaku, bijutsu,
		        looks, tairyoku, kenkou, speed, power, wanryoku, kyakuryoku,
		        love, omoshirosa
		 FROM player_status WHERE player_id = $1`, playerID).
		Scan(&energy, &energyMax, &nou, &nouMax, &satiety,
			&kokugo, &suugaku, &rika, &syakai, &eigo, &ongaku, &bijutsu,
			&looks, &tairyoku, &kenkou, &speed, &power, &wanryoku, &kyakuryoku,
			&love, &omoshirosa)
	if errors.Is(err, pgx.ErrNoRows) {
		return effects.State{}, player.ErrNotFound
	}
	if err != nil {
		return effects.State{}, fmt.Errorf("read status: %w", err)
	}

	var money int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0) FROM ledger_entry WHERE account = $1`,
		ledger.PlayerAccount(playerID)).Scan(&money); err != nil {
		return effects.State{}, fmt.Errorf("read money: %w", err)
	}

	m := detailedParamMax
	return effects.State{
		Money: money,
		Params: map[string]effects.ParamState{
			"energy":     {Value: energy, Max: energyMax},
			"nou_energy": {Value: nou, Max: nouMax},
			"satiety":    {Value: satiety, Max: 100},
			"kokugo":     {Value: kokugo, Max: m},
			"suugaku":    {Value: suugaku, Max: m},
			"rika":       {Value: rika, Max: m},
			"syakai":     {Value: syakai, Max: m},
			"eigo":       {Value: eigo, Max: m},
			"ongaku":     {Value: ongaku, Max: m},
			"bijutsu":    {Value: bijutsu, Max: m},
			"looks":      {Value: looks, Max: m},
			"tairyoku":   {Value: tairyoku, Max: m},
			"kenkou":     {Value: kenkou, Max: m},
			"speed":      {Value: speed, Max: m},
			"power":      {Value: power, Max: m},
			"wanryoku":   {Value: wanryoku, Max: m},
			"kyakuryoku": {Value: kyakuryoku, Max: m},
			"love":       {Value: love, Max: m},
			"omoshirosa": {Value: omoshirosa, Max: m},
		},
	}, nil
}

func (s *Service) applyEffect(ctx context.Context, tx pgx.Tx, playerID int64, actionType string, eff effects.Effect, state effects.State) error {
	plan := eff.Plan(state)

	for _, pc := range plan.Params {
		col, ok := statusColumns[pc.Name]
		if !ok {
			return fmt.Errorf("no status column for param %q", pc.Name)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE player_status SET `+col+` = $1, updated_at = now() WHERE player_id = $2`,
			pc.NewValue, playerID); err != nil {
			return fmt.Errorf("update %s: %w", col, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO status_history (player_id, field, old_value, new_value, reason)
			 VALUES ($1, $2, $3, $4, $5)`,
			playerID, pc.Name, strconv.Itoa(pc.OldValue), strconv.Itoa(pc.NewValue), actionType); err != nil {
			return fmt.Errorf("insert status_history: %w", err)
		}
	}
	// パラメータが変化したらパワー上限(パラメータ由来)を再計算する。
	if len(plan.Params) > 0 {
		if err := player.RefreshPowerMax(ctx, tx, playerID); err != nil {
			return err
		}
	}

	if plan.MoneyDelta != 0 {
		// 冪等性は action_log(player_id, key) が保証するため台帳refは不要。
		if err := s.ledger.PostTx(ctx, tx, actionType, "", []ledger.Entry{
			{Account: ledger.SystemAccount(actionType + "_income"), Delta: -plan.MoneyDelta},
			{Account: ledger.PlayerAccount(playerID), Delta: plan.MoneyDelta},
		}); err != nil {
			return fmt.Errorf("apply money: %w", err)
		}
	}
	return nil
}

// conditionMessage maps a failed predicate to a player-facing message.
func conditionMessage(p *effects.Pred) string {
	if p == nil {
		return "条件を満たしていません。"
	}
	switch {
	case p.Kind == "param_gte" && p.Param == "energy":
		return "身体パワーが足りません。"
	case p.Kind == "param_gte" && p.Param == "nou_energy":
		return "頭脳パワーが足りません。"
	case p.Kind == "money_gte":
		return "お金が足りません。"
	default:
		return "条件を満たしていません。"
	}
}

// paramShortMessage maps an insufficient parameter to a player-facing message.
func paramShortMessage(param string) string {
	switch param {
	case "energy":
		return "身体パワーが足りません。"
	case "nou_energy":
		return "頭脳パワーが足りません。"
	default:
		return "パラメータが足りません。"
	}
}
