import React, { createElement as h, useCallback, useEffect, useMemo, useRef, useState } from 'react';
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

// ── 既読状態 ──────────────────────────────────────────────────────────────────
// 記事は 2 か月で一覧から消えるので、それより古い記録は捨ててよい。

const READ_STORAGE_KEY = 'kijiyomu-read';
const READ_RETENTION_DAYS = 60;

function loadReadState() {
  try {
    const parsed = JSON.parse(localStorage.getItem(READ_STORAGE_KEY) || '{}');
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

function saveReadState(state) {
  try {
    localStorage.setItem(READ_STORAGE_KEY, JSON.stringify(state));
  } catch {
    // 容量超過やプライベートモードでは既読が保存できないだけなので無視する
  }
}

function pruneReadState(state, now) {
  const cutoff = now - READ_RETENTION_DAYS * 86400000;
  const kept = {};
  for (const [url, at] of Object.entries(state)) {
    if (typeof at === 'number' && at >= cutoff) kept[url] = at;
  }
  return kept;
}

function useReadState() {
  const [read, setRead] = useState(() => pruneReadState(loadReadState(), Date.now()));

  const markRead = useCallback((url) => {
    if (!url) return;
    setRead((prev) => {
      if (prev[url]) return prev;
      const next = { ...prev, [url]: Date.now() };
      saveReadState(next);
      return next;
    });
  }, []);

  const clearRead = useCallback(() => {
    setRead({});
    saveReadState({});
  }, []);

  return { read, markRead, clearRead };
}

function clamp(value, min, max) {
  if (value < min) return min;
  if (value > max) return max;
  return value;
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

function Thumb({ article, onOpen }) {
  const [failed, setFailed] = useState(!article.og_image);
  useEffect(() => setFailed(!article.og_image), [article.og_image]);

  if (failed) {
    const cp = THUMB_EMOJIS[hashInt(article.url || article.title || '') % THUMB_EMOJIS.length];
    return h(
      'a',
      { className: 'card-no-thumb', href: article.url, target: '_blank', rel: 'noopener', onClick: onOpen },
      h('img', { className: 'twemoji-thumb', src: `${TWEMOJI_CDN}${cp}.svg`, alt: '' })
    );
  }

  return h(
    'div',
    { className: 'card-thumb' },
    h('a', { href: article.url, target: '_blank', rel: 'noopener', onClick: onOpen },
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

function ArticleCard({ article, isRead, isActive, onOpen }) {
  const bullets = Array.isArray(article.summary) ? article.summary : [];
  const tags = Array.isArray(article.tags) ? article.tags.slice(0, 3) : [];
  const sources = articleSources(article);
  const openArticle = (event) => {
    // リンクやボタン自身のクリックはそれぞれのハンドラに任せる
    if (!article.url || event.target.closest('a, button')) return;
    onOpen();
    window.open(article.url, '_blank', 'noopener');
  };
  const openArticleByKey = (event) => {
    if (event.key !== 'Enter') return;
    openArticle(event);
  };

  return h(
    'article',
    { className: `card${isRead ? ' card-read' : ''}${isActive ? ' card-active' : ''}` },
    h(Thumb, { article, onOpen }),
    h(
      'div',
      { className: 'card-body', role: 'link', tabIndex: 0, onClick: openArticle, onKeyDown: openArticleByKey },
      h('div', { className: 'card-title' },
        h('a', { href: article.url, target: '_blank', rel: 'noopener', onClick: onOpen }, article.title || '')
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

const SORT_MODES = [
  { id: 'recommended', label: 'おすすめ' },
  { id: 'recent', label: '新着' },
];

function SegmentedControl({ options, value, open, onChange }) {
  return h(
    'div',
    { className: 'filter-segment', role: 'group' },
    ...options.map((option) => h(
      'button',
      {
        type: 'button',
        key: option.id,
        className: `filter-segment-item${value === option.id ? ' filter-segment-item-active' : ''}`,
        tabIndex: open ? 0 : -1,
        'aria-pressed': value === option.id ? 'true' : 'false',
        onClick: () => onChange(option.id),
      },
      option.label
    ))
  );
}

function FilterPanel({
  open, onToggleOpen,
  query, onQueryChange,
  tags, selectedTag, onSelectTag,
  sources, selectedSource, onSelectSource,
  sortMode, onSortModeChange,
  unreadOnly, onUnreadOnlyChange, readCount, onClearRead,
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
      h(
        'div',
        { className: 'filter-section' },
        h('div', { className: 'filter-section-label' }, '並び順'),
        h(SegmentedControl, { options: SORT_MODES, value: sortMode, open, onChange: onSortModeChange })
      ),
      h(
        'div',
        { className: 'filter-section' },
        h(
          'label',
          { className: 'filter-check' },
          h('input', {
            type: 'checkbox',
            checked: unreadOnly,
            tabIndex: open ? 0 : -1,
            onChange: (event) => onUnreadOnlyChange(event.target.checked),
          }),
          h('span', null, '未読のみ表示')
        ),
        readCount > 0
          ? h(
              'button',
              { type: 'button', className: 'filter-clear filter-clear-left', tabIndex: open ? 0 : -1, onClick: onClearRead },
              `既読 ${readCount} 件をリセット`
            )
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

// gridColumnCount はカードグリッドの実際の列数を返す。j/k の縦移動に使う。
function gridColumnCount(gridEl) {
  if (!gridEl) return 1;
  const columns = window.getComputedStyle(gridEl).gridTemplateColumns;
  return Math.max(1, columns.split(' ').filter(Boolean).length);
}

function publishedTime(article) {
  const t = new Date(article.date || 0).getTime();
  return Number.isNaN(t) ? 0 : t;
}

function App({ articles }) {
  const gridRef = useRef(null);
  const [query, setQuery] = useState('');
  const [selectedTag, setSelectedTag] = useState(null);
  const [selectedSource, setSelectedSource] = useState(null);
  const [sortMode, setSortMode] = useState('recommended');
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [filterOpen, setFilterOpen] = useState(false);
  const [cursor, setCursor] = useState(-1);
  const { read, markRead, clearRead } = useReadState();

  const tags = useMemo(() => countBy(articles, (a) => (Array.isArray(a.tags) ? a.tags : [])), [articles]);
  const sources = useMemo(() => countBy(articles, articleSources), [articles]);

  const needle = query.trim().toLowerCase();
  const visibleArticles = useMemo(() => {
    const matched = articles.filter((article) => {
      if (selectedTag && !(Array.isArray(article.tags) && article.tags.includes(selectedTag))) return false;
      if (selectedSource && !articleSources(article).includes(selectedSource)) return false;
      if (unreadOnly && read[article.url]) return false;
      if (needle && !searchTextOf(article).includes(needle)) return false;
      return true;
    });
    // data.json は関連度順で届くので、新着順のときだけ並べ直す
    if (sortMode === 'recent') matched.sort((a, b) => publishedTime(b) - publishedTime(a));
    return matched;
  }, [articles, needle, selectedTag, selectedSource, unreadOnly, read, sortMode]);

  const filtered = Boolean(needle || selectedTag || selectedSource || unreadOnly);
  const readCount = useMemo(() => Object.keys(read).length, [read]);

  const clearFilters = () => {
    setQuery('');
    setSelectedTag(null);
    setSelectedSource(null);
    setUnreadOnly(false);
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

  // 絞り込みが変わるとカーソル位置の記事も変わるので、選択を解除する
  useEffect(() => setCursor(-1), [needle, selectedTag, selectedSource, unreadOnly, sortMode]);

  useEffect(() => {
    if (cursor < 0) return;
    const card = gridRef.current?.children[cursor];
    card?.scrollIntoView({ block: 'nearest' });
  }, [cursor]);

  useEffect(() => {
    const onKeyDown = (event) => {
      if (event.metaKey || event.ctrlKey || event.altKey) return;

      const target = event.target;
      const typing = target instanceof HTMLElement
        && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable);

      if (event.key === 'Escape') {
        setFilterOpen(false);
        if (typing) target.blur();
        return;
      }
      if (typing) return;

      if (event.key === '/') {
        event.preventDefault();
        setFilterOpen(true);
        return;
      }

      const steps = { j: gridColumnCount(gridRef.current), k: -gridColumnCount(gridRef.current), l: 1, h: -1 };
      if (event.key in steps) {
        if (visibleArticles.length === 0) return;
        event.preventDefault();
        setCursor((prev) => (prev < 0 ? 0 : clamp(prev + steps[event.key], 0, visibleArticles.length - 1)));
        return;
      }

      if (event.key === 'Enter' && cursor >= 0) {
        const article = visibleArticles[cursor];
        if (!article?.url) return;
        event.preventDefault();
        markRead(article.url);
        window.open(article.url, '_blank', 'noopener');
      }
    };

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [visibleArticles, cursor, markRead]);

  useEffect(() => {
    const label = document.getElementById('selected-tag-label');
    if (!label) return;
    const parts = [];
    if (selectedTag) parts.push(`タグ: ${selectedTag}`);
    if (selectedSource) parts.push(`ソース: ${selectedSource}`);
    if (needle) parts.push(`"${query.trim()}"`);
    if (unreadOnly) parts.push('未読のみ');
    if (parts.length > 0) parts.push(`${visibleArticles.length}件`);
    label.hidden = parts.length === 0;
    label.textContent = parts.join(' · ');
  }, [selectedTag, selectedSource, needle, query, unreadOnly, visibleArticles.length]);

  return h(
    React.Fragment,
    null,
    h(
      'div',
      { className: 'card-grid', ref: gridRef },
      visibleArticles.map((article, index) => h(ArticleCard, {
        key: article.url || index,
        article,
        isRead: Boolean(read[article.url]),
        isActive: index === cursor,
        onOpen: () => markRead(article.url),
      }))
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
      onSelectTag: setSelectedTag,
      sources,
      selectedSource,
      onSelectSource: setSelectedSource,
      sortMode,
      onSortModeChange: setSortMode,
      unreadOnly,
      onUnreadOnlyChange: setUnreadOnly,
      readCount,
      onClearRead: clearRead,
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
