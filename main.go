package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	stdhtml "html"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	openai "github.com/sashabaranov/go-openai"
	"gopkg.in/yaml.v3"
)

//go:embed templates/main.html
var htmlTmpl string

//go:embed static/dist/app.css
var cssContent string

//go:embed static/dist/app.js
var jsContent string

//go:embed static/logo.png
var logoPNG []byte

//go:embed static/favicon.png
var faviconPNG []byte

// ─── CLI ──────────────────────────────────────────────────────────────────────

var CLI struct {
	APIBase   string `help:"OpenAI-compatible API host URL" env:"AI_API_BASE"`
	APIKey    string `help:"API key (optional)" env:"AI_API_KEY"`
	Model     string `help:"Model name" env:"AI_MODEL" default:"gpt-4o-mini"`
	Out       string `help:"Output HTML file" default:"kijiyomu.html"`
	DataIn    string `help:"Read intermediate JSON and render HTML without fetching"`
	DataOut   string `help:"Write intermediate JSON after fetching"`
	CacheFile string `help:"Cache file" default:".kijiyomu_cache.json"`
	Config    string `help:"Feed config YAML file" default:"kijiyomu.yaml"`
}

// ─── Feed config ───────────────────────────────────────────────────────────────

type FeedConfig struct {
	ID    string `yaml:"id"`
	Name  string `yaml:"name"`
	Type  string `yaml:"type"` // hn, rss, atom, rdf, anthropic
	URL   string `yaml:"url"`
	Limit int    `yaml:"limit"` // for type: hn (default: 50)
}

type Config struct {
	// Profile は読者の興味関心の記述。AI の関連度判定とキーワード概算の両方で使う。
	Profile string       `yaml:"profile"`
	Feeds   []FeedConfig `yaml:"feeds"`
	Tags    []string     `yaml:"tags"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ─── Data types ───────────────────────────────────────────────────────────────

type Article struct {
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Source    string   `json:"source"`
	Score     int      `json:"score,omitempty"`     // points/bookmarks
	OGImage   string   `json:"og_image,omitempty"`  // og:image URL
	Date      string   `json:"date,omitempty"`      // RFC3339 published date
	Summary   []string `json:"summary,omitempty"`   // 3-bullet Japanese summary
	Tags      []string `json:"tags,omitempty"`      // exactly 3 tags from configured vocabulary
	Relevance int      `json:"relevance,omitempty"` // プロフィールへの関連度 0-100
}

type RenderData struct {
	SchemaVersion int       `json:"schema_version"`
	Date          string    `json:"date"`
	Articles      []Article `json:"articles"`
	Sources       []string  `json:"sources,omitempty"`
}

// RSS / Atom

type RSSFeed struct {
	Channel struct {
		Items []RSSItem `xml:"item"`
	} `xml:"channel"`
}

type RSSItem struct {
	Title     string `xml:"title"`
	Link      string `xml:"link"`
	PubDate   string `xml:"pubDate"`
	Date      string `xml:"date"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
}

type AtomFeed struct {
	Entries []AtomEntry `xml:"entry"`
}

type AtomEntry struct {
	Title     string `xml:"title"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
	Link      struct {
		Href string `xml:"href,attr"`
	} `xml:"link"`
}

// RDF/RSS 1.0（はてなブックマーク等）

type RDFFeed struct {
	Items []RDFItem `xml:"item"`
}

type RDFItem struct {
	Title string `xml:"title"`
	Link  string `xml:"link"`
	Date  string `xml:"date"`
}

// HN API

type HNStory struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Score int    `json:"score"`
	Time  int64  `json:"time"`
}

// ─── Cache ────────────────────────────────────────────────────────────────────

type CacheEntry struct {
	OGImage   string   `json:"og_image,omitempty"`  // og:image URL ("-" = not found)
	Summary   []string `json:"summary,omitempty"`   // 3-bullet Japanese summary
	Tags      []string `json:"tags,omitempty"`      // exactly 3 tags from configured vocabulary
	Relevance int      `json:"relevance,omitempty"` // プロフィールへの関連度 0-100（0 = 未評価）
	FailedAt  string   `json:"failed_at,omitempty"` // 直近の AI 呼び出し失敗時刻 (RFC3339)
	SeenAt    string   `json:"seen_at,omitempty"`   // 最後に記事一覧へ現れた時刻 (RFC3339)
}

const (
	// aiRetryInterval は AI 呼び出しに失敗した記事を再試行するまでの間隔。
	// 2 時間ごとの実行で毎回同じ記事に失敗し続けるのを防ぐ。
	aiRetryInterval = 24 * time.Hour
	// cacheEntryTTL は記事一覧から消えたエントリを保持しておく期間。
	// フィードの一時的な取得失敗で有効なキャッシュを捨てないよう猶予を持たせる。
	cacheEntryTTL = 14 * 24 * time.Hour
)

// retryBlocked は直近の失敗から aiRetryInterval 以内かどうかを返す。
func (e CacheEntry) retryBlocked(now time.Time) bool {
	if e.FailedAt == "" {
		return false
	}
	failedAt, err := time.Parse(time.RFC3339, e.FailedAt)
	if err != nil {
		return false
	}
	return now.Sub(failedAt) < aiRetryInterval
}

type Cache struct {
	mu      sync.Mutex
	entries map[string]CacheEntry
	path    string
}

func loadCache(path string) *Cache {
	c := &Cache{entries: make(map[string]CacheEntry), path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	if err := json.Unmarshal(data, &c.entries); err != nil {
		log.Printf("[WARN] cache load: %v", err)
	}
	log.Printf("[INFO] cache loaded: %d entries from %s", len(c.entries), path)
	return c
}

func (c *Cache) save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		log.Printf("[WARN] cache marshal: %v", err)
		return
	}
	if err := os.WriteFile(c.path, data, 0644); err != nil {
		log.Printf("[WARN] cache save: %v", err)
	}
}

func (c *Cache) get(u string) (CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[u]
	return e, ok
}

func (c *Cache) set(u string, entry CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[u] = entry
}

// markFailed は AI 呼び出しの失敗を記録する。以後 aiRetryInterval の間は再試行しない。
func (c *Cache) markFailed(u string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[u]
	e.FailedAt = now.Format(time.RFC3339)
	c.entries[u] = e
}

// touch は今回の実行で扱った記事に最終参照時刻を記録する。prune の判定材料になる。
func (c *Cache) touch(articles []Article, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := now.Format(time.RFC3339)
	for _, a := range articles {
		e := c.entries[a.URL]
		e.SeenAt = stamp
		c.entries[a.URL] = e
	}
}

// prune は最終参照から cacheEntryTTL 以上経過したエントリを削除し、削除件数を返す。
// SeenAt を持たない旧フォーマットのエントリも対象になる。
func (c *Cache) prune(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for u, e := range c.entries {
		seenAt, err := time.Parse(time.RFC3339, e.SeenAt)
		if err != nil || now.Sub(seenAt) > cacheEntryTTL {
			delete(c.entries, u)
			removed++
		}
	}
	return removed
}

// ─── HTTP ─────────────────────────────────────────────────────────────────────

const userAgent = "Mozilla/5.0 (compatible; kijiyomu/1.0)"

const (
	feedFetchTimeout = 20 * time.Second
	pageFetchTimeout = 15 * time.Second
	aiRequestTimeout = 120 * time.Second
)

const (
	feedBodyLimit    = 8 * 1024 * 1024
	newsPageLimit    = 2 * 1024 * 1024
	articleTextLimit = 256 * 1024
	ogImageLimit     = 64 * 1024
)

var (
	// feedClient はフィード API 用。ページ取得より少し長めに待つ。
	feedClient = &http.Client{Timeout: feedFetchTimeout}
	// pageClient は記事ページ・OG イメージ取得用。
	pageClient = &http.Client{Timeout: pageFetchTimeout}
)

// httpGetBody は共通の User-Agent とタイムアウトで GET し、limit バイトまで本文を読む。
// タイムアウトのないデフォルトクライアントを使うと 1 本の応答なしフィードでジョブ全体が
// 止まるため、取得系はすべてこれを経由させる。
func httpGetBody(client *http.Client, rawURL, accept string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// ─── Fetchers ─────────────────────────────────────────────────────────────────

const feedAccept = "application/rss+xml, application/atom+xml, application/xml;q=0.9, */*;q=0.8"

const recentArticleMonths = 2
const requiredArticleTagCount = 3

var defaultArticleTags = []string{
	"LLM/言語モデル",
	"生成AI",
	"ML/機械学習",
	"AIエージェント",
	"研究/論文",
	"開発ツール",
	"プログラミング言語",
	"Web/フロントエンド",
	"バックエンド/API",
	"インフラ/クラウド",
	"データベース",
	"セキュリティ",
	"モバイル",
	"プロダクト/事例",
	"デザイン/UX",
	"その他",
}

var articleTagKeywords = map[string][]string{
	"LLM/言語モデル":   {"llm", "gpt", "openai", "claude", "gemini", "llama", "mistral", "大規模言語モデル", "language model", "chatgpt", "chat model"},
	"生成AI":        {"stable diffusion", "dall-e", "midjourney", "sora", "imagen", "text-to-image", "text-to-video", "multimodal", "マルチモーダル", "画像生成", "動画生成", "音声生成", "generative ai", "生成ai"},
	"ML/機械学習":     {"machine learning", "deep learning", "neural network", "training", "fine-tuning", "dataset", "pytorch", "tensorflow", "huggingface", "inference", "quantization", "機械学習", "深層学習", "学習"},
	"AIエージェント":    {"agent", "エージェント", "mcp", "codex", "claude code", "copilot", "autonomous"},
	"研究/論文":       {"paper", "arxiv", "research", "論文", "研究", "experiment", "benchmark"},
	"開発ツール":       {"tool", "cli", "ide", "editor", "vscode", "github", "devtool", "開発ツール"},
	"プログラミング言語":   {"rust", "go", "golang", "typescript", "python", "java", "言語", "compiler", "コンパイラ"},
	"Web/フロントエンド": {"react", "vue", "frontend", "front-end", "css", "html", "browser", "web", "フロントエンド"},
	"バックエンド/API":  {"api", "backend", "server", "grpc", "openapi", "microservice", "バックエンド"},
	"インフラ/クラウド":   {"cloud", "aws", "gcp", "azure", "kubernetes", "docker", "terraform", "infra", "インフラ"},
	"データベース":      {"database", "db", "sql", "postgres", "mysql", "sqlite", "redis", "データベース"},
	"セキュリティ":      {"security", "auth", "認証", "認可", "脆弱性", "oauth", "暗号", "セキュリティ"},
	"モバイル":        {"ios", "android", "flutter", "swift", "kotlin", "mobile", "モバイル"},
	"プロダクト/事例":    {"case study", "事例", "導入", "product", "プロダクト", "release", "launch"},
	"デザイン/UX":     {"design", "ux", "ui", "figma", "デザイン", "ユーザー体験"},
}

var reArxivQuerySource = regexp.MustCompile(`^ArXiv Query:\s*search_query=all:"([^"]+)".*`)

func displaySourceName(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}

	parts := strings.Split(source, " / ")
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if match := reArxivQuerySource.FindStringSubmatch(part); len(match) == 2 {
			parts[i] = "ArXiv: " + match[1]
		} else {
			parts[i] = part
		}
	}
	return strings.Join(parts, " / ")
}

func normalizeArticleSources(articles []Article) []Article {
	for i := range articles {
		articles[i].Source = displaySourceName(articles[i].Source)
	}
	return articles
}

// ─── Relevance ────────────────────────────────────────────────────────────────

const (
	// neutralRelevance は関連度を判定できなかった記事に与える既定値。
	neutralRelevance = 50
	// relevanceHalfLifeDays は並び替えスコアが半減するまでの日数。
	// 関連度がやや低くても新しい記事が上に来るようにするための減衰。
	relevanceHalfLifeDays = 7.0
	// profileKeywordBonus はプロフィールの語 1 つの一致で加算する点数。
	profileKeywordBonus = 12
	// profileKeywordPenalty は「関心低め」の語が一致したときに引く点数。
	profileKeywordPenalty = 30
)

// profileKeywords はプロフィール記述から取り出した、関連度概算用の語。
type profileKeywords struct {
	positive []string
	negative []string
}

var reProfileLine = regexp.MustCompile(`^\s*[-*]\s*([^:：]+)[:：]\s*(.+)$`)

// splitProfileValues は "Rust, Go" や "ゲーム開発(ブラウザゲーム/Phaser3)" のような
// 記述を個々の語に分解する。
func splitProfileValues(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ',', '、', '/', '・', '(', ')', '（', '）', '「', '」':
			return true
		}
		return false
	})

	values := make([]string, 0, len(fields))
	for _, f := range fields {
		v := strings.ToLower(strings.TrimSpace(f))
		// 1 文字の語はどの記事にも当たってしまうため捨てる
		if len([]rune(v)) < 2 {
			continue
		}
		values = append(values, v)
	}
	return values
}

// parseProfileKeywords は "- 分野: LLM, ゲーム開発" 形式のプロフィールを語に分解する。
// ラベルに「関心低」を含む行はネガティブ側に振り分ける。
func parseProfileKeywords(profile string) profileKeywords {
	var kw profileKeywords
	for _, line := range strings.Split(profile, "\n") {
		m := reProfileLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		label := m[1]
		if strings.Contains(label, "関心低") || strings.Contains(label, "興味なし") || strings.Contains(label, "興味無し") {
			kw.negative = append(kw.negative, splitProfileValues(m[2])...)
			continue
		}
		kw.positive = append(kw.positive, splitProfileValues(m[2])...)
	}
	return kw
}

func (k profileKeywords) empty() bool {
	return len(k.positive) == 0 && len(k.negative) == 0
}

// fallbackRelevance は AI を使わずに、プロフィールの語との重なりで関連度を概算する。
// AI 未設定時と、関連度を持たない古いキャッシュの穴埋めに使う。
func fallbackRelevance(article Article, kw profileKeywords) int {
	if kw.empty() {
		return neutralRelevance
	}

	haystack := strings.ToLower(strings.Join(append([]string{
		article.Title,
		strings.Join(article.Tags, " "),
	}, article.Summary...), " "))

	score := neutralRelevance
	for _, word := range kw.positive {
		if strings.Contains(haystack, word) {
			score += profileKeywordBonus
		}
	}
	for _, word := range kw.negative {
		if strings.Contains(haystack, word) {
			score -= profileKeywordPenalty
		}
	}

	return clampRelevance(score)
}

func clampRelevance(score int) int {
	switch {
	case score < 0:
		return 0
	case score > 100:
		return 100
	default:
		return score
	}
}

// ensureArticleRelevance は関連度が未設定の記事をキーワード概算で埋める。
func ensureArticleRelevance(articles []Article, kw profileKeywords) []Article {
	for i := range articles {
		if articles[i].Relevance == 0 {
			articles[i].Relevance = fallbackRelevance(articles[i], kw)
		}
	}
	return articles
}

// rankScore は関連度を経過日数で減衰させた並び替え用スコアを返す。
func rankScore(a Article, now time.Time) float64 {
	relevance := float64(a.Relevance)
	published, ok := parseArticleDate(a.Date)
	if !ok {
		// 日付不明は減衰の基準が無いので割り引いて中位に置く
		return relevance * 0.5
	}

	ageDays := now.Sub(published).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	return relevance * math.Pow(0.5, ageDays/relevanceHalfLifeDays)
}

// sortArticlesByRank は関連度 × 新しさの順に並べ替える。
func sortArticlesByRank(articles []Article, now time.Time) []Article {
	sort.SliceStable(articles, func(i, j int) bool {
		return rankScore(articles[i], now) > rankScore(articles[j], now)
	})
	return articles
}

func configuredArticleTags(cfg *Config) []string {
	if cfg == nil || len(cfg.Tags) == 0 {
		return append([]string(nil), defaultArticleTags...)
	}

	tags := uniqueTrimmedStrings(cfg.Tags)
	if len(tags) < requiredArticleTagCount {
		log.Printf("[WARN] config tags must contain at least %d entries; using defaults", requiredArticleTagCount)
		return append([]string(nil), defaultArticleTags...)
	}
	return tags
}

func uniqueTrimmedStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func normalizeArticleTags(tags, allowedTags []string) []string {
	allowed := map[string]bool{}
	for _, tag := range allowedTags {
		allowed[tag] = true
	}

	result := make([]string, 0, requiredArticleTagCount)
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !allowed[tag] || seen[tag] {
			continue
		}
		seen[tag] = true
		result = append(result, tag)
		if len(result) == requiredArticleTagCount {
			return result
		}
	}
	return result
}

func completeArticleTags(tags, fallback, allowedTags []string) []string {
	result := normalizeArticleTags(tags, allowedTags)
	seen := map[string]bool{}
	for _, tag := range result {
		seen[tag] = true
	}

	for _, tag := range fallback {
		if len(result) == requiredArticleTagCount {
			return result
		}
		if strings.TrimSpace(tag) == "" || seen[tag] {
			continue
		}
		for _, allowed := range allowedTags {
			if tag == allowed {
				result = append(result, tag)
				seen[tag] = true
				break
			}
		}
	}

	for _, tag := range allowedTags {
		if len(result) == requiredArticleTagCount {
			return result
		}
		if !seen[tag] {
			result = append(result, tag)
			seen[tag] = true
		}
	}
	return result
}

func fallbackArticleTags(article Article, allowedTags []string) []string {
	text := strings.ToLower(strings.Join([]string{
		article.Title,
		article.Source,
		strings.Join(article.Summary, " "),
		article.URL,
	}, " "))

	type scoredTag struct {
		tag   string
		score int
		index int
	}

	scored := make([]scoredTag, 0, len(allowedTags))
	for i, tag := range allowedTags {
		score := 0
		for _, keyword := range articleTagKeywords[tag] {
			if strings.Contains(text, strings.ToLower(keyword)) {
				score++
			}
		}
		scored = append(scored, scoredTag{tag: tag, score: score, index: i})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].index < scored[j].index
	})

	fallback := make([]string, 0, requiredArticleTagCount)
	for _, item := range scored {
		if item.score == 0 {
			continue
		}
		fallback = append(fallback, item.tag)
	}
	fallback = append(fallback, "その他")
	return completeArticleTags(nil, fallback, allowedTags)
}

func ensureArticleTags(articles []Article, allowedTags []string) []Article {
	for i := range articles {
		articles[i].Tags = completeArticleTags(articles[i].Tags, fallbackArticleTags(articles[i], allowedTags), allowedTags)
	}
	return articles
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func formatArticleDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseArticleDate(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 MST",
		"Jan 2, 2006",
		"January 2, 2006",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func articleDate(raw string) string {
	t, ok := parseArticleDate(raw)
	if !ok {
		return ""
	}
	return formatArticleDate(t)
}

func fetchRSS(rawURL, source string) []Article {
	body, err := httpGetBody(feedClient, rawURL, feedAccept, feedBodyLimit)
	if err != nil {
		log.Printf("[WARN] %s: %v", source, err)
		return nil
	}

	var feed RSSFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		log.Printf("[WARN] %s RSS parse: %v", source, err)
		return nil
	}
	var articles []Article
	for _, item := range feed.Channel.Items {
		link := item.Link
		if link == "" {
			continue
		}
		articles = append(articles, Article{
			Title:  strings.TrimSpace(item.Title),
			URL:    strings.TrimSpace(link),
			Source: source,
			Date:   articleDate(firstNonEmpty(item.PubDate, item.Date, item.Published, item.Updated)),
		})
	}
	return articles
}

func fetchAtom(rawURL, source string) []Article {
	body, err := httpGetBody(feedClient, rawURL, feedAccept, feedBodyLimit)
	if err != nil {
		log.Printf("[WARN] %s: %v", source, err)
		return nil
	}

	var feed AtomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		log.Printf("[WARN] %s Atom parse: %v", source, err)
		return nil
	}
	var articles []Article
	for _, e := range feed.Entries {
		articles = append(articles, Article{
			Title:  strings.TrimSpace(e.Title),
			URL:    strings.TrimSpace(e.Link.Href),
			Source: source,
			Date:   articleDate(firstNonEmpty(e.Published, e.Updated)),
		})
	}
	return articles
}

func fetchHN(limit int) []Article {
	body, err := httpGetBody(feedClient, "https://hacker-news.firebaseio.com/v0/topstories.json", "application/json", feedBodyLimit)
	if err != nil {
		log.Printf("[WARN] HN topstories: %v", err)
		return nil
	}
	var ids []int
	if err := json.Unmarshal(body, &ids); err != nil {
		log.Printf("[WARN] HN decode ids: %v", err)
		return nil
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}

	type result struct {
		idx     int
		article Article
	}
	ch := make(chan result, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(idx, id int) {
			defer wg.Done()
			rawURL := fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id)
			itemBody, err := httpGetBody(feedClient, rawURL, "application/json", feedBodyLimit)
			if err != nil {
				return
			}
			var story HNStory
			if err := json.Unmarshal(itemBody, &story); err != nil {
				return
			}
			if story.URL == "" {
				story.URL = fmt.Sprintf("https://news.ycombinator.com/item?id=%d", story.ID)
			}
			ch <- result{idx, Article{
				Title:  story.Title,
				URL:    story.URL,
				Source: "Hacker News",
				Score:  story.Score,
				Date:   formatArticleDate(time.Unix(story.Time, 0)),
			}}
		}(i, id)
	}
	wg.Wait()
	close(ch)

	articles := make([]Article, 0, len(ids))
	for r := range ch {
		articles = append(articles, r.article)
	}
	return articles
}

func fetchRDF(rawURL, source string) []Article {
	body, err := httpGetBody(feedClient, rawURL, feedAccept, feedBodyLimit)
	if err != nil {
		log.Printf("[WARN] %s: %v", source, err)
		return nil
	}

	var feed RDFFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		log.Printf("[WARN] %s RDF parse: %v", source, err)
		return nil
	}
	var articles []Article
	for _, item := range feed.Items {
		if item.Link == "" {
			continue
		}
		articles = append(articles, Article{
			Title:  strings.TrimSpace(item.Title),
			URL:    strings.TrimSpace(item.Link),
			Source: source,
			Date:   articleDate(item.Date),
		})
	}
	return articles
}

func fetchAnthropicNews(rawURL, source string) []Article {
	body, err := httpGetBody(pageClient, rawURL, "text/html", newsPageLimit)
	if err != nil {
		log.Printf("[WARN] %s: %v", source, err)
		return nil
	}
	page := string(body)

	base, err := url.Parse(rawURL)
	if err != nil {
		log.Printf("[WARN] %s URL parse: %v", source, err)
		return nil
	}

	type pattern struct {
		link  *regexp.Regexp
		title *regexp.Regexp
	}
	patterns := []pattern{
		{
			link:  regexp.MustCompile(`<a href="([^"]+)" class="[^"]*PublicationList[^"]*"[^>]*>(.*?)</a>`),
			title: regexp.MustCompile(`<span class="[^"]*title[^"]*">([^<]+)</span>`),
		},
		{
			link:  regexp.MustCompile(`<a href="([^"]+)" class="[^"]*FeaturedGrid[^"]*"[^>]*>(.*?)</a>`),
			title: regexp.MustCompile(`<h[2-4][^>]*>([^<]+)</h[2-4]>`),
		},
	}
	datePattern := regexp.MustCompile(`<time[^>]*>([^<]+)</time>`)

	seen := make(map[string]bool)
	var articles []Article
	for _, p := range patterns {
		for _, match := range p.link.FindAllStringSubmatch(page, -1) {
			href := stdhtml.UnescapeString(strings.TrimSpace(match[1]))
			if !strings.HasPrefix(href, "/news/") && href != "/glasswing" && href != "/81k-interviews" {
				continue
			}
			u, err := url.Parse(href)
			if err != nil {
				continue
			}
			fullURL := base.ResolveReference(u).String()
			if seen[fullURL] {
				continue
			}
			titleMatch := p.title.FindStringSubmatch(match[2])
			if len(titleMatch) < 2 {
				continue
			}
			title := strings.TrimSpace(stdhtml.UnescapeString(titleMatch[1]))
			if title == "" {
				continue
			}
			date := ""
			if dateMatch := datePattern.FindStringSubmatch(match[2]); len(dateMatch) >= 2 {
				date = articleDate(stdhtml.UnescapeString(dateMatch[1]))
			}
			seen[fullURL] = true
			articles = append(articles, Article{
				Title:  title,
				URL:    fullURL,
				Source: source,
				Date:   date,
			})
		}
	}
	return articles
}

// ─── Deduplication ────────────────────────────────────────────────────────────

// normalizeURL はトラッキング系クエリパラメータを除去して正規化する
func normalizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return strings.ToLower(rawURL)
	}
	// utm_* などトラッキング系を除去
	q := u.Query()
	for k := range q {
		kl := strings.ToLower(k)
		if strings.HasPrefix(kl, "utm_") || kl == "ref" || kl == "from" || kl == "source" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	result := u.String()
	return strings.TrimRight(result, "/")
}

// deduplicateArticles は同じ URL の記事をまとめ、ソース名を結合する
func deduplicateArticles(articles []Article) []Article {
	type group struct {
		article Article
		sources []string
	}
	seen := make(map[string]int) // normalizedURL → index in groups
	groups := make([]group, 0, len(articles))

	for _, a := range articles {
		key := normalizeURL(a.URL)
		if idx, ok := seen[key]; ok {
			// 既存グループにソースを追加、スコアは高い方を採用
			g := &groups[idx]
			// 重複しないソースのみ追加
			alreadyHas := false
			for _, s := range g.sources {
				if s == a.Source {
					alreadyHas = true
					break
				}
			}
			if !alreadyHas {
				g.sources = append(g.sources, a.Source)
			}
			if a.Score > g.article.Score {
				g.article.Score = a.Score
			}
			if g.article.Date == "" {
				g.article.Date = a.Date
			}
		} else {
			seen[key] = len(groups)
			groups = append(groups, group{article: a, sources: []string{a.Source}})
		}
	}

	result := make([]Article, len(groups))
	for i, g := range groups {
		a := g.article
		a.Source = strings.Join(g.sources, " / ")
		result[i] = a
	}
	return result
}

func filterRecentArticles(articles []Article, months int, now time.Time) []Article {
	cutoff := now.AddDate(0, -months, 0)
	filtered := articles[:0]
	for _, a := range articles {
		if a.Date == "" {
			filtered = append(filtered, a)
			continue
		}
		published, ok := parseArticleDate(a.Date)
		if !ok || !published.Before(cutoff) {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// ─── OG image ─────────────────────────────────────────────────────────────────

// fetchOGImage はページの og:image URL を返す。見つからない場合は空文字。
func fetchOGImage(rawURL string) string {
	body, err := httpGetBody(pageClient, rawURL, "text/html", ogImageLimit)
	if err != nil {
		return ""
	}
	lower := strings.ToLower(string(body))

	// <meta property="og:image" content="..."> を探す（順不同属性に対応）
	searchFor := []string{`property="og:image"`, `property='og:image'`}
	for _, prop := range searchFor {
		idx := strings.Index(lower, prop)
		if idx < 0 {
			continue
		}
		// タグの開始 < を逆方向に探す
		tagStart := strings.LastIndex(lower[:idx], "<")
		if tagStart < 0 {
			continue
		}
		// タグの終了 > を探す
		tagEnd := strings.Index(lower[tagStart:], ">")
		if tagEnd < 0 {
			continue
		}
		tag := string(body[tagStart : tagStart+tagEnd+1])
		tagLower := strings.ToLower(tag)
		ci := strings.Index(tagLower, "content=")
		if ci < 0 {
			continue
		}
		after := tag[ci+8:]
		if len(after) == 0 {
			continue
		}
		quote := after[0]
		if quote != '"' && quote != '\'' {
			continue
		}
		end := strings.IndexByte(after[1:], quote)
		if end < 0 {
			continue
		}
		return strings.TrimSpace(after[1 : end+1])
	}
	return ""
}

// fetchOGImages は全記事の og:image を並列取得する
func fetchOGImages(articles []Article, cache *Cache) []Article {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for i := range articles {
		// キャッシュ確認
		if e, ok := cache.get(articles[i].URL); ok && e.OGImage != "" {
			if e.OGImage != "-" {
				articles[i].OGImage = e.OGImage
			}
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			a := &articles[idx]
			img := fetchOGImage(a.URL)
			sentinel := img
			if sentinel == "" {
				sentinel = "-" // 「取得済みだが画像なし」を記録
			}
			a.OGImage = img

			e, _ := cache.get(a.URL)
			e.OGImage = sentinel
			cache.set(a.URL, e)
		}(i)
	}
	wg.Wait()
	return articles
}

// ─── AI client ────────────────────────────────────────────────────────────────

func cleanAPIBase(apiBase string) string {
	if u, err := url.Parse(apiBase); err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return apiBase
}

func newAIClient(apiBase, apiKey string) *openai.Client {
	cfg := openai.DefaultConfig(apiKey)
	if apiBase != "" {
		base := cleanAPIBase(apiBase)
		cfg.BaseURL = base + "/v1"
		log.Printf("[INFO] AI base URL: %s", cfg.BaseURL)
	}
	return openai.NewClientWithConfig(cfg)
}

func callAI(client *openai.Client, model, system, userMsg string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiRequestTimeout)
	defer cancel()

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: system},
			{Role: openai.ChatMessageRoleUser, Content: userMsg},
		},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty response")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// ─── AI summarization ─────────────────────────────────────────────────────────

var (
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reTag    = regexp.MustCompile(`<[^>]+>`)
	reSpace  = regexp.MustCompile(`\s+`)
)

// stripHTML はHTMLタグ・スクリプト・スタイルを除去してプレーンテキストを返す
func stripHTML(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reTag.ReplaceAllString(s, " ")
	s = stdhtml.UnescapeString(s)
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

func arxivHTMLURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "arxiv.org" && host != "www.arxiv.org" {
		return "", false
	}
	if !strings.HasPrefix(u.Path, "/pdf/") {
		return "", false
	}

	id := strings.TrimPrefix(u.Path, "/pdf/")
	id = strings.TrimSuffix(id, ".pdf")
	if strings.TrimSpace(id) == "" {
		return "", false
	}

	u.Host = "arxiv.org"
	u.Path = "/html/" + id
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), true
}

func articleTextURLs(rawURL string) []string {
	if htmlURL, ok := arxivHTMLURL(rawURL); ok {
		return []string{htmlURL, rawURL}
	}
	return []string{rawURL}
}

func fetchPlainText(rawURL string) string {
	body, err := httpGetBody(pageClient, rawURL, "text/html", articleTextLimit)
	if err != nil {
		return ""
	}
	return stripHTML(string(body))
}

// fetchArticleText は記事ページのプレーンテキストを取得する
func fetchArticleText(rawURL string) string {
	for _, candidateURL := range articleTextURLs(rawURL) {
		text := fetchPlainText(candidateURL)
		if text != "" {
			return text
		}
	}
	return ""
}

const enrichSystemPrompt = `あなたは技術記事のキュレーションアシスタントです。
与えられた記事について次の3つを判定し、JSONオブジェクトのみを返してください。

- summary: 記事の要点を日本語で3点にまとめた文字列の配列
- tags: 提示された候補タグから重複なしで選んだちょうど3つの文字列の配列
- relevance: 読者プロフィールへの関連度を表す0〜100の整数

relevance は読者プロフィールとの一致度です。プロフィールの言語・分野・製品に
強く重なる記事は高く、「関心低め」に該当する記事は低くしてください。
判断材料が乏しい場合は50前後にしてください。

説明文・前置き・コードブロック記法は不要。
例: {"summary":["要点1","要点2","要点3"],"tags":["AI/LLM","開発ツール","研究/論文"],"relevance":72}`

// articleEnrichment は enrichSystemPrompt に対する AI の応答。
type articleEnrichment struct {
	Summary   []string `json:"summary"`
	Tags      []string `json:"tags"`
	Relevance int      `json:"relevance"`
}

const maxArticleTextLen = 4000

func buildEnrichPrompt(article Article, allowedTags []string, profile, text string) string {
	var b strings.Builder
	if strings.TrimSpace(profile) != "" {
		fmt.Fprintf(&b, "読者プロフィール:\n%s\n\n", strings.TrimSpace(profile))
	}
	fmt.Fprintf(&b, "候補タグ:\n- %s\n\n", strings.Join(allowedTags, "\n- "))
	fmt.Fprintf(&b, "タイトル: %s\nソース: %s\nURL: %s\n", article.Title, article.Source, article.URL)
	if text != "" {
		fmt.Fprintf(&b, "\n本文:\n%s", text)
	}
	return b.String()
}

// trimAIJSONFence はコードブロック記法で包まれた応答から中身を取り出す。
func trimAIJSONFence(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}

func parseArticleEnrichment(content string) (articleEnrichment, error) {
	var result articleEnrichment
	if err := json.Unmarshal([]byte(trimAIJSONFence(content)), &result); err != nil {
		return articleEnrichment{}, err
	}
	return result, nil
}

// enrichArticles は要約・タグ・関連度を 1 回の AI 呼び出しでまとめて求める。
// 記事ごとに要約とタグで 2 回呼んでいたものを 1 回に減らしている。
func enrichArticles(articles []Article, client *openai.Client, model string, cache *Cache, allowedTags []string, profile string, kw profileKeywords) []Article {
	now := time.Now()
	uncached := make([]int, 0, len(articles))
	cached, blocked := 0, 0

	for i := range articles {
		entry, ok := cache.get(articles[i].URL)
		if ok && applyCachedEnrichment(&articles[i], entry, allowedTags, kw) {
			cached++
			continue
		}
		if ok && entry.retryBlocked(now) {
			// 直近で失敗しているので、間隔を空けるまで API を呼ばずキーワード推定で埋める
			applyFallbackEnrichment(&articles[i], allowedTags, kw)
			blocked++
			continue
		}
		uncached = append(uncached, i)
	}
	log.Printf("  enriching %d articles (cached: %d, recently failed: %d)", len(uncached), cached, blocked)

	var wg sync.WaitGroup
	sem := make(chan struct{}, 3) // AI API への同時リクエスト数を制限

	for _, idx := range uncached {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			a := &articles[i]
			text := fetchArticleText(a.URL)
			if runes := []rune(text); len(runes) > maxArticleTextLen {
				text = string(runes[:maxArticleTextLen])
			}

			prompt := buildEnrichPrompt(*a, allowedTags, profile, text)
			content, err := callAI(client, model, enrichSystemPrompt, prompt)
			if err != nil {
				log.Printf("[WARN] enrich %s: %v", a.URL, err)
				applyFallbackEnrichment(a, allowedTags, kw)
				cache.markFailed(a.URL, time.Now())
				return
			}

			result, err := parseArticleEnrichment(content)
			if err != nil {
				log.Printf("[WARN] enrich JSON parse %s: %v", a.URL, err)
				applyFallbackEnrichment(a, allowedTags, kw)
				cache.markFailed(a.URL, time.Now())
				return
			}

			a.Summary = result.Summary
			a.Tags = completeArticleTags(result.Tags, fallbackArticleTags(*a, allowedTags), allowedTags)
			a.Relevance = clampRelevance(result.Relevance)
			if a.Relevance == 0 {
				a.Relevance = fallbackRelevance(*a, kw)
			}

			e, _ := cache.get(a.URL)
			e.Summary = a.Summary
			e.Tags = a.Tags
			e.Relevance = a.Relevance
			e.FailedAt = ""
			cache.set(a.URL, e)
		}(idx)
	}
	wg.Wait()
	return articles
}

// applyCachedEnrichment はキャッシュ済みの要約・タグを記事へ写す。
// 揃っていなければ false を返して AI 呼び出しに回す。
// 関連度だけが欠けている場合は、関連度導入前のキャッシュとみなして概算で埋める。
func applyCachedEnrichment(article *Article, entry CacheEntry, allowedTags []string, kw profileKeywords) bool {
	if len(entry.Summary) == 0 {
		return false
	}
	tags := normalizeArticleTags(entry.Tags, allowedTags)
	if len(tags) != requiredArticleTagCount {
		return false
	}

	article.Summary = entry.Summary
	article.Tags = tags
	article.Relevance = entry.Relevance
	if article.Relevance == 0 {
		article.Relevance = fallbackRelevance(*article, kw)
	}
	return true
}

// applyFallbackEnrichment は AI を使えないときにタグと関連度だけでも埋める。
func applyFallbackEnrichment(article *Article, allowedTags []string, kw profileKeywords) {
	if len(normalizeArticleTags(article.Tags, allowedTags)) != requiredArticleTagCount {
		article.Tags = fallbackArticleTags(*article, allowedTags)
	}
	if article.Relevance == 0 {
		article.Relevance = fallbackRelevance(*article, kw)
	}
}

// ─── HTML output ──────────────────────────────────────────────────────────────

func collectSources(articles []Article) []string {
	sourceSet := map[string]bool{}
	for _, a := range articles {
		sourceSet[a.Source] = true
	}
	var sources []string
	for s := range sourceSet {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	return sources
}

func loadRenderData(path string) (*RenderData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var renderData RenderData
	if err := json.Unmarshal(data, &renderData); err != nil {
		return nil, err
	}
	if renderData.Sources == nil {
		renderData.Sources = collectSources(renderData.Articles)
	}
	if renderData.Date == "" {
		jst := time.FixedZone("JST", 9*60*60)
		renderData.Date = time.Now().In(jst).Format("2006-01-02 15:04")
	}
	return &renderData, nil
}

func saveRenderData(path string, renderData RenderData) error {
	data, err := json.MarshalIndent(renderData, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func renderHTML(path string, renderData RenderData) error {
	funcMap := template.FuncMap{
		"dateLabel": dateLabel,
	}
	tmpl := template.Must(template.New("feed").Funcs(funcMap).Parse(htmlTmpl))
	articlesJSON, err := json.Marshal(renderData.Articles)
	if err != nil {
		return err
	}

	data := struct {
		Date           string
		Articles       []Article
		Sources        []string
		ArticlesJSON   template.JS
		CSS            template.CSS
		JS             template.JS
		LogoDataURI    template.URL
		FaviconDataURI template.URL
	}{
		Date:           renderData.Date,
		Articles:       renderData.Articles,
		Sources:        renderData.Sources,
		ArticlesJSON:   template.JS(articlesJSON),
		CSS:            template.CSS(cssContent),
		JS:             template.JS(jsContent),
		LogoDataURI:    template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(logoPNG)),
		FaviconDataURI: template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(faviconPNG)),
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return tmpl.Execute(f, data)
}

// ─── PWA files ────────────────────────────────────────────────────────────────

const swJS = `const CACHE = 'kijiyomu-v1';

self.addEventListener('install', () => self.skipWaiting());

self.addEventListener('activate', e => {
  e.waitUntil(
    caches.keys()
      .then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', e => {
  if (e.request.mode !== 'navigate') return;
  e.respondWith(
    fetch(e.request)
      .then(res => {
        const copy = res.clone();
        caches.open(CACHE).then(c => c.put(e.request, copy));
        return res;
      })
      .catch(() => caches.match(e.request))
  );
});
`

func writePWAFiles(outHTMLPath string) error {
	dir := filepath.Dir(outHTMLPath)
	htmlName := filepath.Base(outHTMLPath)
	iconDir := filepath.Join(dir, "static")
	if err := os.MkdirAll(iconDir, 0755); err != nil {
		return fmt.Errorf("create static dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(iconDir, "icon-192.png"), faviconPNG, 0644); err != nil {
		return fmt.Errorf("write static/icon-192.png: %w", err)
	}
	if err := os.WriteFile(filepath.Join(iconDir, "icon-512.png"), faviconPNG, 0644); err != nil {
		return fmt.Errorf("write static/icon-512.png: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apple-touch-icon.png"), faviconPNG, 0644); err != nil {
		return fmt.Errorf("write apple-touch-icon.png: %w", err)
	}

	type icon struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose,omitempty"`
	}
	manifest := struct {
		Name            string `json:"name"`
		ShortName       string `json:"short_name"`
		Description     string `json:"description"`
		StartURL        string `json:"start_url"`
		Display         string `json:"display"`
		BackgroundColor string `json:"background_color"`
		ThemeColor      string `json:"theme_color"`
		Icons           []icon `json:"icons"`
	}{
		Name:            "KijiYomu",
		ShortName:       "KijiYomu",
		Description:     "AIキュレーションRSSリーダー",
		StartURL:        "./" + htmlName,
		Display:         "standalone",
		BackgroundColor: "#F8F8FB",
		ThemeColor:      "#F8F8FB",
		Icons: []icon{
			{Src: "static/icon-192.png", Sizes: "192x192", Type: "image/png"},
			{Src: "static/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any maskable"},
		},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestJSON, 0644); err != nil {
		return fmt.Errorf("write manifest.json: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "sw.js"), []byte(swJS), 0644); err != nil {
		return fmt.Errorf("write sw.js: %w", err)
	}

	return nil
}

// ─── Template helpers ─────────────────────────────────────────────────────────

func dateLabel(raw string) string {
	t, ok := parseArticleDate(raw)
	if !ok {
		return strings.TrimSpace(raw)
	}
	return t.In(time.Local).Format("2006-01-02")
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	kong.Parse(&CLI)

	cache := loadCache(CLI.CacheFile)
	var feedCfg *Config
	if cfg, err := loadConfig(CLI.Config); err != nil {
		log.Printf("[WARN] config load (%s): %v", CLI.Config, err)
	} else {
		feedCfg = cfg
	}
	allowedTags := configuredArticleTags(feedCfg)
	profile := ""
	if feedCfg != nil {
		profile = feedCfg.Profile
	}
	profileWords := parseProfileKeywords(profile)

	var aiClient *openai.Client
	if CLI.APIBase != "" {
		aiClient = newAIClient(CLI.APIBase, CLI.APIKey)
	} else {
		log.Println("[INFO] AI_API_BASE not set — AI summarization/tagging skipped")
	}

	if CLI.DataIn != "" {
		renderData, err := loadRenderData(CLI.DataIn)
		if err != nil {
			log.Fatalf("load intermediate data: %v", err)
		}
		now := time.Now()
		renderData.SchemaVersion = 2
		renderData.Articles = normalizeArticleSources(renderData.Articles)
		renderData.Articles = filterRecentArticles(renderData.Articles, recentArticleMonths, now)
		renderData.Articles = ensureArticleTags(renderData.Articles, allowedTags)
		renderData.Articles = ensureArticleRelevance(renderData.Articles, profileWords)
		renderData.Articles = sortArticlesByRank(renderData.Articles, now)
		renderData.Sources = collectSources(renderData.Articles)
		if CLI.DataOut != "" {
			if err := saveRenderData(CLI.DataOut, *renderData); err != nil {
				log.Fatalf("write intermediate data: %v", err)
			}
			log.Printf("Written data: %s (%d articles)", CLI.DataOut, len(renderData.Articles))
		}
		if err := renderHTML(CLI.Out, *renderData); err != nil {
			log.Fatalf("render HTML: %v", err)
		}
		if err := writePWAFiles(CLI.Out); err != nil {
			log.Printf("[WARN] PWA files: %v", err)
		}
		log.Printf("Written: %s (%d articles)", CLI.Out, len(renderData.Articles))
		return
	}

	log.Println("Fetching articles...")

	type fetchJob struct {
		name string
		fn   func() []Article
	}

	var jobs []fetchJob
	if feedCfg != nil {
		for _, f := range feedCfg.Feeds {
			limit := f.Limit
			if limit == 0 {
				limit = 50
			}
			var fn func() []Article
			switch f.Type {
			case "hn":
				lim := limit
				fn = func() []Article { return fetchHN(lim) }
			case "rss":
				fn = func() []Article { return fetchRSS(f.URL, f.Name) }
			case "atom":
				fn = func() []Article { return fetchAtom(f.URL, f.Name) }
			case "rdf":
				fn = func() []Article { return fetchRDF(f.URL, f.Name) }
			case "anthropic":
				fn = func() []Article { return fetchAnthropicNews(f.URL, f.Name) }
			default:
				log.Printf("[WARN] unknown feed type %q for %q", f.Type, f.Name)
				continue
			}
			jobs = append(jobs, fetchJob{name: f.Name, fn: fn})
		}
	}

	var mu sync.Mutex
	var allArticles []Article
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func(j fetchJob) {
			defer wg.Done()
			arts := j.fn()
			log.Printf("  [%s] %d articles", j.name, len(arts))
			mu.Lock()
			allArticles = append(allArticles, arts...)
			mu.Unlock()
		}(job)
	}
	wg.Wait()
	log.Printf("Total fetched: %d articles", len(allArticles))

	// 同一URL記事を統合
	before := len(allArticles)
	allArticles = deduplicateArticles(allArticles)
	allArticles = normalizeArticleSources(allArticles)
	log.Printf("After dedup: %d articles (removed %d duplicates)", len(allArticles), before-len(allArticles))

	before = len(allArticles)
	allArticles = filterRecentArticles(allArticles, recentArticleMonths, time.Now())
	log.Printf("After recent filter: %d articles (removed %d older than %d months)", len(allArticles), before-len(allArticles), recentArticleMonths)

	// OG image fetch (all articles, cached)
	log.Println("Fetching OG images...")
	allArticles = fetchOGImages(allArticles, cache)

	// AI enrichment: 要約・タグ・関連度をまとめて 1 回で求める
	if aiClient != nil {
		log.Printf("Enriching with AI (model: %s)...", CLI.Model)
		allArticles = enrichArticles(allArticles, aiClient, CLI.Model, cache, allowedTags, profile, profileWords)
	} else {
		allArticles = ensureArticleTags(allArticles, allowedTags)
	}
	allArticles = ensureArticleRelevance(allArticles, profileWords)
	allArticles = sortArticlesByRank(allArticles, time.Now())

	cacheNow := time.Now()
	cache.touch(allArticles, cacheNow)
	if removed := cache.prune(cacheNow); removed > 0 {
		log.Printf("Cache: pruned %d stale entries", removed)
	}
	cache.save()

	renderData := RenderData{
		SchemaVersion: 2,
		Date:          time.Now().In(time.FixedZone("JST", 9*60*60)).Format("2006-01-02 15:04"),
		Articles:      allArticles,
		Sources:       collectSources(allArticles),
	}
	if CLI.DataOut != "" {
		if err := saveRenderData(CLI.DataOut, renderData); err != nil {
			log.Fatalf("write intermediate data: %v", err)
		}
		log.Printf("Written data: %s (%d articles)", CLI.DataOut, len(renderData.Articles))
	}
	if err := renderHTML(CLI.Out, renderData); err != nil {
		log.Fatalf("render HTML: %v", err)
	}
	if err := writePWAFiles(CLI.Out); err != nil {
		log.Printf("[WARN] PWA files: %v", err)
	}
	log.Printf("Written: %s (%d articles)", CLI.Out, len(allArticles))
}
