import React, { createElement as h, useCallback, useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';

const TWEMOJI_CDN = 'https://cdn.jsdelivr.net/gh/twitter/twemoji@14.0.2/assets/svg/';
const THUMB_EMOJIS = [
  '1f4f0','1f4bb','1f680','1f916','1f9e0','1f50d','1f4da','1f4a1','1f310','1f9ea',
  '1f4ca','1f527','1f512','1f4e1','1f9f0','1f4c8','1f525','2728','1f3af','1f4dd',
  '1f4e3','1f914','1f9be','1f4f1','1f4af','1f30e','1f9f2','1f4ac','1f4f8','1f9f5',
];

const CARD_MIN_WIDTH = 280;

function hashInt(str) {
  let h = 0;
  for (let i = 0; i < str.length; i++) h = Math.imul(31, h) + str.charCodeAt(i) | 0;
  return Math.abs(h);
}

function readArticles() {
  const el = document.getElementById('articles-data');
  if (!el) return [];
  try {
    return JSON.parse(el.textContent || '[]');
  } catch {
    return [];
  }
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
    h(
      'a',
      { href: article.url, target: '_blank', rel: 'noopener' },
      h('img', { src: article.og_image, alt: '', loading: 'lazy', onError: () => setFailed(true) })
    )
  );
}

function ArticleCard({ article, index, selected }) {
  const title = article.title || '';
  const bullets = Array.isArray(article.summary) ? article.summary : [];

  return h(
    'article',
    {
      className: `card${selected ? ' selected' : ''}`,
      'data-index': index,
      'data-source': article.source,
      'data-url': article.url,
    },
    h(Thumb, { article }),
    h(
      'div',
      { className: 'card-body' },
      h('div', { className: 'card-title' }, h('a', { href: article.url, target: '_blank', rel: 'noopener' }, title)),
      h(
        'div',
        { className: 'card-meta' },
        h('span', { className: 'source-tag' }, article.source),
        article.date ? h('time', { className: 'published-date', dateTime: article.date }, dateLabel(article.date)) : null,
        article.score > 0 ? h('span', { className: 'hn-score' }, `▲${article.score}`) : null
      ),
      bullets.length > 0
        ? h('ul', { className: 'card-summary' }, ...bullets.map((b, i) => h('li', { key: i }, b)))
        : null
    )
  );
}

function App({ articles }) {
  const gridRef = useRef(null);
  const [columns, setColumns] = useState(1);
  const [selectedIdx, setSelectedIdx] = useState(-1);

  useEffect(() => {
    const updateColumns = () => {
      if (!gridRef.current) return;
      const width = gridRef.current.clientWidth;
      const gap = window.matchMedia('(max-width: 640px)').matches ? 16 : 24;
      setColumns(Math.max(1, Math.floor((width + gap) / (CARD_MIN_WIDTH + gap))));
    };
    updateColumns();
    const observer = new ResizeObserver(updateColumns);
    if (gridRef.current) observer.observe(gridRef.current);
    window.addEventListener('resize', updateColumns);
    return () => { observer.disconnect(); window.removeEventListener('resize', updateColumns); };
  }, []);

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

  const selectIndex = useCallback((idx) => {
    const next = Math.max(0, Math.min(idx, articles.length - 1));
    setSelectedIdx(next);
    gridRef.current?.querySelector(`[data-index="${next}"]`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }, [articles.length]);

  useEffect(() => {
    const onKeyDown = (e) => {
      if (e.target.tagName === 'INPUT' || e.metaKey || e.ctrlKey || !articles.length) return;
      switch (e.key) {
        case 'j': case 'J': e.preventDefault(); selectIndex(selectedIdx < 0 ? 0 : selectedIdx + columns); break;
        case 'k': case 'K': e.preventDefault(); selectIndex(selectedIdx < 0 ? 0 : selectedIdx - columns); break;
        case 'h': case 'H': e.preventDefault(); selectIndex(selectedIdx < 0 ? 0 : selectedIdx - 1); break;
        case 'l': case 'L': e.preventDefault(); selectIndex(selectedIdx < 0 ? 0 : selectedIdx + 1); break;
        case 'Enter':
          if (selectedIdx >= 0 && selectedIdx < articles.length)
            window.open(articles[selectedIdx].url, '_blank', 'noopener');
          break;
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [articles, columns, selectIndex, selectedIdx]);

  return h(
    'div',
    { className: 'card-grid', ref: gridRef },
    articles.map((article, index) => h(ArticleCard, {
      key: article.url || index,
      article,
      index,
      selected: index === selectedIdx,
    }))
  );
}

createRoot(document.getElementById('articles-root')).render(h(App, { articles: readArticles() }));
