<script setup lang="ts">
import { ref } from 'vue';
import { api, type Player, type StatementEntry, type LoanQuote } from '../api';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
// 総資産=所持金+普通口座+スーパー定期-ローン残高(日額×残回数)。
const total = () =>
  props.player.money +
  props.player.savings +
  props.player.super_savings -
  props.player.loan_daily * props.player.loan_count;

const depositAmt = ref<number>(props.player.money);
const withdrawAmt = ref<number>(0);
const message = ref('');
const kind = ref<'ok' | 'error'>('ok');
const busy = ref(false);

async function run(label: string, fn: () => Promise<Player>) {
  busy.value = true;
  message.value = '';
  try {
    emit('update', await fn());
    message.value = `${label}しました。`;
    kind.value = 'ok';
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
    kind.value = 'error';
  } finally {
    busy.value = false;
  }
}
const doDeposit = () => run('預け入れ', () => api.deposit(props.player.id, depositAmt.value));
const doWithdraw = () => run('引き出し', () => api.withdraw(props.player.id, withdrawAmt.value));

// 入出金明細。ボタン押下で取得する。null=未取得、[]=取引なし。
// 普通口座とスーパー定期は別々の通帳として表示する。
const statement = ref<StatementEntry[] | null>(null);
const superStatement = ref<StatementEntry[] | null>(null);
async function loadStatement(account: 'normal' | 'super' = 'normal') {
  busy.value = true;
  message.value = '';
  try {
    const entries = await api.bankStatement(props.player.id, account);
    if (account === 'super') superStatement.value = entries;
    else statement.value = entries;
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
    kind.value = 'error';
  } finally {
    busy.value = false;
  }
}
const fmtDate = (iso: string) => {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`;
};

// 振り込み(送金)。相手はメンバー名、普通口座から引き落とす。
const transferName = ref('');
const transferAmt = ref<number>(0);
const doTransfer = () =>
  run('振り込み', async () => {
    const p = await api.transfer(props.player.id, transferName.value, transferAmt.value);
    await reloadStatement();
    return p;
  });

// スーパー定期(100万円単位で入力)。
const superDepositMan = ref<number>(0);
const superCancelMan = ref<number>(0);
// 表示中の明細だけ取り直す(未取得のものは開かない)。
const reloadStatement = async () => {
  if (statement.value) statement.value = await api.bankStatement(props.player.id);
  if (superStatement.value) superStatement.value = await api.bankStatement(props.player.id, 'super');
};
const doSuperDeposit = () =>
  run('スーパー定期の預け入れ', async () => {
    const p = await api.superDeposit(props.player.id, superDepositMan.value * 1_000_000);
    await reloadStatement();
    return p;
  });
const doSuperCancel = (all: boolean) =>
  run(all ? 'スーパー定期の全額解約' : 'スーパー定期の解約', async () => {
    const p = await api.superCancel(props.player.id, superCancelMan.value * 1_000_000, all);
    await reloadStatement();
    return p;
  });

// ローン。見積り(借入可能額+返済プラン)を取得してから借り入れる。
const loanQuote = ref<LoanQuote | null>(null);
async function loadLoanQuote() {
  busy.value = true;
  message.value = '';
  try {
    loanQuote.value = await api.loanQuote(props.player.id);
  } catch (e) {
    message.value = e instanceof Error ? e.message : String(e);
    kind.value = 'error';
  } finally {
    busy.value = false;
  }
}
const doLoanBorrow = (count: number) =>
  run('借り入れ', async () => {
    const p = await api.loanBorrow(props.player.id, count);
    loanQuote.value = null;
    await reloadStatement();
    return p;
  });
const doLoanRepay = () =>
  run('ローンの一括返済', async () => {
    const p = await api.loanRepay(props.player.id);
    await reloadStatement();
    return p;
  });
</script>

<template>
  <div class="facility-page bank-page">
    <button class="btn back" @click="emit('back')">街に戻る</button>

    <div class="bank-header">
      <div class="info">
        いらっしゃいませ。●総資産 <span class="hl">{{ yen(total()) }}円</span><br />
        ●{{ player.display_name }}さんの所持金：<span class="hl">{{ yen(player.money) }}円</span>
      </div>
      <div class="title">銀　行</div>
    </div>

    <div v-if="message" :class="['message', kind]" data-test="message">{{ message }}</div>

    <!--
      PCは左右2カラム、モバイルは.colをdisplay:contentsで解体しorderで
      普通口座→明細→スーパー定期→明細→振り込み→ローンの縦一列に並べ替える。
    -->
    <div class="panel-white two-col">
      <div class="col">
        <section class="bsec bsec-normal">
          <h3 class="sec">■普通口座<span class="blue">●現在の預け入れ額：{{ yen(player.savings) }}</span></h3>
          <p class="note">
            ※普通口座にお金を預けておくと、1日1回0.5％の利息がつきます。<br />
            (毎日AM5:00に付与)
          </p>
          <div class="row">
            <span class="lbl">◆お　預　け</span>
            <input type="number" v-model.number="depositAmt" data-test="deposit-amount" /> 円
            <button class="btn" :disabled="busy" data-test="deposit" @click="doDeposit">預ける</button>
          </div>
          <div class="row">
            <span class="lbl">◆お引き出し</span>
            <input type="number" v-model.number="withdrawAmt" data-test="withdraw-amount" /> 円
            <button class="btn" :disabled="busy" data-test="withdraw" @click="doWithdraw">引き出す</button>
          </div>
        </section>

        <section class="bsec bsec-stmt">
          <h3 class="sec">■入出金明細</h3>
          <p class="note">※普通口座の入出金明細を見ることができます(最新30件)。</p>
          <button class="btn" :disabled="busy" data-test="statement" @click="loadStatement('normal')">
            入出金明細を見る
          </button>
          <div v-if="statement" class="table-scroll">
            <table class="statement">
              <thead>
                <tr><th>年月日</th><th>お取り引き</th><th class="num">金額</th><th class="num">残高</th></tr>
              </thead>
              <tbody>
                <tr v-if="!statement.length">
                  <td colspan="4" class="muted">まだ取引がありません。</td>
                </tr>
                <tr v-for="(s, i) in statement" :key="i">
                  <td>{{ fmtDate(s.at) }}</td>
                  <td>{{ s.label }}</td>
                  <td class="num" :class="s.amount >= 0 ? 'plus' : 'minus'">
                    {{ s.amount >= 0 ? '+' : '' }}{{ yen(s.amount) }}
                  </td>
                  <td class="num">{{ yen(s.balance) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section class="bsec bsec-transfer">
          <h3 class="sec">■振り込み</h3>
          <p class="note">
            ※参加者のメンバー名がわかれば送金することができます。<br />
            お金は普通口座から引き落とされます(送金は1回100万円まで、超えた分は寄付されます)。
          </p>
          <div class="row">
            <span class="lbl">◆お相手</span>
            <input type="text" v-model.trim="transferName" placeholder="メンバー名" data-test="transfer-name" />
          </div>
          <div class="row">
            <span class="lbl">◆金　額</span>
            <input type="number" v-model.number="transferAmt" data-test="transfer-amount" /> 円
            <button class="btn" :disabled="busy" data-test="transfer" @click="doTransfer">振り込む</button>
          </div>
        </section>
      </div>

      <div class="col">
        <section class="bsec bsec-super">
          <h3 class="sec">
            ■スーパー定期<span class="blue">●スーパー定期預金額：{{ yen(player.super_savings) }}</span>
          </h3>
          <p class="note">
            ※スーパー定期では1日1回1％の利息がつきます。<br />
            預け入れは100万円単位、引き出しは解約(全額または100万円単位)となります。
          </p>
          <div class="row">
            <span class="lbl">◆お　預　け</span>
            <input type="number" v-model.number="superDepositMan" min="0" data-test="super-deposit-amount" /> 百万円
            <button class="btn" :disabled="busy" data-test="super-deposit" @click="doSuperDeposit">預ける</button>
          </div>
          <div class="row">
            <span class="lbl">◆解　　約</span>
            <input type="number" v-model.number="superCancelMan" min="0" data-test="super-cancel-amount" /> 百万円
            <button class="btn" :disabled="busy" data-test="super-cancel" @click="doSuperCancel(false)">部分解約</button>
            <button class="btn" :disabled="busy" data-test="super-cancel-all" @click="doSuperCancel(true)">全額解約</button>
          </div>
        </section>

        <section class="bsec bsec-super-stmt">
          <h3 class="sec">■スーパー定期明細</h3>
          <p class="note">※スーパー定期の預入・解約・利息の明細を見ることができます(最新30件)。</p>
          <button class="btn" :disabled="busy" data-test="super-statement" @click="loadStatement('super')">
            スーパー定期明細を見る
          </button>
          <div v-if="superStatement" class="table-scroll">
            <table class="statement">
              <thead>
                <tr><th>年月日</th><th>お取り引き</th><th class="num">金額</th><th class="num">残高</th></tr>
              </thead>
              <tbody>
                <tr v-if="!superStatement.length">
                  <td colspan="4" class="muted">まだ取引がありません。</td>
                </tr>
                <tr v-for="(s, i) in superStatement" :key="i">
                  <td>{{ fmtDate(s.at) }}</td>
                  <td>{{ s.label }}</td>
                  <td class="num" :class="s.amount >= 0 ? 'plus' : 'minus'">
                    {{ s.amount >= 0 ? '+' : '' }}{{ yen(s.amount) }}
                  </td>
                  <td class="num">{{ yen(s.balance) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section class="bsec bsec-loan">
          <h3 class="sec">■ローン</h3>
          <p class="note">※当銀行へのご利用度や収入に応じてお金を借りることができます。</p>
          <!-- 返済中 -->
          <template v-if="player.loan_count > 0">
            <p class="note">
              現在のローン残高：<span class="blue">{{ yen(player.loan_daily * player.loan_count) }}円</span><br />
              （日額 {{ yen(player.loan_daily) }}円 × 残り{{ player.loan_count }}回）<br />
              ※毎日AM5:00に日額が普通口座から自動で引き落とされます。
            </p>
            <button class="btn" :disabled="busy" data-test="loan-repay" @click="doLoanRepay">一括返済する</button>
          </template>
          <!-- 未借入 -->
          <template v-else>
            <button v-if="!loanQuote" class="btn" :disabled="busy" data-test="loan-quote" @click="loadLoanQuote">
              借入可能額を調べる
            </button>
            <div v-else>
              <p class="note">借入可能額：<span class="blue">{{ yen(loanQuote.limit) }}円</span></p>
              <template v-if="loanQuote.limit > 0">
                <p class="note">返済回数を選んで借り入れます(融資額は借入可能額の全額)。</p>
                <div class="table-scroll">
                  <table class="statement">
                    <thead>
                      <tr><th>返済回数</th><th>利率</th><th class="num">日額</th><th class="num">総返済</th><th></th></tr>
                    </thead>
                    <tbody>
                      <tr v-for="pl in loanQuote.plans" :key="pl.count">
                        <td>{{ pl.count }}回</td>
                        <td>{{ pl.rate }}%</td>
                        <td class="num">{{ yen(pl.daily) }}</td>
                        <td class="num">{{ yen(pl.total) }}</td>
                        <td>
                          <button class="btn" :disabled="busy" @click="doLoanBorrow(pl.count)">借りる</button>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </template>
              <p v-else class="note muted">現在借り入れできる金額がありません。</p>
            </div>
          </template>
        </section>
      </div>
    </div>

    <div style="text-align: center; margin-top: 8px">
      <button class="btn" @click="emit('back')">街に戻る</button>
    </div>
  </div>
</template>

<style scoped>
.bank-page {
  background-color: #999999;
  /* 旧shop_bak.gifのCSS再現: 6px周期の1pxライン */
  background-image: repeating-linear-gradient(180deg, transparent 0 2px, #cccccc 2px 3px, transparent 3px 6px);
  padding: 6px;
  min-height: 80vh;
}
.btn.back {
  margin-bottom: 6px;
}
.bank-header {
  display: flex;
  border: 1px solid #333;
  margin-bottom: 8px;
}
.bank-header .info {
  flex: 1 1 auto;
  background: #fff;
  padding: 8px 12px;
  color: #006699;
  line-height: 1.7;
}
.bank-header .hl {
  color: #0033cc;
  font-weight: bold;
}
.bank-header .title {
  flex: 0 0 300px;
  background: #333;
  color: #fff;
  font-size: 22px;
  font-weight: bold;
  letter-spacing: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.panel-white {
  background: #fff;
  border: 1px solid #333;
  padding: 12px;
}
.two-col {
  display: flex;
  gap: 24px;
}
.two-col .col {
  flex: 1 1 0;
  min-width: 0;
}
.table-scroll {
  overflow-x: auto;
}
.sec {
  color: #cc0000;
  font-size: 13px;
  margin: 14px 0 6px;
}
.bsec:first-child > .sec {
  margin-top: 0;
}
.sec .blue {
  color: #0033cc;
  font-weight: normal;
}
.note {
  font-size: 12px;
  color: #333;
  margin: 4px 0;
  line-height: 1.5;
}
.row {
  margin: 6px 0;
}
.row .lbl {
  color: #006699;
  margin-right: 6px;
}
.statement {
  width: 100%;
  border-collapse: collapse;
  margin-top: 6px;
  font-size: 12px;
}
.statement th,
.statement td {
  border-bottom: 1px solid #e5e5e5;
  padding: 3px 6px;
  text-align: left;
}
.statement th {
  color: #333;
  border-bottom: 1px solid #999;
}
.statement .num {
  text-align: right;
  white-space: nowrap;
}
.statement .plus {
  color: #067a06;
}
.statement .minus {
  color: #cc3300;
}
/* モバイル: 2カラムを解体し、orderで縦一列に並べ替える */
@media (max-width: 700px) {
  .two-col {
    flex-direction: column;
    gap: 0;
  }
  .two-col .col {
    display: contents;
  }
  .bsec-normal {
    order: 1;
  }
  .bsec-stmt {
    order: 2;
  }
  .bsec-super {
    order: 3;
  }
  .bsec-super-stmt {
    order: 4;
  }
  .bsec-transfer {
    order: 5;
  }
  .bsec-loan {
    order: 6;
  }
  /* display:contents下では:first-child基準が並び順と一致しないため上マージンを取り直す */
  .two-col .bsec > .sec {
    margin-top: 14px;
  }
  .two-col .bsec-normal > .sec {
    margin-top: 0;
  }
  /* タイトル帯が広すぎて挨拶文が潰れるため縮める */
  .bank-header .title {
    flex: 0 0 96px;
    font-size: 16px;
    letter-spacing: 3px;
  }
  .row input[type='number'],
  .row input[type='text'] {
    max-width: 45vw;
  }
}
</style>
