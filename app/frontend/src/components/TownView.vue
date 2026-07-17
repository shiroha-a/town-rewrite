<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, watch } from 'vue';
import { api, type Player, type Params, type TownFacility } from '../api';
import { satietyLabel } from '../params';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ navigate: [view: string]; reload: []; logout: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');

const total = computed(() => props.player.money + props.player.savings);

// パワーバーは残量%(0-100)。色は旧town_maker準拠: >59%青 / >19%黄 / それ以下赤。
function pct(v: number, max: number): number {
  if (max <= 0) return 0;
  return Math.min(100, Math.max(0, Math.round((v / max) * 100)));
}
function barColor(p: number): string {
  if (p > 59) return 'blue';
  if (p > 19) return 'yellow';
  return 'red';
}
const energyPct = computed(() => pct(props.player.status.energy, props.player.status.energy_max));
const nouPct = computed(() => pct(props.player.status.nou_energy, props.player.status.nou_energy_max));
const energyColor = computed(() => barColor(energyPct.value));
const nouColor = computed(() => barColor(nouPct.value));

// 体重はg保持なので表示はkg小数第1位に整形する。
const weightKg = computed(() => (props.player.status.weight_g / 1000).toFixed(1));

// 街マップに置く施設。配置は管理者が編集可能で、起動時にAPIから取得する。
// 職業安定所(work.gif)で転職する。
const facilities = ref<TownFacility[]>([]);
const facilityAt = (col: number, row: number) => facilities.value.find((f) => f.col === col && f.row === row);

const cols = Array.from({ length: 16 }, (_, i) => i + 1);
const rows = 'ABCDEFGHIJKL'.split('');

onMounted(async () => {
  try {
    facilities.value = await api.townMap();
  } catch {
    // マップ取得に失敗しても他機能は使えるよう空配置で継続する。
    facilities.value = [];
  }
  try {
    const s = await api.stocks();
    stockPrices.value = s.prices;
  } catch {
    stockPrices.value = [];
  }
  refreshUnread();
  try {
    greetings.value = await api.greetings(6);
  } catch {
    greetings.value = [];
  }
  // 街を開いた=来訪として足あとに記帳する(1日1回)。
  api.attendanceCheckin(props.player.id).catch(() => {});
  // 街を開いた時にランダムイベントを抽選する(発生したら通知しステータス再取得)。
  api
    .eventRoll(props.player.id)
    .then((res) => {
      if (res.event) {
        eventNotice.value = { message: res.event.message, good: res.event.good };
        emit('reload');
      }
    })
    .catch(() => {});
});

// ランダムイベントの通知(発生時のみ)。
const eventNotice = ref<{ message: string; good: boolean } | null>(null);

// 街トップのチャット窓に表示する最新のあいさつ。
const greetings = ref<import('../api').Greeting[]>([]);

// 新着メール通知。街トップ表示時とポーリングで未読数を取得する。
const unreadMail = ref(0);
async function refreshUnread() {
  try {
    unreadMail.value = (await api.getMailUnread(props.player.id)).unread;
  } catch {
    unreadMail.value = 0;
  }
}
// 親のポーリング(player更新)に合わせて未読も更新する。
watch(() => props.player, refreshUnread);

// 株価ティッカー(街トップの帯)。全銘柄の現在株価を表示する。
const stockPrices = ref<{ symbol: string; price: number }[]>([]);
const tickerText = computed(() => {
  if (!stockPrices.value.length) return '株価情報を取得中…';
  const wide = 'ＡＢＣＤＥ';
  return stockPrices.value
    .map((s, i) => `${wide[i] ?? s.symbol}株 ${s.price.toLocaleString('ja-JP')}円`)
    .join('，');
});

function clickFacility(f: TownFacility) {
  if (f.ready) emit('navigate', f.key);
}

// 管理者のみ管理者画面への導線を出す。
const isAdmin = computed(() => props.player.roles.includes('admin'));

// コマンドアイコン列。仕事(go_work.gif)は学生以外(=転職済み)のときだけ出現する。
const commands = computed(() => {
  const list = [{ key: 'reload', img: 'reload', alt: '更新' }];
  if (props.player.status.job !== '学生') {
    list.push({ key: 'work', img: 'go_work', alt: '仕事' });
  }
  list.push(
    { key: 'item', img: 'item', alt: 'アイテム使用' },
    { key: 'mail', img: 'mail', alt: 'メール' },
    { key: 'doukyo', img: 'doukyo', alt: 'キャラ作成' },
    { key: 'aisatu', img: 'aisatu', alt: 'あいさつ' },
    { key: 'off', img: 'off', alt: 'ログアウト' },
  );
  return list;
});
function clickCommand(key: string) {
  if (key === 'work' && workCooldown.value) return; // クールタイム中は無効
  if (key === 'reload') emit('reload');
  else if (key === 'off') emit('logout');
  else emit('navigate', key);
}

// サーバ時刻基準の1秒クロック。就労クールタイムのカウントダウンをリアルタイム表示する。
const skewMs = ref(0);
function syncSkew() {
  const serverNow = new Date(props.player.server_now).getTime();
  if (!Number.isNaN(serverNow)) skewMs.value = serverNow - Date.now();
}
syncSkew();
watch(() => props.player.server_now, syncSkew);
const nowMs = ref(Date.now());
let timer: number | undefined;
onMounted(() => {
  timer = window.setInterval(() => {
    nowMs.value = Date.now();
  }, 1000);
});
onUnmounted(() => {
  if (timer !== undefined) window.clearInterval(timer);
});
const serverCorrectedNow = computed(() => nowMs.value + skewMs.value);

// 就労クールタイム中の残り時間ラベル(可能ならnull)。
const workCooldown = computed<string | null>(() => {
  const at = props.player.status.work_available_at;
  if (!at) return null;
  const target = new Date(at).getTime();
  const remain = target - serverCorrectedNow.value;
  if (Number.isNaN(target) || remain <= 0) return null;
  const sec = Math.ceil(remain / 1000);
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return m > 0 ? `あと${m}分${String(s).padStart(2, '0')}秒` : `あと${s}秒`;
});

// パラメータ一覧(バックエンドの実値を表示)
type ParamKey = keyof Params;
const zunou: { label: string; key: ParamKey }[] = [
  { label: '国語', key: 'kokugo' },
  { label: '数学', key: 'suugaku' },
  { label: '理科', key: 'rika' },
  { label: '社会', key: 'syakai' },
  { label: '英語', key: 'eigo' },
  { label: '音楽', key: 'ongaku' },
  { label: '美術', key: 'bijutsu' },
];
const shintai: { label: string; key: ParamKey }[] = [
  { label: 'ルックス', key: 'looks' },
  { label: '体力', key: 'tairyoku' },
  { label: '健康', key: 'kenkou' },
  { label: 'スピード', key: 'speed' },
  { label: 'パワー', key: 'power' },
  { label: '腕力', key: 'wanryoku' },
  { label: '脚力', key: 'kyakuryoku' },
];
const others: { label: string; key: ParamKey }[] = [
  { label: 'LOVE', key: 'love' },
  { label: '面白さ', key: 'omoshirosa' },
];

// パラメータバー(旧town_maker準拠): 自分の全パラメータの最大値を基準にした相対バー。
// 幅% = 値/最大×100。最大値の項目が満タンで、各項目の相対的な強さが一目で分かる。
const paramMax = computed(() =>
  Math.max(1, ...[...zunou, ...shintai, ...others].map((p) => props.player.params[p.key])),
);
const paramBar = (v: number) => Math.max(3, Math.round((v / paramMax.value) * 100));
</script>

<template>
  <div class="participant">
    現在の総参加者(1人)：★
    <img :src="`/img/img062.gif`" width="12" height="12" style="vertical-align: middle" alt="" />
    <span class="name">{{ player.display_name }}</span>★
  </div>

  <button v-if="unreadMail > 0" class="mail-notice" @click="emit('navigate', 'mail')">
    ★受信箱に{{ unreadMail }}通の新しいメッセージが届いています！
  </button>

  <div class="town">
    <!-- 左カラム: 街マップ -->
    <div class="col-left">
      <div class="mapwrap">
        <div class="townmap-grid">
          <div class="th corner"></div>
          <div v-for="c in cols" :key="'h' + c" class="th">{{ c }}</div>
          <template v-for="(r, ri) in rows" :key="r">
            <div class="th">{{ r }}</div>
            <div v-for="c in cols" :key="r + '-' + c" class="tcell">
              <button
                v-if="facilityAt(c, ri)"
                class="facility"
                :title="facilityAt(c, ri)!.alt"
                @click="clickFacility(facilityAt(c, ri)!)"
              >
                <img :src="`/img/${facilityAt(c, ri)!.img}.gif`" :alt="facilityAt(c, ri)!.alt" />
              </button>
            </div>
          </template>
        </div>
      </div>
      <div class="ticker">{{ tickerText }}</div>
      <button class="chat-head" @click="emit('navigate', 'aisatu')">●チャット(あいさつ)</button>
      <div v-if="greetings.length" class="chat-feed">
        <div v-for="g in greetings" :key="g.id" class="chat-line">
          <span class="cn">{{ g.user_name }}</span>：<span :style="{ color: g.color }">{{ g.body }}</span>
        </div>
      </div>
      <div class="left-links">
        <button class="link-btn" @click="emit('navigate', 'shopping')">商店街</button>
        <button class="link-btn" @click="emit('navigate', 'ashiato')">足あと帳</button>
        <button class="link-btn" @click="emit('navigate', 'yakuba')">役場(住民名鑑)</button>
      </div>
    </div>

    <!-- 右カラム: 街情報 + コマンド + ステータス -->
    <div class="col-right">
      <div class="right-cols">
        <div style="flex: 1 1 auto; min-width: 0">
          <div class="whitebox town-info">
            <div class="midasi">「Ｔｏｗｎ」内<br />公園</div>
            <div class="num">地　価：2000万<br />経済力：--円<br />繁栄度：--</div>
          </div>

          <div class="command-icons">
            <button v-if="isAdmin" class="admin-link" title="管理者画面" @click="emit('navigate', 'admin')">
              ⚙ 管理者
            </button>
            <button
              v-for="cmd in commands"
              :key="cmd.key"
              :title="cmd.key === 'work' && workCooldown ? `まだ働けません（${workCooldown}）` : cmd.alt"
              :disabled="cmd.key === 'work' && !!workCooldown"
              :class="{ 'on-cooldown': cmd.key === 'work' && !!workCooldown }"
              @click="clickCommand(cmd.key)"
            >
              <img :src="`/img/${cmd.img}.gif`" width="32" height="32" :alt="cmd.alt" />
              <span v-if="cmd.key === 'work' && workCooldown" class="cmd-cooldown">{{ workCooldown }}</span>
              <span v-if="cmd.key === 'mail' && unreadMail > 0" class="cmd-badge">{{ unreadMail }}</span>
            </button>
          </div>

          <div v-if="eventNotice" :class="['event-notice', eventNotice.good ? 'good' : 'bad']" @click="eventNotice = null">
            ★イベント発生！ {{ eventNotice.message }}
          </div>

          <div class="orangebox status">
            <div class="honbun2">
              <span class="honbun2">名　前</span>：<span class="name">{{ player.display_name }}</span>
              <span class="muted">({{ player.remote_user_id }}@{{ player.instance_host }})</span>
              <span v-if="player.roles.includes('admin')" class="tyuu"> [管理者]</span>
            </div>
            <div class="honbun2">
              <span class="honbun2">持ち金</span>：<span class="money">{{ yen(player.money) }}円</span>
              <span class="small">（総資産：{{ yen(total) }}円）（貯金：{{ yen(player.savings) }}円）</span>
            </div>
            <div class="honbun2">
              <span class="honbun2">職　業</span>：{{ player.status.job }}（レベル {{ player.status.job_level }} / 経験値 {{ player.status.job_exp }} / 勤務 {{ player.status.job_kaisuu }}回）
            </div>
            <div v-if="player.status.mastered_jobs.length > 0" class="honbun2">
              <span class="honbun2">マスター職</span>：{{ player.status.mastered_jobs.join('、') }}
            </div>
            <div class="honbun2">
              <span class="honbun2">身体パワー</span>：{{ player.status.energy }} （MAX値：{{ player.status.energy_max }}）<br />
              <span class="powerbar">
                <span class="bar-fill" :class="energyColor" :style="{ width: energyPct + '%' }"></span>
              </span>
            </div>
            <div class="honbun2">
              <span class="honbun2">頭脳パワー</span>：{{ player.status.nou_energy }}（MAX値：{{ player.status.nou_energy_max }}）<br />
              <span class="powerbar">
                <span class="bar-fill" :class="nouColor" :style="{ width: nouPct + '%' }"></span>
              </span>
            </div>
            <div class="honbun2">
              <span class="honbun2">コンディション</span>：<span :class="{ sick: player.status.disease_name }">{{ player.status.condition }}</span>
            </div>
            <div class="honbun2"><span class="honbun2">空腹度</span>：{{ satietyLabel(player.status.satiety) }}</div>
            <div class="honbun2">
              <span class="honbun2">身　長</span>：{{ player.status.height_cm }}cm　<span class="honbun2">体　重</span>：{{ weightKg }}kg
            </div>
            <div class="honbun2">
              <span class="honbun2">体　型</span>：{{ player.status.body_type }}（BMI {{ player.status.bmi }}）
            </div>
            <div class="honbun2">
              <span class="honbun2">所有物</span>：購入商品 {{ player.items.reduce((n, i) => n + i.quantity, 0) }} / 25<br />
              <span class="honbun5" v-for="it in player.items" :key="it.item_id">○{{ it.name }}({{ it.quantity }}個) </span>
            </div>
          </div>
        </div>

        <!-- パラメータ一覧 -->
        <div class="params">
          <div class="phead">頭　脳</div>
          <table>
            <tbody>
              <tr v-for="p in zunou" :key="p.key">
                <td>{{ p.label }}：</td>
                <td class="v">
                  <span class="pbar">
                    <span class="pbar-fill" :style="{ width: paramBar(player.params[p.key]) + '%' }"></span>
                    <span class="pbar-val">{{ player.params[p.key] }}</span>
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
          <div class="phead">身　体</div>
          <table>
            <tbody>
              <tr v-for="p in shintai" :key="p.key">
                <td>{{ p.label }}：</td>
                <td class="v">
                  <span class="pbar">
                    <span class="pbar-fill" :style="{ width: paramBar(player.params[p.key]) + '%' }"></span>
                    <span class="pbar-val">{{ player.params[p.key] }}</span>
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
          <div class="phead">その他</div>
          <table>
            <tbody>
              <tr v-for="p in others" :key="p.key">
                <td>{{ p.label }}：</td>
                <td class="v">
                  <span class="pbar">
                    <span class="pbar-fill" :style="{ width: paramBar(player.params[p.key]) + '%' }"></span>
                    <span class="pbar-val">{{ player.params[p.key] }}</span>
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </div>

  <div class="footer">
    [HOME]<br />
    - TOWN リライト版 (Vue) -
  </div>
</template>
