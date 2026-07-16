<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { api, type Player, type Greeting } from '../api';

// あいさつ: 街の一言掲示板。投稿すると報酬(ランダム+ジャンケンで倍/半)がもらえる。
const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const isAdmin = computed(() => props.player.roles.includes('admin'));

const CATEGORIES = ['あいさつ', '雑談', '今日の出来事', '今の気分', 'なんとなく', 'お話ししよう', '宣伝'];
const COLORS = [
  { label: '黒', value: '#333333' },
  { label: '緑', value: '#009933' },
  { label: '茶', value: '#996633' },
  { label: '紫', value: '#663399' },
  { label: '桃', value: '#cc3399' },
  { label: '橙', value: '#ff6600' },
  { label: '紺', value: '#333399' },
];
const JANKEN = [
  { label: 'ジャンケンしない', value: 'none' },
  { label: 'おまかせ', value: 'omakase' },
  { label: 'グー', value: 'gu' },
  { label: 'チョキ', value: 'choki' },
  { label: 'パー', value: 'pa' },
];

const list = ref<Greeting[]>([]);
const category = ref('あいさつ');
const body = ref('');
const color = ref('#333333');
const janken = ref('none');
const message = ref('');
const kind = ref<'ok' | 'error'>('ok');
const busy = ref(false);

const yen = (n: number) => n.toLocaleString('ja-JP');

async function load() {
  try {
    list.value = await api.greetings(30);
  } catch (e) {
    fail(e);
  }
}
onMounted(load);

function fail(e: unknown) {
  message.value = e instanceof Error ? e.message : String(e);
  kind.value = 'error';
}

async function post() {
  if (!body.value.trim()) {
    message.value = 'ひとことを入力してください。';
    kind.value = 'error';
    return;
  }
  busy.value = true;
  message.value = '';
  try {
    const res = await api.postGreeting(props.player.id, category.value, body.value, color.value, janken.value);
    emit('update', res.player);
    const r = res.result;
    const parts: string[] = ['投稿しました。'];
    if (r.janken) parts.push(`ジャンケン${r.janken}(相手は${r.janken_pc})`);
    if (r.jackpot) parts.push('大当たり！');
    if (r.reward > 0) parts.push(`${yen(r.reward)}円もらいました。`);
    else if (r.reward < 0) parts.push(`${yen(-r.reward)}円払いました。`);
    if (r.fine) parts.push('NGワードで罰金30,000円!');
    message.value = parts.join(' ');
    kind.value = 'ok';
    body.value = '';
    await load();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}

async function del(g: Greeting) {
  if (!window.confirm('この発言を削除しますか?')) return;
  busy.value = true;
  try {
    await api.adminDeleteGreeting(props.player.id, g.id);
    await load();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="facility-page chat-page">
    <button class="btn back" @click="emit('back')">街に戻る</button>

    <div class="fac-header">
      <div class="lead">
        街のみんなにひとことどうぞ。投稿すると報酬がもらえます(ジャンケンに勝つと倍、負けると半分)。<br />
        「宣伝」は2万円かかります。NGワードには罰金があります。
      </div>
      <div class="title">あいさつ</div>
    </div>

    <div v-if="message" :class="['message', kind]" data-test="message">{{ message }}</div>

    <div class="panel-white post-form">
      <div class="opts">
        <label>種類
          <select v-model="category">
            <option v-for="c in CATEGORIES" :key="c" :value="c">{{ c }}</option>
            <option v-if="isAdmin" value="管理人">管理人</option>
          </select>
        </label>
        <label>色
          <select v-model="color">
            <option v-for="c in COLORS" :key="c.value" :value="c.value">{{ c.label }}</option>
          </select>
        </label>
        <label>ジャンケン
          <select v-model="janken">
            <option v-for="j in JANKEN" :key="j.value" :value="j.value">{{ j.label }}</option>
          </select>
        </label>
      </div>
      <div class="post-row">
        <input v-model="body" maxlength="60" placeholder="ひとこと(60字まで)" @keyup.enter="post" />
        <button class="btn primary" :disabled="busy" @click="post">投稿</button>
      </div>
    </div>

    <div class="panel-white board">
      <h3>みんなの声（{{ list.length }}）</h3>
      <div v-if="!list.length" class="muted">まだ投稿がありません。</div>
      <div v-for="g in list" :key="g.id" class="post" :data-test="`post-${g.id}`">
        <span class="pname">{{ g.user_name }}</span>
        <span class="pcat">（{{ g.category }}）</span>
        <span class="pbody" :style="{ color: g.color }">{{ g.body }}</span>
        <span v-if="g.janken" class="pjanken">[ジャンケン{{ g.janken }}]</span>
        <span class="pdate">{{ g.posted_at }}</span>
        <button v-if="isAdmin" class="btn mini danger" :disabled="busy" @click="del(g)">削除</button>
      </div>
    </div>

    <div style="text-align: center; margin-top: 8px">
      <button class="btn" @click="emit('back')">街に戻る</button>
    </div>
  </div>
</template>

<style scoped>
.chat-page {
  background-color: #f0ead8;
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
  border: 1px solid #ccb;
  padding: 8px 12px;
  font-size: 12px;
  color: #333;
  line-height: 1.6;
}
.fac-header .title {
  flex: 0 0 160px;
  background: #cc6600;
  color: #fff;
  font-weight: bold;
  font-size: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid #ccb;
}
.panel-white {
  background: #fff;
  border: 1px solid #ccb;
  padding: 10px;
  margin-bottom: 8px;
}
.panel-white h3 {
  margin: 0 0 8px;
  font-size: 14px;
  color: #663300;
}
.post-form .opts {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 12px;
  margin-bottom: 8px;
}
.post-form .opts label {
  display: flex;
  align-items: center;
  gap: 4px;
}
.post-row {
  display: flex;
  gap: 6px;
}
.post-row input {
  flex: 1 1 auto;
}
.board .post {
  padding: 4px 2px;
  border-bottom: 1px solid #eee;
  font-size: 13px;
  line-height: 1.6;
}
.pname {
  color: #333399;
  font-weight: bold;
}
.pcat {
  color: #999;
  font-size: 11px;
}
.pbody {
  margin-left: 4px;
  word-break: break-word;
}
.pjanken {
  color: #cc6600;
  font-size: 11px;
  margin-left: 4px;
}
.pdate {
  color: #aab;
  font-size: 10px;
  margin-left: 6px;
}
.muted {
  color: #999;
  font-size: 12px;
}
.btn.mini {
  padding: 1px 6px;
  font-size: 11px;
  margin-left: 6px;
}
.btn.danger {
  background: #cc3333;
  color: #fff;
  border-color: #992222;
}
.btn.primary {
  background: #cc6600;
  color: #fff;
  border-color: #994c00;
}
</style>
