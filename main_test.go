package main

import (
	"net/http"
	"net/http/httptest"
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
