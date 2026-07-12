// Package action applies player actions (work, purchases, item use) through the
// data-driven effect engine. Every action is server-authoritative and atomic:
// condition check, status changes, money movement and the action log all commit
// together, and a client idempotency key makes retries safe.
package action

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/shiroha-a/town/internal/condition"
	"github.com/shiroha-a/town/internal/effects"
	"github.com/shiroha-a/town/internal/gametime"
	"github.com/shiroha-a/town/internal/ledger"
	"github.com/shiroha-a/town/internal/player"
	"github.com/shiroha-a/town/internal/rng"
	"github.com/shiroha-a/town/internal/settings"
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

// detailedParamMax is the soft cap for the detailed parameters (国語/体力/…),
// which have no per-column max. energy/nou_energy use their own *_max columns.
const detailedParamMax = 999

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
