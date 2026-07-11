<script setup lang="ts">
import { ref, onMounted } from 'vue';
import { api, type Player, type JobOption } from '../api';
import { PARAM_COLUMNS } from '../params';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const jobs = ref<JobOption[]>([]);
const message = ref('');
const kind = ref<'ok' | 'error'>('ok');
const busy = ref(false);
const yen = (n: number) => n.toLocaleString('ja-JP');

onMounted(async () => {
  try {
    jobs.value = await api.jobs();
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
    kind.value = 'error';
  }
});

const stars = (n: number) => '★'.repeat(Math.max(1, n));

// 前提マスター職を満たしているか(なければ常にtrue)。
function masterOk(job: JobOption): boolean {
  return job.require_master === '' || props.player.status.mastered_jobs.includes(job.require_master);
}

async function take(job: JobOption) {
  busy.value = true;
  message.value = '';
  try {
    emit('update', await api.changeJob(props.player.id, job.name));
    message.value = `${job.name}に転職しました。仕事ボタンが使えるようになりました。`;
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
  <div class="facility-page job-page">
    <button class="btn back" @click="emit('back')">街に戻る</button>
    <div class="job-header">
      <div class="lead">
        必要な条件を満たしていればその職業に就くことができます。<br />
        なりたい職業めざして勉強・トレーニングに励みましょう。<br />
        ※転職をすると経験値・出勤回数は0に戻ります。<br />
        現在の職業：<b>{{ player.status.job }}</b>
      </div>
      <div class="title">職業安定所</div>
    </div>

    <div v-if="message" :class="['message', kind]" data-test="message">{{ message }}</div>

    <div class="panel-white">
      <div class="cap">必要パラメータ(その職業に就くために必要な値)</div>
      <div class="table-scroll">
        <table class="job-table">
          <thead>
            <tr>
              <th class="l">職業</th>
              <th>ランク</th>
              <th v-for="c in PARAM_COLUMNS" :key="c.key" class="p">{{ c.label }}</th>
              <th>給料</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="job in jobs" :key="job.id" :data-test="`job-${job.id}`">
              <td class="l">
                {{ job.name }}
                <span v-if="job.require_master" class="req-master" :class="{ met: masterOk(job) }">
                  （要「{{ job.require_master }}」マスター）
                </span>
              </td>
              <td class="rank">{{ stars(job.rank) }}</td>
              <td v-for="c in PARAM_COLUMNS" :key="c.key" class="p" :class="{ req: (job.requirements[c.key] ?? 0) > 0 }">
                {{ job.requirements[c.key] ?? 0 }}
              </td>
              <td class="right money">{{ yen(job.pay) }}円</td>
              <td class="right">
                <button
                  class="btn"
                  :disabled="busy || player.status.job === job.name || !masterOk(job)"
                  @click="take(job)"
                >
                  {{ player.status.job === job.name ? '就業中' : '就く' }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div style="text-align: center; margin-top: 8px">
      <button class="btn" @click="emit('back')">街に戻る</button>
    </div>
  </div>
</template>

<style scoped>
.job-page {
  background-color: #669966;
  background-image: url(/img/shop_bak.gif);
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  margin-bottom: 6px;
}
.job-header {
  display: flex;
  margin-bottom: 8px;
  border: 1px solid #333;
}
.job-header .lead {
  flex: 1 1 auto;
  background: #fff;
  padding: 8px 12px;
  color: #333;
  line-height: 1.6;
}
.job-header .title {
  flex: 0 0 140px;
  background: #336633;
  color: #fff;
  font-weight: bold;
  font-size: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.panel-white {
  background: #fff;
  border: 1px solid #333;
  padding: 8px;
}
.cap {
  font-size: 11px;
  color: #cc0000;
  margin-bottom: 4px;
}
.table-scroll {
  overflow-x: auto;
}
.job-table {
  border-collapse: collapse;
  font-size: 11px;
  white-space: nowrap;
}
.job-table th {
  background: #cfe8cf;
  color: #063;
  padding: 2px 4px;
  border: 1px solid #b0d0b0;
}
.job-table td {
  padding: 2px 4px;
  border: 1px solid #eee;
  text-align: center;
}
.job-table th.l,
.job-table td.l {
  text-align: left;
}
.job-table td.right {
  text-align: right;
}
.job-table th.p,
.job-table td.p {
  width: 20px;
  color: #bbb;
}
.job-table td.p.req {
  color: #cc0000;
  font-weight: bold;
  background: #ffecec;
}
.money {
  color: #cc3300;
  font-weight: bold;
}
.job-table td.rank {
  color: #e0a000;
  letter-spacing: -1px;
}
.req-master {
  font-size: 10px;
  color: #cc0000;
}
.req-master.met {
  color: #067a06;
}
</style>
