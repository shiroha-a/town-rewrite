package action

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/shiroha-a/town/internal/building"
	"github.com/shiroha-a/town/internal/effects"
	"github.com/shiroha-a/town/internal/ledger"
	"github.com/shiroha-a/town/internal/player"
	"github.com/shiroha-a/town/internal/townmap"
)

// DoBuildHouse builds a house on an empty plot for the player (建設会社 フェーズ2a).
// The build fee is drawn from the player's bank savings (普通口座). It enforces
// the 4-house limit, empty-plot validation (no facility, no existing house), and
// the legacy cost formula (1軒目=地価+外装+内装, 2軒目以降=地価+外装×2).
func (s *Service) DoBuildHouse(ctx context.Context, playerID int64, town, row, col int, exterior string, interiorRank int, idempotencyKey string) (*player.Player, error) {
	return s.runAction(ctx, playerID, "build_house", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		if town < 0 || col < 1 || col > townmap.Cols || row < 0 || row >= townmap.Rows {
			return &ConditionError{Message: "建築場所の指定が正しくありません。"}
		}
		// 所有軒数(上限)。建てる前の軒数で1軒目/2軒目以降の費用式を分ける。
		var count int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM player_houses WHERE owner_id = $1`, playerID).Scan(&count); err != nil {
			return fmt.Errorf("count houses: %w", err)
		}
		if count >= building.MochiieMax {
			return &ConditionError{Message: fmt.Sprintf("家は%d軒までしか持てません。", building.MochiieMax)}
		}
		// 管理者が空地(town_plots)に指定したマスにのみ建てられる。
		var isPlot bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM town_plots WHERE town = $1 AND grid_row = $2 AND grid_col = $3)`,
			town, row, col).Scan(&isPlot); err != nil {
			return fmt.Errorf("check plot: %w", err)
		}
		if !isPlot {
			return &ConditionError{Message: "そこは空地に指定されていません。空地に設定された場所にのみ家を建てられます。"}
		}
		cost, err := building.BuildCost(town, exterior, interiorRank, count)
		if err != nil {
			return &ConditionError{Message: "外装または内装の指定が正しくありません。"}
		}
		// 街0(メイン街)は既存施設のセルに建てられない。町マップのfacilities JSONBを直接引く。
		if town == 0 {
			var onFacility bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(
				   SELECT 1 FROM town_map, jsonb_array_elements(facilities) f
				   WHERE id = 1 AND (f->>'col')::int = $1 AND (f->>'row')::int = $2)`,
				col, row).Scan(&onFacility); err != nil {
				return fmt.Errorf("check facility cell: %w", err)
			}
			if onFacility {
				return &ConditionError{Message: "その場所には施設があるため建てられません。"}
			}
		}
		// 空地判定(同一マスに既存の家が無いこと)。UNIQUE制約も二重の保険になる。
		var occupied bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM player_houses WHERE town = $1 AND grid_row = $2 AND grid_col = $3)`,
			town, row, col).Scan(&occupied); err != nil {
			return fmt.Errorf("check plot: %w", err)
		}
		if occupied {
			return &ConditionError{Message: "その場所にはすでに家が建っています。"}
		}
		// 建築費は普通口座(savings)から引き落とす。
		var savings int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(delta), 0) FROM ledger_entry WHERE account = $1`,
			ledger.SavingsAccount(playerID)).Scan(&savings); err != nil {
			return fmt.Errorf("read savings: %w", err)
		}
		if savings < cost {
			return &ConditionError{Message: fmt.Sprintf("普通口座の残高が足りません。建築費%d円が必要です。", cost)}
		}
		if err := s.ledger.PostTx(ctx, tx, "build_house", "", []ledger.Entry{
			{Account: ledger.SavingsAccount(playerID), Delta: -cost},
			{Account: ledger.SystemAccount("build_house"), Delta: cost},
		}); err != nil {
			return fmt.Errorf("charge build fee: %w", err)
		}
		// 2軒目以降は内装を選ばない(家のみ)。interior_rankは0を格納。
		ir := interiorRank
		if count > 0 {
			ir = 0
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_houses (owner_id, town, grid_row, grid_col, exterior, interior_rank, tuika)
			 VALUES ($1, $2, $3, $4, $5, $6, 0)`,
			playerID, town, row, col, exterior, ir); err != nil {
			return fmt.Errorf("insert house: %w", err)
		}
		return nil
	})
}
