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

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
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

export const api = {
  register: (instanceHost: string, remoteUserId: string, displayName: string) =>
    request<Player>('POST', '/players', {
      instance_host: instanceHost,
      remote_user_id: remoteUserId,
      display_name: displayName,
    }),
  getPlayer: (id: number) => request<Player>('GET', `/players/${id}`),
  shopItems: () => request<ShopItem[]>('GET', '/items'),
  facilityMenu: (facility: string) => request<ShopItem[]>('GET', `/facilities/${facility}/menu`),
  eat: (id: number, foodId: number) =>
    request<Player>('POST', `/players/${id}/eat`, {
      food_id: foodId,
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
};
