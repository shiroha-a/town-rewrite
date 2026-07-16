// Thin fetch wrapper around the backend REST API. In dev, /api is proxied to
// the Go backend by Vite (see vite.config.ts).

export interface ItemStack {
  item_id: number;
  name: string;
  quantity: number;
  remaining_uses: number;
  sets: number;
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
  mastered: string[];
}
export type WorkResponse = Player & { work_result: WorkResult };

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
  hospitalTreat: (id: number) =>
    request<Player>('POST', `/players/${id}/hospital/treat`, {
      idempotency_key: newIdempotencyKey(),
    }),
  onsenBathe: (id: number, bathId: number) =>
    request<Player>('POST', `/players/${id}/onsen/bathe`, {
      bath_id: bathId,
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
  adminGetSettings: (actingId: number) =>
    request<GameSettings>('GET', '/admin/settings', undefined, adminHeaders(actingId)),
  adminUpdateSettings: (actingId: number, settings: GameSettings) =>
    request<GameSettings>('PUT', '/admin/settings', settings, adminHeaders(actingId)),
  adminUpdateTownMap: (actingId: number, facilities: TownFacility[]) =>
    request<TownFacility[]>('PUT', '/admin/townmap', facilities, adminHeaders(actingId)),
};
