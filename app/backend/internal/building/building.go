// Package building holds the reference data and cost rules for the construction
// company (建設会社): the five towns and their land prices, the exterior price
// map, the interior ranks, and the house-building cost formula. It is shared by
// the action layer (which spends money and records houses) and the content
// layer (which lists the catalog for the build screen), mirroring how jobrule
// is shared. All prices here are in 万円 (×10000 円) unless noted.
package building

import (
	"fmt"
	"sync"
)

// MochiieMax is the legacy per-player house limit (mochiie_max).
const MochiieMax = 4

// yenPerMan converts a 万円 price to 円.
const yenPerMan = 10000

// Town is one of the buildable towns (town_ini.cgi). LandPrice is in 万円.
// Hidden towns (隠し町/kakushimachi) are excluded from the warp destination list
// and cannot be warped to.
type Town struct {
	No        int    `json:"no"`
	Name      string `json:"name"`
	LandPrice int    `json:"land_price"`
	Hidden    bool   `json:"hidden"`
}

// defaultTowns lists the five legacy towns (0=公園 .. 4=謎の街). Used to seed the
// editable town list when settings has none.
var defaultTowns = []Town{
	{No: 0, Name: "公園", LandPrice: 2000},
	{No: 1, Name: "シー・リゾート", LandPrice: 1000},
	{No: 2, Name: "カントリータウン", LandPrice: 500},
	{No: 3, Name: "ダウンタウン", LandPrice: 250},
	{No: 4, Name: "謎の街", LandPrice: 250},
}

// towns is the current (admin-editable) town table. Persisted in settings and
// synced here via SetTowns at startup and on admin update. Guarded by townsMu.
var (
	townsMu sync.RWMutex
	towns   = append([]Town(nil), defaultTowns...)
)

// DefaultTowns returns a copy of the legacy default town table (for seeding).
func DefaultTowns() []Town {
	out := make([]Town, len(defaultTowns))
	copy(out, defaultTowns)
	return out
}

// SetTowns replaces the runtime town table (No is reassigned by index).
func SetTowns(ts []Town) {
	next := make([]Town, len(ts))
	for i, t := range ts {
		t.No = i
		next[i] = t
	}
	townsMu.Lock()
	towns = next
	townsMu.Unlock()
}

// Towns returns a copy of the current town table.
func Towns() []Town {
	townsMu.RLock()
	defer townsMu.RUnlock()
	out := make([]Town, len(towns))
	copy(out, towns)
	return out
}

// TownCount returns the number of towns currently configured.
func TownCount() int {
	townsMu.RLock()
	defer townsMu.RUnlock()
	return len(towns)
}

// IsHidden reports whether the town at no is a hidden town (warp-excluded).
func IsHidden(no int) bool {
	townsMu.RLock()
	defer townsMu.RUnlock()
	for _, t := range towns {
		if t.No == no {
			return t.Hidden
		}
	}
	return false
}

// townByNo finds a town by its number.
func townByNo(no int) (Town, bool) {
	townsMu.RLock()
	defer townsMu.RUnlock()
	for _, t := range towns {
		if t.No == no {
			return t, true
		}
	}
	return Town{}, false
}

// Exterior is a house appearance: an image key and its price in 万円.
type Exterior struct {
	Key   string `json:"key"`   // gif名(拡張子なし)。例: house4
	Price int    `json:"price"` // 万円
}

// exteriors is the exterior price map (%ie_hash), restricted to the artwork
// bundled in the rewrite (house1-19, kamakura, bil2-5). Ordered cheapest-first
// so the build screen can present a natural progression.
var exteriors = []Exterior{
	{Key: "house1", Price: 150},
	{Key: "house2", Price: 150},
	{Key: "house3", Price: 150},
	{Key: "kamakura", Price: 150},
	{Key: "house4", Price: 800},
	{Key: "house5", Price: 800},
	{Key: "house6", Price: 800},
	{Key: "house7", Price: 1800},
	{Key: "house8", Price: 1800},
	{Key: "house9", Price: 1800},
	{Key: "house19", Price: 3200},
	{Key: "house10", Price: 3200},
	{Key: "house11", Price: 3200},
	{Key: "house12", Price: 3200},
	{Key: "house16", Price: 3600},
	{Key: "house18", Price: 3600},
	{Key: "house17", Price: 3800},
	{Key: "bil2", Price: 4000},
	{Key: "house13", Price: 4400},
	{Key: "house14", Price: 4400},
	{Key: "house15", Price: 4400},
	{Key: "bil3", Price: 4400},
	{Key: "bil4", Price: 4600},
	{Key: "bil5", Price: 4600},
}

// Exteriors returns a copy of the exterior catalog.
func Exteriors() []Exterior {
	out := make([]Exterior, len(exteriors))
	copy(out, exteriors)
	return out
}

// ExteriorPrice returns the 万円 price for an exterior key.
func ExteriorPrice(key string) (int, bool) {
	for _, e := range exteriors {
		if e.Key == key {
			return e.Price, true
		}
	}
	return 0, false
}

// Interior is a house interior rank. Slots is the number of in-house content
// slots the rank grants (used from フェーズ3). Price is in 万円.
type Interior struct {
	Rank  int    `json:"rank"`  // 0=A(最上級)..3=D
	Name  string `json:"name"`  // "A".."D"
	Price int    `json:"price"` // 万円
	Slots int    `json:"slots"` // 家内コンテンツ枠
}

// interiors lists the four interior ranks (best-first).
var interiors = []Interior{
	{Rank: 0, Name: "A", Price: 1200, Slots: 4},
	{Rank: 1, Name: "B", Price: 800, Slots: 3},
	{Rank: 2, Name: "C", Price: 400, Slots: 2},
	{Rank: 3, Name: "D", Price: 100, Slots: 1},
}

// Interiors returns a copy of the interior rank table.
func Interiors() []Interior {
	out := make([]Interior, len(interiors))
	copy(out, interiors)
	return out
}

// interiorByRank finds an interior rank by its number.
func interiorByRank(rank int) (Interior, bool) {
	for _, in := range interiors {
		if in.Rank == rank {
			return in, true
		}
	}
	return Interior{}, false
}

// SlotsByRank returns the number of in-house content slots for an interior rank
// (A=4..D=1)。レガシー: rank0/空=4枠。範囲外は最小の1枠。
func SlotsByRank(rank int) int {
	if in, ok := interiorByRank(rank); ok {
		return in.Slots
	}
	return 1
}

// IsHouseContentKind reports whether k is a valid in-house content kind.
// ""=非公開(枠を使わない), "bbs"=通常掲示板, "shop"=お店, "nushi"=家主板,
// "url"=独自URL(dokuzi_url)。
func IsHouseContentKind(k string) bool {
	switch k {
	case "", "bbs", "shop", "nushi", "url":
		return true
	}
	return false
}

// BuildCost returns the construction cost in 円 for a house. houseCount is the
// number of houses the player already owns (0 = building the first, i.e. マイホーム).
//
//	1軒目:      地価 + 外装 + 内装(A-D)
//	2軒目以降:  地価 + 外装×2  (tuika=家のみ; 運営/株式会社/持ち物店はフェーズ4以降)
func BuildCost(townNo int, exterior string, interiorRank, houseCount int) (int64, error) {
	t, ok := townByNo(townNo)
	if !ok {
		return 0, fmt.Errorf("unknown town %d", townNo)
	}
	ext, ok := ExteriorPrice(exterior)
	if !ok {
		return 0, fmt.Errorf("unknown exterior %q", exterior)
	}
	var man int
	if houseCount == 0 {
		in, ok := interiorByRank(interiorRank)
		if !ok {
			return 0, fmt.Errorf("unknown interior rank %d", interiorRank)
		}
		man = t.LandPrice + ext + in.Price
	} else {
		man = t.LandPrice + ext*2
	}
	return int64(man) * yenPerMan, nil
}

// RebuildCost returns the cost in 円 to rebuild an existing house with a new
// exterior and interior rank (建て替え). The land price is excluded because it
// was already paid when the plot was first built on; this is charged in cash.
func RebuildCost(exterior string, interiorRank int) (int64, error) {
	ext, ok := ExteriorPrice(exterior)
	if !ok {
		return 0, fmt.Errorf("unknown exterior %q", exterior)
	}
	in, ok := interiorByRank(interiorRank)
	if !ok {
		return 0, fmt.Errorf("unknown interior rank %d", interiorRank)
	}
	return int64(ext+in.Price) * yenPerMan, nil
}

// SellValue returns the refund in 円 when a house is demolished/sold: the town's
// land price (地価×10000). Used from フェーズ2c.
func SellValue(townNo int) (int64, error) {
	t, ok := townByNo(townNo)
	if !ok {
		return 0, fmt.Errorf("unknown town %d", townNo)
	}
	return int64(t.LandPrice) * yenPerMan, nil
}

// shopKinds are the sellable shop categories (店の種類, town_ini.cgi:112).
// アダルト/DVDは対象外として除外している。
var shopKinds = []string{
	"スーパー", "書籍", "食料品", "薬", "スポーツ用品", "電化製品", "美容",
	"ファーストフード", "日用品", "お花", "デザート", "ギフト", "アルコール",
	"ゲーム", "ドリンク", "秘密の商品", "沖縄名産店", "サンリオ", "ペット",
}

// ShopKinds returns a copy of the shop category list.
func ShopKinds() []string {
	out := make([]string, len(shopKinds))
	copy(out, shopKinds)
	return out
}

// IsShopKind reports whether s is a valid shop category.
func IsShopKind(s string) bool {
	for _, k := range shopKinds {
		if k == s {
			return true
		}
	}
	return false
}

// SuperMarketKind is the one category that can stock every kind, at a 1.5×
// purchase-cost premium (スーパー).
const SuperMarketKind = "スーパー"

// Shop markup (掛け率) bounds (レガシー忠実): 0.3 < markup <= 3.0.
const (
	ShopMarkupDefault = 2.0
	ShopMarkupMax     = 3.0
	ShopMarkupMin     = 0.3
)

// Shop stock limits (レガシー忠実): 40 kinds per shop, 80 per item.
const (
	ShopMaxKinds = 40
	ShopMaxStock = 80
)
