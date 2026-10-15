// Logs dialog: events and per-minute readings kept on disk, one day at a
// time, with a CSV export for a range of days.
import { $, clock, num, setText } from './core.js';

const MODES = { line: 'Mains', battery: 'Battery', test: 'Self-test', standby: 'Standby', fault: 'Fault' };

let source, getState;
let days = []; // [{date, events, readings}], newest first
let kind = 'events';
let shown = ''; // the date on screen, so stale replies are ignored

const pad = (n) => String(n).padStart(2, '0');
const ymd = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const daysAgo = (n) => {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return ymd(d);
};

function size(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 ** 2) return `${num(bytes / 1024, 1)} KB`;
  return `${num(bytes / 1024 ** 2, 1)} MB`;
}

export function wireLogs(src, getStateFn) {
  source = src;
  getState = getStateFn;
  $('logs-btn').hidden = !src.logs;
  if (!src.logs) return;
  $('logs-btn').addEventListener('click', () => openLogs());
  $('lg-close').addEventListener('click', () => $('logs-dialog').close());
  for (const id of ['lg-tab-events', 'lg-tab-readings']) {
    $(id).addEventListener('click', (e) => showKind(e.currentTarget.dataset.kind));
  }
  $('lg-date').addEventListener('change', () => showDay($('lg-date').value));
  // Days sort newest first, so "older" is the next one down the list.
  $('lg-older').addEventListener('click', () => {
    const d = days.find((x) => x.date < $('lg-date').value);
    if (d) showDay(d.date);
  });
  $('lg-newer').addEventListener('click', () => {
    const later = days.filter((x) => x.date > $('lg-date').value);
    if (later.length) showDay(later[later.length - 1].date);
  });
  $('lg-export').addEventListener('click', exportCSV);
}

/** Opens the dialog on today's logs; kind is 'events' or 'readings'. */
export async function openLogs(which = kind) {
  setText('lg-error', '');
  $('lg-to').value = ymd(new Date());
  $('lg-from').value = daysAgo(6);
  $('logs-dialog').showModal();
  try {
    const r = await source.logs.days();
    days = r.days;
    const keep = getState().settings?.log_retention_days;
    setText('lg-usage', `${size(r.bytes)} on disk${keep ? `, kept ${keep} days` : ''}`);
    const oldest = days.length ? days[days.length - 1].date : ymd(new Date());
    for (const id of ['lg-date', 'lg-from', 'lg-to']) {
      $(id).min = oldest;
      $(id).max = ymd(new Date());
    }
    if ($('lg-from').value < oldest) $('lg-from').value = oldest;
  } catch (err) {
    setText('lg-error', err.message);
  }
  kind = which;
  showDay(ymd(new Date()));
}

function showKind(k) {
  kind = k;
  showDay($('lg-date').value);
}

async function showDay(date) {
  $('lg-date').value = date;
  shown = date;
  for (const [id, k] of [['lg-tab-events', 'events'], ['lg-tab-readings', 'readings']]) {
    $(id).setAttribute('aria-selected', String(k === kind));
  }
  setText('lg-kind', kind);
  $('lg-older').disabled = !days.some((x) => x.date < date);
  $('lg-newer').disabled = !days.some((x) => x.date > date);
  setText('lg-error', '');
  try {
    const rows = await (kind === 'events' ? source.logs.events(date) : source.logs.readings(date));
    if (shown !== date) return;
    if (kind === 'events') renderEvents(rows);
    else renderReadings(rows);
  } catch (err) {
    setText('lg-error', err.message);
  }
}

function cells(tr, values) {
  for (const [text, cls] of values) {
    const td = document.createElement('td');
    if (cls) td.className = cls;
    td.append(text);
    tr.append(td);
  }
}

function setTable(which, count, emptyText) {
  $('lg-events-table').hidden = which !== 'events' || !count;
  $('lg-readings-table').hidden = which !== 'readings' || !count;
  $('lg-empty').hidden = count > 0;
  setText('lg-empty', emptyText);
}

function renderEvents(list) {
  const body = $('lg-events');
  body.textContent = '';
  for (const e of list) {
    const tr = document.createElement('tr');
    const dot = document.createElement('span');
    dot.className = `lv lv-${e.level}`;
    const msg = document.createDocumentFragment();
    msg.append(dot, e.message);
    cells(tr, [[clock(new Date(e.time), true)], [msg, 'left']]);
    body.append(tr);
  }
  setTable('events', list.length, 'No events on this day.');
}

function renderReadings(list) {
  const body = $('lg-readings');
  body.textContent = '';
  const v = (x) => num(x, 1);
  for (const r of list) {
    const tr = document.createElement('tr');
    cells(tr, [
      [clock(new Date(r.t))], [MODES[r.mode] || r.mode, 'left'], [v(r.in)], [`${v(r.in_min)}–${v(r.in_max)}`],
      [v(r.out)], [v(r.load)], [v(r.load_max)], [v(r.batt)], [r.charge == null ? '—' : v(r.charge)], [v(r.freq)],
    ]);
    body.append(tr);
  }
  setTable('readings', list.length, 'No readings on this day. Readings are saved while the UPS is connected.');
}

function exportCSV() {
  const from = $('lg-from').value;
  const to = $('lg-to').value;
  if (!from || !to || from > to) {
    setText('lg-error', 'Pick a start date on or before the end date.');
    return;
  }
  setText('lg-error', '');
  const a = document.createElement('a');
  a.href = source.logs.exportURL(kind, from, to);
  a.download = `PowerPilot-${kind}-${from}-to-${to}.csv`;
  document.body.append(a);
  a.click();
  a.remove();
}
