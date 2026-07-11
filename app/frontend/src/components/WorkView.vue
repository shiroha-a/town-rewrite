<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { api, type Player, type WorkResult } from '../api';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

// レガシー準拠: 仕事ボタンを押した時点で働き、この画面は「結果」を表示する。
// 給料・昇給・ボーナスはサーバの work_result をそのまま使い、旧do_workのメッセージを再現。
interface ViewResult extends WorkResult {
  energyUsed: number;
  nouUsed: number;
}
const result = ref<ViewResult | null>(null);
const error = ref('');
const done = ref(false);
const yen = (n: number) => n.toLocaleString('ja-JP');

onMounted(async () => {
  try {
    const before = props.player;
    const after = await api.work(props.player.id);
    emit('update', after);
    result.value = {
      ...after.work_result,
      energyUsed: before.status.energy - after.status.energy,
      nouUsed: before.status.nou_energy - after.status.nou_energy,
    };
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    done.value = true;
  }
});
</script>

<template>
  <div class="work-result">
    <button class="btn back" @click="emit('back')">街に戻る</button>

    <div class="result-box" v-if="done">
      <template v-if="result">
        <div class="dai">●仕事に出かけました</div>
        <!-- 給料(支払間隔到達時のみ支給) -->
        <div class="line" v-if="result.pay > 0 && result.pay_every === 1">
          ・{{ yen(result.pay) }}円の給料をもらいました！
        </div>
        <div class="line" v-else-if="result.pay > 0">
          ・{{ yen(result.pay) }}円（{{ yen(result.this_salary) }}円×{{ result.pay_every }}回出勤）の給料が出ました！
        </div>
        <div class="line muted" v-else>・今回は給料日ではありませんでした。</div>
        <!-- 経験値・レベル -->
        <div class="line" :class="{ minus: result.exp_gained < 0 }">
          ・経験値が{{ result.exp_gained >= 0 ? '+' : '' }}{{ result.exp_gained }}（レベル{{ result.new_level }}）
        </div>
        <!-- 昇給(レベルアップ時) -->
        <template v-if="result.leveled_up">
          <div class="line levelup">・レベルが{{ result.new_level }}に上がりました！</div>
          <div class="line raise">・{{ yen(result.this_salary) }}円／1回に昇給しました。</div>
        </template>
        <!-- レベルアップボーナス -->
        <div class="line bonus" v-if="result.bonus > 0">
          ・{{ yen(result.bonus) }}円のボーナスが出ました！
        </div>
        <!-- マスター認定 -->
        <div class="line master" v-for="m in result.mastered" :key="m">・「{{ m }}」をマスターしました！</div>
        <!-- パワー消費 -->
        <div class="line">・身体パワーを{{ result.energyUsed }}使いました。</div>
        <div class="line" v-if="result.nouUsed > 0">・頭脳パワーを{{ result.nouUsed }}使いました。</div>
      </template>
      <template v-else>
        <div class="err">ERROR !</div>
        <div class="line" data-test="message">{{ error }}</div>
      </template>
      <div class="back-line">
        <button class="btn" @click="emit('back')">戻る</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 背景は body の wall029 プレイドをそのまま見せる(レガシー準拠) */
.work-result {
  min-height: 70vh;
  padding: 6px;
}
.btn.back {
  margin-bottom: 6px;
}
.result-box {
  width: 320px;
  margin: 0 auto;
  background: #fff;
  border: 1px solid #999;
  padding: 10px 14px;
  color: #006699;
  line-height: 1.9;
}
.dai {
  color: #000;
  font-weight: bold;
}
.line {
  color: #006699;
}
.line.minus {
  color: #cc3300;
}
.line.levelup {
  color: #e07800;
  font-weight: bold;
}
.line.raise {
  color: #067a06;
  font-weight: bold;
}
.line.bonus {
  color: #cc3300;
  font-weight: bold;
}
.line.master {
  color: #cc0088;
  font-weight: bold;
}
.line.muted {
  color: #999;
}
.err {
  color: #ff3300;
  font-weight: bold;
  text-align: center;
}
.back-line {
  text-align: center;
  margin-top: 8px;
}
</style>
