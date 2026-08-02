# KijiYomu

複数の技術系フィードから記事を集め、AI が要約・タグ付け・興味との関連度判定をまとめて行い、カード形式のビューを生成する CLI ツール。出力は静的ファイルだけなので、そのまま GitHub Pages に置けます。

## セットアップ

```bash
pnpm install
pnpm run build
go build -o kijiyomu .
```

`.env` を作って dotenvx 経由で実行すると、API キーを環境変数で渡せます。

```bash
dotenvx run -- go run main.go
```

## .env の設定

```env
AI_API_BASE=https://api.ai.sakura.ad.jp/v1/chat/completions
AI_API_KEY=your-api-key
AI_MODEL=gpt-oss-120b
```

## 使い方

### キャッシュ（中間 JSON）を作成する

フィード取得・OG 画像取得・AI 要約をすべて実行し、結果を JSON に保存します。

```bash
dotenvx run -- ./kijiyomu --data-out kijiyomu-data.json
```

### キャッシュから HTML を生成する

ネットワークアクセスや AI 呼び出しなしで HTML だけ再生成します。

```bash
go run main.go --data-in kijiyomu-data.json --out kijiyomu.html
```

### その他の例

```bash
# AI 要約なし（API 設定不要）。関連度はキーワード一致で概算する
./kijiyomu

# 出力先を指定。存在しないディレクトリは自動で作られる
dotenvx run -- ./kijiyomu --out _site/index.html
```

### 出力されるファイル

`--out` で指定した HTML の隣に、配信に必要なものが一式そろいます。

| ファイル | 内容 |
|---|---|
| `index.html`（`--out` の名前） | ページ本体。CSS と JS はインライン |
| `data.json` | 記事データ。ブラウザが `fetch` で読む |
| `manifest.json` / `sw.js` | PWA 用 |
| `static/logo.png` ほか | ロゴ・アイコン |
| `apple-touch-icon.png` | iOS ホーム画面用 |

## オプション一覧

| フラグ | 環境変数 | デフォルト | 説明 |
|---|---|---|---|
| `--api-base` | `AI_API_BASE` | (なし) | OpenAI 互換 API の URL |
| `--api-key` | `AI_API_KEY` | (なし) | API キー |
| `--model` | `AI_MODEL` | `gpt-4o-mini` | モデル名 |
| `--out` | | `kijiyomu.html` | 出力 HTML ファイル名 |
| `--inline-data` | | (off) | 記事 JSON を HTML に埋め込む（`data.json` を出さない） |
| `--data-in` | | (なし) | 中間 JSON を読み込んで HTML だけ生成 |
| `--data-out` | | (なし) | 取得・要約後の中間 JSON を保存 |
| `--cache-file` | | `.kijiyomu_cache.json` | キャッシュファイルのパス |
| `--config` | | `kijiyomu.yaml` | フィードソース設定ファイル |

## HTML デザインのローカル調整

`templates/main.html`、`src/style.css`、`src/main.jsx` を試行錯誤するときは、先に中間 JSON を作っておくと外部 API を呼ばずに再生成できます。React/CSS の変更後は `pnpm run build` で `static/dist/` を更新してから HTML を再生成します。

```bash
# 1. キャッシュ作成（初回のみ）
dotenvx run -- go run main.go --data-out kijiyomu-data.json

# 2. デザイン変更後に HTML を再生成
pnpm run build
go run main.go --data-in kijiyomu-data.json --inline-data --out kijiyomu.html
```

`--data-in` 指定時はフィード取得・OG 画像取得・AI 要約をすべてスキップします。

記事データは通常 `data.json` として HTML の隣に出力され、ブラウザが `fetch` で読み込みます。`file://` で直接開くと `fetch` がブロックされるため、ローカルで確認するときは `--inline-data` を付けて HTML に埋め込んでください。

## 設定（kijiyomu.yaml）

### profile — 関連度の判定基準

`profile` に興味関心を書いておくと、AI が記事ごとに 0〜100 の関連度を判定します。カードは「関連度 × 新しさ」の順に並びます。関連度は 7 日で半減するよう減衰させているので、多少関連度が低くても新しい記事は上に来ます。

```yaml
profile: |
  - 言語/技術: Rust, Go, TypeScript, Python
  - 分野: システムプログラミング, LLM/AIエージェント, ゲーム開発
  - 関心低め: スポーツ
```

`- ラベル: 値1, 値2` の形式で書いておくと、AI を設定していない場合でも語の一致でおおまかな関連度を計算します。ラベルに「関心低め」を含む行はマイナス側に働きます。

### tags / feeds

`kijiyomu.yaml` でタグの種類とフィードソースを自由に追加・削除できます。記事には `tags` の候補から重複なしで3つのタグが付きます。

```yaml
tags:
  - LLM/言語モデル
  - 生成AI
  - ML/機械学習
  - AIエージェント
  - 研究/論文
  - 開発ツール
  - プログラミング言語
  - Web/フロントエンド
  - バックエンド/API
  - インフラ/クラウド
  - データベース
  - セキュリティ
  - モバイル
  - プロダクト/事例
  - デザイン/UX
  - その他

feeds:
  - name: Hacker News
    type: hn
    limit: 50          # 省略すると 50 が使用される

  - name: はてなブックマーク
    type: rdf
    url: https://b.hatena.ne.jp/hotentry/it.rss

  - name: Zenn
    type: rss
    url: https://zenn.dev/feed

  - name: Qiita
    type: atom
    url: https://qiita.com/popular-items/feed

  - name: Anthropic News
    type: anthropic
    url: https://www.anthropic.com/news
```

### type の種類

| type | 説明 |
|---|---|
| `hn` | Hacker News（Firebase API） |
| `rss` | RSS 2.0 |
| `atom` | Atom |
| `rdf` | RDF/RSS 1.0（はてなブックマーク等） |
| `anthropic` | Anthropic News ページ |

## HTML の機能

- **カードグリッド表示** — OG イメージ付きのカード形式
- **AI 要約** — 各記事の要点を日本語3箇条で表示
- **関連度順の並び** — `profile` との関連度と公開日から算出したスコアの降順
- **記事データの分離配信** — HTML とは別の `data.json` を読み込むため、更新のたびに再取得するのは記事データだけ
- **検索と絞り込み** — 右下のボタンからタイトル・要約の検索、タグ／ソース別の絞り込み
- **既読管理** — 開いた記事は薄く表示。未読のみに絞り込める（localStorage、60日で自動削除）
- **並び替え** — おすすめ（関連度 × 新しさ）と新着の切り替え
- **キーボード操作** — `j`/`k` で上下、`h`/`l` で左右、Enter で開く、`/` で検索、Esc で閉じる
- **直近記事のみ表示** — 公開日が取れる記事は直近2か月分に絞り込み
- **重複排除** — 同一 URL の記事は複数ソースをまとめて表示

- **オフライン表示** — Service Worker が直近の内容をキャッシュする

## GitHub Actions による自動実行

`.github/workflows/kijiyomu.yml` で 2 時間ごとに実行し、GitHub Pages へデプロイします。`--out _site/index.html` で成果物を直接 `_site` に書き出すため、コピー手順はありません。

リポジトリの **Settings → Secrets** に以下を登録してください:

| シークレット名 | 内容 |
|---|---|
| `AI_API_BASE` | API の URL |
| `AI_API_KEY` | API キー |
| `AI_MODEL` | モデル名 |

未設定でも実行は通ります（AI 要約とタグ付けがスキップされます）。

AI 要約・タグ・関連度・OG イメージのキャッシュ（`.kijiyomu_cache.json`）は `actions/cache` で runs をまたいで保持されます。記事一覧から消えて 14 日経ったエントリは自動的に捨てられます。

## PWA

`manifest.json` と `sw.js` を出力するため、ホーム画面に追加してスタンドアロン起動できます。Service Worker のキャッシュ方針は次の通りです。

| 対象 | 方針 |
|---|---|
| HTML・`data.json` | ネットワーク優先（オフライン時のみキャッシュ） |
| ロゴ・アイコン | キャッシュ優先（内容が変わらないため） |
