// Thin fetch wrapper around the backend REST API. In dev, /api is proxied to
// the Go backend by Vite (see vite.config.ts).

export interface ItemStack {
  item_id: number;
  name: string;
  quantity: number;
  remaining_uses: number;
  sets: number;
  durability_unit: string; // 'use'(回) or 'day'(日)
  money: number;
  params: Record<string, number>;
  interval_min: number;
  // クールタイム中の再使用可能時刻(ISO8601)。使用可能ならnull。
  next_available_at: string | null;
}

export interface Params {
  kokugo: number;
  suugaku: number;
  rika: number;
  syakai: number;
  eigo: number;
  ongaku: number;
  bijutsu: number;
  looks: number;
  tairyoku: number;
  kenkou: number;
  speed: number;
  power: number;
  wanryoku: number;
  kyakuryoku: number;
  love: number;
  omoshirosa: number;
}

export interface Player {
  id: number;
  instance_host: string;
  remote_user_id: string;
  display_name: string;
  roles: string[];
  money: number;
  savings: number;
  super_savings: number;
  loan_daily: number;
  loan_count: number;
  status: {
    energy: number;
    energy_max: number;
    nou_energy: number;
    nou_energy_max: number;
    job: string;
    job_level: number;
    job_exp: number;
    job_kaisuu: number;
    mastered_jobs: string[];
    satiety: number;
    height_cm: number;
    weight_g: number;
    bmi: number;
    body_type: string;
    disease_index: number;
    disease_name: string;
    condition: string;
    work_available_at: string | null;
    energy_full_at: string | null;
    nou_energy_full_at: string | null;
    onsen_multiplier: number;
  };
  params: Params;
  items: ItemStack[];
  // サーバの現在時刻(ISO8601)。クライアント時計のずれを補正しカウントダウンに使う。
  server_now: string;
}

export interface JobOption {
  id: number;
  name: string;
  pay: number;
  salary: number;
  rank: number;
  require_master: string;
  requirements: Record<string, number>;
  work_params: Record<string, number>;
  energy_cost: number;
  nou_energy_cost: number;
  pay_interval: number;
}

export interface ShopItem {
  id: number;
  name: string;
  category: string;
  price: number;
  money: number;
  params: Record<string, number>;
  interval_min: number;
  durability: number;
  durability_unit: string; // 'use'(回) or 'day'(日)
  power_multiplier: number; // 温泉の回復速度倍率(0=温泉ではない)
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  headers?: Record<string, string>,
): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers: { ...(body ? { 'Content-Type': 'application/json' } : {}), ...(headers ?? {}) },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) {
    throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`);
  }
  return data as T;
}

// クライアント側で冪等キーを生成する(同一操作の二重送信対策)。
// crypto.randomUUIDはセキュアコンテキスト(HTTPS/localhost)限定のため、
// HTTP経由の非localhostアクセスでも動くようフォールバックを持つ。
export function newIdempotencyKey(): string {
  const c = globalThis.crypto;
  if (c && typeof c.randomUUID === 'function') {
    return c.randomUUID();
  }
  return `k-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

// 住民名鑑(公開一覧)の1件。
export interface PublicSummary {
  id: number;
  display_name: string;
  job: string;
  job_level: number;
}
// 公開プロフィール(お金/身元などの非公開項目は含まない)。
export interface PublicProfile {
  id: number;
  display_name: string;
  status: Player['status'];
  params: Params;
}

// 仕事1回の結果サマリ(給料・昇給・ボーナス・経験値)。
export interface WorkResult {
  exp_gained: number;
  new_level: number;
  leveled_up: boolean;
  this_salary: number;
  pay: number;
  pay_every: number;
  bonus: number;
  work_bonus: number;
  weight_loss_g: number;
  mastered: string[];
}
export type WorkResponse = Player & { work_result: WorkResult };

// 普通口座の入出金明細1行。
export interface StatementEntry {
  at: string;
  label: string;
  amount: number;
  balance: number;
}

// ミニゲーム(カジノ)1プレイの結果。detailはゲーム別の結果詳細。
export interface CasinoPlayResult {
  player: Player;
  payout: number;
  win: boolean;
  detail: Record<string, unknown>;
}

// スクラッチのカード1枚。valuesは開封済みセルのindex->値(未開封は含まない)。
export interface ScratchCard {
  index: number;
  values: Record<number, number>;
  opened: number;
  finished: boolean;
  atari: number;
}
export interface ScratchState {
  game: string;
  cols: number;
  cells: number;
  atari_max: number;
  open_max: number;
  cards: ScratchCard[];
}
export interface ScratchOpenResult {
  player: Player;
  value: number;
  win: boolean;
  bonus: boolean;
  prize: number;
  state: ScratchState;
}

// ブラックジャックの盤面。進行中は親(oya)の1枚目のみ公開しoya_hidden枚が伏せ。
export interface BJState {
  active: boolean;
  rate: number;
  ply: number[];
  ply_score: number;
  oya: number[];
  oya_score: number;
  oya_hidden: number;
  phase: string; // 'playing' | 'over'
  result: string; // 'win' | 'lose' | 'push'
  payout: number;
}

// ポーカーの状態。phase: none(未購入)/ready(配札前)/dealt(交換前)。
export interface PokerState {
  active: boolean;
  points: number;
  hand: number[];
  phase: string;
  result: number; // 直近の役(-1=未判定)
  result_name: string;
  gain: number; // 直近のポイント増減
}

// ロト6の状態(自分の当日の購入券・上限・直近抽選)。
export interface Loto6Ticket {
  numbers: number[];
}
export interface Loto6DrawInfo {
  date: string;
  winning: number[];
}
export interface Loto6State {
  my_tickets: Loto6Ticket[];
  today_count: number;
  daily_limit: number;
  cost: number;
  last_draw: Loto6DrawInfo | null;
}

// ローンの返済プラン1件(返済回数・利率・日額・総返済額)。
export interface LoanPlanQuote {
  count: number;
  rate: number;
  daily: number;
  total: number;
}
// ローンの借入見積り(借入可能額と各返済プラン)。
export interface LoanQuote {
  limit: number;
  has_loan: boolean;
  plans: LoanPlanQuote[];
}

// 効果エンジンのop(add_param / add_money)。
export interface EffectOp {
  op: 'add_param' | 'add_money';
  param?: string;
  amount: number;
}
// 条件(param_gte)。
export interface Condition {
  pred: 'param_gte';
  param: string;
  value: number;
}
export interface AdminItem {
  id: number;
  name: string;
  category: string;
  price: number;
  effect: EffectOp[];
  enabled: boolean;
}
export interface AdminJob {
  id: number;
  name: string;
  requirements: Condition[];
  effect: EffectOp[];
  salary: number;
  pay_interval: number;
  bonus_rate: number;
  raise_rate: number;
  rank: number;
  require_master: string;
  body_cost: number;
  nou_cost: number;
  enabled: boolean;
}
// 職業の作成/更新ペイロード。
export interface JobPayload {
  name: string;
  requirements: Condition[];
  effect: EffectOp[];
  salary: number;
  pay_interval: number;
  bonus_rate: number;
  raise_rate: number;
  rank: number;
  require_master: string;
  body_cost: number;
  nou_cost: number;
  enabled: boolean;
}
export interface SimResult {
  plan: {
    money_delta: number;
    params: { name: string; old_value: number; new_value: number }[];
  };
  warnings: string[];
}

export interface AdminPlayerSummary {
  id: number;
  display_name: string;
  roles: string[];
  money: number;
  job: string;
  job_level: number;
}
// プレイヤーの管理者編集ペイロード。
export interface AdminPlayerPayload {
  display_name: string;
  money: number;
  is_admin: boolean;
  params: Params;
  energy: number;
  nou_energy: number;
  satiety: number;
  job: string;
  job_level: number;
  job_exp: number;
  disease_index: number;
  height_cm: number;
  weight_g: number;
}

export interface GameSettings {
  initial_money: number;
  daily_interest_permille: number;
  energy_recovery_sec: number;
  nou_recovery_sec: number;
  satiety_decay_sec: number;
  condition_eval_interval_min: number;
  work_interval_min: number;
  debug_no_cooldown: boolean;
  depart_daily_count: number;
  syokudou_daily_count: number;
}

export interface TownFacility {
  key: string;
  img: string;
  alt: string;
  col: number;
  row: number;
  ready: boolean;
}

export interface StockPrice {
  symbol: string;
  price: number;
}
export interface StockHolding {
  symbol: string;
  price: number;
  shares: number;
  value: number;
  cost_total: number;
  avg_cost: number;
  unrealized: number;
  inv_total: number;
  ret_total: number;
  net: number;
}
export interface StocksResp {
  prices: StockPrice[];
  event_log: string[];
}
export interface PlayerStocksResp {
  holdings: StockHolding[];
  history: string[];
}

export interface KeibaHorse {
  name: string;
  img: string;
  odds: number;
}
export interface KeibaRankEntry {
  name: string;
  profit: number;
  invested: number;
  won: number;
}
export interface KeibaRaceResp {
  race_id: number;
  lineup: KeibaHorse[];
  ranking: KeibaRankEntry[];
}
export interface KeibaResult {
  winner_index: number;
  winner_name: string;
  winner_odds: number;
  payout: number;
  invested: number;
  steps: number[][];
  lineup: KeibaHorse[];
}
export interface KeibaBetResp {
  player: Player;
  result: KeibaResult;
}

export interface MailMessage {
  id: number;
  direction: string;
  counterpart_id: number | null;
  counterpart_name: string;
  body: string;
  sent_at: string;
  saved: boolean;
  unread: boolean;
}
export interface MailContact {
  id: number;
  name: string;
}
export interface Mailbox {
  received: MailMessage[];
  sent: MailMessage[];
  address_book: MailContact[];
  unread: number;
}

export interface Greeting {
  id: number;
  user_id: number | null;
  user_name: string;
  category: string;
  body: string;
  color: string;
  janken: string;
  posted_at: string;
}
export interface GreetResult {
  reward: number;
  jackpot: boolean;
  janken: string;
  janken_pc: string;
  fine: boolean;
}
export interface GreetResp {
  player: Player;
  result: GreetResult;
}

export interface AttendanceMember {
  id: number;
  name: string;
  cells: string[]; // present | absent | blank(dates と同順)
}
export interface AttendanceRank {
  name: string;
  present: number;
  days: number;
  rate: number;
}
export interface AttendanceBoard {
  dates: string[];
  members: AttendanceMember[];
  ranking: AttendanceRank[];
}

export interface EventOutcome {
  name: string;
  message: string;
  good: boolean;
  money_delta: number;
  params: Record<string, number> | null;
  disease_delta: number;
  weight_g: number;
  special: string;
}
export interface EventRollResp {
  player: Player;
  event: EventOutcome | null;
}

export interface ShopSummary {
  owner_id: number;
  owner_name: string;
  name: string;
  listings: number;
}
export interface ShopListing {
  item_id: number;
  item_name: string;
  category: string;
  price: number;
  stock: number;
  money: number;
  params: Record<string, number>;
}
export interface ShopDetail {
  owner_id: number;
  owner_name: string;
  name: string;
  listings: ShopListing[];
}

export interface Character {
  owner_id: number;
  name: string;
  abilities: Record<string, number>;
  sintai: number;
  zunou: number;
  wins: number;
  losses: number;
  draws: number;
}
export interface CLeagueRank {
  owner_id: number;
  owner_name: string;
  char_name: string;
  wins: number;
  losses: number;
  draws: number;
  sintai: number;
  zunou: number;
}
export interface BattleRound {
  ability: string;
  comment: string;
  a_score: number;
  b_score: number;
  winner: string;
}
export interface BattleResult {
  winner: string;
  rounds: BattleRound[];
}
export interface BattleResp {
  player: Player;
  result: BattleResult;
}

function adminHeaders(actingId: number): Record<string, string> {
  return { 'X-Acting-Player-Id': String(actingId) };
}

export const api = {
  register: (instanceHost: string, remoteUserId: string, displayName: string) =>
    request<Player>('POST', '/players', {
      instance_host: instanceHost,
      remote_user_id: remoteUserId,
      display_name: displayName,
    }),
  getPlayer: (id: number) => request<Player>('GET', `/players/${id}`),
  listPlayers: () => request<PublicSummary[]>('GET', '/players'),
  playerProfile: (id: number) => request<PublicProfile>('GET', `/players/${id}/profile`),
  townMap: () => request<TownFacility[]>('GET', '/townmap'),
  stocks: () => request<StocksResp>('GET', '/stocks'),
  playerStocks: (id: number) => request<PlayerStocksResp>('GET', `/players/${id}/stocks`),
  stockBuy: (id: number, symbol: string, quantity: number) =>
    request<Player>('POST', `/players/${id}/stocks/buy`, {
      symbol,
      quantity,
      idempotency_key: newIdempotencyKey(),
    }),
  stockSell: (id: number, symbol: string, quantity: number) =>
    request<Player>('POST', `/players/${id}/stocks/sell`, {
      symbol,
      quantity,
      idempotency_key: newIdempotencyKey(),
    }),
  stockSettle: (id: number) =>
    request<Player>('POST', `/players/${id}/stocks/settle`, {
      idempotency_key: newIdempotencyKey(),
    }),
  getMail: (id: number) => request<Mailbox>('GET', `/players/${id}/mail`),
  getMailUnread: (id: number) => request<{ unread: number }>('GET', `/players/${id}/mail/unread`),
  mailSend: (id: number, recipientId: number, body: string) =>
    request<{ ok: boolean }>('POST', `/players/${id}/mail/send`, { recipient_id: recipientId, body }),
  mailDelete: (id: number, msgId: number) =>
    request<{ ok: boolean }>('DELETE', `/players/${id}/mail/${msgId}`),
  mailSave: (id: number, msgId: number, saved: boolean) =>
    request<{ ok: boolean }>('PUT', `/players/${id}/mail/${msgId}/save`, { saved }),
  greetings: (limit?: number) =>
    request<Greeting[]>('GET', `/greetings${limit ? `?limit=${limit}` : ''}`),
  postGreeting: (id: number, category: string, body: string, color: string, janken: string) =>
    request<GreetResp>('POST', `/players/${id}/greetings`, {
      category,
      body,
      color,
      janken,
      idempotency_key: newIdempotencyKey(),
    }),
  getCharacter: (id: number) => request<Character | null>('GET', `/players/${id}/character`),
  cleague: () => request<CLeagueRank[]>('GET', '/cleague'),
  setCharacterName: (id: number, name: string) =>
    request<Player>('POST', `/players/${id}/character`, { name, idempotency_key: newIdempotencyKey() }),
  growCharacter: (id: number, inputs: Record<string, number>) =>
    request<Player>('POST', `/players/${id}/character/grow`, { inputs, idempotency_key: newIdempotencyKey() }),
  battle: (id: number, opponentId: number) =>
    request<BattleResp>('POST', `/players/${id}/character/battle`, {
      opponent_id: opponentId,
      idempotency_key: newIdempotencyKey(),
    }),
  listShops: () => request<ShopSummary[]>('GET', '/shops'),
  getShop: (ownerId: number) => request<ShopDetail>('GET', `/shops/${ownerId}`),
  shopOpen: (id: number, name: string) =>
    request<Player>('POST', `/players/${id}/shop/open`, { name, idempotency_key: newIdempotencyKey() }),
  shopStock: (id: number, itemId: number, quantity: number, price: number) =>
    request<{ ok: boolean }>('POST', `/players/${id}/shop/stock`, { item_id: itemId, quantity, price }),
  shopUnstock: (id: number, itemId: number, quantity: number) =>
    request<{ ok: boolean }>('POST', `/players/${id}/shop/unstock`, { item_id: itemId, quantity }),
  shopPrice: (id: number, itemId: number, price: number) =>
    request<{ ok: boolean }>('POST', `/players/${id}/shop/price`, { item_id: itemId, price }),
  shopBuy: (id: number, ownerId: number, itemId: number, quantity: number) =>
    request<Player>('POST', `/players/${id}/shop/buy`, {
      owner_id: ownerId,
      item_id: itemId,
      quantity,
      idempotency_key: newIdempotencyKey(),
    }),
  shopOffer: (id: number, ownerId: number, amount: number) =>
    request<Player>('POST', `/players/${id}/shop/offer`, {
      owner_id: ownerId,
      amount,
      idempotency_key: newIdempotencyKey(),
    }),
  attendanceBoard: () => request<AttendanceBoard>('GET', '/attendance'),
  attendanceCheckin: (id: number) =>
    request<{ recorded: boolean }>('POST', `/players/${id}/attendance/checkin`),
  eventRoll: (id: number) =>
    request<EventRollResp>('POST', `/players/${id}/events/roll`, {
      idempotency_key: newIdempotencyKey(),
    }),
  keibaRace: (id: number) => request<KeibaRaceResp>('GET', `/players/${id}/keiba`),
  keibaBet: (id: number, raceId: number, bets: number[]) =>
    request<KeibaBetResp>('POST', `/players/${id}/keiba/bet`, {
      race_id: raceId,
      bets,
      idempotency_key: newIdempotencyKey(),
    }),
  shopItems: () => request<ShopItem[]>('GET', '/items'),
  facilityMenu: (facility: string) => request<ShopItem[]>('GET', `/facilities/${facility}/menu`),
  eat: (id: number, foodId: number) =>
    request<Player>('POST', `/players/${id}/eat`, {
      food_id: foodId,
      idempotency_key: newIdempotencyKey(),
    }),
  schoolAttend: (id: number, courseId: number) =>
    request<Player>('POST', `/players/${id}/school/attend`, {
      course_id: courseId,
      idempotency_key: newIdempotencyKey(),
    }),
  facilityUse: (id: number, facility: string, menuId: number) =>
    request<Player>('POST', `/players/${id}/facilities/${facility}/use`, {
      menu_id: menuId,
      idempotency_key: newIdempotencyKey(),
    }),
  jobs: () => request<JobOption[]>('GET', '/jobs'),
  changeJob: (id: number, jobName: string) =>
    request<Player>('POST', `/players/${id}/job`, {
      job_name: jobName,
      idempotency_key: newIdempotencyKey(),
    }),
  work: (id: number) =>
    request<WorkResponse>('POST', `/players/${id}/work`, { idempotency_key: newIdempotencyKey() }),
  buy: (id: number, itemId: number) =>
    request<Player>('POST', `/players/${id}/buy`, {
      item_id: itemId,
      idempotency_key: newIdempotencyKey(),
    }),
  use: (id: number, itemId: number) =>
    request<Player>('POST', `/players/${id}/use`, {
      item_id: itemId,
      idempotency_key: newIdempotencyKey(),
    }),
  deposit: (id: number, amount: number) =>
    request<Player>('POST', `/players/${id}/bank/deposit`, {
      amount,
      idempotency_key: newIdempotencyKey(),
    }),
  withdraw: (id: number, amount: number) =>
    request<Player>('POST', `/players/${id}/bank/withdraw`, {
      amount,
      idempotency_key: newIdempotencyKey(),
    }),
  bankStatement: (id: number) => request<StatementEntry[]>('GET', `/players/${id}/bank/statement`),
  transfer: (id: number, toName: string, amount: number) =>
    request<Player>('POST', `/players/${id}/bank/transfer`, {
      to_name: toName,
      amount,
      idempotency_key: newIdempotencyKey(),
    }),
  superDeposit: (id: number, amount: number) =>
    request<Player>('POST', `/players/${id}/bank/super/deposit`, {
      amount,
      idempotency_key: newIdempotencyKey(),
    }),
  superCancel: (id: number, amount: number, all: boolean) =>
    request<Player>('POST', `/players/${id}/bank/super/cancel`, {
      amount,
      all,
      idempotency_key: newIdempotencyKey(),
    }),
  loanQuote: (id: number) => request<LoanQuote>('GET', `/players/${id}/bank/loan/quote`),
  loanBorrow: (id: number, count: number) =>
    request<Player>('POST', `/players/${id}/bank/loan/borrow`, {
      count,
      idempotency_key: newIdempotencyKey(),
    }),
  loanRepay: (id: number) =>
    request<Player>('POST', `/players/${id}/bank/loan/repay`, {
      idempotency_key: newIdempotencyKey(),
    }),
  casinoPlay: (id: number, game: string, bet: number, params: unknown) =>
    request<CasinoPlayResult>('POST', `/players/${id}/casino/${game}/play`, {
      bet,
      params,
      idempotency_key: newIdempotencyKey(),
    }),
  scratchState: (id: number, game: string) => request<ScratchState>('GET', `/players/${id}/scratch/${game}`),
  scratchOpen: (id: number, game: string, card: number, cell: number) =>
    request<ScratchOpenResult>('POST', `/players/${id}/scratch/${game}/open`, {
      card,
      cell,
      idempotency_key: newIdempotencyKey(),
    }),
  bjState: (id: number) => request<BJState>('GET', `/players/${id}/blackjack`),
  bjStart: (id: number, rate: number) =>
    request<BJState>('POST', `/players/${id}/blackjack/start`, { rate, idempotency_key: newIdempotencyKey() }),
  bjHit: (id: number) =>
    request<BJState>('POST', `/players/${id}/blackjack/hit`, { idempotency_key: newIdempotencyKey() }),
  bjStand: (id: number) =>
    request<BJState>('POST', `/players/${id}/blackjack/stand`, { idempotency_key: newIdempotencyKey() }),
  pokerState: (id: number) => request<PokerState>('GET', `/players/${id}/poker`),
  pokerBuy: (id: number) =>
    request<PokerState>('POST', `/players/${id}/poker/buy`, { idempotency_key: newIdempotencyKey() }),
  pokerDeal: (id: number) =>
    request<PokerState>('POST', `/players/${id}/poker/deal`, { idempotency_key: newIdempotencyKey() }),
  pokerDraw: (id: number, hold: number[]) =>
    request<PokerState>('POST', `/players/${id}/poker/draw`, { hold, idempotency_key: newIdempotencyKey() }),
  pokerCashout: (id: number) =>
    request<PokerState>('POST', `/players/${id}/poker/cashout`, { idempotency_key: newIdempotencyKey() }),
  loto6State: (id: number) => request<Loto6State>('GET', `/players/${id}/loto6`),
  loto6Buy: (id: number, numbers: number[]) =>
    request<Loto6State>('POST', `/players/${id}/loto6/buy`, { numbers, idempotency_key: newIdempotencyKey() }),
  hospitalTreat: (id: number) =>
    request<Player>('POST', `/players/${id}/hospital/treat`, {
      idempotency_key: newIdempotencyKey(),
    }),
  onsenBathe: (id: number, bathId: number) =>
    request<Player>('POST', `/players/${id}/onsen/bathe`, {
      bath_id: bathId,
      idempotency_key: newIdempotencyKey(),
    }),
  onsenLeave: (id: number) =>
    request<Player>('POST', `/players/${id}/onsen/leave`, {
      idempotency_key: newIdempotencyKey(),
    }),
  onsenTick: (id: number) =>
    request<Player>('POST', `/players/${id}/onsen/tick`, {
      idempotency_key: newIdempotencyKey(),
    }),

  // 管理者API(X-Acting-Player-Idヘッダ + adminロール)。
  adminListItems: (actingId: number) =>
    request<AdminItem[]>('GET', '/admin/items', undefined, adminHeaders(actingId)),
  adminCreateItem: (
    actingId: number,
    item: { name: string; category: string; price: number; effect: EffectOp[] },
  ) => request<AdminItem>('POST', '/admin/items', item, adminHeaders(actingId)),
  adminUpdateItem: (
    actingId: number,
    id: number,
    item: { name: string; category: string; price: number; effect: EffectOp[]; enabled: boolean },
  ) => request<AdminItem>('PUT', `/admin/items/${id}`, item, adminHeaders(actingId)),
  adminDeleteItem: (actingId: number, id: number) =>
    request<{ deleted: boolean }>('DELETE', `/admin/items/${id}`, undefined, adminHeaders(actingId)),
  adminListJobs: (actingId: number) =>
    request<AdminJob[]>('GET', '/admin/jobs', undefined, adminHeaders(actingId)),
  adminCreateJob: (actingId: number, job: JobPayload) =>
    request<AdminJob>('POST', '/admin/jobs', job, adminHeaders(actingId)),
  adminUpdateJob: (actingId: number, id: number, job: JobPayload) =>
    request<AdminJob>('PUT', `/admin/jobs/${id}`, job, adminHeaders(actingId)),
  adminDeleteJob: (actingId: number, id: number) =>
    request<{ deleted: boolean }>('DELETE', `/admin/jobs/${id}`, undefined, adminHeaders(actingId)),
  adminSimulate: (
    actingId: number,
    effect: EffectOp[],
    state: { money: number; params: Record<string, { value: number; max: number }> },
  ) => request<SimResult>('POST', '/admin/simulate', { effect, state }, adminHeaders(actingId)),
  adminListPlayers: (actingId: number) =>
    request<AdminPlayerSummary[]>('GET', '/admin/players', undefined, adminHeaders(actingId)),
  adminUpdatePlayer: (actingId: number, id: number, payload: AdminPlayerPayload) =>
    request<Player>('PUT', `/admin/players/${id}`, payload, adminHeaders(actingId)),
  adminDeletePlayer: (actingId: number, id: number) =>
    request<{ deleted: boolean }>('DELETE', `/admin/players/${id}`, undefined, adminHeaders(actingId)),
  adminDeleteGreeting: (actingId: number, id: number) =>
    request<{ deleted: boolean }>('DELETE', `/admin/greetings/${id}`, undefined, adminHeaders(actingId)),
  adminGetSettings: (actingId: number) =>
    request<GameSettings>('GET', '/admin/settings', undefined, adminHeaders(actingId)),
  adminUpdateSettings: (actingId: number, settings: GameSettings) =>
    request<GameSettings>('PUT', '/admin/settings', settings, adminHeaders(actingId)),
  adminUpdateTownMap: (actingId: number, facilities: TownFacility[]) =>
    request<TownFacility[]>('PUT', '/admin/townmap', facilities, adminHeaders(actingId)),
};
