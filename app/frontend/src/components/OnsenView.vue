<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { api, type Player, type ShopItem } from '../api';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const baths = ref<ShopItem[]>([]);
const message = ref('');
const kind = ref<'ok' | 'error'>('ok');
const busy = ref(false);

onMounted(async () => {
  try {
    baths.value = await api.facilityMenu('onsen');
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
    kind.value = 'error';
  }
});

async function bathe(bath: ShopItem) {
  busy.value = true;
  message.value = '';
  try {
    emit('update', await api.onsenBathe(props.player.id, bath.id));
    message.value = `${bath.name}に入りました。ゆっくり温まってパワーが回復しました。`;
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
  <div class="facility-page onsen-page">
    <button class="btn back" @click="emit('back')">街に戻る</button>

    <h2 class="onsen-title">風呂の選択</h2>
    <div class="onsen-sub">
      ●{{ player.display_name }}さんの所持金：<span class="money">{{ yen(player.money) }}円</span>
      ／ 身体パワー {{ player.status.energy }}/{{ player.status.energy_max }}
      ・頭脳パワー {{ player.status.nou_energy }}/{{ player.status.nou_energy_max }}
    </div>

    <div v-if="message" :class="['message', kind]" data-test="message">{{ message }}</div>

    <div class="onsen-note">
      風呂は自然回復を「回復倍率」ぶん加速します。時間が経っているほど、また倍率が高いほど、一度に多く回復します。
    </div>

    <div class="bath-box">
      <table class="bath-table">
        <thead>
          <tr>
            <th class="l">風呂</th>
            <th>料金</th>
            <th>回復倍率</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="bath in baths" :key="bath.id" :data-test="`bath-${bath.id}`">
            <td class="l">{{ bath.name }}</td>
            <td class="price">{{ yen(bath.price) }}円</td>
            <td class="mult">×{{ bath.power_multiplier }}</td>
            <td>
              <button class="btn" :disabled="busy" @click="bathe(bath)">入る</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div style="text-align: center; margin-top: 8px">
      <button class="btn" @click="emit('back')">街へ戻る</button>
    </div>
  </div>
</template>

<style scoped>
.onsen-page {
  background-color: #336699;
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  margin-bottom: 6px;
}
.onsen-title {
  text-align: center;
  color: #fff;
  font-size: 18px;
  margin: 10px 0 6px;
}
.onsen-sub {
  text-align: center;
  color: #fff;
  font-size: 12px;
  margin-bottom: 10px;
}
.money {
  color: #ffff66;
  font-weight: bold;
}
.bath-box {
  background: #ffffcc;
  border: 1px solid #666;
  max-width: 460px;
  margin: 0 auto;
  padding: 10px 14px;
}
.bath-table {
  width: 100%;
  border-collapse: collapse;
}
.bath-table th {
  padding: 4px 8px;
  background: #f0e6b0;
  color: #663300;
  border-bottom: 1px solid #ccb;
  font-size: 12px;
}
.bath-table th.l {
  text-align: left;
}
.bath-table td {
  padding: 4px 8px;
  text-align: center;
}
.bath-table td.l {
  text-align: left;
  font-weight: bold;
}
.bath-table .price {
  color: #cc3300;
  font-weight: bold;
  text-align: right;
}
.bath-table .mult {
  color: #0066aa;
  font-weight: bold;
}
.onsen-note {
  max-width: 460px;
  margin: 0 auto 8px;
  color: #ffffcc;
  font-size: 11px;
  line-height: 1.5;
  text-align: center;
}
</style>
