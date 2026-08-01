import React, { createElement as h, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Search } from 'lucide-react';
import './style.css';

const TWEMOJI_CDN = 'https://cdn.jsdelivr.net/gh/twitter/twemoji@14.0.2/assets/svg/';
const THUMB_EMOJIS = [
  '1f4f0','1f4bb','1f680','1f916','1f9e0','1f50d','1f4da','1f4a1','1f310','1f9ea',
  '1f4ca','1f527','1f512','1f4e1','1f9f0','1f4c8','1f525','2728','1f3af','1f4dd',
  '1f4e3','1f914','1f9be','1f4f1','1f4af','1f30e','1f9f2','1f4ac','1f4f8','1f9f5',
];

function hashInt(str) {
  let h = 0;
  for (let i = 0; i < str.length; i++) h = Math.imul(31, h) + str.charCodeAt(i) | 0;
  return Math.abs(h);
}

const DATA_URL = './data.json';

// 記事は既定では data.json として配信される。--inline-data で生成した HTML の
// 場合だけ script タグに埋め込まれているので、それを先に見る。
function readInlineArticles() {
  const el = document.getElementById('articles-data');
  if (!el) return null;
  try { return JSON.parse(el.textContent || '[]'); } catch { return null; }
}

async function fetchArticles() {
  const res = await fetch(DATA_URL);
  if (!res.ok) throw new Error(`${DATA_URL}: ${res.status}`);
  const data = await res.json();
  return Array.isArray(data.articles) ? data.articles : [];
}

function dateLabel(raw) {
  if (!raw) return '';
  const d = new Date(raw);
  if (Number.isNaN(d.getTime())) return raw;
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

function Thumb({ article }) {
  const [failed, setFailed] = useState(!article.og_image);
  useEffect(() => setFailed(!article.og_image), [article.og_image]);

  if (failed) {
    const cp = THUMB_EMOJIS[hashInt(article.url || article.title || '') % THUMB_EMOJIS.length];
    return h(
      'a',
      { className: 'card-no-thumb', href: article.url, target: '_blank', rel: 'noopener' },
      h('img', { className: 'twemoji-thumb', src: `${TWEMOJI_CDN}${cp}.svg`, alt: '' })
    );
  }

  return h(
    'div',
    { className: 'card-thumb' },
    h('a', { href: article.url, target: '_blank', rel: 'noopener' },
      h('img', { src: article.og_image, alt: '', loading: 'lazy', onError: () => setFailed(true) })
    )
  );
}

function articleSources(article) {
  if (Array.isArray(article.sources)) return article.sources;
  // 旧スキーマ: "Zenn / はてなブックマーク" の 1 本の文字列
  if (typeof article.source === 'string' && article.source) return article.source.split(' / ');
  return [];
}

function ArticleCard({ article }) {
  const bullets = Array.isArray(article.summary) ? article.summary : [];
  const tags = Array.isArray(article.tags) ? article.tags.slice(0, 3) : [];
  const sources = articleSources(article);
  const openArticle = (event) => {
    if (!article.url || event.target.closest('a, button')) return;
    window.open(article.url, '_blank', 'noopener');
  };
  const openArticleByKey = (event) => {
    if (event.key !== 'Enter') return;
    openArticle(event);
  };

  return h(
    'article',
    { className: 'card' },
    h(Thumb, { article }),
    h(
      'div',
      { className: 'card-body', role: 'link', tabIndex: 0, onClick: openArticle, onKeyDown: openArticleByKey },
      h('div', { className: 'card-title' },
        h('a', { href: article.url, target: '_blank', rel: 'noopener' }, article.title || '')
      ),
      h(
        'div',
        { className: 'card-meta' },
        ...sources.map((source) => h('span', { className: 'source-tag', key: source }, source)),
        ...tags.map((tag) => h('span', { className: 'article-tag', key: tag }, tag)),
        article.date ? h('time', { className: 'published-date', dateTime: article.date }, dateLabel(article.date)) : null,
        article.score > 0 ? h('span', { className: 'hn-score' }, `▲${article.score}`) : null
      ),
      bullets.length > 0
        ? h('ul', { className: 'card-summary' }, ...bullets.map((b, i) => h('li', { key: i }, b)))
        : null
    )
  );
}

function countBy(articles, pick) {
  const counts = new Map();
  for (const article of articles) {
    for (const value of pick(article)) {
      if (!value) continue;
      counts.set(value, (counts.get(value) || 0) + 1);
    }
  }
  return Array.from(counts.entries())
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
}

// 検索対象の文字列は記事ごとに 1 度だけ組み立てて使い回す
const searchIndex = new WeakMap();

function searchTextOf(article) {
  let text = searchIndex.get(article);
  if (text === undefined) {
    text = [article.title || '', ...(article.summary || []), ...(article.tags || []), ...articleSources(article)]
      .join(' ')
      .toLowerCase();
    searchIndex.set(article, text);
  }
  return text;
}

function FilterChips({ items, selected, open, onSelect }) {
  return h(
    'div',
    { className: 'filter-chips' },
    h(
      'button',
      {
        type: 'button',
        className: `filter-chip${selected === null ? ' filter-chip-active' : ''}`,
        tabIndex: open ? 0 : -1,
        onClick: () => onSelect(null),
      },
      'すべて'
    ),
    ...items.map(({ name, count }) => h(
      'button',
      {
        type: 'button',
        key: name,
        className: `filter-chip${selected === name ? ' filter-chip-active' : ''}`,
        tabIndex: open ? 0 : -1,
        onClick: () => onSelect(selected === name ? null : name),
      },
      h('span', null, name),
      h('span', { className: 'filter-chip-count' }, String(count))
    ))
  );
}

function FilterPanel({
  open, onToggleOpen,
  query, onQueryChange,
  tags, selectedTag, onSelectTag,
  sources, selectedSource, onSelectSource,
  matchCount, filtered, onClear,
}) {
  const searchRef = useRef(null);

  useEffect(() => {
    if (open) searchRef.current?.focus();
  }, [open]);

  return h(
    'div',
    { className: 'filter-dock' },
    h(
      'div',
      { className: `filter-panel${open ? ' filter-panel-open' : ''}`, 'aria-hidden': open ? 'false' : 'true' },
      h('input', {
        ref: searchRef,
        type: 'search',
        className: 'filter-search',
        placeholder: 'タイトル・要約から検索',
        'aria-label': '記事を検索',
        value: query,
        tabIndex: open ? 0 : -1,
        onChange: (event) => onQueryChange(event.target.value),
      }),
      h(
        'div',
        { className: 'filter-status' },
        h('span', null, `${matchCount}件`),
        filtered
          ? h('button', { type: 'button', className: 'filter-clear', tabIndex: open ? 0 : -1, onClick: onClear }, '絞り込みを解除')
          : null
      ),
      tags.length > 0
        ? h(
            'div',
            { className: 'filter-section' },
            h('div', { className: 'filter-section-label' }, 'タグ'),
            h(FilterChips, { items: tags, selected: selectedTag, open, onSelect: onSelectTag })
          )
        : null,
      sources.length > 0
        ? h(
            'div',
            { className: 'filter-section' },
            h('div', { className: 'filter-section-label' }, 'ソース'),
            h(FilterChips, { items: sources, selected: selectedSource, open, onSelect: onSelectSource })
          )
        : null
    ),
    h(
      'button',
      {
        type: 'button',
        className: `filter-toggle${open ? ' filter-toggle-active' : ''}${filtered ? ' filter-toggle-filtered' : ''}`,
        'aria-label': '検索・絞り込み',
        'aria-expanded': open ? 'true' : 'false',
        onClick: onToggleOpen,
      },
      h(Search, { size: 24, strokeWidth: 2, 'aria-hidden': 'true' })
    )
  );
}

function App({ articles }) {
  const gridRef = useRef(null);
  const [query, setQuery] = useState('');
  const [selectedTag, setSelectedTag] = useState(null);
  const [selectedSource, setSelectedSource] = useState(null);
  const [filterOpen, setFilterOpen] = useState(false);

  const tags = useMemo(() => countBy(articles, (a) => (Array.isArray(a.tags) ? a.tags : [])), [articles]);
  const sources = useMemo(() => countBy(articles, articleSources), [articles]);

  const needle = query.trim().toLowerCase();
  const visibleArticles = useMemo(() => {
    if (!needle && !selectedTag && !selectedSource) return articles;
    return articles.filter((article) => {
      if (selectedTag && !(Array.isArray(article.tags) && article.tags.includes(selectedTag))) return false;
      if (selectedSource && !articleSources(article).includes(selectedSource)) return false;
      if (needle && !searchTextOf(article).includes(needle)) return false;
      return true;
    });
  }, [articles, needle, selectedTag, selectedSource]);

  const filtered = Boolean(needle || selectedTag || selectedSource);
  const clearFilters = () => {
    setQuery('');
    setSelectedTag(null);
    setSelectedSource(null);
  };

  useEffect(() => {
    const header = document.querySelector('header');
    if (!header) return;
    let lastY = window.scrollY;
    const onScroll = () => {
      const y = window.scrollY;
      if (y > lastY && y > 60) header.classList.add('header-hidden');
      else header.classList.remove('header-hidden');
      lastY = y;
    };
    window.addEventListener('scroll', onScroll, { passive: true });
    return () => window.removeEventListener('scroll', onScroll);
  }, []);

  useEffect(() => {
    const onKeyDown = (event) => {
      if (event.key === 'Escape') setFilterOpen(false);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  useEffect(() => {
    const label = document.getElementById('selected-tag-label');
    if (!label) return;
    const parts = [];
    if (selectedTag) parts.push(`タグ: ${selectedTag}`);
    if (selectedSource) parts.push(`ソース: ${selectedSource}`);
    if (needle) parts.push(`"${query.trim()}"`);
    if (parts.length > 0) parts.push(`${visibleArticles.length}件`);
    label.hidden = parts.length === 0;
    label.textContent = parts.join(' · ');
  }, [selectedTag, selectedSource, needle, query, visibleArticles.length]);

  return h(
    React.Fragment,
    null,
    h(
      'div',
      { className: 'card-grid', ref: gridRef },
      visibleArticles.map((article, index) => h(ArticleCard, { key: article.url || index, article }))
    ),
    filtered && visibleArticles.length === 0
      ? h('p', { className: 'app-status' }, '条件に合う記事がありません。')
      : null,
    h(FilterPanel, {
      open: filterOpen,
      onToggleOpen: () => setFilterOpen((value) => !value),
      query,
      onQueryChange: setQuery,
      tags,
      selectedTag,
      onSelectTag: (tag) => setSelectedTag(tag),
      sources,
      selectedSource,
      onSelectSource: (source) => setSelectedSource(source),
      matchCount: visibleArticles.length,
      filtered,
      onClear: clearFilters,
    })
  );
}

const inlineArticles = readInlineArticles();

function Root() {
  const [state, setState] = useState(
    inlineArticles ? { status: 'ready', articles: inlineArticles } : { status: 'loading', articles: [] }
  );

  useEffect(() => {
    if (inlineArticles) return undefined;
    let alive = true;
    fetchArticles()
      .then((articles) => { if (alive) setState({ status: 'ready', articles }); })
      .catch(() => { if (alive) setState({ status: 'error', articles: [] }); });
    return () => { alive = false; };
  }, []);

  if (state.status === 'loading') return h('p', { className: 'app-status' }, '記事を読み込んでいます…');
  if (state.status === 'error') return h('p', { className: 'app-status' }, '記事を読み込めませんでした。時間をおいて再読み込みしてください。');
  return h(App, { articles: state.articles });
}

createRoot(document.getElementById('articles-root')).render(h(Root));
