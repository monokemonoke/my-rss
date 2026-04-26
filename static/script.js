// ── keyboard navigation ──────────────────────────────────────────────────────
let selectedIdx = -1;

function visibleCards() {
  return Array.from(document.querySelectorAll('.card'));
}

function colCount() {
  const cards = visibleCards();
  if (cards.length < 2) return 1;
  const top0 = cards[0].getBoundingClientRect().top;
  let n = 1;
  for (let i = 1; i < cards.length; i++) {
    if (Math.abs(cards[i].getBoundingClientRect().top - top0) < 4) n++;
    else break;
  }
  return n;
}

function selectCard(idx) {
  const cards = visibleCards();
  if (!cards.length) return;
  // clamp
  idx = Math.max(0, Math.min(idx, cards.length - 1));
  if (selectedIdx >= 0 && selectedIdx < cards.length)
    cards[selectedIdx].classList.remove('selected');
  selectedIdx = idx;
  const card = cards[selectedIdx];
  card.classList.add('selected');
  card.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

document.addEventListener('keydown', e => {
  if (e.target.tagName === 'INPUT' || e.metaKey || e.ctrlKey) return;
  const cards = visibleCards();
  if (!cards.length) return;
  const cols = colCount();
  switch (e.key) {
    case 'j': case 'J':
      e.preventDefault();
      selectCard(selectedIdx < 0 ? 0 : selectedIdx + cols); break;
    case 'k': case 'K':
      e.preventDefault();
      selectCard(selectedIdx < 0 ? 0 : selectedIdx - cols); break;
    case 'h': case 'H':
      e.preventDefault();
      selectCard(selectedIdx < 0 ? 0 : selectedIdx - 1); break;
    case 'l': case 'L':
      e.preventDefault();
      selectCard(selectedIdx < 0 ? 0 : selectedIdx + 1); break;
    case 'Enter':
      if (selectedIdx >= 0 && selectedIdx < cards.length) {
        const a = cards[selectedIdx].querySelector('.card-title a');
        if (a) window.open(a.href, '_blank', 'noopener');
      }
      break;
  }
});
