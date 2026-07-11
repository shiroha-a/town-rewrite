# town リライト（app/）

レガシーCGI/Perl版の町育成ゲームを、Vue + Go + PostgreSQL + Redis で作り直す実装。
設計は`.tmp/design.md`のフェーズ3を参照。バックエンドファーストで進めている。

## 構成

```
app/
├── backend/            # Go (1バイナリ2モード: web / worker)
│   ├── cmd/town/       # エントリーポイント
│   ├── internal/
│   │   ├── config/     # default.yml ローダ(env上書き対応)
│   │   ├── db/         # pgxpool + goose マイグレーション(埋め込み)
│   │   ├── rediscli/   # Redisクライアント(揮発用途のみ)
│   │   ├── ledger/     # マネー台帳(複式・追記専用)
│   │   ├── player/     # プレイヤー登録・ステータス
│   │   ├── httpapi/    # REST /api/v1
│   │   ├── worker/     # 時間進行(リーダー選出・日次冪等)
│   │   └── integration/# API結合テスト(要DB)
│   ├── default.yml
│   └── Dockerfile
├── deploy/
│   └── compose.yaml    # postgres + redis + web + worker
└── frontend/           # Vue 3 + Vite (SPA)
    └── src/            # api.ts, App.vue, components/(Login/Town)
```

## 開発環境の起動

依存(PostgreSQL/Redis)だけを立てて、バックエンドはホストで動かすのが最速。

```sh
# 依存を起動(postgres:55432 / redis:56379。ホストの5432/6379が使用中のためずらしている)
docker compose -f app/deploy/compose.yaml up -d postgres redis

# バックエンドをホストで起動(default.ymlがlocalhost:55432/56379を指す)
cd app/backend
go run ./cmd/town web       # REST API: http://localhost:8090
go run ./cmd/town worker    # 別ターミナルで時間進行worker
```

フルスタックをコンテナで動かす場合:

```sh
docker compose -f app/deploy/compose.yaml up -d --build
```

## フロントエンド(Vue)

バックエンド(:8090)を起動した状態で:

```sh
cd app/frontend
npm install --include=dev   # NODE_ENV=production対策で--include=dev
npm run dev                 # http://localhost:5173
```

Viteのdevサーバが `/api` を `:8090` (TOWN_API_TARGETで変更可)へプロキシするため、
ブラウザからは同一オリジンに見えCORS不要。現状はコアループ(登録/ステータス/仕事/
銀行/デパート購入/持ち物使用)を実装。ログインはMiAuth導入までの暫定devログイン
(新規登録 or プレイヤーID再開、localStorage保持)。

## API(現状)

| Method | Path | 内容 |
|---|---|---|
| GET | /api/v1/health | ヘルスチェック |
| POST | /api/v1/players | プレイヤー登録(冪等)。body: `{instance_host, remote_user_id, display_name?}` |
| GET | /api/v1/players/{id} | プレイヤー取得(所持金は台帳から算出) |
| GET | /api/v1/items | 店の商品カタログ(公開) |
| POST | /api/v1/players/{id}/work | アルバイト(効果適用) |
| POST | /api/v1/players/{id}/buy \| /use | 購入 / 使用 |
| POST | /api/v1/players/{id}/bank/deposit \| /withdraw | 預金 / 引き出し |
| POST | /api/v1/admin/items \| /jobs \| /simulate | 管理者コンテンツ(暫定認可) |

最初に登録したプレイヤーは管理者ロール。新規登録時に初期所持金50万円が台帳経由で付与される。

## テスト

```sh
cd app/backend
go test ./...                # ユニットテスト(config, worker.gameDate)

# API結合テスト(要 postgres 起動)
TOWN_TEST_DATABASE_URL="postgres://town:town@localhost:55432/town_test?sslmode=disable" \
  go test ./internal/integration/...
```
