<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import {
  api,
  type Player,
  type Town,
  type HouseCell,
  type HouseShopView,
  type HouseShopItem,
  type BbsPost,
} from '../api';
import Toast from './Toast.vue';
import { useToast } from '../toast';

// 家訪問(レガシー original_house.cgi houmon)。街で家をクリックすると開き、
// 家主が公開しているコンテンツ(掲示板/お店/家主板)とさい銭箱を表示する。
const props = defineProps<{ player: Player; houseId: number }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const busy = ref(false);
const message = ref('');
const { toast, showToast, closeToast } = useToast();

const house = ref<HouseCell | null>(null);
const townList = ref<Town[]>([]);
const townName = (no: number) => townList.value.find((t) => t.no === no)?.name ?? '';
const rowLabel = (row: number) => String.fromCharCode(65 + row);

onMounted(async () => {
  try {
    const [houses, towns] = await Promise.all([api.houses(props.player.id), api.towns()]);
    townList.value = towns;
    house.value = houses.find((h) => h.id === props.houseId) ?? null;
    if (!house.value) {
      message.value = 'その家は見つかりませんでした。';
      return;
    }
    loadShop();
    loadBbs();
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
  }
});

// コンテンツ枠。設定された種類だけ表示する。
function has(kind: string): boolean {
  return house.value?.contents.some((c) => c.kind === kind) ?? false;
}
function slotTitle(kind: string, fallback: string): string {
  const c = house.value?.contents.find((x) => x.kind === kind);
  return c?.title || fallback;
}
const noContents = computed(() => (house.value?.contents.length ?? 0) === 0);

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

// お店(訪問販売)。
const shop = ref<HouseShopView | null>(null);
const buyQty = ref<Record<number, number>>({});
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
    const after = await api.buyFromHouseShop(props.player.id, house.value.id, it.item_id, qty);
    emit('update', after);
    shop.value = await api.houseShop(props.player.id, house.value.id);
    showToast({ variant: 'item', title: '買いました', lines: [`${it.name} を${qty}個 買いました`], icon: 'item' });
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

// 掲示板。
const bbs = ref<BbsPost[]>([]);
const bbsBody = ref('');
const nushiBody = ref('');
const normalPosts = computed(() => bbs.value.filter((p) => p.kind === 'normal'));
const nushiPosts = computed(() => bbs.value.filter((p) => p.kind === 'nushi'));
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
  if (!body.trim()) return;
  busy.value = true;
  try {
    const after = await api.postBbs(props.player.id, house.value.id, kind, body);
    emit('update', after);
    if (kind === 'nushi') nushiBody.value = '';
    else bbsBody.value = '';
    bbs.value = await api.houseBbs(props.player.id, house.value.id);
    showToast({ variant: 'item', title: '書き込みました', lines: [], icon: 'item' });
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
    <button class="btn back" @click="emit('back')">街に戻る</button>

    <div v-if="message" class="err">{{ message }}</div>

    <div v-if="house" class="panel-white">
      <div class="visit-head">
        <img :src="`/img/${house.exterior}.gif`" :alt="house.exterior" />
        <div class="visit-info">
          <div class="visit-owner">{{ house.owner_name }}さんの家</div>
          <div class="visit-loc">{{ townName(house.town) }}／{{ rowLabel(house.row) }}{{ house.col }}</div>
        </div>
      </div>
      <div v-if="house.setumei" class="visit-comment">「{{ house.setumei }}」</div>
      <div v-if="house.own" class="visit-note">
        これはあなたの家です。コメントやコンテンツは建設会社の「自分の家」欄で設定できます。
      </div>
      <div v-else class="saisen-box">
        <span class="saisen-label">さい銭箱</span>
        <select v-model.number="saisenAmount">
          <option v-for="a in saisenChoices" :key="a" :value="a">{{ yen(a) }}円</option>
        </select>
        <button class="btn saisen-btn" :disabled="busy" @click="doSaisen">さい銭する</button>
      </div>

      <!-- 公開コンテンツ無し(家主がコンテンツ枠を未設定) -->
      <div v-if="noContents" class="visit-note">この家には公開されているコンテンツがありません。</div>

      <!-- お店(コンテンツ枠に「お店」が設定された家だけ表示) -->
      <div v-if="has('shop') && shop && shop.has_shop" class="visit-shop">
        <div class="vs-title">{{ slotTitle('shop', shop.title || 'お店') }}（{{ shop.syubetu }}）</div>
        <div v-if="shop.own" class="visit-note">あなたの店です。仕入れ・設定は建設会社から行えます。</div>
        <div v-else-if="shop.items.length === 0" class="orosi-empty">売り切れです。</div>
        <div v-else class="orosi-scroll">
          <table class="orosi-table">
            <thead>
              <tr>
                <th class="l">品名</th>
                <th>価格</th>
                <th>在庫</th>
                <th>数量</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="it in shop.items" :key="it.item_id">
                <td class="l">{{ it.name }}</td>
                <td class="price">{{ yen(it.price) }}円</td>
                <td>{{ it.stock }}</td>
                <td>
                  <input v-model.number="buyQty[it.item_id]" type="number" min="1" :max="it.stock" class="qty-input" />
                </td>
                <td>
                  <button class="btn mini" :disabled="busy" @click="doBuy(it)">買う</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- 掲示板(コンテンツ枠に設定された板だけ表示) -->
      <div v-if="has('bbs') || has('nushi')" class="visit-bbs">
        <template v-if="has('bbs')">
          <div class="vs-title">{{ slotTitle('bbs', '通常掲示板') }}</div>
          <div class="bbs-form">
            <input v-model="bbsBody" maxlength="500" placeholder="コメントを書く" class="bbs-input" />
            <button class="btn mini" :disabled="busy" @click="doPostBbs('normal')">書き込む</button>
          </div>
          <ul class="bbs-list">
            <li v-if="normalPosts.length === 0" class="bbs-empty">まだ書き込みはありません。</li>
            <li v-for="p in normalPosts" :key="p.id">
              <span class="bbs-author">{{ p.author_name }}</span>：{{ p.body }}
              <button v-if="canDeletePost(p)" class="bbs-del" :disabled="busy" @click="doDeleteBbs(p)">×</button>
            </li>
          </ul>
        </template>
        <template v-if="has('nushi')">
          <div class="vs-title">{{ slotTitle('nushi', '家主板') }}</div>
          <div v-if="house.own" class="bbs-form">
            <input v-model="nushiBody" maxlength="500" placeholder="家主板に書く" class="bbs-input" />
            <button class="btn mini" :disabled="busy" @click="doPostBbs('nushi')">書き込む</button>
          </div>
          <ul class="bbs-list">
            <li v-if="nushiPosts.length === 0" class="bbs-empty">まだ書き込みはありません。</li>
            <li v-for="p in nushiPosts" :key="p.id">
              <span class="bbs-author">{{ p.author_name }}</span>：{{ p.body }}
              <button v-if="canDeletePost(p)" class="bbs-del" :disabled="busy" @click="doDeleteBbs(p)">×</button>
            </li>
          </ul>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.house-page {
  background-color: #d8e8c8;
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  margin-bottom: 6px;
}
.btn.mini {
  font-size: 11px;
  padding: 2px 8px;
}
.err {
  background: #fff0f0;
  border: 1px solid #c99;
  color: #a33;
  padding: 6px 10px;
  font-size: 13px;
}
.panel-white {
  background: #fff;
  border: 1px solid #999;
  padding: 8px;
  margin-top: 8px;
  max-width: 560px;
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
.qty-input {
  width: 50px;
  font-size: 12px;
  padding: 2px;
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
</style>
