package main

import (
	"testing"
	"time"
)

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
