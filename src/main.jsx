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

function readArticles() {
  const el = document.getElementById('articles-data');
  if (!el) return [];
  try { return JSON.parse(el.textContent || '[]'); } catch { return []; }
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

function ArticleCard({ article }) {
  const bullets = Array.isArray(article.summary) ? article.summary : [];
  const tags = Array.isArray(article.tags) ? article.tags.slice(0, 3) : [];
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
    { className: 'card', 'data-source': article.source },
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
        h('span', { className: 'source-tag' }, article.source),
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

function tagStatsFromArticles(articles) {
  const counts = new Map();
  for (const article of articles) {
    const tags = Array.isArray(article.tags) ? article.tags : [];
    for (const tag of tags) {
      if (!tag) continue;
      counts.set(tag, (counts.get(tag) || 0) + 1);
    }
  }
  return Array.from(counts.entries())
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
}

function TagFilter({ tags, selectedTag, open, onToggleOpen, onSelectTag }) {
  return h(
    'div',
    { className: 'tag-filter' },
    h(
      'div',
      { className: `tag-panel${open ? ' tag-panel-open' : ''}`, 'aria-hidden': open ? 'false' : 'true' },
      h(
        'button',
        {
          type: 'button',
          className: `filter-chip${selectedTag === null ? ' filter-chip-active' : ''}`,
          tabIndex: open ? 0 : -1,
          onClick: () => onSelectTag(null),
        },
        'すべて'
      ),
      ...tags.map(({ name, count }) => h(
        'button',
        {
          type: 'button',
          key: name,
          className: `filter-chip${selectedTag === name ? ' filter-chip-active' : ''}`,
          tabIndex: open ? 0 : -1,
          onClick: () => onSelectTag(selectedTag === name ? null : name),
        },
        h('span', null, name),
        h('span', { className: 'filter-chip-count' }, String(count))
      ))
    ),
    h(
      'button',
      {
        type: 'button',
        className: `filter-toggle${open ? ' filter-toggle-active' : ''}`,
        'aria-label': 'タグで絞り込む',
        'aria-expanded': open ? 'true' : 'false',
        onClick: onToggleOpen,
      },
      h(Search, { size: 24, strokeWidth: 2, 'aria-hidden': 'true' })
    )
  );
}

function App({ articles }) {
  const gridRef = useRef(null);
  const [selectedTag, setSelectedTag] = useState(null);
  const [filterOpen, setFilterOpen] = useState(false);
  const tags = useMemo(() => tagStatsFromArticles(articles), [articles]);
  const visibleArticles = useMemo(() => {
    if (!selectedTag) return articles;
    return articles.filter((article) => Array.isArray(article.tags) && article.tags.includes(selectedTag));
  }, [articles, selectedTag]);
  const handleSelectTag = (tag) => {
    setSelectedTag(tag);
    setFilterOpen(false);
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
    label.hidden = !selectedTag;
    label.textContent = selectedTag ? `タグ: ${selectedTag}` : '';
  }, [selectedTag]);

  return h(
    React.Fragment,
    null,
    h(
      'div',
      { className: 'card-grid', ref: gridRef },
      visibleArticles.map((article, index) => h(ArticleCard, { key: article.url || index, article }))
    ),
    tags.length > 0
      ? h(TagFilter, {
          tags,
          selectedTag,
          open: filterOpen,
          onToggleOpen: () => setFilterOpen((value) => !value),
          onSelectTag: handleSelectTag,
        })
      : null
  );
}

createRoot(document.getElementById('articles-root')).render(h(App, { articles: readArticles() }));
