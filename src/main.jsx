import React, { createElement as h, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';

const CARD_MIN_WIDTH = 280;
const CARD_BODY_HEIGHT_DESKTOP = 148;
const CARD_BODY_HEIGHT_MOBILE = 132;
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

function bodyHeightForViewport() {
  return window.matchMedia('(max-width: 640px)').matches ? CARD_BODY_HEIGHT_MOBILE : CARD_BODY_HEIGHT_DESKTOP;
}

function measureLayout(el) {
  if (!el) {
    return { width: 0, columns: 1, gap: 24, cardWidth: CARD_MIN_WIDTH, cardHeight: 320, rowHeight: 344 };
  }
  const width = el.clientWidth;
  const gap = gapForViewport();
  const columns = Math.max(1, Math.floor((width + gap) / (CARD_MIN_WIDTH + gap)));
  const cardWidth = Math.max(CARD_MIN_WIDTH, (width - gap * (columns - 1)) / columns);
  const cardHeight = Math.ceil(cardWidth * 9 / 16 + bodyHeightForViewport());
  return { width, columns, gap, cardWidth, cardHeight, rowHeight: cardHeight + gap };
}

function Thumb({ article }) {
  const [failed, setFailed] = useState(!article.og_image);
  useEffect(() => setFailed(!article.og_image), [article.og_image]);

  if (failed) {
    const score = article.ai_score || 0;
    return h(
      'a',
      { className: 'card-no-thumb', href: article.url, target: '_blank', rel: 'noopener' },
      h('span', { className: `score-big ${scoreClass(score)}` }, score > 0 ? score : '?')
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
  const score = article.ai_score || 0;
  const title = article.title_ja || article.title || '';
  const originalTitle = article.title_ja ? article.title : '';

  return h(
    'article',
    {
      className: `card${selected ? ' selected' : ''}`,
      style,
      'data-index': index,
      'data-source': article.source,
      'data-score': score,
      'data-url': article.url,
    },
    h(Thumb, { article }),
    h(
      'div',
      { className: 'card-body' },
      h('div', { className: 'card-title' }, h('a', { href: article.url, target: '_blank', rel: 'noopener' }, title)),
      originalTitle ? h('div', { className: 'card-title-orig' }, originalTitle) : null,
      h(
        'div',
        { className: 'card-meta' },
        score > 0 ? h('span', { className: `score-badge ${scoreClass(score)}` }, score) : null,
        h('span', { className: 'source-tag' }, article.source),
        article.date ? h('time', { className: 'published-date', dateTime: article.date }, dateLabel(article.date)) : null,
        article.score > 0 ? h('span', { className: 'hn-score' }, `▲${article.score}`) : null
      )
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
