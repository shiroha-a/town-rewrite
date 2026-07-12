<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue';
import {
  api,
  type Player,
  type EffectOp,
  type Condition,
  type AdminItem,
  type AdminJob,
  type JobPayload,
  type SimResult,
  type AdminPlayerSummary,
  type AdminPlayerPayload,
} from '../api';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ back: [] }>();

const isAdmin = computed(() => props.player.roles.includes('admin'));

// 各セクションの開閉。既定は折りたたみ(false)。
const open = reactive({ item: false, job: false, user: false });

// 効果/条件で対象にできるパラメータ。
const PARAM_OPTIONS = [
  'energy',
  'nou_energy',
  'satiety',
  'kokugo',
  'suugaku',
  'rika',
  'syakai',
  'eigo',
  'ongaku',
  'bijutsu',
  'looks',
  'tairyoku',
  'kenkou',
  'speed',
  'power',
  'wanryoku',
  'kyakuryoku',
  'love',
  'omoshirosa',
];

const item = reactive<{ name: string; category: string; price: number; effect: EffectOp[] }>({
  name: '',
  category: '',
  price: 0,
  effect: [],
});
function emptyJob(): JobPayload {
  return {
    name: '',
    requirements: [],
    effect: [],
    salary: 1000,
    pay_interval: 1,
    bonus_rate: 0,
    raise_rate: 0,
    rank: 1,
    require_master: '',
    body_cost: 1,
    nou_cost: 0,
    enabled: true,
  };
}
const job = reactive<JobPayload>(emptyJob());

const sim = ref<SimResult | null>(null);
const message = ref('');
const kind = ref<'ok' | 'error'>('ok');
const busy = ref(false);
const items = ref<AdminItem[]>([]);
const jobs = ref<AdminJob[]>([]);

function addOp(list: EffectOp[]) {
  list.push({ op: 'add_param', param: 'tairyoku', amount: 1 });
}
function addReq(list: Condition[]) {
  list.push({ pred: 'param_gte', param: 'tairyoku', value: 10 });
}
function fail(e: unknown) {
  message.value = e instanceof Error ? e.message : String(e);
  kind.value = 'error';
}

const players = ref<AdminPlayerSummary[]>([]);
async function refresh() {
  if (!isAdmin.value) return;
  try {
    items.value = await api.adminListItems(props.player.id);
    jobs.value = await api.adminListJobs(props.player.id);
    players.value = await api.adminListPlayers(props.player.id);
  } catch (e) {
    fail(e);
  }
}
onMounted(refresh);

// プレイヤー編集(頭脳/身体/その他の各パラメータ)。
const DETAIL_PARAMS: { key: keyof Player['params']; label: string }[] = [
  { key: 'kokugo', label: '国語' },
  { key: 'suugaku', label: '数学' },
  { key: 'rika', label: '理科' },
  { key: 'syakai', label: '社会' },
  { key: 'eigo', label: '英語' },
  { key: 'ongaku', label: '音楽' },
  { key: 'bijutsu', label: '美術' },
  { key: 'looks', label: 'ルックス' },
  { key: 'tairyoku', label: '体力' },
  { key: 'kenkou', label: '健康' },
  { key: 'speed', label: 'スピード' },
  { key: 'power', label: 'パワー' },
  { key: 'wanryoku', label: '腕力' },
  { key: 'kyakuryoku', label: '脚力' },
  { key: 'love', label: 'LOVE' },
  { key: 'omoshirosa', label: '面白さ' },
];
const editingPlayer = ref<(AdminPlayerPayload & { id: number }) | null>(null);
// 職業の選択肢: 学生(初期職) + content_jobs。編集中プレイヤーの現職も必ず含める。
const jobOptions = computed(() => {
  const set = new Set<string>(['学生', ...jobs.value.map((j) => j.name)]);
  if (editingPlayer.value?.job) set.add(editingPlayer.value.job);
  return [...set];
});
async function openEditPlayer(id: number) {
  message.value = '';
  try {
    const p = await api.getPlayer(id);
    editingPlayer.value = {
      id: p.id,
      display_name: p.display_name,
      money: p.money,
      is_admin: p.roles.includes('admin'),
      params: { ...p.params },
      energy: p.status.energy,
      nou_energy: p.status.nou_energy,
      satiety: p.status.satiety,
      job: p.status.job,
      job_level: p.status.job_level,
      job_exp: p.status.job_exp,
      disease_index: p.status.disease_index,
      height_cm: p.status.height_cm,
      weight_g: p.status.weight_g,
    };
  } catch (e) {
    fail(e);
  }
}
function closeEditPlayer() {
  editingPlayer.value = null;
}
async function savePlayer() {
  if (!editingPlayer.value) return;
  busy.value = true;
  message.value = '';
  try {
    const { id, ...payload } = editingPlayer.value;
    await api.adminUpdatePlayer(props.player.id, id, payload);
    message.value = `ユーザー「${payload.display_name}」を更新しました。`;
    kind.value = 'ok';
    closeEditPlayer();
    await refresh();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}
async function deletePlayer() {
  if (!editingPlayer.value) return;
  if (!window.confirm(`ユーザー「${editingPlayer.value.display_name}」を論理削除しますか?`)) return;
  busy.value = true;
  message.value = '';
  try {
    await api.adminDeletePlayer(props.player.id, editingPlayer.value.id);
    message.value = 'ユーザーを論理削除しました。';
    kind.value = 'ok';
    closeEditPlayer();
    await refresh();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}

async function simulate() {
  busy.value = true;
  message.value = '';
  try {
    // 仮想的な標準state(お金10万・全パラメータ10/上限999)で試算する。
    const params = Object.fromEntries(PARAM_OPTIONS.map((p) => [p, { value: 10, max: 999 }]));
    sim.value = await api.adminSimulate(props.player.id, item.effect, { money: 100000, params });
  } catch (e) {
    sim.value = null;
    fail(e);
  } finally {
    busy.value = false;
  }
}

async function createItem() {
  busy.value = true;
  message.value = '';
  try {
    await api.adminCreateItem(props.player.id, {
      name: item.name,
      category: item.category,
      price: item.price,
      effect: item.effect,
    });
    message.value = `アイテム「${item.name}」を作成しました。`;
    kind.value = 'ok';
    item.name = '';
    item.category = '';
    item.price = 0;
    item.effect = [];
    await refresh();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}

async function createJob() {
  busy.value = true;
  message.value = '';
  try {
    await api.adminCreateJob(props.player.id, { ...job });
    message.value = `職業「${job.name}」を作成しました。`;
    kind.value = 'ok';
    Object.assign(job, emptyJob());
    await refresh();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}

// 一覧クリックで開く職業編集(詳細)。
const editingJob = ref<AdminJob | null>(null);
function openEditJob(j: AdminJob) {
  editingJob.value = {
    ...j,
    requirements: j.requirements.map((r) => ({ ...r })),
    effect: j.effect.map((o) => ({ ...o })),
  };
}
function closeEditJob() {
  editingJob.value = null;
}
async function saveJob() {
  if (!editingJob.value) return;
  busy.value = true;
  message.value = '';
  try {
    const e = editingJob.value;
    await api.adminUpdateJob(props.player.id, e.id, {
      name: e.name,
      requirements: e.requirements,
      effect: e.effect,
      salary: e.salary,
      pay_interval: e.pay_interval,
      bonus_rate: e.bonus_rate,
      raise_rate: e.raise_rate,
      rank: e.rank,
      require_master: e.require_master,
      body_cost: e.body_cost,
      nou_cost: e.nou_cost,
      enabled: e.enabled,
    });
    message.value = `職業「${e.name}」を更新しました。`;
    kind.value = 'ok';
    closeEditJob();
    await refresh();
  } catch (err) {
    fail(err);
  } finally {
    busy.value = false;
  }
}
async function deleteJob() {
  if (!editingJob.value) return;
  if (!window.confirm(`職業「${editingJob.value.name}」を削除しますか?`)) return;
  busy.value = true;
  message.value = '';
  try {
    await api.adminDeleteJob(props.player.id, editingJob.value.id);
    message.value = '職業を削除しました。';
    kind.value = 'ok';
    closeEditJob();
    await refresh();
  } catch (err) {
    fail(err);
  } finally {
    busy.value = false;
  }
}

// 一覧クリックで開くアイテム編集(詳細)。編集対象は作業用コピー。
const editing = ref<AdminItem | null>(null);
function openEdit(it: AdminItem) {
  editing.value = { ...it, effect: it.effect.map((o) => ({ ...o })) };
}
function closeEdit() {
  editing.value = null;
}
async function saveEdit() {
  if (!editing.value) return;
  busy.value = true;
  message.value = '';
  try {
    await api.adminUpdateItem(props.player.id, editing.value.id, {
      name: editing.value.name,
      category: editing.value.category,
      price: editing.value.price,
      effect: editing.value.effect,
      enabled: editing.value.enabled,
    });
    message.value = `アイテム「${editing.value.name}」を更新しました。`;
    kind.value = 'ok';
    closeEdit();
    await refresh();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}
async function deleteEdit() {
  if (!editing.value) return;
  if (!window.confirm(`アイテム「${editing.value.name}」を削除しますか?`)) return;
  busy.value = true;
  message.value = '';
  try {
    await api.adminDeleteItem(props.player.id, editing.value.id);
    message.value = 'アイテムを削除しました。';
    kind.value = 'ok';
    closeEdit();
    await refresh();
  } catch (e) {
    fail(e);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="facility-page admin-page">
    <button class="btn back" @click="emit('back')">街に戻る</button>
    <div class="admin-header">
      <div class="lead">管理者用のコンテンツ作成画面です。アイテム・職業の追加と効果の試算ができます。</div>
      <div class="title">管理者</div>
    </div>

    <div v-if="!isAdmin" class="message error">この画面は管理者のみ利用できます。</div>

    <template v-else>
      <div v-if="message" :class="['message', kind]" data-test="message">{{ message }}</div>

      <div class="admin-sections">
        <!-- アイテム -->
        <section class="fold">
          <button class="fold-head" @click="open.item = !open.item">
            <span class="caret">{{ open.item ? '▼' : '▶' }}</span> アイテム（{{ items.length }}）
          </button>
          <div v-if="open.item" class="fold-body">
            <section class="panel">
              <h3>アイテム作成</h3>
              <label>品名<input v-model="item.name" placeholder="例: 特製栄養ドリンク" /></label>
              <label>カテゴリ<input v-model="item.category" placeholder="例: ドリンク" /></label>
              <label>値段<input type="number" v-model.number="item.price" /></label>
              <div class="ops">
                <div class="ops-head">使用効果</div>
                <div v-for="(op, i) in item.effect" :key="i" class="op-row">
                  <select v-model="op.op">
                    <option value="add_param">パラメータ</option>
                    <option value="add_money">お金</option>
                  </select>
                  <select v-if="op.op === 'add_param'" v-model="op.param">
                    <option v-for="p in PARAM_OPTIONS" :key="p" :value="p">{{ p }}</option>
                  </select>
                  <input type="number" v-model.number="op.amount" />
                  <button class="btn mini" @click="item.effect.splice(i, 1)">×</button>
                </div>
                <button class="btn mini" @click="addOp(item.effect)">＋効果を追加</button>
              </div>
              <div class="actions">
                <button class="btn" :disabled="busy" @click="simulate">効果を試算</button>
                <button class="btn primary" :disabled="busy || !item.name" @click="createItem">作成</button>
              </div>
              <div v-if="sim" class="sim-box">
                <div class="ops-head">試算結果</div>
                <div v-if="sim.plan.money_delta !== 0">お金: {{ sim.plan.money_delta > 0 ? '+' : '' }}{{ sim.plan.money_delta }}円</div>
                <div v-for="pc in sim.plan.params" :key="pc.name">{{ pc.name }}: {{ pc.old_value }} → {{ pc.new_value }}</div>
                <div v-if="!sim.plan.params.length && sim.plan.money_delta === 0" class="muted">変化なし</div>
                <div v-for="(w, i) in sim.warnings" :key="i" class="warn">⚠ {{ w }}</div>
              </div>
            </section>
            <section class="panel">
              <h3>既存アイテム（{{ items.length }}）<span class="hint"> ※行をクリックで編集</span></h3>
              <div class="table-scroll">
                <table class="list-table">
                  <thead><tr><th>ID</th><th class="l">品名</th><th>カテゴリ</th><th>値段</th><th>有効</th></tr></thead>
                  <tbody>
                    <tr v-for="it in items" :key="it.id" class="clickable" @click="openEdit(it)">
                      <td>{{ it.id }}</td><td class="l">{{ it.name }}</td><td>{{ it.category }}</td>
                      <td class="r">{{ it.price }}</td><td :class="{ off: !it.enabled }">{{ it.enabled ? '○' : '×' }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </section>
          </div>
        </section>

        <!-- 職業 -->
        <section class="fold">
          <button class="fold-head" @click="open.job = !open.job">
            <span class="caret">{{ open.job ? '▼' : '▶' }}</span> 職業（{{ jobs.length }}）
          </button>
          <div v-if="open.job" class="fold-body">
            <section class="panel">
              <h3>職業作成</h3>
              <label>職業名<input v-model="job.name" placeholder="例: 見習い店員" /></label>
              <div class="econ-grid">
                <label>給料<input type="number" v-model.number="job.salary" /></label>
                <label>支払間隔<input type="number" v-model.number="job.pay_interval" /></label>
                <label>ボーナス%<input type="number" v-model.number="job.bonus_rate" /></label>
                <label>昇給%<input type="number" v-model.number="job.raise_rate" /></label>
                <label>ランク<input type="number" v-model.number="job.rank" /></label>
                <label>身体消費<input type="number" v-model.number="job.body_cost" /></label>
                <label>頭脳消費<input type="number" v-model.number="job.nou_cost" /></label>
                <label class="wide2">前提マスター職<input v-model="job.require_master" placeholder="なし" /></label>
              </div>
              <div class="ops">
                <div class="ops-head">就くための必要条件(以上)</div>
                <div v-for="(req, i) in job.requirements" :key="i" class="op-row">
                  <select v-model="req.param">
                    <option v-for="p in PARAM_OPTIONS" :key="p" :value="p">{{ p }}</option>
                  </select>
                  <span class="ge">≧</span>
                  <input type="number" v-model.number="req.value" />
                  <button class="btn mini" @click="job.requirements.splice(i, 1)">×</button>
                </div>
                <button class="btn mini" @click="addReq(job.requirements)">＋条件を追加</button>
              </div>
              <div class="ops">
                <div class="ops-head">働いたときの効果</div>
                <div v-for="(op, i) in job.effect" :key="i" class="op-row">
                  <select v-model="op.op">
                    <option value="add_param">パラメータ</option>
                    <option value="add_money">お金</option>
                  </select>
                  <select v-if="op.op === 'add_param'" v-model="op.param">
                    <option v-for="p in PARAM_OPTIONS" :key="p" :value="p">{{ p }}</option>
                  </select>
                  <input type="number" v-model.number="op.amount" />
                  <button class="btn mini" @click="job.effect.splice(i, 1)">×</button>
                </div>
                <button class="btn mini" @click="addOp(job.effect)">＋効果を追加</button>
              </div>
              <div class="actions">
                <button class="btn primary" :disabled="busy || !job.name" @click="createJob">作成</button>
              </div>
            </section>
            <section class="panel">
              <h3>既存職業（{{ jobs.length }}）<span class="hint"> ※行をクリックで編集</span></h3>
              <div class="table-scroll">
                <table class="list-table">
                  <thead><tr><th>ID</th><th class="l">職業名</th><th>給料</th><th>ランク</th><th>前提職</th><th>有効</th></tr></thead>
                  <tbody>
                    <tr v-for="j in jobs" :key="j.id" class="clickable" @click="openEditJob(j)">
                      <td>{{ j.id }}</td><td class="l">{{ j.name }}</td><td class="r">{{ j.salary }}</td>
                      <td>{{ j.rank }}</td><td class="l">{{ j.require_master }}</td>
                      <td :class="{ off: !j.enabled }">{{ j.enabled ? '○' : '×' }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </section>
          </div>
        </section>

        <!-- ユーザー -->
        <section class="fold">
          <button class="fold-head" @click="open.user = !open.user">
            <span class="caret">{{ open.user ? '▼' : '▶' }}</span> ユーザー（{{ players.length }}）
          </button>
          <div v-if="open.user" class="fold-body">
            <section class="panel">
              <h3>ユーザー一覧<span class="hint"> ※行をクリックで確認/編集</span></h3>
              <div class="table-scroll">
                <table class="list-table">
                  <thead><tr><th>ID</th><th class="l">名前</th><th>職業</th><th>Lv</th><th>所持金</th><th>権限</th></tr></thead>
                  <tbody>
                    <tr v-for="u in players" :key="u.id" class="clickable" @click="openEditPlayer(u.id)">
                      <td>{{ u.id }}</td><td class="l">{{ u.display_name }}</td><td>{{ u.job }}</td>
                      <td>{{ u.job_level }}</td><td class="r">{{ u.money.toLocaleString('ja-JP') }}円</td>
                      <td>{{ u.roles.includes('admin') ? '管理者' : '' }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </section>
          </div>
        </section>
      </div>
    </template>

    <!-- アイテム編集(詳細)モーダル -->
    <div v-if="editing" class="modal-overlay" @click.self="closeEdit">
      <div class="modal">
        <h3>アイテム編集（ID {{ editing.id }}）</h3>
        <label>品名<input v-model="editing.name" /></label>
        <label>カテゴリ<input v-model="editing.category" /></label>
        <label>値段<input type="number" v-model.number="editing.price" /></label>
        <label class="chk"><input type="checkbox" v-model="editing.enabled" /> 有効（オフで無効化）</label>
        <div class="ops">
          <div class="ops-head">使用効果</div>
          <div v-for="(op, i) in editing.effect" :key="i" class="op-row">
            <select v-model="op.op">
              <option value="add_param">パラメータ</option>
              <option value="add_money">お金</option>
            </select>
            <select v-if="op.op === 'add_param'" v-model="op.param">
              <option v-for="p in PARAM_OPTIONS" :key="p" :value="p">{{ p }}</option>
            </select>
            <input type="number" v-model.number="op.amount" />
            <button class="btn mini" @click="editing.effect.splice(i, 1)">×</button>
          </div>
          <button class="btn mini" @click="addOp(editing.effect)">＋効果を追加</button>
        </div>
        <div class="actions">
          <button class="btn primary" :disabled="busy" @click="saveEdit">保存</button>
          <button class="btn danger" :disabled="busy" @click="deleteEdit">削除</button>
          <button class="btn" :disabled="busy" @click="closeEdit">キャンセル</button>
        </div>
      </div>
    </div>

    <!-- 職業編集(詳細)モーダル -->
    <div v-if="editingJob" class="modal-overlay" @click.self="closeEditJob">
      <div class="modal">
        <h3>職業編集（ID {{ editingJob.id }}）</h3>
        <label>職業名<input v-model="editingJob.name" /></label>
        <label class="chk"><input type="checkbox" v-model="editingJob.enabled" /> 有効（オフで無効化）</label>
        <div class="econ-grid">
          <label>給料<input type="number" v-model.number="editingJob.salary" /></label>
          <label>支払間隔<input type="number" v-model.number="editingJob.pay_interval" /></label>
          <label>ボーナス%<input type="number" v-model.number="editingJob.bonus_rate" /></label>
          <label>昇給%<input type="number" v-model.number="editingJob.raise_rate" /></label>
          <label>ランク<input type="number" v-model.number="editingJob.rank" /></label>
          <label>身体消費<input type="number" v-model.number="editingJob.body_cost" /></label>
          <label>頭脳消費<input type="number" v-model.number="editingJob.nou_cost" /></label>
          <label class="wide2">前提マスター職<input v-model="editingJob.require_master" placeholder="なし" /></label>
        </div>
        <div class="ops">
          <div class="ops-head">就くための必要条件(以上)</div>
          <div v-for="(req, i) in editingJob.requirements" :key="i" class="op-row">
            <select v-model="req.param">
              <option v-for="p in PARAM_OPTIONS" :key="p" :value="p">{{ p }}</option>
            </select>
            <span class="ge">≧</span>
            <input type="number" v-model.number="req.value" />
            <button class="btn mini" @click="editingJob.requirements.splice(i, 1)">×</button>
          </div>
          <button class="btn mini" @click="addReq(editingJob.requirements)">＋条件を追加</button>
        </div>
        <div class="ops">
          <div class="ops-head">働いたときの効果</div>
          <div v-for="(op, i) in editingJob.effect" :key="i" class="op-row">
            <select v-model="op.op">
              <option value="add_param">パラメータ</option>
              <option value="add_money">お金</option>
            </select>
            <select v-if="op.op === 'add_param'" v-model="op.param">
              <option v-for="p in PARAM_OPTIONS" :key="p" :value="p">{{ p }}</option>
            </select>
            <input type="number" v-model.number="op.amount" />
            <button class="btn mini" @click="editingJob.effect.splice(i, 1)">×</button>
          </div>
          <button class="btn mini" @click="addOp(editingJob.effect)">＋効果を追加</button>
        </div>
        <div class="actions">
          <button class="btn primary" :disabled="busy" @click="saveJob">保存</button>
          <button class="btn danger" :disabled="busy" @click="deleteJob">削除</button>
          <button class="btn" :disabled="busy" @click="closeEditJob">キャンセル</button>
        </div>
      </div>
    </div>

    <!-- ユーザー編集(詳細)モーダル -->
    <div v-if="editingPlayer" class="modal-overlay" @click.self="closeEditPlayer">
      <div class="modal wide-modal">
        <h3>ユーザー編集（ID {{ editingPlayer.id }}）</h3>
        <label>名前<input v-model="editingPlayer.display_name" /></label>
        <label class="chk"><input type="checkbox" v-model="editingPlayer.is_admin" /> 管理者権限</label>
        <div class="econ-grid">
          <label>所持金<input type="number" v-model.number="editingPlayer.money" /></label>
          <label>職業
            <select v-model="editingPlayer.job">
              <option v-for="name in jobOptions" :key="name" :value="name">{{ name }}</option>
            </select>
          </label>
          <label>職Lv<input type="number" v-model.number="editingPlayer.job_level" /></label>
          <label>職経験値<input type="number" v-model.number="editingPlayer.job_exp" /></label>
          <label>身体P<input type="number" v-model.number="editingPlayer.energy" /></label>
          <label>頭脳P<input type="number" v-model.number="editingPlayer.nou_energy" /></label>
          <label>満腹度<input type="number" v-model.number="editingPlayer.satiety" /></label>
          <label>病気指数<input type="number" v-model.number="editingPlayer.disease_index" /></label>
          <label>身長cm<input type="number" v-model.number="editingPlayer.height_cm" /></label>
          <label>体重g<input type="number" v-model.number="editingPlayer.weight_g" /></label>
        </div>
        <div class="ops">
          <div class="ops-head">パラメータ</div>
          <div class="param-edit">
            <label v-for="p in DETAIL_PARAMS" :key="p.key">
              <span>{{ p.label }}</span>
              <input type="number" v-model.number="editingPlayer.params[p.key]" />
            </label>
          </div>
        </div>
        <div class="actions">
          <button class="btn primary" :disabled="busy" @click="savePlayer">保存</button>
          <button class="btn danger" :disabled="busy" @click="deletePlayer">論理削除</button>
          <button class="btn" :disabled="busy" @click="closeEditPlayer">キャンセル</button>
        </div>
      </div>
    </div>

    <div style="text-align: center; margin-top: 8px">
      <button class="btn" @click="emit('back')">街に戻る</button>
    </div>
  </div>
</template>

<style scoped>
.admin-page {
  background-color: #dfe6ee;
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  margin-bottom: 6px;
}
.admin-header {
  display: flex;
  margin-bottom: 8px;
  border: 1px solid #333;
}
.admin-header .lead {
  flex: 1 1 auto;
  background: #fff;
  padding: 8px 12px;
  color: #333;
}
.admin-header .title {
  flex: 0 0 130px;
  background: #445566;
  color: #fff;
  font-weight: bold;
  font-size: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.admin-sections {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.fold {
  border: 1px solid #99a;
  background: #fff;
}
.fold-head {
  width: 100%;
  text-align: left;
  background: #445566;
  color: #fff;
  border: 0;
  padding: 8px 12px;
  font-size: 14px;
  font-weight: bold;
  cursor: pointer;
}
.fold-head:hover {
  background: #33475a;
}
.fold-head .caret {
  display: inline-block;
  width: 14px;
  color: #cde;
}
.fold-body {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: flex-start;
  padding: 8px;
}
.fold-body .panel {
  flex: 1 1 320px;
  border: 1px solid #ccd;
}
.sim-box {
  margin-top: 8px;
  border: 1px solid #e0e4ea;
  padding: 6px;
  font-size: 12px;
  line-height: 1.6;
}
.sim-box .muted {
  color: #999;
}
.panel {
  flex: 1 1 300px;
  background: #fff;
  border: 1px solid #99a;
  padding: 10px 12px;
}
.panel.wide {
  flex: 1 1 100%;
}
.panel h3 {
  margin: 0 0 8px;
  font-size: 14px;
  color: #334;
  border-bottom: 1px solid #dde;
  padding-bottom: 4px;
}
.panel label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  margin-bottom: 6px;
  color: #445;
}
.panel label input {
  flex: 1 1 auto;
  padding: 2px 4px;
}
.econ-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 2px 8px;
  margin: 4px 0 6px;
}
.econ-grid label {
  margin-bottom: 0;
}
.econ-grid label input[type='number'] {
  width: 70px;
}
.econ-grid .wide2 {
  grid-column: span 2;
}
.ops {
  border: 1px solid #e0e4ea;
  padding: 6px;
  margin: 6px 0;
}
.ops-head {
  font-size: 11px;
  color: #667;
  margin-bottom: 4px;
}
.op-row {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-bottom: 4px;
}
.op-row select,
.op-row input {
  padding: 1px 3px;
  font-size: 12px;
}
.op-row input[type='number'] {
  width: 70px;
}
.ge {
  color: #667;
}
.actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}
.btn.mini {
  padding: 1px 6px;
  font-size: 11px;
}
.btn.primary {
  background: #336699;
  color: #fff;
  border-color: #224466;
}
.sim {
  font-size: 13px;
  line-height: 1.7;
}
.sim .muted {
  color: #999;
}
.warn {
  color: #cc5500;
  font-size: 12px;
  margin-top: 4px;
}
.table-scroll {
  overflow-x: auto;
  max-height: 240px;
  overflow-y: auto;
  margin-bottom: 10px;
}
.list-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}
.list-table th {
  background: #e2e8f0;
  color: #234;
  padding: 2px 6px;
  border: 1px solid #cdd;
  position: sticky;
  top: 0;
}
.list-table td {
  padding: 2px 6px;
  border: 1px solid #eee;
  text-align: center;
}
.list-table th.l,
.list-table td.l {
  text-align: left;
}
.list-table td.r {
  text-align: right;
}
.list-table tr.clickable {
  cursor: pointer;
}
.list-table tr.clickable:hover td {
  background: #eef4fb;
}
.list-table td.off {
  color: #cc3300;
}
.hint {
  font-size: 11px;
  color: #889;
  font-weight: normal;
}
.chk {
  font-size: 12px;
}
.btn.danger {
  background: #cc3333;
  color: #fff;
  border-color: #992222;
}
/* 編集モーダル */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.4);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}
.modal {
  background: #fff;
  border: 1px solid #667;
  border-radius: 4px;
  padding: 14px 16px;
  width: 380px;
  max-width: 92vw;
  max-height: 88vh;
  overflow-y: auto;
  box-shadow: 0 6px 24px rgba(0, 0, 0, 0.3);
}
.modal.wide-modal {
  width: 460px;
}
.modal h3 {
  margin: 0 0 10px;
  font-size: 14px;
  color: #334;
  border-bottom: 1px solid #dde;
  padding-bottom: 5px;
}
.param-edit {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 2px 8px;
}
.param-edit label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  font-size: 12px;
  margin: 0;
}
.param-edit label input {
  width: 70px;
}
</style>
