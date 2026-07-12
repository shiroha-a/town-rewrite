<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { api, type Player, type PublicSummary, type PublicProfile } from '../api';
import { satietyLabel } from '../params';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ back: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const roster = ref<PublicSummary[]>([]);
const selectedId = ref(props.player.id);
const other = ref<PublicProfile | null>(null);
const message = ref('');

const isSelf = computed(() => selectedId.value === props.player.id);
// 表示対象: 自分は player(全項目)、他人は取得した公開プロフィール。
const view = computed<Player | PublicProfile | null>(() => (isSelf.value ? props.player : other.value));
const weightKg = computed(() => (view.value ? (view.value.status.weight_g / 1000).toFixed(1) : '0'));

const zunou = [
  { label: '国語', key: 'kokugo' },
  { label: '数学', key: 'suugaku' },
  { label: '理科', key: 'rika' },
  { label: '社会', key: 'syakai' },
  { label: '英語', key: 'eigo' },
  { label: '音楽', key: 'ongaku' },
  { label: '美術', key: 'bijutsu' },
] as const;
const shintai = [
  { label: 'ルックス', key: 'looks' },
  { label: '体力', key: 'tairyoku' },
  { label: '健康', key: 'kenkou' },
  { label: 'スピード', key: 'speed' },
  { label: 'パワー', key: 'power' },
  { label: '腕力', key: 'wanryoku' },
  { label: '脚力', key: 'kyakuryoku' },
] as const;
const others = [
  { label: 'LOVE', key: 'love' },
  { label: '面白さ', key: 'omoshirosa' },
] as const;

onMounted(async () => {
  try {
    roster.value = await api.listPlayers();
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
  }
});

async function select(id: number) {
  if (id === selectedId.value) return;
  selectedId.value = id;
  other.value = null;
  message.value = '';
  if (id !== props.player.id) {
    try {
      other.value = await api.playerProfile(id);
    } catch (e) {
      message.value = e instanceof Error ? e.message : String(e);
    }
  }
}
</script>

<template>
  <div class="facility-page profile-page">
    <button class="btn back" @click="emit('back')">街に戻る</button>
    <div class="profile-header">
      <div class="lead">役場です。住民名鑑で、各住民のステータスを見ることができます。</div>
      <div class="title">役　場</div>
    </div>

    <div v-if="message" class="message error">{{ message }}</div>

    <div class="profile-layout">
      <!-- 住民名鑑 -->
      <div class="roster">
        <div class="roster-head">住民一覧({{ roster.length }}人)</div>
        <button
          v-for="m in roster"
          :key="m.id"
          class="roster-item"
          :class="{ active: m.id === selectedId }"
          @click="select(m.id)"
        >
          {{ m.display_name }}<span class="rjob">（{{ m.job }} Lv{{ m.job_level }}）</span>
        </button>
      </div>

      <!-- プロフィール本体 -->
      <div class="card" v-if="view">
        <div class="pname">
          ●{{ view.display_name }}
          <span class="self-badge" v-if="isSelf">（あなた）</span>
        </div>
        <table class="pinfo">
          <tbody>
            <tr><th>職業</th><td>{{ view.status.job }}（レベル{{ view.status.job_level }} / 経験値{{ view.status.job_exp }} / 勤務{{ view.status.job_kaisuu }}回）</td></tr>
            <tr v-if="view.status.mastered_jobs.length"><th>マスター職</th><td>{{ view.status.mastered_jobs.join('、') }}</td></tr>
            <tr>
              <th>コンディション</th>
              <td><span :class="{ sick: view.status.disease_name }">{{ view.status.condition }}</span></td>
            </tr>
            <tr><th>身長 / 体重</th><td>{{ view.status.height_cm }}cm / {{ weightKg }}kg</td></tr>
            <tr><th>体型</th><td>{{ view.status.body_type }}（BMI {{ view.status.bmi }}）</td></tr>
            <tr><th>身体パワー</th><td>{{ view.status.energy }} / {{ view.status.energy_max }}</td></tr>
            <tr><th>頭脳パワー</th><td>{{ view.status.nou_energy }} / {{ view.status.nou_energy_max }}</td></tr>
            <tr><th>空腹度</th><td>{{ satietyLabel(view.status.satiety) }}</td></tr>
            <tr v-if="isSelf"><th>持ち金 / 貯金</th><td class="money">{{ yen(player.money) }}円 / {{ yen(player.savings) }}円</td></tr>
          </tbody>
        </table>

        <div class="param-grid">
          <div class="param-col">
            <div class="phead">頭　脳</div>
            <div v-for="p in zunou" :key="p.key" class="prow">
              <span class="plabel">{{ p.label }}</span><span class="pval">{{ view.params[p.key] }}</span>
            </div>
          </div>
          <div class="param-col">
            <div class="phead">身　体</div>
            <div v-for="p in shintai" :key="p.key" class="prow">
              <span class="plabel">{{ p.label }}</span><span class="pval">{{ view.params[p.key] }}</span>
            </div>
          </div>
          <div class="param-col">
            <div class="phead">その他</div>
            <div v-for="p in others" :key="p.key" class="prow">
              <span class="plabel">{{ p.label }}</span><span class="pval">{{ view.params[p.key] }}</span>
            </div>
          </div>
        </div>
      </div>
      <div class="card" v-else>読み込み中…</div>
    </div>

    <div style="text-align: center; margin-top: 8px">
      <button class="btn" @click="emit('back')">街に戻る</button>
    </div>
  </div>
</template>

<style scoped>
.profile-page {
  background-color: #e8dcc0;
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  margin-bottom: 6px;
}
.profile-header {
  display: flex;
  margin-bottom: 8px;
  border: 1px solid #333;
}
.profile-header .lead {
  flex: 1 1 auto;
  background: #fff;
  padding: 8px 12px;
  color: #333;
}
.profile-header .title {
  flex: 0 0 140px;
  background: #997a44;
  color: #fff;
  font-weight: bold;
  font-size: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.profile-layout {
  display: flex;
  gap: 8px;
  align-items: flex-start;
}
.roster {
  flex: 0 0 180px;
  background: #fff;
  border: 1px solid #999;
  max-height: 70vh;
  overflow-y: auto;
}
.roster-head {
  background: #997a44;
  color: #fff;
  font-size: 12px;
  padding: 4px 8px;
  position: sticky;
  top: 0;
}
.roster-item {
  display: block;
  width: 100%;
  text-align: left;
  border: 0;
  border-bottom: 1px solid #eee;
  background: #fff;
  padding: 5px 8px;
  font-size: 12px;
  cursor: pointer;
}
.roster-item:hover {
  background: #f5eede;
}
.roster-item.active {
  background: #ffe9b0;
  font-weight: bold;
}
.rjob {
  color: #888;
  font-size: 11px;
}
.card {
  flex: 1 1 auto;
  background: #fff;
  border: 1px solid #999;
  padding: 12px;
}
.pname {
  font-size: 15px;
  font-weight: bold;
  color: #663300;
  margin-bottom: 8px;
}
.self-badge {
  color: #cc3300;
  font-size: 12px;
}
.pinfo {
  border-collapse: collapse;
  font-size: 13px;
  margin-bottom: 12px;
  width: 100%;
}
.pinfo th {
  text-align: left;
  background: #f0e6cf;
  color: #663300;
  padding: 3px 8px;
  border: 1px solid #e0d0a8;
  white-space: nowrap;
  width: 120px;
}
.pinfo td {
  padding: 3px 8px;
  border: 1px solid #eee;
}
.pinfo td.money {
  color: #cc3300;
  font-weight: bold;
}
.sick {
  color: #cc0033;
  font-weight: bold;
}
.param-grid {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}
.param-col {
  flex: 1 1 120px;
  border: 1px solid #ddd;
}
.phead {
  background: #cbb684;
  color: #3a2a10;
  text-align: center;
  font-size: 12px;
  font-weight: bold;
  padding: 2px;
}
.prow {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
  padding: 2px 8px;
  border-bottom: 1px solid #f0f0f0;
}
.plabel {
  color: #555;
}
.pval {
  font-weight: bold;
  color: #333;
}
</style>
