// パラメータのラベル定義。バックエンドの param 名(romaji)と対応。
// メイン画面右のパラメータ一覧・デパート/持ち物の上昇パラメータ・
// 職業安定所の必要パラメータ表示で共有する。

export const PARAM_LABEL: Record<string, string> = {
  energy: '身P',
  nou_energy: '頭P',
  kokugo: '国',
  suugaku: '数',
  rika: '理',
  syakai: '社',
  eigo: '英',
  ongaku: '音',
  bijutsu: '美',
  looks: 'ル',
  tairyoku: '体',
  kenkou: '健',
  speed: 'ス',
  power: 'パ',
  wanryoku: '腕',
  kyakuryoku: '脚',
  love: '恋',
  omoshirosa: '面',
};

// デパート等の横並び表で使う列順(パワー2種 + 詳細16種)。
export const PARAM_ORDER: string[] = [
  'energy',
  'nou_energy',
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

// デパート/持ち物/職業安定所の横並び表で使う列(レガシー順、エッチ(Ｈ)は排除)。
// レガシー: 国 数 理 社 英 音 美 | ル 体 健 ス パ 腕 脚 | L 面 | 身体 頭脳
export const PARAM_COLUMNS: { key: string; label: string }[] = [
  { key: 'kokugo', label: '国' },
  { key: 'suugaku', label: '数' },
  { key: 'rika', label: '理' },
  { key: 'syakai', label: '社' },
  { key: 'eigo', label: '英' },
  { key: 'ongaku', label: '音' },
  { key: 'bijutsu', label: '美' },
  { key: 'looks', label: 'ル' },
  { key: 'tairyoku', label: '体' },
  { key: 'kenkou', label: '健' },
  { key: 'speed', label: 'ス' },
  { key: 'power', label: 'パ' },
  { key: 'wanryoku', label: '腕' },
  { key: 'kyakuryoku', label: '脚' },
  { key: 'love', label: 'L' },
  { key: 'omoshirosa', label: '面' },
  { key: 'energy', label: '身' },
  { key: 'nou_energy', label: '頭' },
];

const label = (k: string) => PARAM_LABEL[k] ?? k;

// 上昇/消費パラメータを "体力+2 国語+2" のように整形。
export function effectSummary(params: Record<string, number>, money: number): string {
  const parts: string[] = [];
  if (money) parts.push(`${money > 0 ? '+' : ''}${money.toLocaleString('ja-JP')}円`);
  for (const [k, v] of Object.entries(params)) {
    if (v) parts.push(`${label(k)}${v > 0 ? '+' : ''}${v}`);
  }
  return parts.length ? parts.join(' ') : '-';
}

// 必要パラメータを "体力≧8" のように整形。
export function requirementSummary(reqs: Record<string, number>): string {
  const parts = Object.entries(reqs).map(([k, v]) => `${label(k)}≧${v}`);
  return parts.length ? parts.join(' ') : 'なし';
}

// 空腹値(満腹度 0-100)をレガシー風のラベルに変換。
export function satietyLabel(s: number): string {
  if (s >= 80) return '満腹';
  if (s >= 60) return '丁度いい';
  if (s >= 40) return 'やや空腹';
  if (s >= 15) return '空腹';
  return 'ペコペコ';
}
