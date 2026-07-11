<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { api, type Player, type ShopItem } from '../api';
import { PARAM_COLUMNS, satietyLabel } from '../params';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const menu = ref<ShopItem[]>([]);
const message = ref('');
const kind = ref<'ok' | 'error'>('ok');
const busy = ref(false);

onMounted(async () => {
  try {
    menu.value = await api.facilityMenu('syokudou');
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
    kind.value = 'error';
  }
});

async function eat(food: ShopItem) {
  busy.value = true;
  message.value = '';
  try {
    emit('update', await api.eat(props.player.id, food.id));
    message.value = `${food.name}を食べました。`;
    kind.value = 'ok';
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
    kind.value = 'error';
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="facility-page syokudou-page">
    <button class="btn back" @click="emit('back')">街に戻る</button>

    <div class="syokudou-header">
      <div class="lead">
        セントラル食堂です。メニューは毎日変わります。<br />
        満腹のときは食事できません(お腹が空くと食べられます)。<br />
        ●{{ player.display_name }}さんの所持金：<span class="money">{{ yen(player.money) }}円</span>
        ／ 空腹度：{{ satietyLabel(player.status.satiety) }}
      </div>
      <div class="title">食　堂</div>
    </div>

    <div v-if="message" :class="['message', kind]" data-test="message">{{ message }}</div>

    <div class="panel-white table-scroll">
      <table class="menu-table">
        <thead>
          <tr>
            <th class="l">メニュー</th>
            <th>値段</th>
            <th v-for="c in PARAM_COLUMNS" :key="c.key" class="p">{{ c.label }}</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="food in menu" :key="food.id" :data-test="`food-${food.id}`">
            <td class="l">{{ food.name }}</td>
            <td class="price">{{ yen(food.price) }}円</td>
            <td v-for="c in PARAM_COLUMNS" :key="c.key" class="p" :class="{ up: (food.params[c.key] ?? 0) > 0 }">
              {{ food.params[c.key] ?? 0 }}
            </td>
            <td class="eat"><button class="btn" :disabled="busy" @click="eat(food)">食べる</button></td>
          </tr>
        </tbody>
      </table>
    </div>

    <div style="text-align: center; margin-top: 8px">
      <button class="btn" @click="emit('back')">街に戻る</button>
    </div>
  </div>
</template>

<style scoped>
.syokudou-page {
  background-color: #ccff66;
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  margin-bottom: 6px;
}
.syokudou-header {
  display: flex;
  margin-bottom: 8px;
}
.syokudou-header .lead {
  flex: 1 1 auto;
  background: #fff;
  border: 1px solid #999;
  padding: 8px 12px;
  font-size: 12px;
  color: #333;
  line-height: 1.6;
}
.syokudou-header .title {
  flex: 0 0 130px;
  background: #333;
  color: #fff;
  font-weight: bold;
  font-size: 16px;
  letter-spacing: 4px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid #999;
}
.money {
  color: #cc3300;
  font-weight: bold;
}
.panel-white {
  background: #fff;
  border: 1px solid #999;
  padding: 8px;
}
.table-scroll {
  overflow-x: auto;
}
.menu-table {
  border-collapse: collapse;
  font-size: 11px;
  white-space: nowrap;
}
.menu-table th {
  background: #ffff99;
  color: #663300;
  padding: 2px 4px;
  border: 1px solid #e0e080;
}
.menu-table td {
  padding: 2px 4px;
  border: 1px solid #eee;
  text-align: center;
}
.menu-table th.l,
.menu-table td.l {
  text-align: left;
}
.menu-table td.price {
  color: #cc3300;
  font-weight: bold;
  text-align: right;
}
.menu-table th.p,
.menu-table td.p {
  width: 20px;
  color: #999;
}
.menu-table td.p.up {
  color: #060;
  font-weight: bold;
  background: #eaffea;
}
.menu-table td.eat {
  width: 56px;
}
</style>
