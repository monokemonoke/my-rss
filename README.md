# KijiYomu

複数の技術系フィードから記事を収集し、AI が各記事の要点を日本語3箇条で要約して HTML カードビューで出力する CLI ツール。

## セットアップ

```bash
pnpm install
pnpm run build
go build -o kijiyomu .
```

dotenvx を使う場合:

```bash
cp .env.example .env  # または手動で作成
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
# AI 要約なし（API 設定不要）
./kijiyomu

# 出力ファイル名を指定
dotenvx run -- ./kijiyomu --out feed.html
```

## オプション一覧

| フラグ | 環境変数 | デフォルト | 説明 |
|---|---|---|---|
| `--api-base` | `AI_API_BASE` | (なし) | OpenAI 互換 API の URL |
| `--api-key` | `AI_API_KEY` | (なし) | API キー |
| `--model` | `AI_MODEL` | `gpt-4o-mini` | モデル名 |
| `--out` | | `kijiyomu.html` | 出力 HTML ファイル名 |
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
go run main.go --data-in kijiyomu-data.json --out kijiyomu.html
```

`--data-in` 指定時はフィード取得・OG 画像取得・AI 要約をすべてスキップします。

## フィードソースの設定（kijiyomu.yaml）

`kijiyomu.yaml` でフィードソースを自由に追加・削除できます。

```yaml
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
- **React 仮想スクロール** — 静的 HTML に記事 JSON を埋め込み、表示範囲のカードだけ描画
- **キーボード操作** — `j`/`k`/`h`/`l` でカード移動、Enter で記事を開く
- **直近記事のみ表示** — 公開日が取れる記事は直近2か月分に絞り込み
- **重複排除** — 同一 URL の記事は複数ソースをまとめて表示

## GitHub Actions による自動実行

`.github/workflows/kijiyomu.yml` で 6 時間ごとに実行し、GitHub Pages へデプロイします。

リポジトリの **Settings → Secrets** に以下を登録してください:

| シークレット名 | 内容 |
|---|---|
| `AI_API_BASE` | API の URL |
| `AI_API_KEY` | API キー |
| `AI_MODEL` | モデル名 |

キャッシュ（AI 要約・OG イメージ）は `actions/cache` で runs をまたいで保持されます。
