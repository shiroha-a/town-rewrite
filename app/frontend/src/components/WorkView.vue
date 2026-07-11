<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { api, type Player } from '../api';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

// レガシー準拠: 仕事ボタンを押した時点で働き、この画面は「結果」を表示する。
interface WorkResult {
  money: number;
  energy: number;
  nou: number;
  exp: number;
  level: number;
  leveledUp: boolean;
  mastered: string[]; // 今回新たにマスターした職業
}
const result = ref<WorkResult | null>(null);
const error = ref('');
const done = ref(false);
const yen = (n: number) => n.toLocaleString('ja-JP');

onMounted(async () => {
  try {
    const before = props.player;
    const after = await api.work(props.player.id);
    emit('update', after);
    const newlyMastered = after.status.mastered_jobs.filter(
      (m) => !before.status.mastered_jobs.includes(m),
    );
    result.value = {
      money: after.money - before.money,
      energy: after.status.energy - before.status.energy,
      nou: after.status.nou_energy - before.status.nou_energy,
      exp: after.status.job_exp - before.status.job_exp,
      level: after.status.job_level,
      leveledUp: after.status.job_level > before.status.job_level,
      mastered: newlyMastered,
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
        <div class="line" v-if="result.money > 0">・{{ yen(result.money) }}円の給料をもらいました！</div>
        <div class="line" v-else>・今回は給料日ではありませんでした。</div>
        <div class="line" :class="{ minus: result.exp < 0 }">
          ・経験値が{{ result.exp >= 0 ? '+' : '' }}{{ result.exp }}(レベル{{ result.level }})
        </div>
        <div class="line levelup" v-if="result.leveledUp">・レベルアップ！</div>
        <div class="line master" v-for="m in result.mastered" :key="m">・「{{ m }}」をマスターしました！</div>
        <div class="line">・身体パワーを{{ Math.abs(result.energy) }}使いました。</div>
        <div class="line" v-if="result.nou < 0">・頭脳パワーを{{ Math.abs(result.nou) }}使いました。</div>
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
.line.master {
  color: #cc0088;
  font-weight: bold;
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
