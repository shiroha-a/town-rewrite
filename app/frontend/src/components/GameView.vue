<script setup lang="ts">
import { ref, computed, type Component } from 'vue';
import { type Player } from '../api';
import SaikoroGame from './casino/SaikoroGame.vue';
import LotoGame from './casino/LotoGame.vue';
import SlotGame from './casino/SlotGame.vue';
import KujiGame from './casino/KujiGame.vue';
import DonutsGame from './casino/DonutsGame.vue';

defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

// ゲーム一覧。新しいゲームはこの配列と gameComponents の両方に登録する。
const games: { key: string; name: string; desc: string }[] = [
  { key: 'saikoro', name: 'サイコロ', desc: '2つのサイコロの合計が偶数か奇数かを当てる(1:1)' },
  { key: 'loto', name: 'ロト', desc: '6桁の数字を予想し、位置ごとの一致桁数で配当(最大×1000)' },
  { key: 'slot', name: 'スロット', desc: '8ラインスロット、絵柄を揃えて最大×7777' },
  { key: 'kuji', name: 'くじ', desc: '2択ダブルアップ、連勝で配当が2倍ずつ増える' },
  { key: 'donuts', name: 'ドーナツ', desc: 'Hi&Lo、前のカードより大きいか小さいかを当てる' },
];
const gameComponents: Record<string, Component> = {
  saikoro: SaikoroGame,
  loto: LotoGame,
  slot: SlotGame,
  kuji: KujiGame,
  donuts: DonutsGame,
};

const yen = (n: number) => n.toLocaleString('ja-JP');
const selected = ref<string | null>(null);
const current = computed(() => (selected.value ? gameComponents[selected.value] : null));
</script>

<template>
  <div class="casino-page">
    <button class="btn back" @click="emit('back')">街に戻る</button>
    <div class="casino-header">
      <div class="title">ＣＡＳＩＮＯ</div>
      <div class="money">所持金：{{ yen(player.money) }}円</div>
    </div>

    <template v-if="!selected">
      <div class="game-menu">
        <button v-for="g in games" :key="g.key" class="game-card" @click="selected = g.key">
          <div class="gname">{{ g.name }}</div>
          <div class="gdesc">{{ g.desc }}</div>
        </button>
      </div>
    </template>
    <template v-else>
      <button class="btn menu-back" @click="selected = null">← ゲーム選択にもどる</button>
      <component :is="current" :player="player" @update="emit('update', $event)" />
    </template>
  </div>
</template>

<style scoped>
.casino-page {
  background: #1a0a2a;
  min-height: 80vh;
  padding: 6px;
}
.btn.back {
  margin-bottom: 6px;
}
.casino-header {
  text-align: center;
  margin-bottom: 14px;
}
.casino-header .title {
  display: inline-block;
  font-size: 26px;
  font-weight: bold;
  letter-spacing: 8px;
  color: #ffd700;
  text-shadow:
    0 0 8px #ff8c00,
    0 0 2px #fff;
  padding: 6px 24px;
  border: 2px solid #ffd700;
  border-radius: 8px;
  background: linear-gradient(180deg, #33104d, #1a0a2a);
}
.casino-header .money {
  margin-top: 6px;
  color: #ffe;
  font-size: 13px;
}
.game-menu {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 10px;
  max-width: 760px;
  margin: 0 auto;
}
.game-card {
  text-align: left;
  padding: 12px 14px;
  border: 1px solid #7a5cff;
  border-radius: 8px;
  background: linear-gradient(180deg, #2a1650, #1c0e38);
  color: #fff;
  cursor: pointer;
}
.game-card:hover {
  border-color: #ffd700;
  background: linear-gradient(180deg, #3a1f6b, #24123f);
}
.game-card .gname {
  font-size: 16px;
  font-weight: bold;
  color: #ffd700;
  margin-bottom: 4px;
}
.game-card .gdesc {
  font-size: 12px;
  color: #cbb8ff;
}
.menu-back {
  margin-bottom: 10px;
}
</style>
