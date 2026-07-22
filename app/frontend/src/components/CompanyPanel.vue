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
      lines: [`${r.param_name}パラメータが${r.gained}あがりました。運営費として${yen(r.fee)}円かかりました。`],
      icon: 'item',
    });
  } catch (e) {
    showToast({ variant: 'error', title: '教育できませんでした', lines: [e instanceof Error ? e.message : String(e)], icon: 'item' });
  } finally {
    busy.value = false;
  }
}
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

    <div v-if="view.kind === 2 && view.officers.length > 0" class="officers-line">
      役員: {{ view.officers.join('、') }}
    </div>

    <!-- 自分のパラメータ(教育できる人にだけ表示) -->
    <table v-if="canEdu" class="my-params">
      <tr><td v-for="p in PARAMS.slice(0, 8)" :key="p.key" class="ph">{{ p.label }}</td></tr>
      <tr><td v-for="p in PARAMS.slice(0, 8)" :key="p.key">{{ myParams[p.key] ?? 0 }}</td></tr>
      <tr><td v-for="p in PARAMS.slice(8)" :key="p.key" class="ph">{{ p.label }}</td></tr>
      <tr><td v-for="p in PARAMS.slice(8)" :key="p.key">{{ myParams[p.key] ?? 0 }}</td></tr>
    </table>

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
        <span class="sum-label">総合能力値：</span>{{ yen(st.sougou) }}　{{ st.job }}　{{ yen(st.income) }}円
      </div>
      <table class="staff-params">
        <tr><td v-for="p in PARAMS.slice(0, 8)" :key="p.key" class="ph">{{ p.label }}</td></tr>
        <tr><td v-for="p in PARAMS.slice(0, 8)" :key="p.key">{{ st.params[p.key] ?? 0 }}</td></tr>
        <tr><td v-for="p in PARAMS.slice(8)" :key="p.key" class="ph">{{ p.label }}</td></tr>
        <tr><td v-for="p in PARAMS.slice(8)" :key="p.key">{{ st.params[p.key] ?? 0 }}</td></tr>
      </table>
    </div>
    <div v-if="view.staff.length === 0" class="empty">まだ社員がいません。</div>

    <div class="bottom-bar">
      <button v-if="view.own && view.staff.length < view.staff_max" class="btn" :disabled="busy" @click="addStaff">
        社員を増やす
      </button>
      <span class="total">総合 {{ yen(view.total_income) }}円</span>
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
.officers-line {
  margin-top: 6px;
  font-size: 11px;
  background: #fff;
  border: 1px solid #999;
  padding: 6px;
}
.my-params,
.staff-params {
  width: 100%;
  border-collapse: separate;
  border-spacing: 1px;
  background: #fff;
  border: 1px solid #999;
  margin-top: 8px;
  font-size: 11px;
  text-align: center;
}
.my-params td,
.staff-params td {
  padding: 2px 4px;
  background: #f4f4f4;
}
.my-params .ph,
.staff-params .ph {
  background: #ddd;
  color: #444;
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
}
.sum-label {
  font-weight: bold;
  color: #445;
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
