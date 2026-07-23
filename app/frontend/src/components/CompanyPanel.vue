<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { api, type Player, type CompanyView } from '../api';
import Toast from './Toast.vue';
import { useToast } from '../toast';

// 運営/株式会社の社員教育画面(レガシーunei_2.pl/kaishiya.pl)。
// 自分のパラメータを消費して社員に1/10を移転し、養育費(2万円/pt)を支払う。
// 社員は最高給の有資格職に自動で就き、仕送り(日次収入)を生む。
const props = defineProps<{ player: Player; houseId: number }>();
const emit = defineEmits<{ update: [player: Player] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const busy = ref(false);
const { toast, showToast, closeToast } = useToast();

const view = ref<CompanyView | null>(null);
const eduParam = ref<Record<number, string>>({});
const eduAmount = ref<Record<number, number>>({});
const eduPay = ref<Record<number, string>>({});

const PARAMS: { key: string; label: string }[] = [
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
const AMOUNTS = [1, 2, 3, 5, 10, 20, 30, 50, 80, 100, 200, 300, 500, 800, 1000];

// 教育できるか: 運営=オーナーのみ / 株式会社=オーナー+役員。
const canEdu = computed(() => (view.value?.own ?? false) || (view.value?.officer ?? false));
const CREDIT_CARDS = ['クレジットカード', 'ゴールドクレジットカード', 'スペシャルクレジットカード'];
const hasCreditCard = computed(() =>
  props.player.items.some((it) => CREDIT_CARDS.includes(it.name) && it.remaining_uses > 0),
);
const myParams = computed(() => props.player.params as unknown as Record<string, number>);

function canEduNow(canEduAt: string): boolean {
  return !canEduAt || new Date(canEduAt).getTime() <= Date.now();
}

async function reload() {
  view.value = await api.companyView(props.player.id, props.houseId);
  for (const st of view.value.staff) {
    if (!eduParam.value[st.id]) eduParam.value[st.id] = 'kokugo';
    if (!eduAmount.value[st.id]) eduAmount.value[st.id] = 10;
    if (!eduPay.value[st.id]) eduPay.value[st.id] = 'cash';
  }
}
onMounted(reload);

async function addStaff() {
  busy.value = true;
  try {
    const after = await api.companyStaffAdd(props.player.id, props.houseId);
    emit('update', after);
    await reload();
    showToast({ variant: 'item', title: '社員を増やしました', lines: [], icon: 'item' });
  } catch (e) {
    showToast({ variant: 'error', title: 'できませんでした', lines: [e instanceof Error ? e.message : String(e)], icon: 'item' });
  } finally {
    busy.value = false;
  }
}

async function educate(staffId: number) {
  busy.value = true;
  try {
    const after = await api.companyEducate(
      props.player.id,
      props.houseId,
      staffId,
      eduParam.value[staffId] ?? 'kokugo',
      eduAmount.value[staffId] ?? 10,
      eduPay.value[staffId] ?? 'cash',
    );
    emit('update', after);
    await reload();
    const r = after.edu_result;
    showToast({
      variant: 'item',
      title: '社員教育しました',
      lines: [`${r.param_name}が${r.gained}あがりました。運営費として${yen(r.fee)}円かかりました。`],
      icon: 'item',
    });
  } catch (e) {
    showToast({ variant: 'error', title: '教育できませんでした', lines: [e instanceof Error ? e.message : String(e)], icon: 'item' });
  } finally {
    busy.value = false;
  }
}

// --- 株式会社: 会社BBS/製造 ---
const section = ref<'edu' | 'bbs' | 'seizou'>('edu');
const openBody = ref('');
const openJoin = ref(false);
const memberBody = ref('');
const memberLeave = ref(false);
const delBoard = ref('open');
const delNo = ref('');
const fmtDate = (iso: string) => {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
};
function statusLabel(s: string): string {
  return { in: '入会申請中', out: '退会申請中', m_ryoukai: '受領済み', taikai: '退会指定' }[s] ?? '';
}

async function run(title: string, fn: () => Promise<import('../api').Player>) {
  busy.value = true;
  try {
    const after = await fn();
    emit('update', after);
    await reload();
    showToast({ variant: 'item', title, lines: [], icon: 'item' });
  } catch (e) {
    showToast({ variant: 'error', title: 'できませんでした', lines: [e instanceof Error ? e.message : String(e)], icon: 'item' });
  } finally {
    busy.value = false;
  }
}
const postOpen = () =>
  run('投稿しました', async () => {
    const p = await api.companyBbsPost(props.player.id, props.houseId, 'open', openBody.value, openJoin.value);
    openBody.value = '';
    openJoin.value = false;
    return p;
  });
const postMember = () =>
  run('投稿しました', async () => {
    const p = await api.companyBbsPost(props.player.id, props.houseId, 'member', memberBody.value, false, memberLeave.value);
    memberBody.value = '';
    memberLeave.value = false;
    return p;
  });
const approve = (postId: number) => run('許可しました', () => api.companyApprove(props.player.id, props.houseId, postId));
const kick = (officerId: number, name: string) => {
  if (!window.confirm(`${name}さんを退会させますか？`)) return;
  return run('退会させました', () => api.companyKick(props.player.id, props.houseId, officerId));
};
const delPost = () =>
  run('記事を削除しました。', () => api.companyBbsDelete(props.player.id, props.houseId, delBoard.value, Number(delNo.value) || 0));

// 製造フォーム。
const szName = ref('');
const szParams = ref<Record<string, string>>({});
const szCal = ref('');
const szKankaku = ref('10');
const szZaiko = ref('1');
const szTaikyuu = ref('1');
const szPrice = ref('');
const doSeizou = async () => {
  busy.value = true;
  try {
    const params: Record<string, number> = {};
    for (const [k, v] of Object.entries(szParams.value)) {
      const n = Number(v) || 0;
      if (n > 0) params[k] = n;
    }
    const after = await api.companySeizou(props.player.id, props.houseId, {
      name: szName.value,
      params,
      cal: Number(szCal.value) || 0,
      kankaku: Number(szKankaku.value) || 10,
      zaiko: Number(szZaiko.value) || 0,
      taikyuu: Number(szTaikyuu.value) || 0,
      price: Number(szPrice.value) || 0,
    });
    emit('update', after);
    await reload();
    const r = after.seizou_result;
    showToast({
      variant: 'item',
      title: '生産しました',
      lines: [`${r.name}（在庫${r.zaiko}・耐久${r.taikyuu}回・${yen(r.price)}円）をお店に並べました`],
      icon: 'item',
    });
  } catch (e) {
    showToast({ variant: 'error', title: '生産できませんでした', lines: [e instanceof Error ? e.message : String(e)], icon: 'item' });
  } finally {
    busy.value = false;
  }
};
</script>

<template>
  <div v-if="view" class="company">
    <Toast :toast="toast" @close="closeToast" />
    <!-- 説明+社員教育の見出し(レガシー: assen_style+オレンジ箱) -->
    <table class="co-head">
      <tr>
        <td class="co-desc">
          ●自分の持っているパラメータを社員教育のために使ったりできます。<br />
          ●会社は与えたパラメータの{{ view.edu_efficiency }}分の1しか得ることができません。<br />
          ●会社のパラメータ１に対して{{ yen(view.edu_fee_point) }}円の養育費がかかります。<br />
          ●社員教育できる間隔は{{ view.edu_interval_min / 60 }}時間です。<br />
          ●社員総数は、{{ view.staff.length }}人です。{{ view.staff_max }}人が上限です。
        </td>
        <td class="co-label">社員教育</td>
      </tr>
    </table>

    <!-- 株式会社: セクション切替+役員一覧(オーナーは退会指定可) -->
    <div v-if="view.kind === 2" class="kaisha-bar">
      <button class="btn" :class="{ active: section === 'edu' }" @click="section = 'edu'">社員教育</button>
      <button class="btn" :class="{ active: section === 'bbs' }" @click="section = 'bbs'">会社掲示板</button>
      <button v-if="view.own" class="btn" :class="{ active: section === 'seizou' }" @click="section = 'seizou'">製造</button>
      <span class="officers-line">
        役員:
        <template v-if="view.officers.length === 0">なし</template>
        <span v-for="o in view.officers" :key="o.player_id" class="officer">
          {{ o.name }}<button v-if="view.own" class="kick" :disabled="busy" @click="kick(o.player_id, o.name)">×</button>
        </span>
      </span>
    </div>

    <template v-if="view.kind === 1 || section === 'edu'">
    <!-- 自分のパラメータ(教育できる人にだけ表示) -->
    <template v-if="canEdu">
      <div class="param-caption">●自分のパラメータ</div>
      <div class="param-grid">
        <div v-for="p in PARAMS" :key="p.key" class="param-cell">
          <span class="pname">{{ p.label }}</span>
          <span class="pval" :class="{ zero: !(myParams[p.key] ?? 0) }">{{ yen(myParams[p.key] ?? 0) }}</span>
        </div>
      </div>
    </template>

    <!-- 社員一覧 -->
    <div v-for="st in view.staff" :key="st.id" class="staff">
      <div v-if="canEdu" class="edu-log">最後の教育：{{ st.edu_log || '（まだ教育していません）' }}</div>
      <div v-if="canEdu" class="edu-form">
        <select v-model="eduParam[st.id]">
          <option v-for="p in PARAMS" :key="p.key" :value="p.key">{{ p.label }}パラメータを</option>
        </select>
        <select v-model.number="eduAmount[st.id]">
          <option v-for="a in AMOUNTS" :key="a" :value="a">{{ a }}</option>
        </select>
        <span class="divide">÷{{ view.edu_efficiency }}</span>
        支払い
        <select v-model="eduPay[st.id]">
          <option value="cash">現金</option>
          <option value="credit" :disabled="!hasCreditCard">クレジット</option>
        </select>
        <button class="btn" :disabled="busy || !canEduNow(st.can_edu_at)" @click="educate(st.id)">あげる</button>
        <span v-if="!canEduNow(st.can_edu_at)" class="wait">まだできません</span>
      </div>
      <div class="staff-sum">
        <span class="sum-label">総合能力値：</span>{{ yen(st.sougou) }}
        <span class="staff-job">{{ st.job }}</span>
        <span class="staff-income">仕送り {{ yen(st.income) }}円/日</span>
      </div>
      <div class="param-grid staff-grid">
        <div v-for="p in PARAMS" :key="p.key" class="param-cell">
          <span class="pname">{{ p.label }}</span>
          <span class="pval" :class="{ zero: !(st.params[p.key] ?? 0) }">{{ yen(st.params[p.key] ?? 0) }}</span>
        </div>
      </div>
    </div>
    <div v-if="view.staff.length === 0" class="empty">まだ社員がいません。</div>

    <div class="bottom-bar">
      <button v-if="view.own && view.staff.length < view.staff_max" class="btn" :disabled="busy" @click="addStaff">
        社員を増やす
      </button>
      <span class="total">総合 {{ yen(view.total_income) }}円</span>
    </div>
    </template>

    <!-- 会社掲示板(株式会社: 来訪者板+メンバー板) -->
    <div v-if="view.kind === 2 && section === 'bbs'" class="bbs-cols">
      <div class="bbs-col">
        <div class="bbs-head">■メッセージ来訪者</div>
        <textarea v-model="openBody" rows="4" class="bbs-area"></textarea>
        <label v-if="!view.own && !view.officer" class="chk"><input v-model="openJoin" type="checkbox" />●入会希望</label>
        <div><button class="btn" :disabled="busy" @click="postOpen">OK</button></div>
        <div class="bbs-head2">来訪者掲示板</div>
        <div v-for="p in view.bbs_open" :key="p.id" class="bbs-post">
          <div class="bbs-meta">
            {{ p.no }} : {{ p.author_name }}（{{ fmtDate(p.created_at) }}）
            <span v-if="p.status" class="bbs-status">{{ statusLabel(p.status) }}</span>
            <button
              v-if="view.own && p.status === 'in'"
              class="btn mini-btn"
              :disabled="busy"
              @click="approve(p.id)"
            >入会</button>
          </div>
          <div class="bbs-body">{{ p.body }}</div>
        </div>
      </div>
      <div v-if="view.own || view.officer" class="bbs-col">
        <div class="bbs-head">■メッセージメンバー</div>
        <textarea v-model="memberBody" rows="4" class="bbs-area"></textarea>
        <label v-if="!view.own" class="chk"><input v-model="memberLeave" type="checkbox" />●退会希望</label>
        <div><button class="btn" :disabled="busy" @click="postMember">OK</button></div>
        <div class="bbs-head2">メンバー掲示板</div>
        <div v-for="p in view.bbs_member" :key="p.id" class="bbs-post">
          <div class="bbs-meta">
            {{ p.no }} : {{ p.author_name }}（{{ fmtDate(p.created_at) }}）
            <span v-if="p.status" class="bbs-status">{{ statusLabel(p.status) }}</span>
            <button
              v-if="view.own && p.status === 'out'"
              class="btn mini-btn"
              :disabled="busy"
              @click="approve(p.id)"
            >退会</button>
          </div>
          <div class="bbs-body">{{ p.body }}</div>
        </div>
        <div v-if="view.own" class="del-line">
          <select v-model="delBoard">
            <option value="open">オープン掲示板</option>
            <option value="member">メンバー掲示板</option>
          </select>
          <input v-model="delNo" class="no-inp" placeholder="番号" />
          <button class="btn" :disabled="busy" @click="delPost">削除</button>
        </div>
      </div>
    </div>

    <!-- 製造(株式会社オーナー: 原料の範囲でオリジナル商品を1日1回生産) -->
    <div v-if="view.kind === 2 && section === 'seizou' && view.materials" class="seizou">
      <div v-if="!view.materials.has_shop" class="seizou-warn">
        商品を並べるお店がありません。先に家の店を開いてください。
      </div>
      <div v-else-if="view.materials.made_today" class="seizou-warn">本日の生産は完了しました。</div>
      <table class="sz-table">
        <tr><td>種類</td><td>{{ view.materials.shop_syubetu || '（店なし）' }}</td></tr>
        <tr><td>品名</td><td><input v-model="szName" maxlength="50" class="sz-name" placeholder="(空欄=自分の名前の商品)" /></td></tr>
        <tr v-for="p in PARAMS" :key="p.key">
          <td>{{ p.label }}値</td>
          <td>
            <input v-model="szParams[p.key]" class="sz-num" />
            <span class="sz-max">原料 {{ view.materials.maxima[p.key] ?? 0 }}</span>
          </td>
        </tr>
        <tr><td>カロリー</td><td><input v-model="szCal" class="sz-num" /><span class="sz-max">食料 {{ view.materials.syoku }}</span></td></tr>
        <tr><td>間隔(分)</td><td><input v-model="szKankaku" class="sz-num" /></td></tr>
        <tr><td>在庫</td><td><input v-model="szZaiko" class="sz-num" /><span class="sz-max">在庫×耐久 ≦ 社員{{ view.materials.staff_count }}人×min(原料/設定値)</span></td></tr>
        <tr><td>耐久</td><td><input v-model="szTaikyuu" class="sz-num" /></td></tr>
        <tr><td>値段</td><td><input v-model="szPrice" class="sz-num wide" placeholder="0=既定" /></td></tr>
      </table>
      <button class="btn" :disabled="busy || !view.materials.has_shop || view.materials.made_today" @click="doSeizou">変更／作成</button>
    </div>
  </div>
</template>

<style scoped>
.company {
  max-width: 820px;
  margin: 8px auto 0;
  font-size: 12px;
  color: #333;
}
.co-head {
  width: 100%;
  border-collapse: collapse;
  background: #fff;
  border: 1px solid #666;
  font-size: 11px;
  line-height: 170%;
}
.co-head td {
  padding: 10px;
}
.co-label {
  background: #ff6633;
  color: #fff;
  text-align: center;
  width: 30%;
  font-size: 24px;
}
.kaisha-bar {
  margin-top: 6px;
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  background: #fff;
  border: 1px solid #999;
  padding: 6px;
}
.kaisha-bar .btn.active {
  background: #666;
  color: #fff;
}
.officers-line {
  font-size: 11px;
  margin-left: auto;
}
.officer {
  margin-left: 6px;
  white-space: nowrap;
}
.kick {
  border: none;
  background: none;
  color: #c44;
  cursor: pointer;
  font-weight: bold;
}
.bbs-cols {
  display: flex;
  gap: 10px;
  margin-top: 10px;
  align-items: flex-start;
}
.bbs-col {
  flex: 1 1 50%;
  background: #fff;
  border: 1px solid #999;
  padding: 10px;
  font-size: 11px;
}
.bbs-head {
  font-weight: bold;
  color: #336699;
}
.bbs-head2 {
  font-weight: bold;
  color: #336699;
  margin-top: 12px;
  border-top: 1px solid #ccc;
  padding-top: 8px;
}
.bbs-area {
  width: 100%;
  box-sizing: border-box;
  font-size: 12px;
  margin-top: 4px;
}
.chk {
  display: block;
  margin: 4px 0;
}
.bbs-post {
  margin-top: 8px;
}
.bbs-meta {
  color: #666;
}
.bbs-status {
  color: #cc6600;
  font-weight: bold;
}
.mini-btn {
  font-size: 10px;
  padding: 0 6px;
}
.bbs-body {
  white-space: pre-line;
  color: #333;
}
.del-line {
  margin-top: 12px;
  display: flex;
  gap: 4px;
  align-items: center;
}
.no-inp {
  width: 60px;
}
.seizou {
  margin-top: 10px;
  background: #fff;
  border: 1px solid #999;
  padding: 10px;
}
.seizou-warn {
  color: #c44;
  font-size: 12px;
  margin-bottom: 6px;
}
.sz-table {
  border-collapse: separate;
  border-spacing: 1px;
  font-size: 11px;
  margin-bottom: 8px;
}
.sz-table td {
  padding: 2px 6px;
  background: #f4f4f4;
}
.sz-table td:first-child {
  background: #ddd;
  white-space: nowrap;
}
.sz-num {
  width: 60px;
  font-size: 11px;
}
.sz-num.wide {
  width: 110px;
}
.sz-name {
  width: 260px;
  font-size: 11px;
}
.sz-max {
  margin-left: 6px;
  color: #888;
  font-size: 10px;
}
/* パラメータ表: ラベルの直下に値を置く8列グリッド。0は淡色にして
   上がっている能力だけが目に入るようにする。 */
.param-caption {
  margin-top: 10px;
  font-size: 11px;
  font-weight: bold;
  color: #445;
}
.param-grid {
  display: grid;
  grid-template-columns: repeat(8, minmax(58px, 1fr));
  gap: 2px;
  background: #fff;
  border: 1px solid #999;
  padding: 4px;
  margin-top: 4px;
  max-width: 560px;
}
.param-cell {
  display: flex;
  flex-direction: column;
  text-align: center;
}
.pname {
  background: #556677;
  color: #fff;
  font-size: 10px;
  line-height: 16px;
  white-space: nowrap;
}
.pval {
  background: #f4f7fa;
  font-size: 12px;
  font-weight: bold;
  color: #223;
  padding: 2px 0;
}
.pval.zero {
  color: #bbb;
  font-weight: normal;
}
@media (max-width: 640px) {
  .param-grid {
    grid-template-columns: repeat(4, 1fr);
  }
}
.staff {
  margin-top: 12px;
}
.edu-log {
  background: #ffffaa;
  padding: 5px 8px;
  font-size: 11px;
  border: 1px solid #ccc;
  border-bottom: none;
}
.edu-form {
  background: #dddddd;
  padding: 6px 8px;
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  border: 1px solid #ccc;
  font-size: 12px;
}
.divide {
  color: #ff0000;
  font-weight: bold;
}
.wait {
  color: #c44;
  font-size: 11px;
}
.staff-sum {
  margin-top: 4px;
  font-size: 12px;
  display: flex;
  align-items: center;
  gap: 10px;
}
.sum-label {
  font-weight: bold;
  color: #445;
}
.staff-job {
  background: #eef4ee;
  border: 1px solid #9c9;
  padding: 0 8px;
  border-radius: 3px;
  color: #262;
}
.staff-income {
  color: #663300;
  font-weight: bold;
}
.empty {
  margin-top: 12px;
  color: #777;
}
.bottom-bar {
  margin: 14px 0 6px;
  display: flex;
  align-items: center;
  gap: 12px;
  justify-content: center;
}
.total {
  font-weight: bold;
}
</style>
