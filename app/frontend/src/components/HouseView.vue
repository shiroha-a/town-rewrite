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

// 家訪問(レガシー original_house.cgi houmon)。レガシーのレイアウトを再現:
// 上部1行[街に戻る|コンテンツボタン|さい銭箱] + 中央寄せのコンテンツボックス。
// 背景色・囲み枠・文字色はレガシーの既定スタイルに合わせる
// (bbs=#ffcc66/点線青枠, 家主板=#99cc99/点線緑枠, 店=#ffcc33/白テーブル, URL=白)。
const props = defineProps<{ player: Player; houseId: number }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const busy = ref(false);
const message = ref('');
const { toast, showToast, closeToast } = useToast();

const house = ref<HouseCell | null>(null);
const allHouses = ref<HouseCell[]>([]);
const townList = ref<Town[]>([]);

// コンテンツ枠(枠順)。ボタンとして描画し、selectedSlotの枠を表示する。
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
// ページ背景色(レガシー既定: bbs=#ffcc66 / gentei=#99cc99 / omise=#ffcc33 / dokuzi=白)。
const pageBg = computed(() => {
  switch (current.value?.kind) {
    case 'nushi':
      return '#99cc99';
    case 'shop':
      return '#ffcc33';
    case 'url':
      return '#ffffff';
    default:
      return '#ffcc66';
  }
});

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

// さい銭(レガシー同様、自分の家でも表示・可能)。
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

// お店(レガシーomise): radioで商品を選び、下部の個数(1〜4)+支払い方法で購入。
const shop = ref<HouseShopView | null>(null);
const selectedItemId = ref<number | null>(null);
const buyQtySel = ref(1);
const qtyChoices = [1, 2, 3, 4];
const payMethod = ref<'cash' | 'credit'>('cash');
const CREDIT_CARDS = ['クレジットカード', 'ゴールドクレジットカード', 'スペシャルクレジットカード'];
const hasCreditCard = computed(() =>
  props.player.items.some((it) => CREDIT_CARDS.includes(it.name) && it.remaining_uses > 0),
);
const neighborDiscount = computed(
  () => allHouses.value.some((h) => h.own && h.town === house.value?.town) && !house.value?.own,
);
// 商品テーブルのパラメータ列(レガシーの 国..面。アダルト系は対象外)。
const PARAM_COLS: { key: string; label: string }[] = [
  { key: 'kokugo', label: '国' },
  { key: 'suugaku', label: '数' },
  { key: 'rika', label: '理' },
  { key: 'syakai', label: '社' },
  { key: 'eigo', label: '英' },
  { key: 'ongaku', label: '音' },
  { key: 'bijutsu', label: '美' },
  { key: 'looks', label: 'ル' },
  { key: 'tairyoku', label: '体' },
  { key: 'kenkou', label: '健' },
  { key: 'speed', label: 'ス' },
  { key: 'power', label: 'パ' },
  { key: 'wanryoku', label: '腕' },
  { key: 'kyakuryoku', label: '脚' },
  { key: 'love', label: 'L' },
  { key: 'omoshirosa', label: '面' },
];
const paramVal = (it: HouseShopItem, key: string) => it.params[key] || '';
// 種別(カテゴリ)ごとにまとめて「▼種別」行を挟む(レガシーの表示順)。
const shopGroups = computed(() => {
  const groups: { category: string; items: HouseShopItem[] }[] = [];
  for (const it of shop.value?.items ?? []) {
    const g = groups.find((x) => x.category === it.category);
    if (g) g.items.push(it);
    else groups.push({ category: it.category, items: [it] });
  }
  return groups;
});
async function loadShop() {
  shop.value = null;
  try {
    shop.value = await api.houseShop(props.player.id, props.houseId);
  } catch {
    shop.value = null;
  }
}
async function doBuy() {
  if (!house.value || selectedItemId.value === null) return;
  const it = shop.value?.items.find((x) => x.item_id === selectedItemId.value);
  if (!it) return;
  busy.value = true;
  try {
    const after = await api.buyFromHouseShop(props.player.id, house.value.id, it.item_id, buyQtySel.value, payMethod.value);
    emit('update', after);
    shop.value = await api.houseShop(props.player.id, house.value.id);
    const br = after.buy_result;
    const lines = [`${it.name} を${buyQtySel.value}個（${br.method === 'credit' ? 'クレジット・普通口座' : '現金'} ${yen(br.paid)}円）`];
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

// 掲示板(通常=textarea投稿+ページング / 家主板=ブログ風記事+5件ページング)。
const bbs = ref<BbsPost[]>([]);
const bbsBody = ref('');
const nushiTitle = ref('');
const nushiBody = ref('');
const normalPosts = computed(() => bbs.value.filter((p) => p.kind === 'normal'));
const nushiPosts = computed(() => bbs.value.filter((p) => p.kind === 'nushi'));
const BBS_PER_PAGE = 10;
const NUSHI_PER_PAGE = 5; // レガシー gentei_kensuu 既定
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
  <div class="house-page facility-page" :style="{ backgroundColor: house ? pageBg : '#ffcc66' }">
    <Toast :toast="toast" @close="closeToast" />

    <!-- 訪問不可(コンテンツ未公開)や読み込みエラー -->
    <div v-if="message" class="err-panel">
      <div class="err">{{ message }}</div>
      <button class="btn" @click="emit('back')">街に戻る</button>
    </div>

    <template v-if="house">
      <!-- 上部1行(レガシー): 街に戻る + コンテンツボタン + さい銭箱(右寄せ) -->
      <div class="visit-bar">
        <button class="btn" @click="emit('back')">街に戻る</button>
        <button
          v-for="c in contents"
          :key="c.slot"
          class="btn tabbtn"
          :class="{ active: c.slot === selectedSlot }"
          @click="selectTab(c)"
        >
          {{ tabLabel(c) }}
        </button>
        <span class="bar-spacer"></span>
        <span class="saisen-box">
          <select v-model.number="saisenAmount">
            <option v-for="a in saisenChoices" :key="a" :value="a">{{ yen(a) }}円</option>
          </select>
          <button class="btn" :disabled="busy" @click="doSaisen">さい銭する</button>
        </span>
      </div>

      <!-- 通常掲示板(レガシーbbs1: #ffffcc・青点線枠・中央寄せ) -->
      <div v-if="current && current.kind === 'bbs'" class="cbox bbs-box">
        <div class="bbs-title">{{ tabLabel(current) }}</div>
        <div v-if="current.comment" class="bbs-lead">{{ current.comment }}</div>
        <div class="bbs-form">
          <textarea v-model="bbsBody" maxlength="500" rows="4" class="bbs-area"></textarea>
          <div><button class="btn" :disabled="busy" @click="doPostBbs('normal')">新規投稿</button></div>
        </div>
        <div class="bbs-posts">
          <div v-if="normalPosts.length === 0" class="bbs-empty">まだ書き込みはありません。</div>
          <div v-for="p in bbsPagePosts" :key="p.id" class="bbs-post">
            <span class="bbs-author">{{ p.author_name }}</span>
            <span class="bbs-date">（{{ fmtDate(p.created_at) }}）</span>
            <span class="bbs-no">記事no.{{ p.id }}</span>
            <button v-if="canDeletePost(p)" class="bbs-del" :disabled="busy" @click="doDeleteBbs(p)">×</button>
            <div class="bbs-body">{{ p.body }}</div>
          </div>
        </div>
        <div v-if="bbsMaxPage > 0" class="pager">
          <button class="btn" :disabled="bbsPage <= 0" @click="bbsPage--">BACK</button>
          <button class="btn" :disabled="bbsPage >= bbsMaxPage" @click="bbsPage++">NEXT</button>
        </div>
      </div>

      <!-- 家主板(レガシーgentei: #ffffcc・緑点線枠・記事形式) -->
      <div v-else-if="current && current.kind === 'nushi'" class="cbox nushi-box">
        <div class="nushi-title">{{ tabLabel(current) }}</div>
        <div v-if="current.comment" class="nushi-lead">{{ current.comment }}</div>
        <div v-if="house.own" class="bbs-form">
          <input v-model="nushiTitle" maxlength="40" placeholder="記事タイトル" class="bbs-input" />
          <textarea v-model="nushiBody" maxlength="500" rows="4" class="bbs-area" placeholder="本文"></textarea>
          <div><button class="btn" :disabled="busy" @click="doPostBbs('nushi')">投稿する</button></div>
        </div>
        <div class="bbs-posts">
          <div v-if="nushiPosts.length === 0" class="bbs-empty">まだ記事はありません。</div>
          <div v-for="p in nushiPagePosts" :key="p.id" class="nushi-article">
            <div class="nushi-daimei">{{ p.title || '(無題)' }}</div>
            <div class="nushi-body">
              {{ p.body }}（{{ fmtDate(p.created_at) }}）<span class="bbs-no">記事no.{{ p.id }}</span>
              <button v-if="canDeletePost(p)" class="bbs-del" :disabled="busy" @click="doDeleteBbs(p)">×</button>
            </div>
          </div>
        </div>
        <div v-if="nushiMaxPage > 0" class="pager">
          <button class="btn" :disabled="nushiPage <= 0" @click="nushiPage--">BACK</button>
          <button class="btn" :disabled="nushiPage >= nushiMaxPage" @click="nushiPage++">NEXT</button>
        </div>
      </div>

      <!-- お店(レガシーomise: #ffcc33地・白タイトル行・全パラメータ列の商品表) -->
      <template v-else-if="current && current.kind === 'shop'">
        <template v-if="shop && shop.has_shop">
          <table class="omise-head">
            <tr>
              <td class="omise-title">{{ tabLabel(current) }}</td>
              <td class="omise-lead">{{ current.comment || shop.title }}（{{ shop.syubetu }}）</td>
            </tr>
          </table>
          <div v-if="shop.own" class="omise-note">※自分のお店で商品を買うことはできません。</div>
          <div v-if="shop.items.length === 0" class="omise-note">売り切れです。</div>
          <div v-else class="omise-scroll">
            <table class="omise-table">
              <tr>
                <td class="hanrei" :colspan="PARAM_COLS.length + 8">
                  凡例：(国)＝国語up値、(数)＝数学up値、(理)＝理科up値、(社)＝社会up値、(英)＝英語up値、(音)＝音楽up値、(美)＝美術up値、（ル）=ルックスup値、（体）=体力up値、（健）=健康up値、（ス）=スピードup値、（パ）=パワーup値、（腕）=腕力up値、（脚）=脚力up値、（L）=LOVEup値、（面）=面白さup値。青字は所持中(残数)。
                </td>
              </tr>
              <tr class="koumoku">
                <td>商品</td>
                <td>価格</td>
                <td>在庫</td>
                <td v-for="pc in PARAM_COLS" :key="pc.key">{{ pc.label }}</td>
                <td>カロリー</td>
                <td>耐久</td>
                <td>使用<br />間隔</td>
                <td>身体<br />消費</td>
                <td>頭脳<br />消費</td>
              </tr>
              <template v-for="g in shopGroups" :key="g.category">
                <tr class="syubetu-row">
                  <td :colspan="PARAM_COLS.length + 8">▼{{ g.category }}</td>
                </tr>
                <tr v-for="it in g.items" :key="it.item_id" class="syouhin-row">
                  <td class="hinmoku">
                    <input
                      v-if="!shop.own"
                      v-model.number="selectedItemId"
                      type="radio"
                      :value="it.item_id"
                      name="syo_hinmoku"
                    />
                    <span :class="{ motteru: it.owned > 0 }">{{ it.name }}<template v-if="it.owned > 0">({{ it.owned }})</template></span>
                  </td>
                  <td class="r">{{ yen(it.price) }}円</td>
                  <td class="r">{{ it.stock }}</td>
                  <td v-for="pc in PARAM_COLS" :key="pc.key">{{ paramVal(it, pc.key) }}</td>
                  <td class="r">{{ it.calorie_g || '' }}</td>
                  <td>{{ it.durability }}{{ it.durability_unit === 'day' ? '日' : '回' }}</td>
                  <td>{{ it.interval_min }}分</td>
                  <td>{{ it.body_cost || '' }}</td>
                  <td>{{ it.nou_cost || '' }}</td>
                </tr>
              </template>
              <tr v-if="!shop.own">
                <td :colspan="PARAM_COLS.length + 8" class="buy-row">
                  個数
                  <select v-model.number="buyQtySel">
                    <option v-for="q in qtyChoices" :key="q" :value="q">{{ q }}</option>
                  </select>
                  支払い方法
                  <select v-model="payMethod">
                    <option value="cash">現金</option>
                    <option value="credit" :disabled="!hasCreditCard">クレジット（普通口座）</option>
                  </select>
                  <button class="btn" :disabled="busy || selectedItemId === null" @click="doBuy">購入する</button>
                  <span v-if="neighborDiscount" class="neighbor-note">※この街の住人は単価10%引き</span>
                </td>
              </tr>
            </table>
          </div>
        </template>
        <div v-else class="omise-note">お店は準備中です。</div>
      </template>

      <!-- 独自URL(レガシーdokuzi: 白地・IFRAME 800x400) -->
      <div v-else-if="current && current.kind === 'url'" class="dokuzi-wrap">
        <div class="dokuzi-title">{{ tabLabel(current) }}</div>
        <div v-if="current.comment" class="dokuzi-lead">{{ current.comment }}</div>
        <iframe
          :src="current.url"
          class="dokuzi-frame"
          sandbox="allow-scripts allow-forms allow-popups"
          referrerpolicy="no-referrer"
        ></iframe>
      </div>
    </template>
  </div>
</template>

<style scoped>
/* bodyのmargin(5px)を打ち消し、レガシーのbody背景色のように全面を塗る。 */
.house-page {
  padding: 6px;
  margin: -5px;
  min-height: 100vh;
  box-sizing: border-box;
}
.err-panel {
  background: #fff;
  border: 1px solid #999;
  padding: 10px;
  max-width: 480px;
  margin: 20px auto;
  text-align: center;
}
.err-panel .err {
  color: #a33;
  font-size: 14px;
  margin-bottom: 8px;
}
/* 上部1行: 街に戻る + コンテンツボタン + さい銭箱(右) */
.visit-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 5px;
  padding: 5px;
  max-width: 820px;
  margin: 0 auto;
}
.bar-spacer {
  flex: 1 1 auto;
}
.btn.tabbtn.active {
  background: #666;
  color: #fff;
}
.saisen-box {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
  color: #000;
}
/* 中央寄せのコンテンツボックス(bbs/gentei共通の囲みテーブル) */
.cbox {
  margin: 10px auto 0;
  padding: 14px;
  font-size: 11px;
  line-height: 16px;
  color: #666666;
  background-color: #ffffcc;
}
.bbs-box {
  width: 500px;
  max-width: 96%;
  border: 4px dotted #336699;
}
.nushi-box {
  width: 520px;
  max-width: 96%;
  border: 4px dotted #339966;
}
.bbs-title {
  font-size: 16px;
  color: #666666;
  line-height: 180%;
  text-align: center;
}
.bbs-lead {
  font-size: 11px;
  line-height: 16px;
  color: #336699;
  white-space: pre-line;
}
.nushi-title {
  font-size: 20px;
  color: #339966;
  line-height: 150%;
  text-align: center;
}
.nushi-lead {
  font-size: 11px;
  color: #ff6600;
  line-height: 160%;
  white-space: pre-line;
}
.bbs-form {
  margin: 8px 0;
}
.bbs-area {
  width: 100%;
  box-sizing: border-box;
  font-size: 12px;
  padding: 3px;
  resize: vertical;
}
.bbs-input {
  width: 100%;
  box-sizing: border-box;
  font-size: 12px;
  padding: 3px;
  margin-bottom: 4px;
}
.bbs-posts {
  margin-top: 8px;
}
.bbs-post {
  padding: 4px 0;
  border-bottom: 1px dashed #ccc;
}
.bbs-author {
  font-size: 11px;
  color: #ff6600;
  font-weight: bold;
}
.bbs-date {
  color: #999;
  font-size: 10px;
}
.bbs-no {
  font-size: 9px;
  color: #999;
}
.bbs-body {
  white-space: pre-line;
  color: #444;
}
.nushi-article {
  margin-bottom: 6px;
}
.nushi-daimei {
  font-size: 14px;
  color: #445555;
  line-height: 200%;
  font-weight: bold;
}
.nushi-body {
  white-space: pre-line;
  color: #555;
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
  justify-content: center;
  gap: 8px;
  margin-top: 8px;
}
/* お店(omise) */
.omise-head {
  width: 100%;
  max-width: 820px;
  margin: 8px auto 0;
  border-collapse: collapse;
  font-size: 11px;
  line-height: 18px;
  color: #666666;
  background-color: #ffffff;
  border: 1px solid #666666;
}
.omise-head td {
  padding: 10px;
}
.omise-title {
  font-size: 18px;
  color: #ff6600;
  line-height: 160%;
  white-space: nowrap;
}
.omise-lead {
  font-size: 11px;
  line-height: 16px;
  color: #000000;
  width: 65%;
}
.omise-note {
  max-width: 820px;
  margin: 6px auto;
  font-size: 11px;
  color: #663300;
}
.omise-scroll {
  max-width: 820px;
  margin: 8px auto 0;
  overflow-x: auto;
}
.omise-table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 1px;
  font-size: 10px;
  color: #336699;
  background-color: #ffffff;
  border: 1px solid #666666;
}
.omise-table td {
  padding: 4px 5px;
  text-align: center;
  white-space: nowrap;
}
.omise-table td.r {
  text-align: right;
}
.omise-table .hanrei {
  text-align: left;
  white-space: normal;
  font-size: 10px;
  color: #336699;
}
.omise-table .koumoku td {
  font-size: 11px;
  color: #000000;
  background-color: #ffcc66;
}
.omise-table .syubetu-row td {
  background-color: #ffff88;
  text-align: left;
  color: #333;
}
.omise-table .syouhin-row td {
  font-size: 11px;
  color: #333333;
  background-color: #ffffaa;
}
.omise-table .hinmoku {
  text-align: left;
}
.motteru {
  color: #0000ff;
}
.buy-row {
  text-align: left !important;
  background: #fff;
  font-size: 11px;
  color: #333;
}
.neighbor-note {
  color: #2a7a2a;
  font-weight: bold;
  margin-left: 8px;
}
/* 独自URL(dokuzi) */
.dokuzi-wrap {
  max-width: 820px;
  margin: 10px auto 0;
  text-align: center;
}
.dokuzi-title {
  font-size: 16px;
  color: #666666;
  line-height: 160%;
}
.dokuzi-lead {
  font-size: 11px;
  line-height: 16px;
  color: #336699;
  white-space: pre-line;
}
.dokuzi-frame {
  width: 100%;
  max-width: 800px;
  height: 400px;
  border: 1px solid #ccc;
  background: #fff;
  margin-top: 6px;
}
</style>
