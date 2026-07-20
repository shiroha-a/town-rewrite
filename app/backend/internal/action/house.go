package action

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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

// DoSellHouse demolishes one of the player's houses and refunds the land price
// (地価×10000) in cash. The exterior/interior cost is not refunded, and admins
// receive no refund (レガシー忠実). The freed plot returns to buildable state.
func (s *Service) DoSellHouse(ctx context.Context, playerID, houseID int64, idempotencyKey string) (*player.Player, error) {
	isAdmin, err := s.players.HasRole(ctx, playerID, "admin")
	if err != nil {
		return nil, err
	}
	return s.runAction(ctx, playerID, "sell_house", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var town int
		err := tx.QueryRow(ctx,
			`SELECT town FROM player_houses WHERE id = $1 AND owner_id = $2`, houseID, playerID).Scan(&town)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その家は所有していません。"}
		}
		if err != nil {
			return fmt.Errorf("load house: %w", err)
		}
		refund, err := building.SellValue(town)
		if err != nil {
			return &ConditionError{Message: "家の街情報が不正です。"}
		}
		if isAdmin {
			refund = 0 // 管理者は返金なし
		}
		if _, err := tx.Exec(ctx, `DELETE FROM player_houses WHERE id = $1`, houseID); err != nil {
			return fmt.Errorf("delete house: %w", err)
		}
		if refund > 0 {
			// 返金は地価分のみを現金へ(外装・内装費は戻らない)。
			if err := s.ledger.PostTx(ctx, tx, "sell_house", "", []ledger.Entry{
				{Account: ledger.SystemAccount("house_sell"), Delta: -refund},
				{Account: ledger.PlayerAccount(playerID), Delta: refund},
			}); err != nil {
				return fmt.Errorf("refund: %w", err)
			}
		}
		return nil
	})
}

// DoRebuildHouse rebuilds an existing house with a new exterior and interior
// rank. The cost (外装+内装)×10000 is charged in cash; the land price is not
// re-charged because it was already paid at build time.
func (s *Service) DoRebuildHouse(ctx context.Context, playerID, houseID int64, exterior string, interiorRank int, idempotencyKey string) (*player.Player, error) {
	return s.runAction(ctx, playerID, "rebuild_house", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM player_houses WHERE id = $1 AND owner_id = $2)`,
			houseID, playerID).Scan(&exists); err != nil {
			return fmt.Errorf("check house: %w", err)
		}
		if !exists {
			return &ConditionError{Message: "その家は所有していません。"}
		}
		cost, err := building.RebuildCost(exterior, interiorRank)
		if err != nil {
			return &ConditionError{Message: "外装または内装の指定が正しくありません。"}
		}
		if state.Money < cost {
			return &ConditionError{Message: fmt.Sprintf("現金が足りません。建て替え費用%d円が必要です。", cost)}
		}
		if err := s.ledger.PostTx(ctx, tx, "rebuild_house", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -cost},
			{Account: ledger.SystemAccount("house_rebuild"), Delta: cost},
		}); err != nil {
			return fmt.Errorf("charge rebuild: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE player_houses SET exterior = $1, interior_rank = $2 WHERE id = $3`,
			exterior, interiorRank, houseID); err != nil {
			return fmt.Errorf("update house: %w", err)
		}
		return nil
	})
}

// maxSetumeiLen is the mouse-over comment length limit (legacy 40字).
const maxSetumeiLen = 40

// DoSetHouseComment sets the mouse-over comment (setumei) of the player's house
// (マイホーム設定 フェーズ3a).
func (s *Service) DoSetHouseComment(ctx context.Context, playerID, houseID int64, setumei, idempotencyKey string) (*player.Player, error) {
	setumei = strings.TrimSpace(setumei)
	if utf8.RuneCountInString(setumei) > maxSetumeiLen {
		return nil, &ConditionError{Message: fmt.Sprintf("コメントは%d文字以内で入力してください。", maxSetumeiLen)}
	}
	return s.runAction(ctx, playerID, "house_comment", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		ct, err := tx.Exec(ctx,
			`UPDATE player_houses SET setumei = $1 WHERE id = $2 AND owner_id = $3`,
			setumei, houseID, playerID)
		if err != nil {
			return fmt.Errorf("update setumei: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return &ConditionError{Message: "その家は所有していません。"}
		}
		return nil
	})
}

// Allowed offering amounts and daily caps for さい銭 (レガシー忠実).
var saisenAmounts = map[int64]bool{100: true, 500: true, 1000: true, 2000: true, 5000: true, 10000: true}

const (
	saisenPerTargetDaily   = 20000  // 同一相手への1日上限(円)
	saisenTargetTotalDaily = 100000 // 相手が1日に受け取れる総額(円)
)

// DoSaisen offers money at a house's offering box: it moves the amount from the
// visitor's cash to the owner's bank savings, subject to daily caps (同一相手
// 20000円/日、相手受取総額100000円/日). A player cannot offer at their own house.
func (s *Service) DoSaisen(ctx context.Context, playerID, houseID, amount int64, idempotencyKey string) (*player.Player, error) {
	if !saisenAmounts[amount] {
		return nil, &ConditionError{Message: "さい銭の金額が正しくありません。"}
	}
	date := s.gameDate(time.Now())
	return s.runAction(ctx, playerID, "saisen", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var ownerID int64
		err := tx.QueryRow(ctx, `SELECT owner_id FROM player_houses WHERE id = $1`, houseID).Scan(&ownerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その家は存在しません。"}
		}
		if err != nil {
			return fmt.Errorf("load house owner: %w", err)
		}
		if ownerID == playerID {
			return &ConditionError{Message: "自分の家にはさい銭できません。"}
		}
		if state.Money < amount {
			return &ConditionError{Message: "所持金が足りません。"}
		}
		var toPair int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount), 0) FROM saisen_log WHERE from_id = $1 AND to_id = $2 AND game_date = $3`,
			playerID, ownerID, date).Scan(&toPair); err != nil {
			return fmt.Errorf("sum pair saisen: %w", err)
		}
		if toPair+amount > saisenPerTargetDaily {
			return &ConditionError{Message: fmt.Sprintf("同じ相手へのさい銭は1日%d円までです。", saisenPerTargetDaily)}
		}
		var toTotal int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount), 0) FROM saisen_log WHERE to_id = $1 AND game_date = $2`,
			ownerID, date).Scan(&toTotal); err != nil {
			return fmt.Errorf("sum total saisen: %w", err)
		}
		if toTotal+amount > saisenTargetTotalDaily {
			return &ConditionError{Message: "この家は今日のさい銭受け取り上限に達しています。"}
		}
		// 送金は現金→相手の普通口座。
		if err := s.ledger.PostTx(ctx, tx, "saisen", "", []ledger.Entry{
			{Account: ledger.PlayerAccount(playerID), Delta: -amount},
			{Account: ledger.SavingsAccount(ownerID), Delta: amount},
		}); err != nil {
			return fmt.Errorf("saisen transfer: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO saisen_log (from_id, to_id, amount, game_date) VALUES ($1, $2, $3, $4)`,
			playerID, ownerID, amount, date); err != nil {
			return fmt.Errorf("insert saisen log: %w", err)
		}
		return nil
	})
}

// maxShopTitleLen caps a shop title length.
const maxShopTitleLen = 50

// DoOpenHouseShop opens (or reconfigures) the shop attached to the player's
// house: its title, category (syubetu), and base markup (掛け率, 0.3<率<=3).
// Changing the category clears the existing stock (店の種類変更で在庫全消去).
func (s *Service) DoOpenHouseShop(ctx context.Context, playerID, houseID int64, title, syubetu string, markup float64, idempotencyKey string) (*player.Player, error) {
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > maxShopTitleLen {
		return nil, &ConditionError{Message: fmt.Sprintf("店名は%d文字以内で入力してください。", maxShopTitleLen)}
	}
	if !building.IsShopKind(syubetu) {
		return nil, &ConditionError{Message: "店の種類が正しくありません。"}
	}
	if markup <= building.ShopMarkupMin || markup > building.ShopMarkupMax {
		return nil, &ConditionError{Message: fmt.Sprintf("販売掛け率は%gより大きく%g以下にしてください。", building.ShopMarkupMin, building.ShopMarkupMax)}
	}
	return s.runAction(ctx, playerID, "open_house_shop", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM player_houses WHERE id = $1 AND owner_id = $2)`,
			houseID, playerID).Scan(&exists); err != nil {
			return fmt.Errorf("check house: %w", err)
		}
		if !exists {
			return &ConditionError{Message: "その家は所有していません。"}
		}
		// 店の種類を変更した場合は在庫を全消去する(レガシー忠実)。
		var oldSyubetu string
		err := tx.QueryRow(ctx, `SELECT syubetu FROM house_shops WHERE house_id = $1`, houseID).Scan(&oldSyubetu)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("load shop: %w", err)
		}
		if err == nil && oldSyubetu != syubetu {
			if _, err := tx.Exec(ctx, `DELETE FROM house_shop_stock WHERE house_id = $1`, houseID); err != nil {
				return fmt.Errorf("clear stock: %w", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO house_shops (house_id, title, syubetu, markup) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (house_id) DO UPDATE SET title = $2, syubetu = $3, markup = $4`,
			houseID, title, syubetu, markup); err != nil {
			return fmt.Errorf("upsert shop: %w", err)
		}
		return nil
	})
}

// DoShiire purchases qty of an item from the wholesaler into the player's house
// shop stock (卸問屋での仕入れ フェーズ4b). The cost is drawn from the bank
// savings. スーパーは店の全種類を1.5倍で扱える。
func (s *Service) DoShiire(ctx context.Context, playerID, houseID, itemID int64, qty int, idempotencyKey string) (*player.Player, error) {
	if qty <= 0 {
		return nil, &ConditionError{Message: "数量が正しくありません。"}
	}
	return s.runAction(ctx, playerID, "shiire", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var syubetu string
		err := tx.QueryRow(ctx,
			`SELECT hs.syubetu FROM house_shops hs JOIN player_houses h ON h.id = hs.house_id
			 WHERE hs.house_id = $1 AND h.owner_id = $2`, houseID, playerID).Scan(&syubetu)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その家に自分の店がありません。"}
		}
		if err != nil {
			return fmt.Errorf("load shop: %w", err)
		}
		var (
			category string
			price    int64
			facility string
		)
		err = tx.QueryRow(ctx,
			`SELECT category, price, facility FROM content_items WHERE id = $1 AND enabled`, itemID).
			Scan(&category, &price, &facility)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その商品は仕入れられません。"}
		}
		if err != nil {
			return fmt.Errorf("load item: %w", err)
		}
		if facility != "" {
			return &ConditionError{Message: "その商品は仕入れられません。"}
		}
		super := syubetu == building.SuperMarketKind
		if super {
			if !building.IsShopKind(category) || category == building.SuperMarketKind {
				return &ConditionError{Message: "その商品は扱えません。"}
			}
		} else if category != syubetu {
			return &ConditionError{Message: "この店ではその商品を扱えません。"}
		}
		buyPrice := price
		if super {
			buyPrice = price * 3 / 2 // スーパーは1.5倍
		}
		total := buyPrice * int64(qty)

		var curStock int
		exists := true
		err = tx.QueryRow(ctx,
			`SELECT stock FROM house_shop_stock WHERE house_id = $1 AND item_id = $2`, houseID, itemID).Scan(&curStock)
		if errors.Is(err, pgx.ErrNoRows) {
			exists = false
		} else if err != nil {
			return fmt.Errorf("load stock: %w", err)
		}
		if curStock+qty > building.ShopMaxStock {
			return &ConditionError{Message: fmt.Sprintf("在庫は1商品%d個までです(現在%d個)。", building.ShopMaxStock, curStock)}
		}
		if !exists {
			var kinds int
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM house_shop_stock WHERE house_id = $1`, houseID).Scan(&kinds); err != nil {
				return fmt.Errorf("count kinds: %w", err)
			}
			if kinds >= building.ShopMaxKinds {
				return &ConditionError{Message: fmt.Sprintf("店に置ける商品は%d種類までです。", building.ShopMaxKinds)}
			}
		}
		var savings int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(delta), 0) FROM ledger_entry WHERE account = $1`,
			ledger.SavingsAccount(playerID)).Scan(&savings); err != nil {
			return fmt.Errorf("read savings: %w", err)
		}
		if savings < total {
			return &ConditionError{Message: fmt.Sprintf("普通口座の残高が足りません(仕入れ額%d円)。", total)}
		}
		if err := s.ledger.PostTx(ctx, tx, "shiire", "", []ledger.Entry{
			{Account: ledger.SavingsAccount(playerID), Delta: -total},
			{Account: ledger.SystemAccount("shiire"), Delta: total},
		}); err != nil {
			return fmt.Errorf("charge shiire: %w", err)
		}
		if exists {
			if _, err := tx.Exec(ctx,
				`UPDATE house_shop_stock SET stock = stock + $1 WHERE house_id = $2 AND item_id = $3`,
				qty, houseID, itemID); err != nil {
				return fmt.Errorf("update stock: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx,
				`INSERT INTO house_shop_stock (house_id, item_id, buy_price, stock) VALUES ($1, $2, $3, $4)`,
				houseID, itemID, buyPrice, qty); err != nil {
				return fmt.Errorf("insert stock: %w", err)
			}
		}
		return nil
	})
}
