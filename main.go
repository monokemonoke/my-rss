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
	Feeds []FeedConfig `yaml:"feeds"`
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
	Title   string   `json:"title"`
	URL     string   `json:"url"`
	Source  string   `json:"source"`
	Score   int      `json:"score,omitempty"`    // points/bookmarks
	OGImage string   `json:"og_image,omitempty"` // og:image URL
	Date    string   `json:"date,omitempty"`     // RFC3339 published date
	Summary []string `json:"summary,omitempty"`  // 3-bullet Japanese summary
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
	OGImage string   `json:"og_image,omitempty"` // og:image URL ("-" = not found)
	Summary []string `json:"summary,omitempty"`  // 3-bullet Japanese summary
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

// ─── Fetchers ─────────────────────────────────────────────────────────────────

const recentArticleMonths = 2

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
	resp, err := http.Get(rawURL)
	if err != nil {
		log.Printf("[WARN] %s: %v", source, err)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

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
	resp, err := http.Get(rawURL)
	if err != nil {
		log.Printf("[WARN] %s: %v", source, err)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

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
	resp, err := http.Get("https://hacker-news.firebaseio.com/v0/topstories.json")
	if err != nil {
		log.Printf("[WARN] HN topstories: %v", err)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var ids []int
	if err := json.NewDecoder(resp.Body).Decode(&ids); err != nil {
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
			r, err := http.Get(rawURL)
			if err != nil {
				return
			}
			defer func() { _ = r.Body.Close() }()
			var story HNStory
			if err := json.NewDecoder(r.Body).Decode(&story); err != nil {
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
	resp, err := http.Get(rawURL)
	if err != nil {
		log.Printf("[WARN] %s: %v", source, err)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

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
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		log.Printf("[WARN] %s: %v", source, err)
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; kijiyomu/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[WARN] %s: %v", source, err)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[WARN] %s: status %s", source, resp.Status)
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
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
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; kijiyomu/1.0)")
	req.Header.Set("Accept", "text/html")

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
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
	resp, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
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

// stripHTML はHTMLタグ・スクリプト・スタイルを除去してプレーンテキストを返す
func stripHTML(s string) string {
	reBlock := regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</\1>`)
	s = reBlock.ReplaceAllString(s, " ")
	reTag := regexp.MustCompile(`<[^>]+>`)
	s = reTag.ReplaceAllString(s, " ")
	s = stdhtml.UnescapeString(s)
	reSpace := regexp.MustCompile(`\s+`)
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

// fetchArticleText は記事ページのプレーンテキストを取得する
func fetchArticleText(rawURL string) string {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; kijiyomu/1.0)")
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	return stripHTML(string(body))
}

const summarizeSystemPrompt = `あなたは記事の要約専門アシスタントです。
与えられた記事の内容を日本語で3点の箇条書きにまとめてください。
必ずJSON配列のみ返すこと。説明文・前置き・コードブロック記法は不要。
例: ["要点1の説明", "要点2の説明", "要点3の説明"]`

// summarizeArticles は全記事の要約を並列生成する（キャッシュ済みはスキップ）
func summarizeArticles(articles []Article, client *openai.Client, model string, cache *Cache) []Article {
	uncached := make([]int, 0, len(articles))
	for i := range articles {
		if e, ok := cache.get(articles[i].URL); ok && len(e.Summary) > 0 {
			articles[i].Summary = e.Summary
		} else {
			uncached = append(uncached, i)
		}
	}
	log.Printf("  summarizing %d articles (cached: %d)", len(uncached), len(articles)-len(uncached))

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
			const maxTextLen = 4000
			if len([]rune(text)) > maxTextLen {
				runes := []rune(text)
				text = string(runes[:maxTextLen])
			}

			prompt := fmt.Sprintf("タイトル: %s\n\n本文:\n%s", a.Title, text)
			content, err := callAI(client, model, summarizeSystemPrompt, prompt)
			if err != nil {
				log.Printf("[WARN] summarize %s: %v", a.URL, err)
				return
			}

			content = strings.TrimSpace(content)
			content = strings.TrimPrefix(content, "```json")
			content = strings.TrimPrefix(content, "```")
			content = strings.TrimSuffix(content, "```")

			var bullets []string
			if err := json.Unmarshal([]byte(content), &bullets); err != nil {
				log.Printf("[WARN] summarize JSON parse %s: %v", a.URL, err)
				return
			}

			a.Summary = bullets
			e, _ := cache.get(a.URL)
			e.Summary = bullets
			cache.set(a.URL, e)
		}(idx)
	}
	wg.Wait()
	return articles
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
		renderData.Date = time.Now().Format("2006-01-02 15:04")
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
		Date            string
		Articles        []Article
		Sources         []string
		ArticlesJSON    template.JS
		CSS             template.CSS
		JS              template.JS
		LogoDataURI     template.URL
		FaviconDataURI  template.URL
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

	if err := os.WriteFile(filepath.Join(dir, "icon-192.png"), faviconPNG, 0644); err != nil {
		return fmt.Errorf("write icon-192.png: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "icon-512.png"), faviconPNG, 0644); err != nil {
		return fmt.Errorf("write icon-512.png: %w", err)
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
			{Src: "icon-192.png", Sizes: "192x192", Type: "image/png"},
			{Src: "icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any maskable"},
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

	var aiClient *openai.Client
	if CLI.APIBase != "" {
		aiClient = newAIClient(CLI.APIBase, CLI.APIKey)
	} else {
		log.Println("[INFO] AI_API_BASE not set — summarization skipped")
	}

	if CLI.DataIn != "" {
		renderData, err := loadRenderData(CLI.DataIn)
		if err != nil {
			log.Fatalf("load intermediate data: %v", err)
		}
		renderData.Articles = filterRecentArticles(renderData.Articles, recentArticleMonths, time.Now())
		if aiClient != nil {
			log.Printf("Summarizing with AI (model: %s)...", CLI.Model)
			renderData.Articles = summarizeArticles(renderData.Articles, aiClient, CLI.Model, cache)
			cache.save()
		}
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
	var feedCfg *Config
	if cfg, err := loadConfig(CLI.Config); err != nil {
		log.Printf("[WARN] config load (%s): %v", CLI.Config, err)
	} else {
		feedCfg = cfg
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
	log.Printf("After dedup: %d articles (removed %d duplicates)", len(allArticles), before-len(allArticles))

	before = len(allArticles)
	allArticles = filterRecentArticles(allArticles, recentArticleMonths, time.Now())
	log.Printf("After recent filter: %d articles (removed %d older than %d months)", len(allArticles), before-len(allArticles), recentArticleMonths)

	// OG image fetch (all articles, cached)
	log.Println("Fetching OG images...")
	allArticles = fetchOGImages(allArticles, cache)

	// AI summarization
	if aiClient != nil {
		log.Printf("Summarizing with AI (model: %s)...", CLI.Model)
		allArticles = summarizeArticles(allArticles, aiClient, CLI.Model, cache)
	}

	cache.save()

	renderData := RenderData{
		SchemaVersion: 1,
		Date:          time.Now().Format("2006-01-02 15:04"),
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
