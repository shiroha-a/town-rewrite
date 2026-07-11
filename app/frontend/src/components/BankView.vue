<script setup lang="ts">
import { ref } from 'vue';
import { api, type Player } from '../api';

const props = defineProps<{ player: Player }>();
const emit = defineEmits<{ update: [player: Player]; back: [] }>();

const yen = (n: number) => n.toLocaleString('ja-JP');
const total = () => props.player.money + props.player.savings;

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

    <div class="panel-white two-col">
      <div class="col">
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

        <h3 class="sec">■入出金明細</h3>
        <p class="note">※普通預金の入出金明細を見ることができます。</p>
        <button class="btn" disabled>入出金明細を見る<span class="muted">(準備中)</span></button>

        <h3 class="sec">■振り込み</h3>
        <p class="note">※参加者のメンバー名がわかれば送金することができます。</p>
        <button class="btn" disabled>振り込み<span class="muted">(準備中)</span></button>
      </div>

      <div class="col">
        <h3 class="sec">■スーパー定期<span class="blue">●スーパー定期預金額：0</span></h3>
        <p class="note">※スーパー定期では1日1回1％の利息がつきます。<span class="muted">(準備中)</span></p>

        <h3 class="sec">■ローン</h3>
        <p class="note">※当銀行へのご利用度や収入に応じてお金を借りることができます。<span class="muted">(準備中)</span></p>
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
  background-image: url(/img/shop_bak.gif);
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
.sec {
  color: #cc0000;
  font-size: 13px;
  margin: 14px 0 6px;
}
.sec:first-child {
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
</style>
