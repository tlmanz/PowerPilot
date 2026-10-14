// Shared DOM and formatting helpers.
// Formatting follows house style: MM/DD/YY dates, thousands separators, 1-decimal percentages.

export const $ = (id) => document.getElementById(id);

export function setText(target, text) {
  const el = typeof target === 'string' ? $(target) : target;
  if (el.textContent !== text) el.textContent = text;
}

const SVGNS = 'http://www.w3.org/2000/svg';

export function svgEl(tag, attrs = {}, text) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  if (text != null) el.textContent = text;
  return el;
}

/** An inline icon from the sprite in index.html. */
export function icon(name, cls = 'icon') {
  const svg = svgEl('svg', { class: cls, 'aria-hidden': 'true' });
  svg.append(svgEl('use', { href: `#i-${name}` }));
  return svg;
}

/** Points an existing <svg><use> at another sprite icon. */
export function setIcon(svg, name) {
  const use = svg.querySelector('use');
  if (use.getAttribute('href') !== `#i-${name}`) use.setAttribute('href', `#i-${name}`);
}

export function badge(el, tone, text) {
  el.className = `badge ${tone}`;
  setText(el, text);
}

let toastTimer;
export function toast(msg, bad = false) {
  const t = $('toast');
  t.textContent = msg;
  t.className = bad ? 'toast bad' : 'toast';
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, bad ? 6000 : 3500);
}

const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');

/** Animates a number shown in el towards value; null shows the placeholder. */
export function tween(el, value, format, placeholder = '—') {
  if (value == null || Number.isNaN(value)) {
    el._tween = null;
    el._value = null;
    setText(el, placeholder);
    return;
  }
  const from = el._value;
  el._value = value;
  if (from == null || reduceMotion.matches || Math.abs(from - value) < 1e-9) {
    setText(el, format(value));
    return;
  }
  const start = performance.now();
  const token = {};
  el._tween = token;
  const frame = (now) => {
    if (el._tween !== token) return;
    const k = Math.min(1, (now - start) / 450);
    const eased = 1 - Math.pow(1 - k, 3);
    setText(el, format(from + (value - from) * eased));
    if (k < 1) requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
}

// ---------- formatting ----------

export const num = (v, d = 0) =>
  Number(v).toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d });
export const pct = (v) => `${num(v, 1)}%`;
const pad = (n) => String(n).padStart(2, '0');
export const date = (d) => `${pad(d.getMonth() + 1)}/${pad(d.getDate())}/${pad(d.getFullYear() % 100)}`;
export function clock(d, secs = false) {
  const h = d.getHours();
  return `${h % 12 || 12}:${pad(d.getMinutes())}${secs ? ':' + pad(d.getSeconds()) : ''} ${h >= 12 ? 'PM' : 'AM'}`;
}
export const dateTime = (d, secs = false) => `${date(d)} ${clock(d, secs)}`;
export const sameDay = (a, b) => a.toDateString() === b.toDateString();

export function duration(min) {
  if (min == null) return '—';
  if (min >= 480) return 'Over 8 h';
  const m = Math.max(0, Math.round(min));
  return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${m % 60} min`;
}
