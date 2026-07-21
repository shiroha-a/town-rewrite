<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import {
  api,
  type Player,
  type BuildingState,
  type TownFacility,
  type MyHouse,
  type OrosiState,
  type OrosiItem,
  type ShopStockView,
  type ShopStockItem,
} from '../api';
import Toast from './Toast.vue';
import { useToast } from '../toast';

// 建設会社(建築系フェーズ2a): 5つの街の空地に家を建てる。建築費は普通口座から
// 引き落とす。1軒目は地価+外装+内装、2軒目以降は地価+外装×2。1人4軒まで。
const props = defineProps<{ player: Player }>();
// visit: 家をクリックしたとき家訪問画面(HouseView)を開く。
const emit = defineEmits<{ update: [player: Player]; back: []; visit: [houseId: number] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const state = ref<BuildingState | null>(null);
const facilities = ref<TownFacility[]>([]); // 全街の施設(選択中の街ぶんを描画)
const message = ref('');
const busy = ref(false);
const { toast, showToast, closeToast } = useToast();

const selectedTown = ref(0);
const selectedCell = ref<{ row: number; col: number } | null>(null);
const selectedExterior = ref('');
const selectedInterior = ref(3); // 既定はD(最安)

async function refresh() {
  state.value = await api.building(props.player.id);
  if (!selectedExterior.value && state.value.exteriors.length > 0) {
    selectedExterior.value = state.value.exteriors[0].key;
  }
  syncDrafts();
}

onMounted(async () => {
  try {
    const [f] = await Promise.all([api.townMap(), refresh()]);
    facilities.value = f;
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
  }
});

// グリッド範囲(row 0..rows-1 = A..L, col 1..cols)。
const rowRange = computed(() => Array.from({ length: state.value?.rows ?? 0 }, (_, i) => i));
const colRange = computed(() => Array.from({ length: state.value?.cols ?? 0 }, (_, i) => i + 1));
const gridStyle = computed(() => ({
  gridTemplateColumns: `repeat(${state.value?.cols ?? 0}, 30px)`,
}));
const rowLabel = (row: number) => String.fromCharCode(65 + row);

const isFirstHouse = computed(() => (state.value?.house_count ?? 0) === 0);
const townName = (no: number) => state.value?.towns.find((t) => t.no === no)?.name ?? '';
const townPlotCount = computed(
  () => state.value?.plots.filter((p) => p.town === selectedTown.value).length ?? 0,
);

// 選択中の街の施設セル(施設はマルチ街化済み)。空き地(akichi)は建築マスとして
// plotAtで別扱いするので、施設アイコンからは除外する。
function facilityAt(row: number, col: number): TownFacility | undefined {
  return facilities.value.find(
    (f) => f.key !== 'akichi' && f.town === selectedTown.value && f.row === row && f.col === col,
  );
}
function houseAt(row: number, col: number) {
  return state.value?.houses.find(
    (h) => h.town === selectedTown.value && h.row === row && h.col === col,
  );
}
// 管理者が空地に指定したマスか。
function plotAt(row: number, col: number): boolean {
  return (
    state.value?.plots.some(
      (p) => p.town === selectedTown.value && p.row === row && p.col === col,
    ) ?? false
  );
}
function cellClass(row: number, col: number) {
  const sel = selectedCell.value;
  const fac = !!facilityAt(row, col);
  const hou = !!houseAt(row, col);
  return {
    facility: fac,
    house: hou,
    own: houseAt(row, col)?.own ?? false,
    selected: !!sel && sel.row === row && sel.col === col,
    empty: plotAt(row, col) && !fac && !hou, // 建築可能な空地
  };
}
function cellImg(row: number, col: number): string | null {
  const f = facilityAt(row, col);
  if (f) return `/img/${f.img}.gif`;
  const h = houseAt(row, col);
  if (h) return `/img/${h.exterior}.gif`;
  return null;
}
function cellTitle(row: number, col: number): string {
  const f = facilityAt(row, col);
  if (f) return f.alt;
  const h = houseAt(row, col);
  if (h) return `${h.owner_name}さんの家`;
  if (plotAt(row, col)) return `${rowLabel(row)}${col}（空地）`;
  return `${rowLabel(row)}${col}`;
}
function clickCell(row: number, col: number) {
  const h = houseAt(row, col);
  if (h) {
    // 家 → 家訪問画面(HouseView)を開く
    emit('visit', h.id);
    return;
  }
  // 空地に指定されたマス(施設・家なし)だけ建築選択できる。
  if (!plotAt(row, col) || facilityAt(row, col)) return;
  selectedCell.value = { row, col };
}
function selectTown(no: number) {
  selectedTown.value = no;
  selectedCell.value = null;
}

// 建築費プレビュー(building.BuildCostと同じ式。単位:円)。
const cost = computed(() => {
  const s = state.value;
  if (!s) return 0;
  const town = s.towns.find((t) => t.no === selectedTown.value);
  const ext = s.exteriors.find((e) => e.key === selectedExterior.value);
  if (!town || !ext) return 0;
  let man = 0;
  if (isFirstHouse.value) {
    const inte = s.interiors.find((i) => i.rank === selectedInterior.value);
    if (!inte) return 0;
    man = town.land_price + ext.price + inte.price;
  } else {
    man = town.land_price + ext.price * 2;
  }
  return man * 10000;
});

async function build() {
  if (!selectedCell.value || !state.value) return;
  busy.value = true;
  const c = cost.value;
  try {
    const after = await api.buildHouse(
      props.player.id,
      selectedTown.value,
      selectedCell.value.row,
      selectedCell.value.col,
      selectedExterior.value,
      isFirstHouse.value ? selectedInterior.value : 0,
    );
    emit('update', after);
    await refresh();
    selectedCell.value = null;
    showToast({
      variant: 'item',
      title: '家を建てた',
      lines: [`建築費 ${yen(c)}円を普通口座から支払いました`],
      icon: 'item',
    });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '建てられませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}

// 建て替え・売却(フェーズ2c)
const rebuildingId = ref<number | null>(null);
const rebuildExterior = ref('');
const rebuildInterior = ref(0);
// 建て替え費用(外装+内装)×10000。地価は既払いのため含めない。
const rebuildCost = computed(() => {
  const ext = state.value?.exteriors.find((e) => e.key === rebuildExterior.value);
  const inte = state.value?.interiors.find((i) => i.rank === rebuildInterior.value);
  if (!ext || !inte) return 0;
  return (ext.price + inte.price) * 10000;
});
// 売却の返金額(地価×10000)。
function sellRefund(town: number): number {
  const t = state.value?.towns.find((x) => x.no === town);
  return t ? t.land_price * 10000 : 0;
}
function startRebuild(h: MyHouse) {
  rebuildingId.value = h.id;
  rebuildExterior.value = h.exterior;
  rebuildInterior.value = h.interior_rank;
}
function cancelRebuild() {
  rebuildingId.value = null;
}
async function doRebuild(h: MyHouse) {
  busy.value = true;
  const c = rebuildCost.value;
  try {
    const after = await api.rebuildHouse(props.player.id, h.id, rebuildExterior.value, rebuildInterior.value);
    emit('update', after);
    await refresh();
    rebuildingId.value = null;
    showToast({
      variant: 'item',
      title: '家を建て替えた',
      lines: [`建て替え費用 ${yen(c)}円を現金で支払いました`],
      icon: 'item',
    });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '建て替えできませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}
async function doSell(h: MyHouse) {
  const refund = sellRefund(h.town);
  const ok = window.confirm(
    `${townName(h.town)}／${rowLabel(h.row)}${h.col}の家を売却しますか？\n地価分 ${yen(refund)}円が現金で戻ります(外装・内装費は戻りません)。`,
  );
  if (!ok) return;
  busy.value = true;
  try {
    const after = await api.sellHouse(props.player.id, h.id);
    emit('update', after);
    await refresh();
    if (rebuildingId.value === h.id) rebuildingId.value = null;
    showToast({
      variant: 'item',
      title: '家を売却した',
      lines: [`地価分 ${yen(refund)}円が現金で戻りました`],
      icon: 'item',
    });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '売却できませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}

// 家コメント設定(フェーズ3a)
const commentDrafts = ref<Record<number, string>>({});

function syncDrafts() {
  const d: Record<number, string> = {};
  for (const h of state.value?.my_houses ?? []) d[h.id] = h.setumei ?? '';
  commentDrafts.value = d;
  // コンテンツ枠の編集ドラフト(枠数ぶんの行。未設定枠はkind空=公開しない)。
  const cd: Record<number, { kind: string; title: string; url: string }[]> = {};
  for (const h of state.value?.my_houses ?? []) {
    const rows: { kind: string; title: string; url: string }[] = [];
    for (let s = 0; s < h.slots; s++) {
      const c = h.contents.find((x) => x.slot === s);
      rows.push({ kind: c?.kind ?? '', title: c?.title ?? '', url: c?.url ?? '' });
    }
    cd[h.id] = rows;
  }
  contentDrafts.value = cd;
}

// コンテンツ枠の設定(レガシー my_house_settei)。枠数は内装ランクで決まる。
const CONTENT_KINDS = [
  { value: '', label: '公開しない' },
  { value: 'bbs', label: '通常掲示板' },
  { value: 'shop', label: 'お店' },
  { value: 'nushi', label: '家主板' },
  { value: 'url', label: '独自URL' },
];
const contentDrafts = ref<Record<number, { kind: string; title: string; url: string }[]>>({});
async function saveContents(h: MyHouse) {
  busy.value = true;
  try {
    const rows = contentDrafts.value[h.id] ?? [];
    const contents = rows.map((r, s) => ({ slot: s, kind: r.kind, title: r.title, url: r.url }));
    const after = await api.setHouseContents(props.player.id, h.id, contents);
    emit('update', after);
    await refresh();
    showToast({
      variant: 'item',
      title: 'コンテンツを保存しました',
      lines: [`${townName(h.town)}／${rowLabel(h.row)}${h.col} の公開コンテンツを更新しました`],
      icon: 'item',
    });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '保存できませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}
async function saveComment(h: MyHouse) {
  busy.value = true;
  try {
    const after = await api.setHouseComment(props.player.id, h.id, commentDrafts.value[h.id] ?? '');
    emit('update', after);
    await refresh();
    showToast({
      variant: 'item',
      title: 'コメントを保存しました',
      lines: [`${townName(h.town)}／${rowLabel(h.row)}${h.col} のコメントを更新しました`],
      icon: 'item',
    });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '保存できませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}

// 家の店 設定(フェーズ4a)
const shopEditId = ref<number | null>(null);
const shopDraft = ref<{ title: string; syubetu: string; markup: number }>({
  title: '',
  syubetu: '',
  markup: 2,
});
function startShop(h: MyHouse) {
  shopEditId.value = h.id;
  shopDraft.value = {
    title: h.shop_title || '',
    syubetu: h.shop_kind || state.value?.shop_kinds[0] || '',
    markup: h.shop_markup || 2,
  };
}
async function saveShop(h: MyHouse) {
  busy.value = true;
  try {
    const after = await api.openHouseShop(
      props.player.id,
      h.id,
      shopDraft.value.title,
      shopDraft.value.syubetu,
      shopDraft.value.markup,
    );
    emit('update', after);
    await refresh();
    shopEditId.value = null;
    showToast({
      variant: 'item',
      title: '店を設定しました',
      lines: [`${shopDraft.value.syubetu}の店を開きました（掛け率${shopDraft.value.markup}倍）`],
      icon: 'item',
    });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '店を設定できませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}

// 卸問屋で仕入れ(フェーズ4b)
const orosiState = ref<OrosiState | null>(null);
const orosiHouseId = ref<number | null>(null);
const shiireQty = ref<Record<number, number>>({});

async function startOrosi(h: MyHouse) {
  try {
    orosiState.value = await api.orosi(props.player.id, h.id);
    orosiHouseId.value = h.id;
  } catch (e) {
    showToast({
      variant: 'error',
      title: '卸問屋を開けませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  }
}
function closeOrosi() {
  orosiState.value = null;
  orosiHouseId.value = null;
}
async function doShiire(it: OrosiItem) {
  if (!orosiHouseId.value) return;
  const qty = shiireQty.value[it.item_id] || 1;
  busy.value = true;
  try {
    const after = await api.shiire(props.player.id, orosiHouseId.value, it.item_id, qty);
    emit('update', after);
    orosiState.value = await api.orosi(props.player.id, orosiHouseId.value);
    await refresh();
    showToast({
      variant: 'item',
      title: '仕入れました',
      lines: [`${it.name} を${qty}個 仕入れました`],
      icon: 'item',
    });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '仕入れできませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}

// 個別価格設定(フェーズ4c仕上げ)
const priceStock = ref<ShopStockView | null>(null);
const priceHouseId = ref<number | null>(null);
const priceDraft = ref<Record<number, number>>({});

async function startPrice(h: MyHouse) {
  try {
    priceStock.value = await api.houseShopStock(props.player.id, h.id);
    priceHouseId.value = h.id;
    const d: Record<number, number> = {};
    for (const it of priceStock.value.items) d[it.item_id] = it.shelf;
    priceDraft.value = d;
  } catch (e) {
    showToast({
      variant: 'error',
      title: '価格設定を開けませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  }
}
function closePrice() {
  priceStock.value = null;
  priceHouseId.value = null;
}
async function savePrice(it: ShopStockItem) {
  if (!priceHouseId.value) return;
  const price = priceDraft.value[it.item_id] ?? it.shelf;
  busy.value = true;
  try {
    const after = await api.setHouseShopPrice(props.player.id, priceHouseId.value, it.item_id, price);
    emit('update', after);
    priceStock.value = await api.houseShopStock(props.player.id, priceHouseId.value);
    showToast({
      variant: 'item',
      title: '価格を設定しました',
      lines: [`${it.name} を${yen(price)}円に設定しました`],
      icon: 'item',
    });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '設定できませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="kentiku-page facility-page">
    <Toast :toast="toast" @close="closeToast" />
    <button class="btn back" @click="emit('back')">街に戻る</button>

    <div class="kentiku-header">
      <div class="lead">
        建設会社です。街の空地に家を建てられます。<br />
        1軒目は「地価＋外装＋内装」、2軒目以降は「地価＋外装×2」の建築費が<b>普通口座</b>から引き落とされます（1人{{ state?.mochiie_max ?? 4 }}軒まで）。
      </div>
      <div class="title">建設会社</div>
    </div>

    <div v-if="message" class="message error" data-test="message">{{ message }}</div>

    <template v-if="state">
      <!-- 街タブ -->
      <div class="town-tabs">
        <button
          v-for="t in state.towns"
          :key="t.no"
          class="tab"
          :class="{ active: selectedTown === t.no }"
          @click="selectTown(t.no)"
        >
          {{ t.name }}<span class="tika">地価{{ t.land_price }}万</span>
        </button>
      </div>

      <!-- 街グリッド(空地クリックで選択) -->
      <div class="grid-scroll">
        <div class="grid" :style="gridStyle">
          <template v-for="row in rowRange" :key="row">
            <div
              v-for="col in colRange"
              :key="`${row}-${col}`"
              class="cell"
              :class="cellClass(row, col)"
              :title="cellTitle(row, col)"
              @click="clickCell(row, col)"
            >
              <img v-if="cellImg(row, col)" :src="cellImg(row, col)!" :alt="cellTitle(row, col)" />
            </div>
          </template>
        </div>
      </div>

      <!-- 建築フォーム -->
      <div v-if="selectedCell" class="build-form panel-white">
        <div class="row">
          <span class="lbl">建築位置</span>
          <span class="val">{{ townName(selectedTown) }}／{{ rowLabel(selectedCell.row) }}{{ selectedCell.col }}</span>
        </div>
        <div class="row">
          <span class="lbl">外装</span>
          <select v-model="selectedExterior" class="sel">
            <option v-for="e in state.exteriors" :key="e.key" :value="e.key">
              {{ e.key }}（{{ e.price }}万）
            </option>
          </select>
          <img class="preview" :src="`/img/${selectedExterior}.gif`" :alt="selectedExterior" />
        </div>
        <div v-if="isFirstHouse" class="row">
          <span class="lbl">内装</span>
          <select v-model.number="selectedInterior" class="sel">
            <option v-for="i in state.interiors" :key="i.rank" :value="i.rank">
              {{ i.name }}（{{ i.price }}万・枠{{ i.slots }}）
            </option>
          </select>
        </div>
        <div v-else class="row note">2軒目以降は内装を選べません（家のみ）。</div>
        <div class="row cost-row">
          <span class="lbl">建築費</span>
          <span class="cost">{{ yen(cost) }}円</span>
        </div>
        <div class="row">
          <button class="btn build-btn" :disabled="busy" @click="build">この場所に建てる</button>
        </div>
      </div>
      <div v-else class="hint">
        <template v-if="townPlotCount === 0">
          この街にはまだ空地が設定されていません（管理者が空地を設定すると建てられます）。
        </template>
        <template v-else>グリッドの空地（緑）をクリックして建築する場所を選んでください。</template>
      </div>

      <!-- 自分の家一覧 -->
      <div class="my-houses panel-white">
        <div class="mh-head">自分の家（{{ state.house_count }}／{{ state.mochiie_max }}軒）</div>
        <div v-if="state.my_houses.length === 0" class="mh-empty">まだ家を持っていません。</div>
        <ul v-else class="mh-list">
          <li v-for="h in state.my_houses" :key="h.id" class="mh-item">
            <div class="mh-row">
              <img :src="`/img/${h.exterior}.gif`" :alt="h.exterior" />
              <span class="mh-loc">{{ townName(h.town) }}／{{ rowLabel(h.row) }}{{ h.col }}</span>
              <span class="mh-ext">{{ h.exterior }}</span>
              <span class="mh-spacer"></span>
              <button class="btn mini" :disabled="busy" @click="startRebuild(h)">建て替え</button>
              <button class="btn mini danger" :disabled="busy" @click="doSell(h)">売却</button>
            </div>
            <div v-if="rebuildingId === h.id" class="mh-rebuild">
              <label class="mh-field">外装
                <select v-model="rebuildExterior">
                  <option v-for="e in state.exteriors" :key="e.key" :value="e.key">
                    {{ e.key }}（{{ e.price }}万）
                  </option>
                </select>
              </label>
              <label class="mh-field">内装
                <select v-model.number="rebuildInterior">
                  <option v-for="i in state.interiors" :key="i.rank" :value="i.rank">
                    {{ i.name }}（{{ i.price }}万）
                  </option>
                </select>
              </label>
              <span class="mh-cost">建て替え費用 {{ yen(rebuildCost) }}円（現金）</span>
              <button class="btn mini build-btn" :disabled="busy" @click="doRebuild(h)">建て替える</button>
              <button class="btn mini" :disabled="busy" @click="cancelRebuild">やめる</button>
            </div>
            <div class="mh-comment">
              <input
                v-model="commentDrafts[h.id]"
                maxlength="40"
                placeholder="家のコメント(40字・訪問者に表示)"
                class="mh-cinput"
              />
              <button class="btn mini" :disabled="busy" @click="saveComment(h)">コメント保存</button>
            </div>
            <!-- コンテンツ枠(内装ランクで枠数が決まる)。設定した枠だけ訪問者に表示。 -->
            <div class="mh-contents">
              <div class="mh-contents-head">コンテンツ枠（内装{{ ['A','B','C','D'][h.interior_rank] ?? '?' }}ランク・{{ h.slots }}枠）</div>
              <div class="mh-contents-note">一番上の枠のコンテンツが、家に入ったとき最初に表示されます。タイトルは訪問画面のボタンに表示されます。</div>
              <div v-for="(row, s) in contentDrafts[h.id]" :key="s" class="mh-content-row">
                <span class="slot-no">枠{{ s + 1 }}</span>
                <select v-model="row.kind">
                  <option v-for="k in CONTENT_KINDS" :key="k.value" :value="k.value">{{ k.label }}</option>
                </select>
                <input
                  v-if="row.kind"
                  v-model="row.title"
                  maxlength="20"
                  class="mh-cinput slot-title"
                  placeholder="タイトル(省略可)"
                />
                <input
                  v-if="row.kind === 'url'"
                  v-model="row.url"
                  class="mh-cinput slot-url"
                  placeholder="https://…(埋め込むURL)"
                />
              </div>
              <button class="btn mini" :disabled="busy" @click="saveContents(h)">コンテンツ保存</button>
            </div>
            <div class="mh-shop">
              <span v-if="h.has_shop" class="shop-badge">
                店: {{ h.shop_title || '(無題)' }}／{{ h.shop_kind }}／掛け率{{ h.shop_markup }}倍
              </span>
              <span v-else class="shop-none">この家に店はありません</span>
              <button class="btn mini" :disabled="busy" @click="startShop(h)">
                {{ h.has_shop ? '店設定を変更' : '店を開く' }}
              </button>
              <button v-if="h.has_shop" class="btn mini" :disabled="busy" @click="startOrosi(h)">
                仕入れる
              </button>
              <button v-if="h.has_shop" class="btn mini" :disabled="busy" @click="startPrice(h)">
                価格設定
              </button>
            </div>
            <div v-if="shopEditId === h.id" class="shop-form">
              <label class="mh-field">店名
                <input v-model="shopDraft.title" maxlength="50" class="mh-cinput" />
              </label>
              <label class="mh-field">種類
                <select v-model="shopDraft.syubetu">
                  <option v-for="k in state.shop_kinds" :key="k" :value="k">{{ k }}</option>
                </select>
              </label>
              <label class="mh-field">掛け率
                <input
                  v-model.number="shopDraft.markup"
                  type="number"
                  step="0.1"
                  min="0.3"
                  max="3"
                  class="markup-input"
                />倍
              </label>
              <button class="btn mini build-btn" :disabled="busy" @click="saveShop(h)">保存</button>
              <button class="btn mini" :disabled="busy" @click="shopEditId = null">やめる</button>
            </div>
          </li>
        </ul>
      </div>

      <!-- 卸問屋(仕入れ) -->
      <div v-if="orosiState" class="orosi-panel panel-white">
        <div class="orosi-head">
          <span class="orosi-title">卸問屋（{{ orosiState.syubetu }}）</span>
          <span class="orosi-info">
            普通口座 {{ yen(orosiState.savings) }}円／在庫種類 {{ orosiState.stock_kinds }}／{{ orosiState.max_kinds }}
          </span>
          <button class="btn mini" @click="closeOrosi">閉じる</button>
        </div>
        <div v-if="orosiState.items.length === 0" class="orosi-empty">仕入れられる商品がありません。</div>
        <div v-else class="orosi-scroll">
          <table class="orosi-table">
            <thead>
              <tr>
                <th class="l">品名</th>
                <th>種類</th>
                <th>仕入れ値</th>
                <th>店在庫</th>
                <th>数量</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="it in orosiState.items" :key="it.item_id">
                <td class="l">{{ it.name }}</td>
                <td>{{ it.category }}</td>
                <td class="price">{{ yen(it.buy_price) }}円</td>
                <td :class="{ full: it.in_stock >= orosiState.max_stock }">
                  {{ it.in_stock }}/{{ orosiState.max_stock }}
                </td>
                <td>
                  <input
                    v-model.number="shiireQty[it.item_id]"
                    type="number"
                    min="1"
                    :max="orosiState.max_stock"
                    class="qty-input"
                  />
                </td>
                <td>
                  <button
                    class="btn mini"
                    :disabled="busy || it.in_stock >= orosiState.max_stock"
                    @click="doShiire(it)"
                  >
                    仕入れる
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- 個別価格設定 -->
      <div v-if="priceStock && priceStock.has_shop" class="orosi-panel panel-white">
        <div class="orosi-head">
          <span class="orosi-title">価格設定</span>
          <span class="orosi-info">掛け率{{ priceStock.markup }}倍／0円で掛け率に戻す</span>
          <button class="btn mini" @click="closePrice">閉じる</button>
        </div>
        <div v-if="priceStock.items.length === 0" class="orosi-empty">
          在庫がありません。まず仕入れてください。
        </div>
        <div v-else class="orosi-scroll">
          <table class="orosi-table">
            <thead>
              <tr>
                <th class="l">品名</th>
                <th>仕入れ値</th>
                <th>上限(×3)</th>
                <th>店頭価格</th>
                <th>新価格</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="it in priceStock.items" :key="it.item_id">
                <td class="l">{{ it.name }}</td>
                <td class="price">{{ yen(it.buy_price) }}円</td>
                <td>{{ yen(it.max_price) }}円</td>
                <td class="price">{{ yen(it.shelf) }}円{{ it.sell_price === null ? '(掛率)' : '' }}</td>
                <td>
                  <input
                    v-model.number="priceDraft[it.item_id]"
                    type="number"
                    min="0"
                    :max="it.max_price"
                    class="qty-input"
                  />
                </td>
                <td>
                  <button class="btn mini" :disabled="busy" @click="savePrice(it)">設定</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>

    <div style="text-align: center; margin-top: 8px">
      <button class="btn" @click="emit('back')">街に戻る</button>
    </div>
  </div>
</template>

<style scoped>
.kentiku-page {
  background-color: #d8e8c8;
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  margin-bottom: 6px;
}
.kentiku-header {
  display: flex;
  margin-bottom: 8px;
}
.kentiku-header .lead {
  flex: 1 1 auto;
  background: #fff;
  border: 1px solid #999;
  padding: 8px 12px;
  font-size: 12px;
  color: #333;
  line-height: 1.6;
}
.kentiku-header .title {
  flex: 0 0 130px;
  background: #4a7a2a;
  color: #fff;
  font-weight: bold;
  font-size: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid #999;
}
.panel-white {
  background: #fff;
  border: 1px solid #999;
  padding: 8px;
  margin-top: 8px;
}
.town-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-bottom: 6px;
}
.town-tabs .tab {
  background: #eef3e8;
  border: 1px solid #99a;
  padding: 4px 8px;
  font-size: 12px;
  cursor: pointer;
  color: #234;
}
.town-tabs .tab.active {
  background: #4a7a2a;
  color: #fff;
  font-weight: bold;
}
.town-tabs .tab .tika {
  margin-left: 4px;
  font-size: 10px;
  opacity: 0.8;
}
.grid-scroll {
  overflow-x: auto;
  background: #fff;
  border: 1px solid #999;
  padding: 6px;
  width: max-content;
  max-width: 100%;
  box-sizing: border-box;
}
.grid {
  display: grid;
  gap: 1px;
  background: #cfe0c0;
  width: max-content;
}
.cell {
  width: 30px;
  height: 30px;
  background: #e6e6e6;
  border: 1px solid #cfcfcf;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}
.cell.empty {
  background: #d6f0c0;
  border-color: #a8d488;
  cursor: pointer;
}
.cell.empty:hover {
  background: #bfe6a0;
}
.cell.facility {
  background: #dfe6ee;
  cursor: not-allowed;
}
.cell.house {
  background: #fff6e0;
  cursor: pointer;
}
.cell.house.own {
  outline: 2px solid #cc7a00;
  outline-offset: -2px;
}
.cell.selected {
  background: #ffd27a;
  outline: 2px solid #cc3300;
  outline-offset: -2px;
}
.cell img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}
.build-form .row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 4px 0;
  font-size: 13px;
}
.build-form .lbl {
  flex: 0 0 64px;
  color: #456;
  font-weight: bold;
}
.build-form .sel {
  font-size: 13px;
  padding: 2px 4px;
}
.build-form .preview {
  width: 40px;
  height: 40px;
  object-fit: contain;
  border: 1px solid #ccc;
  background: #fafafa;
}
.build-form .note {
  color: #888;
  font-size: 12px;
}
.build-form .cost-row .cost {
  color: #cc3300;
  font-weight: bold;
  font-size: 15px;
}
.build-btn {
  background: #4a7a2a;
  color: #fff;
  font-weight: bold;
}
.hint {
  margin-top: 8px;
  font-size: 12px;
  color: #567;
  text-align: center;
}
.visit-panel {
  margin-top: 8px;
}
.visit-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.visit-head img {
  width: 36px;
  height: 36px;
  object-fit: contain;
}
.visit-info {
  flex: 1 1 auto;
}
.visit-owner {
  font-weight: bold;
  color: #345;
}
.visit-loc {
  font-size: 11px;
  color: #789;
}
.visit-comment {
  margin-top: 6px;
  font-size: 13px;
  color: #446;
  background: #f4f8ec;
  border-left: 3px solid #a8d488;
  padding: 4px 8px;
}
.visit-note {
  margin-top: 6px;
  font-size: 12px;
  color: #888;
}
.saisen-box {
  margin-top: 8px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding-top: 6px;
  border-top: 1px dashed #cde;
}
.saisen-label {
  font-weight: bold;
  color: #b5651d;
}
.saisen-btn {
  background: #b5651d;
  color: #fff;
  font-weight: bold;
}
.mh-comment {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
}
.mh-cinput {
  flex: 1 1 auto;
  font-size: 12px;
  padding: 2px 4px;
}
/* コンテンツ枠エディタ(内装ランクで枠数が決まる)。 */
.mh-contents {
  margin-top: 6px;
  border: 1px dashed #cfd8c0;
  padding: 6px;
}
.mh-contents-head {
  font-size: 12px;
  font-weight: bold;
  color: #4a7a2a;
  margin-bottom: 4px;
}
.mh-content-row {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 4px;
}
.mh-content-row .slot-no {
  font-size: 11px;
  color: #667;
  flex: 0 0 auto;
}
.mh-content-row select {
  font-size: 12px;
}
.slot-title {
  max-width: 180px;
}
.slot-url {
  max-width: 240px;
}
.mh-contents-note {
  font-size: 11px;
  color: #889;
  margin-bottom: 4px;
}
.mh-shop {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
}
.shop-badge {
  font-size: 12px;
  color: #7a4a00;
  background: #fff3e0;
  border: 1px solid #e0c080;
  padding: 2px 6px;
  flex: 1 1 auto;
}
.shop-none {
  font-size: 12px;
  color: #999;
  flex: 1 1 auto;
}
.shop-form {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px dashed #cde0bc;
  font-size: 12px;
}
.markup-input {
  width: 56px;
  font-size: 12px;
  padding: 2px 4px;
}
.orosi-panel {
  margin-top: 8px;
}
.orosi-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}
.orosi-title {
  font-weight: bold;
  color: #7a4a00;
  font-size: 14px;
}
.orosi-info {
  font-size: 12px;
  color: #567;
  flex: 1 1 auto;
}
.orosi-empty {
  font-size: 12px;
  color: #888;
}
.orosi-scroll {
  overflow-x: auto;
}
.orosi-table {
  border-collapse: collapse;
  font-size: 12px;
  white-space: nowrap;
  width: 100%;
}
.orosi-table th {
  background: #f0e0c0;
  color: #634;
  padding: 3px 6px;
  border: 1px solid #dc9;
}
.orosi-table td {
  padding: 3px 6px;
  border: 1px solid #eee;
  text-align: center;
}
.orosi-table th.l,
.orosi-table td.l {
  text-align: left;
}
.orosi-table td.price {
  color: #cc3300;
  text-align: right;
}
.orosi-table td.full {
  color: #cc0000;
  font-weight: bold;
}
.qty-input {
  width: 50px;
  font-size: 12px;
  padding: 2px;
}
.visit-shop {
  margin-top: 8px;
  padding-top: 6px;
  border-top: 1px dashed #cde;
}
.vs-title {
  font-weight: bold;
  color: #7a4a00;
  margin-bottom: 4px;
}
.visit-bbs {
  margin-top: 8px;
  padding-top: 6px;
  border-top: 1px dashed #cde;
}
.bbs-form {
  display: flex;
  gap: 6px;
  margin-bottom: 4px;
}
.bbs-input {
  flex: 1 1 auto;
  font-size: 12px;
  padding: 2px 4px;
}
.bbs-list {
  list-style: none;
  margin: 0 0 8px;
  padding: 0;
  font-size: 12px;
}
.bbs-list li {
  padding: 2px 0;
  border-bottom: 1px solid #eee;
  color: #445;
}
.bbs-author {
  font-weight: bold;
  color: #367;
}
.bbs-del {
  border: none;
  background: none;
  color: #c44;
  cursor: pointer;
  font-weight: bold;
}
.bbs-empty {
  color: #999;
}
.my-houses .mh-head {
  font-weight: bold;
  color: #345;
  margin-bottom: 6px;
  border-bottom: 1px solid #cde;
  padding-bottom: 3px;
}
.my-houses .mh-empty {
  font-size: 12px;
  color: #888;
}
.mh-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.mh-item {
  background: #f6faf0;
  border: 1px solid #cde0bc;
  padding: 4px 6px;
  font-size: 12px;
}
.mh-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.mh-row img {
  width: 28px;
  height: 28px;
  object-fit: contain;
}
.mh-spacer {
  flex: 1 1 auto;
}
.mh-rebuild {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
  padding-top: 6px;
  border-top: 1px dashed #cde0bc;
}
.mh-field {
  display: flex;
  align-items: center;
  gap: 3px;
}
.mh-cost {
  color: #cc3300;
  font-weight: bold;
}
.btn.mini {
  padding: 2px 6px;
  font-size: 11px;
}
.btn.danger {
  background: #c44;
  color: #fff;
}
.mh-loc {
  font-weight: bold;
  color: #345;
}
.mh-ext {
  color: #888;
  font-size: 11px;
}
.message.error {
  background: #ffecec;
  border: 1px solid #e0a0a0;
  color: #b00;
  padding: 6px 10px;
  font-size: 12px;
  margin-bottom: 6px;
}
</style>
