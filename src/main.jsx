import React, { createElement as h, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
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

const CARD_MIN_WIDTH = 280;
const CARD_HEIGHT_DESKTOP = 190;
const CARD_HEIGHT_MOBILE = 170;
const OVERSCAN_ROWS = 3;

function readArticles() {
  const el = document.getElementById('articles-data');
  if (!el) return [];
  try {
    return JSON.parse(el.textContent || '[]');
  } catch {
    return [];
  }
}

function scoreClass(score) {
  if (score >= 70) return 'score-high';
  if (score >= 40) return 'score-mid';
  if (score > 0) return 'score-low';
  return 'score-none';
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

function gapForViewport() {
  return window.matchMedia('(max-width: 640px)').matches ? 16 : 24;
}

function cardHeightForViewport() {
  return window.matchMedia('(max-width: 640px)').matches ? CARD_HEIGHT_MOBILE : CARD_HEIGHT_DESKTOP;
}

function measureLayout(el) {
  if (!el) {
    return { width: 0, columns: 1, gap: 24, cardWidth: CARD_MIN_WIDTH, cardHeight: CARD_HEIGHT_DESKTOP, rowHeight: CARD_HEIGHT_DESKTOP + 24 };
  }
  const width = el.clientWidth;
  const gap = gapForViewport();
  const columns = Math.max(1, Math.floor((width + gap) / (CARD_MIN_WIDTH + gap)));
  const cardWidth = Math.max(CARD_MIN_WIDTH, (width - gap * (columns - 1)) / columns);
  const cardHeight = cardHeightForViewport();
  return { width, columns, gap, cardWidth, cardHeight, rowHeight: cardHeight + gap };
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

function ArticleCard({ article, index, selected, style }) {
  const title = article.title || '';
  const bullets = Array.isArray(article.summary) ? article.summary : [];

  return h(
    'article',
    {
      className: `card${selected ? ' selected' : ''}`,
      style,
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
  const [layout, setLayout] = useState(() => measureLayout(null));
  const [scrollY, setScrollY] = useState(() => window.scrollY);
  const [viewportHeight, setViewportHeight] = useState(() => window.innerHeight);
  const [selectedIdx, setSelectedIdx] = useState(-1);

  useEffect(() => {
    const updateLayout = () => setLayout(measureLayout(gridRef.current));
    updateLayout();
    const observer = new ResizeObserver(updateLayout);
    if (gridRef.current) observer.observe(gridRef.current);
    window.addEventListener('resize', updateLayout);
    return () => {
      observer.disconnect();
      window.removeEventListener('resize', updateLayout);
    };
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

  useEffect(() => {
    let frame = 0;
    const updateScroll = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        setScrollY(window.scrollY);
        setViewportHeight(window.innerHeight);
      });
    };
    window.addEventListener('scroll', updateScroll, { passive: true });
    window.addEventListener('resize', updateScroll);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener('scroll', updateScroll);
      window.removeEventListener('resize', updateScroll);
    };
  }, []);

  const scrollToIndex = useCallback((idx) => {
    const grid = gridRef.current;
    if (!grid) return;
    const row = Math.floor(idx / layout.columns);
    const gridTop = grid.getBoundingClientRect().top + window.scrollY;
    window.scrollTo({ top: Math.max(0, gridTop + row * layout.rowHeight - 24), behavior: 'smooth' });
  }, [layout.columns, layout.rowHeight]);

  const selectIndex = useCallback((idx) => {
    const next = Math.max(0, Math.min(idx, articles.length - 1));
    setSelectedIdx(next);
    scrollToIndex(next);
  }, [articles.length, scrollToIndex]);

  useEffect(() => {
    const onKeyDown = (e) => {
      if (e.target.tagName === 'INPUT' || e.metaKey || e.ctrlKey || !articles.length) return;
      const cols = layout.columns;
      switch (e.key) {
        case 'j': case 'J':
          e.preventDefault();
          selectIndex(selectedIdx < 0 ? 0 : selectedIdx + cols);
          break;
        case 'k': case 'K':
          e.preventDefault();
          selectIndex(selectedIdx < 0 ? 0 : selectedIdx - cols);
          break;
        case 'h': case 'H':
          e.preventDefault();
          selectIndex(selectedIdx < 0 ? 0 : selectedIdx - 1);
          break;
        case 'l': case 'L':
          e.preventDefault();
          selectIndex(selectedIdx < 0 ? 0 : selectedIdx + 1);
          break;
        case 'Enter':
          if (selectedIdx >= 0 && selectedIdx < articles.length) {
            window.open(articles[selectedIdx].url, '_blank', 'noopener');
          }
          break;
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [articles, layout.columns, selectIndex, selectedIdx]);

  const visible = useMemo(() => {
    const rows = Math.ceil(articles.length / layout.columns);
    const grid = gridRef.current;
    const gridTop = grid ? grid.getBoundingClientRect().top + window.scrollY : 0;
    const localTop = Math.max(0, scrollY - gridTop);
    const startRow = Math.max(0, Math.floor(localTop / layout.rowHeight) - OVERSCAN_ROWS);
    const endRow = Math.min(rows - 1, Math.ceil((localTop + viewportHeight) / layout.rowHeight) + OVERSCAN_ROWS);
    const items = [];
    for (let row = startRow; row <= endRow; row++) {
      for (let col = 0; col < layout.columns; col++) {
        const index = row * layout.columns + col;
        if (index >= articles.length) break;
        items.push({ index, row, col, article: articles[index] });
      }
    }
    return { items, totalHeight: rows > 0 ? rows * layout.rowHeight - layout.gap : 0 };
  }, [articles, layout, scrollY, viewportHeight]);

  return h(
    'div',
    { className: 'virtual-grid', ref: gridRef, style: { height: `${visible.totalHeight}px` } },
    visible.items.map(({ article, index, row, col }) => h(ArticleCard, {
      key: article.url || index,
      article,
      index,
      selected: index === selectedIdx,
      style: {
        width: `${layout.cardWidth}px`,
        height: `${layout.cardHeight}px`,
        transform: `translate3d(${col * (layout.cardWidth + layout.gap)}px, ${row * layout.rowHeight}px, 0)`,
      },
    }))
  );
}

createRoot(document.getElementById('articles-root')).render(h(App, { articles: readArticles() }));
