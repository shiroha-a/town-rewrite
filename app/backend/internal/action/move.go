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

// 街移動の料金(円)と時間(秒)。レガシー忠実: 徒歩は無料10秒、バスは500円5秒。
const (
	busFare      int64 = 500
	walkMoveSecs int   = 10
	busMoveSecs  int   = 5
	// 迷子で飛ばされる街(レガシー: ダウンタウン=3)。
	maigoTown = 3
	// 事故確率の分母(1/ziko)。徒歩+乗り物で発生、バスは対象外。
	zikoDenominator = 20
	// ローラースルーゴーゴーを持っていると必ず迷子になる(レガシーの癖)。
	rollerItem = "ローラースルーゴーゴー"
)

// vehicleTime maps a movement vehicle item name to its travel time in seconds
// (レガシー town_ini.cgi の %idou_syudan)。所持する最速の乗り物が移動時間になる。
// 自転車/ローラースルーゴーゴーは徒歩より遅いが、自転車は能力が上がる。
var vehicleTime = map[string]int{
	rollerItem:      30,
	"自転車":           20,
	"ベスパ":           10,
	"スーパーカブ":        10,
	"ドゥカティ":         7,
	"ナナハン":          7,
	"カローラ":          7,
	"ボルボ":           6,
	"キャデラック":        6,
	"ベンツ":           5,
	"ロールスロイス":       5,
	"フェアレディZ":       5,
	"スカイラインGTR":     4,
	"ロータスヨーロッパ":     4,
	"アルファロメオ":       4,
	"ジャガー":          4,
	"BMW":           4,
	"ポルシェ":          3,
	"フェラーリ":         2,
	"ミグ25":          1,
}

// walkStatKeys are the 5 physical stats a walk (or 自転車) can raise。
// レガシー(town_lib.pl sub header)では各50%、+1〜5 上昇する。
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
	ArrivedTown  int            // 到着した街(迷子だと目的地と違う)
	Means        string         // "walk"/"bus"
	Vehicle      string         // 使った乗り物名(徒歩なら空)
	Fare         int64          // 支払った料金
	StatGains    map[string]int // 徒歩/自転車の能力上昇(表示名→上昇量)
	Accident     bool           // 交通事故が起きたか
	AccidentItem string         // 事故で耐久度が減った乗り物
	Lost         bool           // 迷子になったか
}

// DoMoveTown moves the player to another town on foot (walk) or by bus.
// Walking is free; if the player owns a vehicle item, the fastest one is used
// and shortens the travel time (自転車は徒歩同様に身体能力が上がる)。乗り物利用中は
// 1/20で交通事故が起き、その乗り物の耐久度が1減る。迷子(設定で有効時)は徒歩系で
// 発生し、目的地ではなくダウンタウンに着く。バスは安全・速い(500円)。
func (s *Service) DoMoveTown(ctx context.Context, playerID int64, dest int, means, idempotencyKey string) (*player.Player, *MoveResult, error) {
	if means != "walk" && means != "bus" {
		return nil, nil, &ConditionError{Message: "移動手段の指定が正しくありません。"}
	}
	if dest < 0 || dest >= townmap.Towns {
		return nil, nil, &ConditionError{Message: "行き先の街の指定が正しくありません。"}
	}
	fare := int64(0)
	if means == "bus" {
		fare = busFare
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
		moveSecs := busMoveSecs
		arrived := dest
		if means == "walk" {
			// 所持する乗り物のうち最速のものを使う(徒歩10秒が基準)。
			vehName, vehItemID, hasRoller, err := s.fastestVehicle(ctx, tx, playerID)
			if err != nil {
				return err
			}
			moveSecs = walkMoveSecs
			if vehName != "" {
				moveSecs = vehicleTime[vehName]
				result.Vehicle = vehName
			}
			// 徒歩か自転車のみ身体5能力が各50%で+1〜5上昇する。
			if vehName == "" || vehName == "自転車" {
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
			// 乗り物利用中は1/20で交通事故。使った乗り物の耐久度が1減る。
			if vehName != "" && rand.Intn(zikoDenominator) == 0 {
				result.Accident = true
				result.AccidentItem = vehName
				if _, err := tx.Exec(ctx,
					`UPDATE player_items pi
					 SET remaining_uses = pi.remaining_uses - 1,
					     quantity = CEIL((pi.remaining_uses - 1)::numeric / GREATEST(ci.durability, 1)),
					     updated_at = now()
					 FROM content_items ci
					 WHERE pi.player_id = $1 AND pi.item_id = $2 AND pi.item_id = ci.id AND pi.remaining_uses > 0`,
					playerID, vehItemID); err != nil {
					return fmt.Errorf("accident durability: %w", err)
				}
				if _, err := tx.Exec(ctx,
					`DELETE FROM player_items WHERE player_id = $1 AND item_id = $2 AND remaining_uses <= 0`,
					playerID, vehItemID); err != nil {
					return fmt.Errorf("drop broken vehicle: %w", err)
				}
			}
			// 迷子(設定で有効時): ローラースルーゴーゴー所持で必ず、なければ1/5。
			// 出発元がダウンタウン(3)なら迷子にならない。迷子だと街3に着く。
			if s.settings.Get().MoveMaigoEnabled && current != maigoTown {
				if hasRoller || rand.Intn(5) == 0 {
					result.Lost = true
					arrived = maigoTown
				}
			}
		}
		result.ArrivedTown = arrived
		// 現在の街を更新。
		if _, err := tx.Exec(ctx, `UPDATE players SET current_town = $1 WHERE id = $2`, arrived, playerID); err != nil {
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

// fastestVehicle returns the player's fastest owned movement vehicle (lowest
// travel time), its item id, and whether they own ローラースルーゴーゴー (which
// forces getting lost). Returns an empty name if no vehicle is owned (=徒歩)。
func (s *Service) fastestVehicle(ctx context.Context, tx pgx.Tx, playerID int64) (string, int64, bool, error) {
	rows, err := tx.Query(ctx,
		`SELECT ci.name, ci.id FROM player_items pi
		 JOIN content_items ci ON pi.item_id = ci.id
		 WHERE pi.player_id = $1 AND pi.remaining_uses > 0`, playerID)
	if err != nil {
		return "", 0, false, fmt.Errorf("read vehicles: %w", err)
	}
	defer rows.Close()
	bestName := ""
	var bestID int64
	hasRoller := false
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			return "", 0, false, fmt.Errorf("scan vehicle: %w", err)
		}
		if name == rollerItem {
			hasRoller = true
		}
		// 乗り物マップにある品のうち最速(時間が最小)のものを選ぶ。
		if t, ok := vehicleTime[name]; ok && (bestName == "" || t < vehicleTime[bestName]) {
			bestName, bestID = name, id
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, false, fmt.Errorf("iterate vehicles: %w", err)
	}
	return bestName, bestID, hasRoller, nil
}
