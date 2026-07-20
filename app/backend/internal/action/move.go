package action

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shiroha-a/town/internal/effects"
	"github.com/shiroha-a/town/internal/ledger"
	"github.com/shiroha-a/town/internal/player"
	"github.com/shiroha-a/town/internal/townmap"
)

// 街移動の手段ごとの料金(円)と移動時間(秒)。レガシー忠実:
// 徒歩は無料10秒、バスは500円5秒。バスは安全(事故/迷子なし)。
const (
	busFare      int64 = 500
	walkMoveSecs int   = 10
	busMoveSecs  int   = 5
)

// walkStatKeys are the 5 physical stats a walk can raise (体力/健康/スピード/腕力/脚力)。
// レガシー(town_lib.pl sub header)では徒歩移動で各50%、+1〜5 上昇する。
var walkStatKeys = []struct {
	key string // player_status の列名
	jp  string // 表示名
}{
	{"tairyoku", "体力"},
	{"kenkou", "健康"},
	{"speed", "スピード"},
	{"wanryoku", "腕力"},
	{"kyakuryoku", "脚力"},
}

// MoveResult summarizes a completed town move for the result toast.
type MoveResult struct {
	ArrivedTown int            // 到着した街
	Means       string         // "walk"/"bus"
	Fare        int64          // 支払った料金
	StatGains   map[string]int // 徒歩の能力上昇(表示名→上昇量)。無ければ空
}

// DoMoveTown moves the player to another town on foot (walk) or by bus.
// Walking is free and can raise physical stats; the bus costs 500 yen from
// cash and is safe/fast. A per-player move cooldown (移動時間) prevents moving
// again until travel finishes. Accident/getting-lost and vehicle speedups are
// tied to the vehicle-item system and handled in a later phase.
func (s *Service) DoMoveTown(ctx context.Context, playerID int64, dest int, means, idempotencyKey string) (*player.Player, *MoveResult, error) {
	if means != "walk" && means != "bus" {
		return nil, nil, &ConditionError{Message: "移動手段の指定が正しくありません。"}
	}
	if dest < 0 || dest >= townmap.Towns {
		return nil, nil, &ConditionError{Message: "行き先の街の指定が正しくありません。"}
	}
	fare := int64(0)
	moveSecs := walkMoveSecs
	if means == "bus" {
		fare = busFare
		moveSecs = busMoveSecs
	}
	result := &MoveResult{ArrivedTown: dest, Means: means, Fare: fare, StatGains: map[string]int{}}
	p, err := s.runAction(ctx, playerID, "move", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		// 現在いる街を取得。同じ街へは移動不可。
		var current int
		if err := tx.QueryRow(ctx, `SELECT current_town FROM players WHERE id = $1`, playerID).Scan(&current); err != nil {
			return fmt.Errorf("read current town: %w", err)
		}
		if current == dest {
			return &ConditionError{Message: "すでにその街にいます。"}
		}
		// 移動クールタイム(移動時間)中は再移動不可。
		var nextAt *time.Time
		if err := tx.QueryRow(ctx,
			`SELECT next_available_at FROM player_facility_cooldowns WHERE player_id = $1 AND facility = 'move'`,
			playerID).Scan(&nextAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read move cooldown: %w", err)
		}
		if !s.settings.Get().DebugNoCooldown && nextAt != nil && time.Now().Before(*nextAt) {
			return &ConditionError{Message: "まだ移動中です。到着までお待ちください。"}
		}
		// バス料金は現金から引き落とす。
		if fare > 0 {
			if state.Money < fare {
				return &ConditionError{Message: fmt.Sprintf("現金が足りません。バス料金%d円が必要です。", fare)}
			}
			if err := s.ledger.PostTx(ctx, tx, "move_bus", "", []ledger.Entry{
				{Account: ledger.PlayerAccount(playerID), Delta: -fare},
				{Account: ledger.SystemAccount("move_bus"), Delta: fare},
			}); err != nil {
				return fmt.Errorf("charge bus fare: %w", err)
			}
		}
		// 徒歩は5つの身体能力が各50%で+1〜5上昇する(レガシー忠実)。
		if means == "walk" {
			gains := make([]int, len(walkStatKeys))
			any := false
			for i := range walkStatKeys {
				if r := rand.Intn(10) + 1; r <= 5 {
					gains[i] = r
					result.StatGains[walkStatKeys[i].jp] = r
					any = true
				}
			}
			if any {
				if _, err := tx.Exec(ctx,
					`UPDATE player_status SET
						tairyoku = tairyoku + $1, kenkou = kenkou + $2, speed = speed + $3,
						wanryoku = wanryoku + $4, kyakuryoku = kyakuryoku + $5, updated_at = now()
					 WHERE player_id = $6`,
					gains[0], gains[1], gains[2], gains[3], gains[4], playerID); err != nil {
					return fmt.Errorf("apply walk stat gains: %w", err)
				}
			}
		}
		// 現在の街を更新。
		if _, err := tx.Exec(ctx, `UPDATE players SET current_town = $1 WHERE id = $2`, dest, playerID); err != nil {
			return fmt.Errorf("update current town: %w", err)
		}
		// 移動クールタイム(秒)を設定。
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_facility_cooldowns (player_id, facility, next_available_at)
			 VALUES ($1, 'move', now() + make_interval(secs => $2))
			 ON CONFLICT (player_id, facility)
			 DO UPDATE SET next_available_at = now() + make_interval(secs => $2)`,
			playerID, moveSecs); err != nil {
			return fmt.Errorf("set move cooldown: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return p, result, nil
}
