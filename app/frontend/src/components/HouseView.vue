<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import {
  api,
  type Player,
  type Town,
  type HouseCell,
  type HouseContent,
  type HouseShopView,
  type HouseShopItem,
  type BbsPost,
} from '../api';
import Toast from './Toast.vue';
import { useToast } from '../toast';
import { PARAM_LABEL } from '../params';

// 家訪問(レガシー original_house.cgi houmon)。タブ切替型:
// 公開コンテンツ枠ごとにボタンが並び、1画面に1コンテンツを表示する。
// 入室時は一番上の枠のコンテンツが初期表示(レガシー my_con0)。
const props = defineProps<{ player: Player; houseId: number }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const busy = ref(false);
const message = ref('');
const { toast, showToast, closeToast } = useToast();

const house = ref<HouseCell | null>(null);
const allHouses = ref<HouseCell[]>([]);
const townList = ref<Town[]>([]);
const townName = (no: number) => townList.value.find((t) => t.no === no)?.name ?? '';
const rowLabel = (row: number) => String.fromCharCode(65 + row);

// コンテンツ枠(枠順)。タブとして描画し、selectedSlotの枠を表示する。
const contents = computed<HouseContent[]>(() => house.value?.contents ?? []);
const selectedSlot = ref<number | null>(null);
const current = computed<HouseContent | null>(
  () => contents.value.find((c) => c.slot === selectedSlot.value) ?? null,
);
function kindLabel(kind: string): string {
  return { bbs: '掲示板', shop: 'お店', nushi: '家主板', url: 'リンク' }[kind] ?? kind;
}
function tabLabel(c: HouseContent): string {
  return c.title || kindLabel(c.kind);
}
function selectTab(c: HouseContent) {
  selectedSlot.value = c.slot;
  bbsPage.value = 0;
  nushiPage.value = 0;
}

onMounted(async () => {
  try {
    const [houses, towns] = await Promise.all([api.houses(props.player.id), api.towns()]);
    townList.value = towns;
    allHouses.value = houses;
    house.value = houses.find((h) => h.id === props.houseId) ?? null;
    if (!house.value) {
      message.value = 'その家は見つかりませんでした。';
      return;
    }
    // レガシー忠実: 公開コンテンツが無い家には入れない。
    if (contents.value.length === 0) {
      message.value = 'まだ人に見せられる家では無いようです。';
      house.value = null;
      return;
    }
    // 一番上の枠が初期表示。
    selectedSlot.value = contents.value[0].slot;
    loadShop();
    loadBbs();
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
  }
});

const fmtDate = (iso: string) => {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
};

// さい銭(自分の家には不可)。
const saisenChoices = [100, 500, 1000, 2000, 5000, 10000];
const saisenAmount = ref(100);
async function doSaisen() {
  if (!house.value) return;
  busy.value = true;
  const amt = saisenAmount.value;
  try {
    const after = await api.saisen(props.player.id, house.value.id, amt);
    emit('update', after);
    showToast({
      variant: 'item',
      title: 'さい銭しました',
      lines: [`${house.value.owner_name}さんに ${yen(amt)}円 をさい銭しました`],
      icon: 'item',
    });
  } catch (e) {
    showToast({
      variant: 'error',
      title: 'さい銭できませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}

// お店(訪問販売)。個数は1〜4(レガシー item_kosuuseigen)。
const shop = ref<HouseShopView | null>(null);
const buyQty = ref<Record<number, number>>({});
const qtyChoices = [1, 2, 3, 4];
// 支払い方法: 現金 or クレジット(クレジットカード所持で普通口座払い)。
const payMethod = ref<'cash' | 'credit'>('cash');
const CREDIT_CARDS = ['クレジットカード', 'ゴールドクレジットカード', 'スペシャルクレジットカード'];
const hasCreditCard = computed(() =>
  props.player.items.some((it) => CREDIT_CARDS.includes(it.name) && it.remaining_uses > 0),
);
// ご近所割引: 自分の家(最初の家)がこの店の街にあれば単価10%引き(表示用の目安)。
const neighborDiscount = computed(
  () => allHouses.value.some((h) => h.own && h.town === house.value?.town) && !house.value?.own,
);
// 商品の使用効果を「国+5 体+3 +500円」形式で要約する。
function effectText(it: HouseShopItem): string {
  const parts: string[] = [];
  for (const [k, v] of Object.entries(it.params)) {
    if (v !== 0) parts.push(`${PARAM_LABEL[k] ?? k}${v > 0 ? '+' : ''}${v}`);
  }
  if (it.money !== 0) parts.push(`${it.money > 0 ? '+' : ''}${yen(it.money)}円`);
  return parts.join(' ') || '－';
}
async function loadShop() {
  shop.value = null;
  try {
    shop.value = await api.houseShop(props.player.id, props.houseId);
  } catch {
    shop.value = null;
  }
}
async function doBuy(it: HouseShopItem) {
  if (!house.value) return;
  const qty = buyQty.value[it.item_id] || 1;
  busy.value = true;
  try {
    const after = await api.buyFromHouseShop(props.player.id, house.value.id, it.item_id, qty, payMethod.value);
    emit('update', after);
    shop.value = await api.houseShop(props.player.id, house.value.id);
    const br = after.buy_result;
    const lines = [`${it.name} を${qty}個（${br.method === 'credit' ? 'クレジット・普通口座' : '現金'} ${yen(br.paid)}円）`];
    if (br.cashback > 0) lines.push(`ご近所キャッシュバック ${yen(br.cashback)}円引き`);
    showToast({ variant: 'item', title: '買いました', lines, icon: 'item' });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '購入できませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}

// 掲示板(通常=textarea投稿+ページング / 家主板=ブログ風記事+ページング)。
const bbs = ref<BbsPost[]>([]);
const bbsBody = ref('');
const nushiTitle = ref('');
const nushiBody = ref('');
const normalPosts = computed(() => bbs.value.filter((p) => p.kind === 'normal'));
const nushiPosts = computed(() => bbs.value.filter((p) => p.kind === 'nushi'));
// ページング(通常10件/家主板5件。レガシーgentei_kensuu相当)。
const BBS_PER_PAGE = 10;
const NUSHI_PER_PAGE = 5;
const bbsPage = ref(0);
const nushiPage = ref(0);
const bbsPagePosts = computed(() =>
  normalPosts.value.slice(bbsPage.value * BBS_PER_PAGE, (bbsPage.value + 1) * BBS_PER_PAGE),
);
const nushiPagePosts = computed(() =>
  nushiPosts.value.slice(nushiPage.value * NUSHI_PER_PAGE, (nushiPage.value + 1) * NUSHI_PER_PAGE),
);
const bbsMaxPage = computed(() => Math.max(0, Math.ceil(normalPosts.value.length / BBS_PER_PAGE) - 1));
const nushiMaxPage = computed(() => Math.max(0, Math.ceil(nushiPosts.value.length / NUSHI_PER_PAGE) - 1));
function canDeletePost(post: BbsPost): boolean {
  return (house.value?.own ?? false) || post.author_id === props.player.id;
}
async function loadBbs() {
  bbs.value = [];
  try {
    bbs.value = await api.houseBbs(props.player.id, props.houseId);
  } catch {
    bbs.value = [];
  }
}
async function doPostBbs(kind: string) {
  if (!house.value) return;
  const body = kind === 'nushi' ? nushiBody.value : bbsBody.value;
  const title = kind === 'nushi' ? nushiTitle.value : '';
  if (!body.trim()) return;
  busy.value = true;
  try {
    const after = await api.postBbs(props.player.id, house.value.id, kind, body, title);
    emit('update', after);
    if (kind === 'nushi') {
      nushiBody.value = '';
      nushiTitle.value = '';
    } else bbsBody.value = '';
    bbs.value = await api.houseBbs(props.player.id, house.value.id);
    showToast({ variant: 'item', title: '投稿しました', lines: [], icon: 'item' });
  } catch (e) {
    showToast({
      variant: 'error',
      title: '書き込めませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}
async function doDeleteBbs(post: BbsPost) {
  if (!house.value) return;
  busy.value = true;
  try {
    const after = await api.deleteBbs(props.player.id, post.id);
    emit('update', after);
    bbs.value = await api.houseBbs(props.player.id, house.value.id);
  } catch (e) {
    showToast({
      variant: 'error',
      title: '削除できませんでした',
      lines: [e instanceof Error ? e.message : String(e)],
      icon: 'item',
    });
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="house-page facility-page">
    <Toast :toast="toast" @close="closeToast" />

    <!-- 訪問不可(コンテンツ未公開)や読み込みエラー -->
    <div v-if="message" class="panel-white err-panel">
      <div class="err">{{ message }}</div>
      <button class="btn back" @click="emit('back')">街に戻る</button>
    </div>

    <template v-if="house">
      <!-- ヘッダ行: 街に戻る + コンテンツ切替ボタン + さい銭箱(レガシーhoumonの構成) -->
      <div class="visit-bar">
        <button class="btn back" @click="emit('back')">街に戻る</button>
        <div class="content-tabs">
          <button
            v-for="c in contents"
            :key="c.slot"
            class="tab"
            :class="{ active: c.slot === selectedSlot }"
            @click="selectTab(c)"
          >
            {{ tabLabel(c) }}
          </button>
        </div>
        <div v-if="!house.own" class="saisen-box">
          <span class="saisen-label">さい銭箱</span>
          <select v-model.number="saisenAmount">
            <option v-for="a in saisenChoices" :key="a" :value="a">{{ yen(a) }}円</option>
          </select>
          <button class="btn saisen-btn" :disabled="busy" @click="doSaisen">さい銭する</button>
        </div>
      </div>

      <!-- 家の情報(家主・場所・コメント) -->
      <div class="house-head panel-white">
        <img :src="`/img/${house.exterior}.gif`" :alt="house.exterior" />
        <div class="visit-info">
          <div class="visit-owner">{{ house.owner_name }}さんの家</div>
          <div class="visit-loc">{{ townName(house.town) }}／{{ rowLabel(house.row) }}{{ house.col }}</div>
        </div>
        <div v-if="house.setumei" class="visit-comment">「{{ house.setumei }}」</div>
      </div>

      <!-- 表示中のコンテンツ(1画面1コンテンツ) -->
      <div v-if="current" class="panel-white content-panel">
        <div class="vs-title">{{ tabLabel(current) }}</div>
        <div v-if="current.comment" class="content-lead">{{ current.comment }}</div>

        <!-- 通常掲示板 -->
        <template v-if="current.kind === 'bbs'">
          <div class="bbs-form">
            <textarea v-model="bbsBody" maxlength="500" rows="3" placeholder="コメントを書く" class="bbs-area"></textarea>
            <button class="btn mini" :disabled="busy" @click="doPostBbs('normal')">新規投稿</button>
          </div>
          <ul class="bbs-list">
            <li v-if="normalPosts.length === 0" class="bbs-empty">まだ書き込みはありません。</li>
            <li v-for="p in bbsPagePosts" :key="p.id">
              <div class="bbs-meta">
                <span class="bbs-author">{{ p.author_name }}</span>
                <span class="bbs-date">（{{ fmtDate(p.created_at) }}）</span>
                <button v-if="canDeletePost(p)" class="bbs-del" :disabled="busy" @click="doDeleteBbs(p)">×</button>
              </div>
              <div class="bbs-body">{{ p.body }}</div>
            </li>
          </ul>
          <div v-if="bbsMaxPage > 0" class="pager">
            <button class="btn mini" :disabled="bbsPage <= 0" @click="bbsPage--">BACK</button>
            <span>{{ bbsPage + 1 }} / {{ bbsMaxPage + 1 }}</span>
            <button class="btn mini" :disabled="bbsPage >= bbsMaxPage" @click="bbsPage++">NEXT</button>
          </div>
        </template>

        <!-- 家主板(ブログ風: 記事タイトル+本文。家主のみ投稿・訪問者は閲覧) -->
        <template v-else-if="current.kind === 'nushi'">
          <div v-if="house.own" class="bbs-form nushi-form">
            <input v-model="nushiTitle" maxlength="40" placeholder="記事タイトル" class="bbs-input" />
            <textarea v-model="nushiBody" maxlength="500" rows="3" placeholder="本文" class="bbs-area"></textarea>
            <button class="btn mini" :disabled="busy" @click="doPostBbs('nushi')">投稿する</button>
          </div>
          <ul class="bbs-list nushi-list">
            <li v-if="nushiPosts.length === 0" class="bbs-empty">まだ記事はありません。</li>
            <li v-for="p in nushiPagePosts" :key="p.id">
              <div class="nushi-title">{{ p.title || '(無題)' }}</div>
              <div class="bbs-body">{{ p.body }}</div>
              <div class="bbs-meta">
                <span class="bbs-date">（{{ fmtDate(p.created_at) }}）</span>
                <span class="bbs-no">記事no.{{ p.id }}</span>
                <button v-if="canDeletePost(p)" class="bbs-del" :disabled="busy" @click="doDeleteBbs(p)">×</button>
              </div>
            </li>
          </ul>
          <div v-if="nushiMaxPage > 0" class="pager">
            <button class="btn mini" :disabled="nushiPage <= 0" @click="nushiPage--">BACK</button>
            <span>{{ nushiPage + 1 }} / {{ nushiMaxPage + 1 }}</span>
            <button class="btn mini" :disabled="nushiPage >= nushiMaxPage" @click="nushiPage++">NEXT</button>
          </div>
        </template>

        <!-- お店(訪問販売) -->
        <template v-else-if="current.kind === 'shop'">
          <template v-if="shop && shop.has_shop">
            <div class="shop-sub">{{ shop.title || 'お店' }}（{{ shop.syubetu }}）</div>
            <div v-if="shop.own" class="visit-note">あなたの店です。仕入れ・設定は「家の設定」から行えます。</div>
            <div v-else-if="shop.items.length === 0" class="orosi-empty">売り切れです。</div>
            <template v-else>
              <div class="pay-row">
                <span class="pay-label">支払い方法</span>
                <select v-model="payMethod" class="qty-sel">
                  <option value="cash">現金</option>
                  <option value="credit" :disabled="!hasCreditCard">クレジット（普通口座）{{ hasCreditCard ? '' : '※カード未所持' }}</option>
                </select>
                <span v-if="neighborDiscount" class="neighbor-note">ご近所割引: この街の住人は単価10%引き</span>
              </div>
              <div class="orosi-scroll">
                <table class="orosi-table">
                  <thead>
                    <tr>
                      <th class="l">品名</th>
                      <th class="l">効果</th>
                      <th>ｶﾛﾘｰ</th>
                      <th>耐久</th>
                      <th>間隔</th>
                      <th>消費(身/頭)</th>
                      <th>価格</th>
                      <th>在庫</th>
                      <th>数量</th>
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="it in shop.items" :key="it.item_id" :class="{ owned: it.owned > 0 }">
                      <td class="l">{{ it.name }}<span v-if="it.owned > 0" class="owned-count">({{ it.owned }})</span></td>
                      <td class="l fx">{{ effectText(it) }}</td>
                      <td>{{ it.calorie_g || '－' }}</td>
                      <td>{{ it.durability }}{{ it.durability_unit === 'day' ? '日' : '回' }}</td>
                      <td>{{ it.interval_min ? it.interval_min + '分' : '－' }}</td>
                      <td>{{ it.body_cost || 0 }}/{{ it.nou_cost || 0 }}</td>
                      <td class="price">{{ yen(it.price) }}円</td>
                      <td>{{ it.stock }}</td>
                      <td>
                        <select v-model.number="buyQty[it.item_id]" class="qty-sel">
                          <option v-for="q in qtyChoices" :key="q" :value="q">{{ q }}</option>
                        </select>
                      </td>
                      <td>
                        <button class="btn mini" :disabled="busy" @click="doBuy(it)">買う</button>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </template>
          </template>
          <div v-else class="orosi-empty">お店は準備中です。</div>
        </template>

        <!-- 独自URL(IFRAME埋め込み) -->
        <template v-else-if="current.kind === 'url'">
          <iframe
            :src="current.url"
            class="dokuzi-frame"
            sandbox="allow-scripts allow-forms allow-popups"
            referrerpolicy="no-referrer"
          ></iframe>
          <div class="visit-note">
            外部ページ: <a :href="current.url" target="_blank" rel="noopener noreferrer">{{ current.url }}</a>
          </div>
        </template>
      </div>
    </template>
  </div>
</template>

<style scoped>
.house-page {
  background-color: #d8e8c8;
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  flex: 0 0 auto;
}
.btn.mini {
  font-size: 11px;
  padding: 2px 8px;
}
.err-panel .err {
  color: #a33;
  font-size: 14px;
  margin-bottom: 8px;
}
.panel-white {
  background: #fff;
  border: 1px solid #999;
  padding: 8px;
  margin-top: 8px;
  max-width: 640px;
}
/* ヘッダ行: 街に戻る + コンテンツタブ + さい銭箱(レガシーの1行構成) */
.visit-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.content-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  flex: 1 1 auto;
}
.content-tabs .tab {
  background: #eef3e8;
  border: 1px solid #99a;
  padding: 4px 10px;
  font-size: 12px;
  cursor: pointer;
  color: #234;
}
.content-tabs .tab.active {
  background: #4a7a2a;
  color: #fff;
  font-weight: bold;
}
.house-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.house-head img {
  width: 36px;
  height: 36px;
  object-fit: contain;
}
.visit-info {
  flex: 0 0 auto;
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
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 0 0 auto;
}
.saisen-label {
  font-weight: bold;
  color: #b5651d;
  font-size: 13px;
}
.saisen-btn {
  background: #b5651d;
  color: #fff;
  font-weight: bold;
}
.content-panel .vs-title {
  font-weight: bold;
  color: #7a4a00;
  margin-bottom: 6px;
  font-size: 15px;
  border-bottom: 2px solid #e8d8b0;
  padding-bottom: 4px;
}
.shop-sub {
  font-size: 12px;
  color: #667;
  margin-bottom: 4px;
}
.content-lead {
  font-size: 12px;
  color: #557;
  white-space: pre-line;
  margin-bottom: 6px;
}
.pay-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 6px;
}
.pay-label {
  font-size: 12px;
  font-weight: bold;
  color: #445;
}
.neighbor-note {
  font-size: 11px;
  color: #2a7a2a;
  font-weight: bold;
}
.orosi-table tr.owned td {
  background: #f2fbe8;
}
.owned-count {
  color: #2a7a2a;
  font-weight: bold;
  font-size: 11px;
  margin-left: 2px;
}
.orosi-table td.fx {
  color: #367;
  font-size: 11px;
  white-space: normal;
  min-width: 90px;
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
.qty-sel {
  font-size: 12px;
}
.bbs-form {
  display: flex;
  gap: 6px;
  margin-bottom: 8px;
  align-items: flex-end;
}
.nushi-form {
  flex-direction: column;
  align-items: stretch;
}
.bbs-input {
  font-size: 12px;
  padding: 3px 4px;
}
.bbs-area {
  flex: 1 1 auto;
  font-size: 12px;
  padding: 3px 4px;
  resize: vertical;
  width: 100%;
  box-sizing: border-box;
}
.bbs-list {
  list-style: none;
  margin: 0 0 8px;
  padding: 0;
  font-size: 12px;
}
.bbs-list li {
  padding: 6px 0;
  border-bottom: 1px solid #eee;
  color: #445;
}
.bbs-meta {
  display: flex;
  align-items: center;
  gap: 4px;
}
.bbs-author {
  font-weight: bold;
  color: #367;
}
.bbs-date {
  color: #999;
  font-size: 11px;
}
.bbs-no {
  color: #aaa;
  font-size: 10px;
}
.bbs-body {
  white-space: pre-line;
  margin-top: 2px;
}
.nushi-title {
  font-weight: bold;
  color: #634;
  font-size: 13px;
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
.pager {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: #667;
}
.dokuzi-frame {
  width: 100%;
  height: 420px;
  border: 1px solid #ccc;
  background: #fff;
}
</style>
