// Small-multiple line charts (one series each) over the last hour, with a
// shared-time crosshair tooltip and a table view for every value.
import { pointFromState } from '../state.js';
import { $, clock, dateTime, num, setText, svgEl } from './core.js';

const HOUR = 3600e3;
const SERIES = [
  { key: 'inputVoltage', id: 'ch-in', unit: 'V', label: 'input', pad: 3 },
  { key: 'outputVoltage', id: 'ch-out', unit: 'V', label: 'output', pad: 3 },
  { key: 'load', id: 'ch-load', unit: '%', label: 'load', min: 0, floor: 10 },
  { key: 'battery', id: 'ch-batt', unit: '%', label: 'battery', min: 0, max: 100 },
  { key: 'temperature', id: 'ch-temp', unit: '°C', label: 'temperature', pad: 1 },
];

let points = [];
let source = 'live';
let queued = false;
let hideTip = () => {};

// Tooltips are position: fixed, so one left showing would float away from its chart on scroll.
addEventListener('scroll', () => hideTip(), { capture: true, passive: true });

const fmt = (v, unit) => (v == null ? '—' : unit === '%' ? `${num(v, 1)}%` : `${num(v, 1)} ${unit}`);

/** Replaces the history (e.g. the service's last hour). */
export function seedCharts(list) {
  points = list.filter((p) => p.t > Date.now() - HOUR).sort((a, b) => a.t - b.t);
  schedule();
}

/** Appends the reading in state and redraws. */
export function updateCharts(state) {
  source = state.source;
  if (state.connected) {
    const p = pointFromState(state);
    if (!points.length || p.t > points[points.length - 1].t) points.push(p);
    const cut = Date.now() - HOUR;
    while (points.length && points[0].t < cut) points.shift();
  }
  schedule();
}

function schedule() {
  if (queued) return;
  queued = true;
  requestAnimationFrame(() => {
    queued = false;
    renderCharts();
  });
}

export function renderCharts() {
  const now = Date.now();
  const start = points.length ? Math.max(now - HOUR, points[0].t) : now - HOUR;
  for (const s of SERIES) {
    const values = points.filter((p) => p[s.key] != null);
    let lo = Infinity, hi = -Infinity;
    for (const p of values) {
      lo = Math.min(lo, p[s.key]);
      hi = Math.max(hi, p[s.key]);
    }
    setText(`${s.id}-range`, values.length ? `Min ${fmt(lo, s.unit)} · Max ${fmt(hi, s.unit)}` : '');
    const y0 = s.min ?? (values.length ? Math.max(0, lo - s.pad) : 0);
    const y1 = s.max ?? (s.floor != null ? Math.max(s.floor, hi + 5) : values.length ? hi + s.pad : 1);
    lineChart($(`${s.id}-plot`), s, {
      xDomain: [start, now],
      yDomain: [y0, y1],
      empty: s.key === 'temperature' && source === 'live' ? 'Not reported by this UPS' : 'Waiting for data…',
    });
  }
  if ($('history-dialog').open) renderHistoryTable();
}

function lineChart(el, series, { xDomain, yDomain, empty }) {
  el.textContent = '';
  const W = Math.max(el.clientWidth, 80);
  const H = Math.max(el.clientHeight, 40);
  const m = { l: 34, r: 8, t: 6, b: 16 };
  const svg = svgEl('svg', { viewBox: `0 0 ${W} ${H}`, width: W, height: H, role: 'img', 'aria-label': `${series.label} over the last hour` });
  el.append(svg);
  const idx = points.map((p, i) => i).filter((i) => points[i][series.key] != null);
  if (!idx.length) {
    svg.append(svgEl('text', { x: W / 2, y: H / 2 + 4, 'text-anchor': 'middle', class: 'empty' }, empty));
    return;
  }
  const [x0, x1] = xDomain;
  const [y0, y1] = yDomain;
  const sx = (x) => m.l + ((x - x0) / (x1 - x0 || 1)) * (W - m.l - m.r);
  const sy = (y) => H - m.b - ((Math.min(Math.max(y, y0), y1) - y0) / (y1 - y0 || 1)) * (H - m.t - m.b);

  const grid = svgEl('g', { class: 'grid' });
  const axis = svgEl('g', { class: 'axis' });
  for (const v of [y0, y1]) {
    grid.append(svgEl('line', { x1: m.l, x2: W - m.r, y1: sy(v), y2: sy(v) }));
    axis.append(svgEl('text', { x: m.l - 6, y: sy(v) + 4, 'text-anchor': 'end' }, num(v, Math.abs(y1 - y0) < 10 ? 1 : 0)));
  }
  axis.append(svgEl('text', { x: m.l, y: H - 3, 'text-anchor': 'start' }, clock(new Date(x0))));
  axis.append(svgEl('text', { x: W - m.r, y: H - 3, 'text-anchor': 'end' }, 'now'));
  svg.append(grid, axis);

  // Gaps (nulls) break the line.
  let d = '', area = '', run = [];
  const flush = () => {
    if (!run.length) return;
    const seg = run.map((i, k) => `${k ? 'L' : 'M'}${sx(points[i].t).toFixed(1)},${sy(points[i][series.key]).toFixed(1)}`).join('');
    d += seg;
    const base = sy(y0).toFixed(1);
    area += `${seg}L${sx(points[run[run.length - 1]].t).toFixed(1)},${base}L${sx(points[run[0]].t).toFixed(1)},${base}Z`;
    run = [];
  };
  points.forEach((p, i) => (p[series.key] == null ? flush() : run.push(i)));
  flush();
  svg.append(svgEl('path', { class: 'area', d: area }), svgEl('path', { class: 'line', d }));
  const last = idx[idx.length - 1];
  svg.append(svgEl('circle', { class: 'marker', cx: sx(points[last].t), cy: sy(points[last][series.key]), r: 4 }));

  // Crosshair + one tooltip listing every series at that time.
  const cross = svgEl('line', { class: 'cross', y1: m.t, y2: H - m.b, visibility: 'hidden' });
  const dot = svgEl('circle', { class: 'marker', r: 4, visibility: 'hidden' });
  const hit = svgEl('rect', { x: m.l, y: 0, width: Math.max(W - m.l - m.r, 1), height: H, fill: 'transparent', tabindex: '0' });
  hit.setAttribute('aria-label', `${series.label} chart: use arrow keys to read values`);
  svg.append(cross, dot, hit);
  const tip = document.createElement('div');
  tip.className = 'tooltip';
  tip.hidden = true;
  el.append(tip);

  let at = idx.length - 1;
  const show = (k) => {
    at = Math.max(0, Math.min(idx.length - 1, k));
    const p = points[idx[at]];
    const px = sx(p.t), py = sy(p[series.key]);
    cross.setAttribute('x1', px);
    cross.setAttribute('x2', px);
    cross.setAttribute('visibility', 'visible');
    dot.setAttribute('cx', px);
    dot.setAttribute('cy', py);
    dot.setAttribute('visibility', 'visible');
    tip.textContent = '';
    const title = document.createElement('div');
    title.className = 't-time';
    title.textContent = dateTime(new Date(p.t), true);
    tip.append(title);
    for (const s of SERIES) {
      if (p[s.key] == null) continue;
      const r = document.createElement('div');
      r.className = `t-row${s === series ? ' current' : ''}`;
      const b = document.createElement('b');
      b.textContent = fmt(p[s.key], s.unit);
      const span = document.createElement('span');
      span.textContent = s.label;
      r.append(document.createElement('i'), b, span);
      tip.append(r);
    }
    tip.hidden = false;
    hideTip = hide;
    // Fixed (viewport) coordinates, so the card's overflow: hidden can't clip it.
    // Above the chart when the window has room, otherwise below it.
    const r = svg.getBoundingClientRect();
    const x = r.left + px * (r.width / W);
    const left = Math.min(Math.max(x - tip.offsetWidth / 2, r.left), r.right - tip.offsetWidth);
    const above = r.top - tip.offsetHeight - 6;
    tip.style.left = `${left}px`;
    tip.style.top = `${above >= 0 ? above : r.bottom + 6}px`;
  };
  const hide = () => {
    tip.hidden = true;
    cross.setAttribute('visibility', 'hidden');
    dot.setAttribute('visibility', 'hidden');
  };
  hit.addEventListener('pointermove', (e) => {
    const r = svg.getBoundingClientRect();
    const t = x0 + (((e.clientX - r.left) * (W / r.width) - m.l) / (W - m.l - m.r)) * (x1 - x0);
    let best = 0;
    for (let k = 1; k < idx.length; k++) if (Math.abs(points[idx[k]].t - t) < Math.abs(points[idx[best]].t - t)) best = k;
    show(best);
  });
  hit.addEventListener('pointerleave', hide);
  hit.addEventListener('focus', () => show(at));
  hit.addEventListener('blur', hide);
  hit.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowLeft') show(at - 1);
    else if (e.key === 'ArrowRight') show(at + 1);
    else return;
    e.preventDefault();
  });
}

function renderHistoryTable() {
  const body = $('history-table');
  body.textContent = '';
  for (const p of points.slice(-120).reverse()) {
    const tr = document.createElement('tr');
    const cells = [dateTime(new Date(p.t), true), ...SERIES.map((s) => (p[s.key] == null ? '—' : num(p[s.key], 1)))];
    for (const v of cells) {
      const td = document.createElement('td');
      td.textContent = v;
      tr.append(td);
    }
    body.append(tr);
  }
}

export function openHistoryDialog() {
  renderHistoryTable();
  $('history-dialog').showModal();
}
