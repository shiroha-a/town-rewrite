package action

import (
	"context"
	"errors"
	"fmt"
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
	busFare        int64 = 500
	walkMoveSecs   int   = 10
	busMoveSecs    int   = 5
)

// DoMoveTown moves the player to another town on foot (walk) or by bus.
// Walking is free; the bus costs 500 yen from cash. A per-player move cooldown
// (移動時間) prevents moving again until travel finishes. Movement events
// (accident/getting lost/stat gain) are added in a later phase.
func (s *Service) DoMoveTown(ctx context.Context, playerID int64, dest int, means, idempotencyKey string) (*player.Player, error) {
	if means != "walk" && means != "bus" {
		return nil, &ConditionError{Message: "移動手段の指定が正しくありません。"}
	}
	if dest < 0 || dest >= townmap.Towns {
		return nil, &ConditionError{Message: "行き先の街の指定が正しくありません。"}
	}
	fare := int64(0)
	moveSecs := walkMoveSecs
	if means == "bus" {
		fare = busFare
		moveSecs = busMoveSecs
	}
	return s.runAction(ctx, playerID, "move", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
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
}
