package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPGetBodySendsUserAgentAndTruncatesAtLimit(t *testing.T) {
	var gotUA, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	body, err := httpGetBody(srv.Client(), srv.URL, "text/html", 4)
	if err != nil {
		t.Fatalf("httpGetBody: %v", err)
	}
	if string(body) != "0123" {
		t.Fatalf("body = %q, want 0123", body)
	}
	if gotUA != userAgent {
		t.Fatalf("User-Agent = %q, want %q", gotUA, userAgent)
	}
	if gotAccept != "text/html" {
		t.Fatalf("Accept = %q, want text/html", gotAccept)
	}
}

func TestHTTPGetBodyFailsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := httpGetBody(srv.Client(), srv.URL, "", 1024); err == nil {
		t.Fatal("httpGetBody returned nil error for 404")
	}
}

func TestHTTPGetBodyRespectsClientTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	client := &http.Client{Timeout: 30 * time.Millisecond}
	if _, err := httpGetBody(client, srv.URL, "", 1024); err == nil {
		t.Fatal("httpGetBody returned nil error for a hanging server")
	}
}

const testProfile = `- 言語/技術: Rust, Go, TypeScript
- 分野: LLM/AIエージェント, ゲーム開発(ブラウザゲーム/Phaser3)
- 関心低め: スポーツ`

func TestParseProfileKeywordsSplitsPositiveAndNegative(t *testing.T) {
	kw := parseProfileKeywords(testProfile)

	wantPositive := []string{"rust", "go", "typescript", "llm", "aiエージェント", "ゲーム開発", "ブラウザゲーム", "phaser3"}
	if len(kw.positive) != len(wantPositive) {
		t.Fatalf("positive = %#v, want %#v", kw.positive, wantPositive)
	}
	for i := range wantPositive {
		if kw.positive[i] != wantPositive[i] {
			t.Fatalf("positive = %#v, want %#v", kw.positive, wantPositive)
		}
	}
	if len(kw.negative) != 1 || kw.negative[0] != "スポーツ" {
		t.Fatalf("negative = %#v, want [スポーツ]", kw.negative)
	}
}

func TestParseProfileKeywordsOnEmptyProfile(t *testing.T) {
	if kw := parseProfileKeywords(""); !kw.empty() {
		t.Fatalf("parseProfileKeywords(\"\") = %#v, want empty", kw)
	}
}

func TestFallbackRelevanceScoresMatchesAndPenalisesDislikes(t *testing.T) {
	kw := parseProfileKeywords(testProfile)

	matching := fallbackRelevance(Article{
		Title:   "RustでLLMエージェントを書く",
		Tags:    []string{"AIエージェント", "プログラミング言語", "開発ツール"},
		Summary: []string{"Rust で書かれたエージェント基盤の紹介"},
	}, kw)
	if matching <= neutralRelevance {
		t.Fatalf("relevance = %d, want > %d for a matching article", matching, neutralRelevance)
	}

	disliked := fallbackRelevance(Article{
		Title: "スポーツ観戦アプリの作り方",
		Tags:  []string{"モバイル", "プロダクト/事例", "その他"},
	}, kw)
	if disliked >= neutralRelevance {
		t.Fatalf("relevance = %d, want < %d for a disliked article", disliked, neutralRelevance)
	}

	unrelated := fallbackRelevance(Article{Title: "確定申告の話"}, kw)
	if unrelated != neutralRelevance {
		t.Fatalf("relevance = %d, want %d for an unrelated article", unrelated, neutralRelevance)
	}
}

func TestFallbackRelevanceIsNeutralWithoutProfile(t *testing.T) {
	got := fallbackRelevance(Article{Title: "Rust"}, profileKeywords{})
	if got != neutralRelevance {
		t.Fatalf("relevance = %d, want %d", got, neutralRelevance)
	}
}

func TestSortArticlesByRankPrefersRelevantAndRecent(t *testing.T) {
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	articles := []Article{
		{Title: "古くて関連度が高い", Date: "2026-07-04T00:00:00Z", Relevance: 90},
		{Title: "新しくて関連度が中くらい", Date: "2026-07-31T00:00:00Z", Relevance: 60},
		{Title: "新しいが関連度が低い", Date: "2026-08-01T00:00:00Z", Relevance: 10},
	}

	got := sortArticlesByRank(articles, now)
	want := []string{"新しくて関連度が中くらい", "新しいが関連度が低い", "古くて関連度が高い"}
	for i := range want {
		if got[i].Title != want[i] {
			t.Fatalf("order = [%s, %s, %s], want %v", got[0].Title, got[1].Title, got[2].Title, want)
		}
	}
}

func TestParseArticleEnrichmentAcceptsFencedJSON(t *testing.T) {
	got, err := parseArticleEnrichment("```json\n{\"summary\":[\"a\",\"b\",\"c\"],\"tags\":[\"LLM/言語モデル\"],\"relevance\":72}\n```")
	if err != nil {
		t.Fatalf("parseArticleEnrichment: %v", err)
	}
	if len(got.Summary) != 3 || got.Relevance != 72 || len(got.Tags) != 1 {
		t.Fatalf("parsed = %#v", got)
	}
}

func TestBuildEnrichPromptIncludesProfileAndTags(t *testing.T) {
	prompt := buildEnrichPrompt(
		Article{Title: "記事タイトル", Source: "Zenn", URL: "https://example.com/a"},
		[]string{"LLM/言語モデル", "開発ツール"},
		testProfile,
		"本文テキスト",
	)
	for _, want := range []string{"読者プロフィール", "関心低め", "LLM/言語モデル", "記事タイトル", "本文テキスト"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt does not contain %q:\n%s", want, prompt)
		}
	}
}

func TestApplyCachedEnrichmentFillsMissingRelevance(t *testing.T) {
	kw := parseProfileKeywords(testProfile)
	article := Article{Title: "Rust で LLM エージェント"}
	entry := CacheEntry{
		Summary: []string{"要点"},
		Tags:    []string{"LLM/言語モデル", "開発ツール", "研究/論文"},
	}

	if !applyCachedEnrichment(&article, entry, defaultArticleTags, kw) {
		t.Fatal("applyCachedEnrichment returned false for a complete entry")
	}
	if article.Relevance == 0 {
		t.Fatal("relevance was left unset for a pre-relevance cache entry")
	}
}

func TestApplyCachedEnrichmentRejectsIncompleteEntry(t *testing.T) {
	article := Article{Title: "記事"}
	entry := CacheEntry{Summary: []string{"要点"}, Tags: []string{"LLM/言語モデル"}}

	if applyCachedEnrichment(&article, entry, defaultArticleTags, profileKeywords{}) {
		t.Fatal("applyCachedEnrichment accepted an entry with fewer than 3 tags")
	}
}

func TestCacheEntryRetryBlockedOnlyWithinInterval(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		entry CacheEntry
		want  bool
	}{
		{"失敗記録なし", CacheEntry{}, false},
		{"直近の失敗", CacheEntry{FailedAt: now.Add(-time.Hour).Format(time.RFC3339)}, true},
		{"間隔を過ぎた失敗", CacheEntry{FailedAt: now.Add(-48 * time.Hour).Format(time.RFC3339)}, false},
		{"壊れた時刻", CacheEntry{FailedAt: "not-a-time"}, false},
	}
	for _, tc := range cases {
		if got := tc.entry.retryBlocked(now); got != tc.want {
			t.Fatalf("%s: retryBlocked = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCachePruneDropsEntriesUnseenBeyondTTL(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	c := &Cache{entries: map[string]CacheEntry{
		"https://example.com/fresh":  {SeenAt: now.Add(-24 * time.Hour).Format(time.RFC3339)},
		"https://example.com/stale":  {SeenAt: now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)},
		"https://example.com/legacy": {Summary: []string{"SeenAt を持たない旧フォーマット"}},
	}}

	if removed := c.prune(now); removed != 2 {
		t.Fatalf("prune removed %d entries, want 2", removed)
	}
	if _, ok := c.entries["https://example.com/fresh"]; !ok {
		t.Fatal("prune dropped a recently seen entry")
	}
}

func TestCacheTouchProtectsEntryFromPrune(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	const url = "https://example.com/legacy"
	c := &Cache{entries: map[string]CacheEntry{url: {Summary: []string{"要約"}}}}

	c.touch([]Article{{URL: url}}, now)

	if removed := c.prune(now); removed != 0 {
		t.Fatalf("prune removed %d entries after touch, want 0", removed)
	}
	if got := c.entries[url].Summary; len(got) != 1 {
		t.Fatalf("touch clobbered the entry payload: %#v", c.entries[url])
	}
}

func TestArticleDateParsesCommonFeedFormats(t *testing.T) {
	cases := []string{
		"Thu, 23 Apr 2026 11:00:00 GMT",
		"2026-04-23T11:00:00Z",
		"Apr 23, 2026",
	}
	for _, tc := range cases {
		if got := articleDate(tc); got == "" {
			t.Fatalf("articleDate(%q) returned empty", tc)
		}
	}
}

func TestFilterRecentArticlesKeepsOnlyRecentDatedArticles(t *testing.T) {
	now := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	articles := []Article{
		{Title: "recent", Date: "2026-04-01T00:00:00Z"},
		{Title: "old", Date: "2026-01-01T00:00:00Z"},
		{Title: "undated"},
	}

	got := filterRecentArticles(articles, 2, now)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Title != "recent" || got[1].Title != "undated" {
		t.Fatalf("titles = %q, %q; want recent, undated", got[0].Title, got[1].Title)
	}
}

func TestDateLabelFormatsArticleDate(t *testing.T) {
	if got := dateLabel("2026-04-23T11:00:00Z"); got != "2026-04-23" {
		t.Fatalf("dateLabel = %q, want 2026-04-23", got)
	}
}

func TestConfiguredArticleTagsUsesDefaultsForMissingConfig(t *testing.T) {
	got := configuredArticleTags(nil)
	if len(got) < requiredArticleTagCount {
		t.Fatalf("len = %d, want at least %d", len(got), requiredArticleTagCount)
	}
}

func TestCompleteArticleTagsKeepsAllowedUniqueTagsAndFillsToThree(t *testing.T) {
	allowed := []string{"AI/LLM", "開発ツール", "研究/論文", "その他"}
	got := completeArticleTags(
		[]string{"AI/LLM", "AI/LLM", "unknown"},
		[]string{"開発ツール", "その他"},
		allowed,
	)
	want := []string{"AI/LLM", "開発ツール", "その他"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %#v, want %#v", got, want)
		}
	}
}

func TestFallbackArticleTagsReturnsExactlyThreeTags(t *testing.T) {
	allowed := defaultArticleTags
	article := Article{
		Title:  "RustでLLM agent CLIを作る",
		Source: "Zenn",
		Summary: []string{
			"RustでAIエージェントを実装する開発ツールの記事",
		},
	}

	got := fallbackArticleTags(article, allowed)
	if len(got) != requiredArticleTagCount {
		t.Fatalf("len = %d, want %d: %#v", len(got), requiredArticleTagCount, got)
	}
}

func TestDisplaySourceNameShortensArxivQuerySource(t *testing.T) {
	source := `ArXiv Query: search_query=all:"AI agent"&id_list=&start=0&max_results=10`
	if got := displaySourceName(source); got != "ArXiv: AI agent" {
		t.Fatalf("displaySourceName = %q, want ArXiv: AI agent", got)
	}
}

func TestDisplaySourceNameShortensArxivQuerySourceInJoinedSources(t *testing.T) {
	source := `Zenn / ArXiv Query: search_query=all:"AI agent"&id_list=&start=0&max_results=10`
	if got := displaySourceName(source); got != "Zenn / ArXiv: AI agent" {
		t.Fatalf("displaySourceName = %q, want Zenn / ArXiv: AI agent", got)
	}
}

func TestArxivHTMLURLConvertsPDFURL(t *testing.T) {
	got, ok := arxivHTMLURL("https://arxiv.org/pdf/2401.12345v2.pdf?download=1")
	if !ok {
		t.Fatal("arxivHTMLURL returned ok=false")
	}
	if got != "https://arxiv.org/html/2401.12345v2" {
		t.Fatalf("arxivHTMLURL = %q, want https://arxiv.org/html/2401.12345v2", got)
	}
}

func TestArticleTextURLsTriesArxivHTMLBeforePDF(t *testing.T) {
	got := articleTextURLs("https://arxiv.org/pdf/2401.12345")
	want := []string{
		"https://arxiv.org/html/2401.12345",
		"https://arxiv.org/pdf/2401.12345",
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("articleTextURLs = %#v, want %#v", got, want)
		}
	}
}
