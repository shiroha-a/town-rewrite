<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { api, type Player, type ShopSummary, type ShopDetail } from '../api';

// 商店街: プレイヤーが開いた商店の一覧・訪問購入・さい銭。自分の商店の管理も行う。
const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const OFFER_OPTIONS = [100, 500, 1000, 2000, 5000, 10000];

const shops = ref<ShopSummary[]>([]);
const detail = ref<ShopDetail | null>(null); // 表示中の店(null=一覧)
const myShop = ref<ShopDetail | null>(null); // 自分の店(あれば)
const message = ref('');
const kind = ref<'ok' | 'error'>('ok');
const busy = ref(false);

// 開店・在庫出品・さい銭の入力。
const newName = ref('');
const buyQty = ref<Record<number, number>>({});
const offerAmount = ref(1000);
const stockItem = ref<number | ''>('');
const stockQty = ref(1);
const stockPrice = ref(1000);

const viewingOwn = computed(() => detail.value?.owner_id === props.player.id);

async function loadList() {
  detail.value = null;
  try {
    shops.value = await api.listShops();
    myShop.value = shops.value.some((s) => s.owner_id === props.player.id)
      ? await api.getShop(props.player.id)
      : null;
  } catch (e) {
    fail(e);
  }
}
onMounted(loadList);

function fail(e: unknown) {
  message.value = e instanceof Error ? e.message : String(e);
  kind.value = 'error';
}

async function visit(ownerId: number) {
  message.value = '';
  try {
    detail.value = await api.getShop(ownerId);
    for (const l of detail.value.listings) if (buyQty.value[l.item_id] === undefined) buyQty.value[l.item_id] = 1;
  } catch (e) {
    fail(e);
  }
}

async function openShop() {
  busy.value = true;
  message.value = '';
  try {
    emit('update', await api.shopOpen(props.player.id, newName.value || '商店'));
    message.value = '商店を開きました。';
    kind.value = 'ok';
    await loadList();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}

async function buy(itemId: number) {
  if (!detail.value) return;
  const qty = buyQty.value[itemId] ?? 1;
  busy.value = true;
  message.value = '';
  try {
    emit('update', await api.shopBuy(props.player.id, detail.value.owner_id, itemId, qty));
    message.value = '購入しました。';
    kind.value = 'ok';
    await visit(detail.value.owner_id);
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}

async function offer() {
  if (!detail.value) return;
  busy.value = true;
  message.value = '';
  try {
    emit('update', await api.shopOffer(props.player.id, detail.value.owner_id, offerAmount.value));
    message.value = `${yen(offerAmount.value)}円さい銭しました。`;
    kind.value = 'ok';
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}

async function stock() {
  if (stockItem.value === '') {
    message.value = '出品する商品を選んでください。';
    kind.value = 'error';
    return;
  }
  busy.value = true;
  message.value = '';
  try {
    await api.shopStock(props.player.id, stockItem.value, stockQty.value, stockPrice.value);
    message.value = '出品しました。';
    kind.value = 'ok';
    emit('update', await api.getPlayer(props.player.id));
    myShop.value = await api.getShop(props.player.id);
    if (detail.value?.owner_id === props.player.id) detail.value = myShop.value;
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}

async function unstock(itemId: number) {
  busy.value = true;
  message.value = '';
  try {
    await api.shopUnstock(props.player.id, itemId, 1);
    emit('update', await api.getPlayer(props.player.id));
    myShop.value = await api.getShop(props.player.id);
    if (detail.value?.owner_id === props.player.id) detail.value = myShop.value;
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="facility-page shop-page">
    <button class="btn back" @click="detail ? loadList() : emit('back')">{{ detail ? '商店街へ' : '街に戻る' }}</button>

    <div class="fac-header">
      <div class="lead">
        住人の商店です。商品を買うと売上はお店の人へ。さい銭(投げ銭)もできます。<br />
        ●{{ player.display_name }}さんの所持金：<span class="money">{{ yen(player.money) }}円</span>
      </div>
      <div class="title">商店街</div>
    </div>

    <div v-if="message" :class="['message', kind]" data-test="message">{{ message }}</div>

    <!-- 一覧 -->
    <template v-if="!detail">
      <div class="panel-white">
        <h3>あなたの商店</h3>
        <div v-if="myShop">
          「{{ myShop.name }}」を営業中。<button class="btn mini" @click="visit(player.id)">管理する</button>
        </div>
        <div v-else class="open-form">
          <input v-model="newName" placeholder="店名(例: アリス商店)" />
          <button class="btn primary" :disabled="busy" @click="openShop">商店を開く(開設費500,000円)</button>
        </div>
      </div>

      <div class="panel-white">
        <h3>商店一覧（{{ shops.length }}）</h3>
        <div v-if="!shops.length" class="muted">まだ商店がありません。</div>
        <div v-for="s in shops" :key="s.owner_id" class="shop-row" @click="visit(s.owner_id)">
          <span class="sname">{{ s.name }}</span>
          <span class="sowner">({{ s.owner_name }})</span>
          <span class="scount">出品 {{ s.listings }}点</span>
        </div>
      </div>
    </template>

    <!-- 店内 -->
    <template v-else>
      <div class="panel-white">
        <h3>{{ detail.name }} <span class="sowner">({{ detail.owner_name }})</span></h3>
        <div class="table-scroll">
          <table class="shop-table">
            <thead>
              <tr><th class="l">商品</th><th>値段</th><th>在庫</th><th></th></tr>
            </thead>
            <tbody>
              <tr v-for="l in detail.listings" :key="l.item_id">
                <td class="l">{{ l.item_name }}<span class="cat">({{ l.category }})</span></td>
                <td class="price">{{ yen(l.price) }}円</td>
                <td>{{ l.stock > 0 ? l.stock : '売切れ' }}</td>
                <td class="act">
                  <template v-if="!viewingOwn">
                    <input type="number" min="1" :max="l.stock" v-model.number="buyQty[l.item_id]" />
                    <button class="btn mini" :disabled="busy || l.stock <= 0" @click="buy(l.item_id)">購入</button>
                  </template>
                  <button v-else class="btn mini" :disabled="busy || l.stock <= 0" @click="unstock(l.item_id)">1個戻す</button>
                </td>
              </tr>
              <tr v-if="!detail.listings.length"><td colspan="4" class="muted">出品がありません。</td></tr>
            </tbody>
          </table>
        </div>

        <!-- さい銭(他人の店) -->
        <div v-if="!viewingOwn" class="offer-row">
          <span>さい銭:</span>
          <select v-model.number="offerAmount">
            <option v-for="o in OFFER_OPTIONS" :key="o" :value="o">{{ yen(o) }}円</option>
          </select>
          <button class="btn" :disabled="busy" @click="offer">さい銭する</button>
        </div>

        <!-- 出品管理(自分の店) -->
        <div v-else class="stock-form">
          <h4>在庫を出品する</h4>
          <div class="stock-row">
            <select v-model="stockItem">
              <option value="">商品を選択</option>
              <option v-for="it in player.items" :key="it.item_id" :value="it.item_id">
                {{ it.name }}(所持{{ it.quantity }})
              </option>
            </select>
            <label>数<input type="number" min="1" v-model.number="stockQty" /></label>
            <label>価格<input type="number" min="0" v-model.number="stockPrice" /></label>
            <button class="btn primary" :disabled="busy" @click="stock">出品</button>
          </div>
        </div>
      </div>
    </template>

    <div style="text-align: center; margin-top: 8px">
      <button class="btn" @click="emit('back')">街に戻る</button>
    </div>
  </div>
</template>

<style scoped>
.shop-page {
  background-color: #f0e6d0;
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  margin-bottom: 6px;
}
.fac-header {
  display: flex;
  margin-bottom: 8px;
}
.fac-header .lead {
  flex: 1 1 auto;
  background: #fff;
  border: 1px solid #cb9;
  padding: 8px 12px;
  font-size: 12px;
  color: #333;
  line-height: 1.6;
}
.fac-header .title {
  flex: 0 0 160px;
  background: #996633;
  color: #fff;
  font-weight: bold;
  font-size: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid #cb9;
}
.money {
  color: #cc3300;
  font-weight: bold;
}
.panel-white {
  background: #fff;
  border: 1px solid #cb9;
  padding: 10px;
  margin-bottom: 8px;
}
.panel-white h3 {
  margin: 0 0 8px;
  font-size: 14px;
  color: #663300;
}
.panel-white h4 {
  margin: 8px 0 4px;
  font-size: 13px;
  color: #663300;
}
.open-form {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.shop-row {
  padding: 5px 4px;
  border-bottom: 1px solid #eee;
  cursor: pointer;
  font-size: 13px;
}
.shop-row:hover {
  background: #fff8ee;
}
.sname {
  font-weight: bold;
  color: #663300;
}
.sowner {
  color: #998;
  font-size: 12px;
  margin-left: 4px;
}
.scount {
  color: #667;
  font-size: 11px;
  margin-left: 8px;
}
.muted {
  color: #999;
  font-size: 12px;
}
.table-scroll {
  overflow-x: auto;
}
.shop-table {
  border-collapse: collapse;
  width: 100%;
  font-size: 12px;
}
.shop-table th,
.shop-table td {
  border: 1px solid #eee;
  padding: 3px 6px;
  text-align: center;
}
.shop-table th {
  background: #f2e8d8;
  color: #663300;
}
.shop-table td.l,
.shop-table th.l {
  text-align: left;
}
.shop-table td.price {
  color: #cc3300;
  font-weight: bold;
  text-align: right;
}
.shop-table .cat {
  color: #aa9;
  font-size: 10px;
  margin-left: 4px;
}
.shop-table td.act input {
  width: 44px;
  margin-right: 4px;
}
.offer-row,
.stock-row {
  display: flex;
  gap: 6px;
  align-items: center;
  margin-top: 8px;
  flex-wrap: wrap;
  font-size: 12px;
}
.stock-row label {
  display: flex;
  align-items: center;
  gap: 3px;
}
.stock-row input {
  width: 70px;
}
.btn.mini {
  padding: 1px 6px;
  font-size: 11px;
}
.btn.primary {
  background: #996633;
  color: #fff;
  border-color: #663300;
}
</style>
