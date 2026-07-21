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
		// 建築可能マス = key='akichi' の空き地施設があるマスのみ(空き地は施設に統合済み)。
		// 通常施設のあるマスには akichi が無い(1セル1施設)ので、自動的に建築不可になる。
		var isPlot bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(
			   SELECT 1 FROM town_map, jsonb_array_elements(facilities) f
			   WHERE id = 1 AND f->>'key' = 'akichi'
			     AND COALESCE((f->>'town')::int, 0) = $1
			     AND (f->>'row')::int = $2 AND (f->>'col')::int = $3)`,
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
		// レガシー(saisensuru)は自分の家へのさい銭も許す(現金→自分の普通口座)。
		// 日次上限(相手別2万/受取10万)は自分の家にも適用される。
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

// DoBuyFromHouseShop buys qty of an item from a house shop (訪問販売 フェーズ4c).
// The shelf price is the per-item price if set, otherwise 仕入れ値×掛け率. Payment
// goes to the owner's bank savings and the item transfers to the buyer. A player
// cannot buy from their own shop.
// maxBuyQty is the legacy per-purchase quantity limit (item_kosuuseigen).
const maxBuyQty = 4

// creditCardNames are the items that enable credit (bank) payment at shops
// (レガシーのクレジット系アイテム)。
var creditCardNames = []string{"クレジットカード", "ゴールドクレジットカード", "スペシャルクレジットカード"}

// HouseBuyResult summarizes a shop purchase for the result toast.
type HouseBuyResult struct {
	Total    int64  // 割引前の合計(店頭価格×個数)
	Cashback int64  // ご近所キャッシュバック(単価10%×個数)
	Paid     int64  // 実際に支払った額
	Method   string // "cash"/"credit"
}

func (s *Service) DoBuyFromHouseShop(ctx context.Context, buyerID, houseID, itemID int64, qty int, payMethod, idempotencyKey string) (*player.Player, *HouseBuyResult, error) {
	if qty <= 0 {
		qty = 1
	}
	if qty > maxBuyQty {
		return nil, nil, &ConditionError{Message: fmt.Sprintf("一度に買えるのは%d個までです。", maxBuyQty)}
	}
	if payMethod == "" {
		payMethod = "cash"
	}
	if payMethod != "cash" && payMethod != "credit" {
		return nil, nil, &ConditionError{Message: "支払い方法が正しくありません。"}
	}
	result := &HouseBuyResult{Method: payMethod}
	p, err := s.runAction(ctx, buyerID, "house_shop_buy", idempotencyKey, func(ctx context.Context, tx pgx.Tx, state effects.State) error {
		var (
			ownerID  int64
			markup   float64
			shopTown int
		)
		err := tx.QueryRow(ctx,
			`SELECT h.owner_id, hs.markup, h.town FROM house_shops hs JOIN player_houses h ON h.id = hs.house_id
			 WHERE hs.house_id = $1`, houseID).Scan(&ownerID, &markup, &shopTown)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その家に店がありません。"}
		}
		if err != nil {
			return fmt.Errorf("load shop: %w", err)
		}
		if ownerID == buyerID {
			return &ConditionError{Message: "自分の店では買えません。"}
		}
		// コンテンツ枠に「お店」が設定されていない家では買えない(店が非公開)。
		if ok, err := hasHouseContent(ctx, tx, houseID, "shop"); err != nil {
			return err
		} else if !ok {
			return &ConditionError{Message: "この家の店は公開されていません。"}
		}
		var (
			buyPrice  int64
			sellPrice *int64
			stock     int
		)
		err = tx.QueryRow(ctx,
			`SELECT buy_price, sell_price, stock FROM house_shop_stock WHERE house_id = $1 AND item_id = $2`,
			houseID, itemID).Scan(&buyPrice, &sellPrice, &stock)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その商品はありません。"}
		}
		if err != nil {
			return fmt.Errorf("load stock: %w", err)
		}
		if stock < qty {
			return &ConditionError{Message: "在庫が足りません。"}
		}
		var price int64
		if sellPrice != nil {
			price = *sellPrice
		} else {
			price = int64(float64(buyPrice) * markup)
		}
		cost := price * int64(qty)
		// ご近所キャッシュバック(レガシー buy_syouhin): 買い手の居住街(最初に建てた家の街)
		// が店の街と同じなら、単価の10%×個数を割引。店主の売上も同額減る。
		var cashback int64
		var residenceTown *int
		if err := tx.QueryRow(ctx,
			`SELECT town FROM player_houses WHERE owner_id = $1 ORDER BY built_at, id LIMIT 1`,
			buyerID).Scan(&residenceTown); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("residence town: %w", err)
		}
		if residenceTown != nil && *residenceTown == shopTown {
			cashback = (price / 10) * int64(qty)
		}
		paid := cost - cashback
		// 支払い方法: 現金 or クレジット(クレジットカード所持で普通口座から)。
		buyerAccount := ledger.PlayerAccount(buyerID)
		if payMethod == "credit" {
			var hasCard bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(
				   SELECT 1 FROM player_items pi JOIN content_items ci ON ci.id = pi.item_id
				   WHERE pi.player_id = $1 AND pi.remaining_uses > 0 AND ci.name = ANY($2))`,
				buyerID, creditCardNames).Scan(&hasCard); err != nil {
				return fmt.Errorf("check credit card: %w", err)
			}
			if !hasCard {
				return &ConditionError{Message: "クレジットカードを持っていません。"}
			}
			var savings int64
			if err := tx.QueryRow(ctx,
				`SELECT COALESCE(SUM(delta), 0) FROM ledger_entry WHERE account = $1`,
				ledger.SavingsAccount(buyerID)).Scan(&savings); err != nil {
				return fmt.Errorf("read savings: %w", err)
			}
			if savings < paid {
				return &ConditionError{Message: "普通口座の残高が足りません。"}
			}
			buyerAccount = ledger.SavingsAccount(buyerID)
		} else if state.Money < paid {
			return &ConditionError{Message: "お金が足りません。"}
		}
		var durability, maxSets int
		if err := tx.QueryRow(ctx,
			`SELECT GREATEST(1, durability), max_sets FROM content_items WHERE id = $1`, itemID).
			Scan(&durability, &maxSets); err != nil {
			return fmt.Errorf("item: %w", err)
		}
		add := durability * qty
		var current int
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(remaining_uses, 0) FROM player_items WHERE player_id = $1 AND item_id = $2`,
			buyerID, itemID).Scan(&current); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if maxSets > 0 && current+add > maxSets*durability {
			return &ConditionError{Message: fmt.Sprintf("これ以上は持てません(最大%dセット)。", maxSets)}
		}
		// 代金(キャッシュバック控除後)は店主の普通口座へ。
		if paid > 0 {
			if err := s.ledger.PostTx(ctx, tx, "house_shop_buy", "", []ledger.Entry{
				{Account: buyerAccount, Delta: -paid},
				{Account: ledger.SavingsAccount(ownerID), Delta: paid},
			}); err != nil {
				return fmt.Errorf("pay: %w", err)
			}
		}
		result.Total = cost
		result.Cashback = cashback
		result.Paid = paid
		if _, err := tx.Exec(ctx,
			`UPDATE house_shop_stock SET stock = stock - $3 WHERE house_id = $1 AND item_id = $2`,
			houseID, itemID, qty); err != nil {
			return fmt.Errorf("reduce stock: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO player_items (player_id, item_id, quantity, remaining_uses) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (player_id, item_id) DO UPDATE SET quantity = player_items.quantity + $3,
			   remaining_uses = player_items.remaining_uses + $4, updated_at = now()`,
			buyerID, itemID, qty, add); err != nil {
			return fmt.Errorf("grant item: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return p, result, nil
}

// maxBbsBodyLen caps a bulletin-board post body length.
const maxBbsBodyLen = 500

// DoPostBbs posts a message to a house's bulletin board (フェーズ3b). kind is
// "normal" (anyone) or "nushi" (家主板, owner only).
func (s *Service) DoPostBbs(ctx context.Context, playerID, houseID int64, kind, title, body, idempotencyKey string) (*player.Player, error) {
	body = strings.TrimSpace(body)
	title = strings.TrimSpace(title)
	if body == "" {
		return nil, &ConditionError{Message: "本文を入力してください。"}
	}
	if utf8.RuneCountInString(body) > maxBbsBodyLen {
		return nil, &ConditionError{Message: fmt.Sprintf("本文は%d文字以内で入力してください。", maxBbsBodyLen)}
	}
	// 記事タイトル(家主板のブログ風記事用)。
	if utf8.RuneCountInString(title) > 40 {
		return nil, &ConditionError{Message: "記事タイトルは40文字以内で入力してください。"}
	}
	if kind != "normal" && kind != "nushi" {
		return nil, &ConditionError{Message: "掲示板の種類が正しくありません。"}
	}
	return s.runAction(ctx, playerID, "house_bbs_post", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var ownerID int64
		err := tx.QueryRow(ctx, `SELECT owner_id FROM player_houses WHERE id = $1`, houseID).Scan(&ownerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その家は存在しません。"}
		}
		if err != nil {
			return fmt.Errorf("load house: %w", err)
		}
		if kind == "nushi" && ownerID != playerID {
			return &ConditionError{Message: "家主板には家主しか書き込めません。"}
		}
		// コンテンツ枠に設定されていない掲示板には書き込めない。
		// (掲示板のkind 'normal' はコンテンツ枠では 'bbs')
		contentKind := kind
		if kind == "normal" {
			contentKind = "bbs"
		}
		if ok, err := hasHouseContent(ctx, tx, houseID, contentKind); err != nil {
			return err
		} else if !ok {
			return &ConditionError{Message: "この家にはその掲示板がありません。"}
		}
		var name string
		if err := tx.QueryRow(ctx, `SELECT display_name FROM players WHERE id = $1`, playerID).Scan(&name); err != nil {
			return fmt.Errorf("load name: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO house_bbs (house_id, kind, author_id, author_name, title, body) VALUES ($1, $2, $3, $4, $5, $6)`,
			houseID, kind, playerID, name, title, body); err != nil {
			return fmt.Errorf("insert bbs: %w", err)
		}
		return nil
	})
}

// HouseContentSlot is one requested content slot assignment (コンテンツ枠設定)。
type HouseContentSlot struct {
	Slot    int
	Kind    string // ""=非公開 / "bbs" / "shop" / "nushi" / "url"
	Title   string
	URL     string // kind="url" の埋め込みURL(http/httpsのみ)
	Comment string // タイトル下コメント(リード文)
}

const maxContentTitleLen = 20

// DoSetHouseContents replaces the house's content slots (レガシー my_house_settei)。
// 内装ランクで決まる枠数までしか設定できない。空kindの枠は非公開(未設定)扱い。
func (s *Service) DoSetHouseContents(ctx context.Context, playerID, houseID int64, contents []HouseContentSlot, idempotencyKey string) (*player.Player, error) {
	for _, c := range contents {
		if !building.IsHouseContentKind(c.Kind) {
			return nil, &ConditionError{Message: "コンテンツの種類が正しくありません。"}
		}
		if utf8.RuneCountInString(c.Title) > maxContentTitleLen {
			return nil, &ConditionError{Message: fmt.Sprintf("タイトルは%d文字以内で入力してください。", maxContentTitleLen)}
		}
		if utf8.RuneCountInString(c.Comment) > 100 {
			return nil, &ConditionError{Message: "コメントは100文字以内で入力してください。"}
		}
		// 独自URLはhttp/httpsのみ許可(javascript:等の混入防止)。
		if c.Kind == "url" {
			u := strings.TrimSpace(c.URL)
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return nil, &ConditionError{Message: "独自URLはhttp://またはhttps://で始まるURLを入力してください。"}
			}
		}
	}
	return s.runAction(ctx, playerID, "house_contents", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var ownerID int64
		var rank int
		err := tx.QueryRow(ctx, `SELECT owner_id, interior_rank FROM player_houses WHERE id = $1`, houseID).
			Scan(&ownerID, &rank)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その家は存在しません。"}
		}
		if err != nil {
			return fmt.Errorf("load house: %w", err)
		}
		if ownerID != playerID {
			return &ConditionError{Message: "自分の家のコンテンツだけ設定できます。"}
		}
		slots := building.SlotsByRank(rank)
		seen := map[int]bool{}
		for _, c := range contents {
			if c.Slot < 0 || c.Slot >= slots {
				return &ConditionError{Message: fmt.Sprintf("この家のコンテンツ枠は%dつまでです。", slots)}
			}
			if seen[c.Slot] {
				return &ConditionError{Message: "同じ枠を複数回指定できません。"}
			}
			seen[c.Slot] = true
		}
		// 全置き換え: 一旦消して、公開する枠(kindあり)だけ入れ直す。
		if _, err := tx.Exec(ctx, `DELETE FROM house_contents WHERE house_id = $1`, houseID); err != nil {
			return fmt.Errorf("clear contents: %w", err)
		}
		for _, c := range contents {
			if c.Kind == "" {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO house_contents (house_id, slot, kind, title, url, comment) VALUES ($1, $2, $3, $4, $5, $6)`,
				houseID, c.Slot, c.Kind, strings.TrimSpace(c.Title), strings.TrimSpace(c.URL), strings.TrimSpace(c.Comment)); err != nil {
				return fmt.Errorf("insert content: %w", err)
			}
		}
		return nil
	})
}

// hasHouseContent reports whether the house has a content slot of the kind.
func hasHouseContent(ctx context.Context, tx pgx.Tx, houseID int64, kind string) (bool, error) {
	var ok bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM house_contents WHERE house_id = $1 AND kind = $2)`,
		houseID, kind).Scan(&ok); err != nil {
		return false, fmt.Errorf("check house content: %w", err)
	}
	return ok, nil
}

// DoDeleteBbs deletes a bulletin-board post. The house owner or the post's
// author may delete it.
func (s *Service) DoDeleteBbs(ctx context.Context, playerID, postID int64, idempotencyKey string) (*player.Player, error) {
	return s.runAction(ctx, playerID, "house_bbs_delete", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var (
			houseID  int64
			authorID int64
		)
		err := tx.QueryRow(ctx, `SELECT house_id, COALESCE(author_id, 0) FROM house_bbs WHERE id = $1`, postID).
			Scan(&houseID, &authorID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その投稿はありません。"}
		}
		if err != nil {
			return fmt.Errorf("load post: %w", err)
		}
		var ownerID int64
		if err := tx.QueryRow(ctx, `SELECT owner_id FROM player_houses WHERE id = $1`, houseID).Scan(&ownerID); err != nil {
			return fmt.Errorf("load owner: %w", err)
		}
		if playerID != ownerID && playerID != authorID {
			return &ConditionError{Message: "その投稿は削除できません。"}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM house_bbs WHERE id = $1`, postID); err != nil {
			return fmt.Errorf("delete bbs: %w", err)
		}
		return nil
	})
}

// DoSetShopPrice sets the per-item shelf price of a house shop item (my_syouhin,
// 個別価格設定). The price must be at most 仕入れ値×3. A price of 0 clears the
// override so the price falls back to 仕入れ値×掛け率.
func (s *Service) DoSetShopPrice(ctx context.Context, playerID, houseID, itemID, sellPrice int64, idempotencyKey string) (*player.Player, error) {
	if sellPrice < 0 {
		return nil, &ConditionError{Message: "販売価格が正しくありません。"}
	}
	return s.runAction(ctx, playerID, "shop_price", idempotencyKey, func(ctx context.Context, tx pgx.Tx, _ effects.State) error {
		var buyPrice int64
		err := tx.QueryRow(ctx,
			`SELECT ss.buy_price FROM house_shop_stock ss
			 JOIN player_houses h ON h.id = ss.house_id
			 WHERE ss.house_id = $1 AND ss.item_id = $2 AND h.owner_id = $3`,
			houseID, itemID, playerID).Scan(&buyPrice)
		if errors.Is(err, pgx.ErrNoRows) {
			return &ConditionError{Message: "その商品は店にありません。"}
		}
		if err != nil {
			return fmt.Errorf("load stock: %w", err)
		}
		if sellPrice == 0 {
			// 0は個別価格の解除(掛け率に戻す)。
			if _, err := tx.Exec(ctx,
				`UPDATE house_shop_stock SET sell_price = NULL WHERE house_id = $1 AND item_id = $2`,
				houseID, itemID); err != nil {
				return fmt.Errorf("clear price: %w", err)
			}
			return nil
		}
		if sellPrice > buyPrice*3 {
			return &ConditionError{Message: "販売価格は仕入れ値の3倍以内にしてください。"}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE house_shop_stock SET sell_price = $1 WHERE house_id = $2 AND item_id = $3`,
			sellPrice, houseID, itemID); err != nil {
			return fmt.Errorf("update price: %w", err)
		}
		return nil
	})
}
