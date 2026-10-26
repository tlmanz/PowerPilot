// Battery health, opened from the Battery Status card. powerpilot-server works
// it out from the UPS's readings (PowerPilotHardware docs/cloud.md, "Battery
// health"), so it shows in Remote mode; this shows it, and the owner's two
// settings. Content and wording follow the mobile app's Battery screen.
import { $, date, num, pct, setIcon, setText, ymd } from './core.js';
import { confirmDialog } from './controls.js';

const RISE = 0.5; // volts over the baseline's drop: the server's "check" rule
const FULL_TEST = 8; // seconds: a shorter test was cut short, so the server doesn't compare it

const VERDICT = {
  good: ['good', 'Good', 'battery'],
  check: ['warn', 'Check soon', 'battery-warn'],
  replace: ['bad', 'Replace soon', 'battery-low'],
  unknown: ['muted', 'Not tested', 'battery-empty'],
};

let source, getState;
let loading = false;
let error = ''; // why the report didn't load
let away = false; // not in Remote mode: explain instead
let drawn = null; // the report the chart shows

const sentence = (s) => s.charAt(0).toUpperCase() + s.slice(1);
const drop = (t) => t.volts_before - t.volts_low;
// The server leaves out what it doesn't know; a zero time is as good as missing.
const when = (s) => (s && !s.startsWith('0001') ? new Date(s) : null);
const day = (s) => (s ? new Date(`${s}T00:00`) : null);

/** How long ago a battery went in, in the unit that reads best. */
function ago(days) {
  if (days === 0) return 'Put in today';
  if (days === 1) return '1 day ago';
  if (days < 60) return `${num(days)} days ago`;
  if (days < 730) return `${num(Math.round(days / 30.44))} months ago`;
  return `${num(days / 365.25, 1)} years ago`;
}

function onBattery(secs) {
  if (!secs) return 'None';
  if (secs < 60) return `${secs} s`;
  const m = Math.floor(secs / 60);
  if (m < 60) return `${m} min`;
  return m % 60 ? `${num(Math.floor(m / 60))} h ${m % 60} min` : `${num(m / 60)} h`;
}

export function wireBattery(src, getStateFn) {
  source = src;
  getState = getStateFn;
  $('bt-health-open').hidden = !src.battery;
  if (!src.battery) return;
  $('bt-health-open').addEventListener('click', openBattery);
  $('bh-close').addEventListener('click', () => $('battery-dialog').close());
  $('bh-retry').addEventListener('click', load);
  $('bh-cloud').addEventListener('click', () => $('cl-open').click());
  $('bh-run-test').addEventListener('click', runTest);
  $('bh-installed').addEventListener('click', changeInstalled);
  $('bh-auto').addEventListener('change', setAutoTest);
  $('bh-replace').addEventListener('click', replace);
}

/** Opens the view and loads the report again. */
export function openBattery() {
  drawn = null;
  setText('bh-error', '');
  $('battery-dialog').showModal();
  load();
}

/** Redraws the open view: the main loop calls it on every state. */
export function updateBatteryView() {
  if ($('battery-dialog').open) render();
}

async function load() {
  loading = true;
  error = '';
  render();
  try {
    await source.battery.load();
    away = false;
  } catch (err) {
    away = err.status === 409;
    if (!away) error = sentence(err.message);
    // A report already on screen stays; say why it isn't fresh.
    if (error && getState().battery.report) setText('bh-error', error);
  } finally {
    loading = false;
    render();
  }
}

function render() {
  const s = getState();
  const r = s.battery.report;
  const name = s.device.name || 'UPS';
  setText('bh-sub', away ? name : `${name} · ${s.source === 'mock'
    ? 'Made-up battery health for the demo'
    : "Worked out by your server from the device's readings"}`);
  $('bh-away').hidden = !away;
  $('bh-wait').hidden = away || !!r;
  $('bh-body').hidden = away || !r;
  setText('bh-wait-text', loading || !error ? 'Loading battery health…' : error);
  $('bh-retry').hidden = loading || !error;
  if (away || !r) return;
  if (drawn !== r) {
    renderReasons(r);
    renderTests(r);
    setText('bh-error', ''); // a new report: what failed before is out of date
    drawn = r;
  }
  renderVerdict(r, s);
  renderUsage(r);
  renderSettings(r);
  const since = when(r.counting_since);
  setText('bh-note', s.source === 'mock'
    ? 'The demo makes these numbers up. Paired with a server, they come from your UPS.'
    : `${since ? `Counting since ${date(since)}` : 'Counting'}, when your server first saw this UPS. The health rules are a starting point and will be tuned as tests build up.`);
}

// The verdict, the server's reasons as they are, and the last self-test.
function renderVerdict(r, s) {
  const [tone, label, ico] = VERDICT[r.health] || VERDICT.unknown;
  $('bh-verdict').dataset.tone = tone;
  setIcon($('bh-icon'), ico);
  setText('bh-label', label);
  const last = (r.tests || [])[0];
  const lt = s.battery.lastTest;
  const value = $('bh-last-value');
  let result = '', at = null;
  if (last) [result, at] = [last.result, when(last.time)];
  else if (lt && lt.ended && (lt.result === 'passed' || lt.result === 'failed')) [result, at] = [lt.result, new Date(lt.ended)];
  const running = s.battery.testRunning;
  setText('bh-last-label', running ? 'Self-test' : result ? 'Last self-test' : 'Takes 10 seconds, on mains');
  value.dataset.result = running ? 'running' : result;
  setText(value, running ? 'Running…' : result ? `${result === 'passed' ? 'Passed' : 'Failed'} · ${date(at)}` : '');
  const run = $('bh-run-test');
  run.hidden = running || !!result;
  if (!run._busy) run.disabled = !s.connected || s.mode !== 'ac';
}

function renderUsage(r) {
  const since = when(r.counting_since);
  const deepestAt = when(r.deepest_at);
  setText('bh-cycles', num(r.cycles || 0, 2));
  setText('bh-outages', num(r.outages || 0));
  setText('bh-outages-sub', since ? `since ${date(since)}` : 'counted by the server');
  setText('bh-onbatt', onBattery(r.battery_seconds || 0));
  setText('bh-deepest', r.deepest_charge == null ? 'None' : pct(r.deepest_charge));
  setText('bh-deepest-sub', deepestAt ? `on ${date(deepestAt)}` : 'no outage yet');
}

function renderReasons(r) {
  const list = $('bh-reasons');
  list.textContent = '';
  for (const reason of r.reasons || []) {
    const li = document.createElement('li');
    li.textContent = reason;
    list.append(li);
  }
}

// Each self-test's voltage drop, the last six, oldest on the left: grey for
// the baseline, amber where the drop is 0.5 V or more above it.
function renderTests(r) {
  const base = r.baseline;
  const chip = $('bh-baseline');
  chip.hidden = !base;
  if (base) setText(chip, `Baseline ${num(drop(base), 1)} V`);
  const shown = (r.tests || []).slice(0, 6).reverse();
  const top = Math.max(base ? drop(base) : 0, ...shown.map(drop));
  const bars = $('bh-bars'), dates = $('bh-dates');
  bars.textContent = dates.textContent = '';
  $('bh-tests-empty').hidden = shown.length > 0;
  bars.hidden = dates.hidden = $('bh-legend').hidden = !shown.length;
  let failed = false;
  for (const t of shown) {
    const isBase = base && when(t.time).getTime() === when(base.time).getTime();
    const kind = t.result === 'failed' ? 'failed'
      : isBase ? 'base'
      : base && t.seconds >= FULL_TEST && drop(t) - drop(base) >= RISE - 1e-9 ? 'rise' : '';
    failed ||= kind === 'failed';
    const col = document.createElement('div');
    col.className = `bh-bar ${kind}`;
    const v = document.createElement('b');
    v.textContent = num(drop(t), 1);
    const bar = document.createElement('i');
    // Of the chart's height less the value's line above the bar.
    bar.style.height = `calc((100% - 1.5rem) * ${top > 0 ? Math.max(0.03, drop(t) / top).toFixed(3) : 0.03})`;
    col.append(v, bar);
    bars.append(col);
    const d = document.createElement('span');
    d.textContent = date(when(t.time));
    dates.append(d);
  }
  $('bh-legend-failed').hidden = !failed;
  bars.setAttribute('aria-label', `Voltage drop in each self-test, oldest first: ${
    shown.map((t) => `${date(when(t.time))} ${num(drop(t), 1)} V`).join(', ')}`);
}

function renderSettings(r) {
  setText('bh-installed-desc', r.age_days == null ? 'When this battery went in, for its age' : ago(r.age_days));
  setText('bh-installed', r.installed ? date(day(r.installed)) : 'Set');
  const auto = $('bh-auto');
  if (!auto._busy) auto.checked = !!r.auto_test;
}

// act runs a change with control disabled meanwhile. The redrawn view shows
// that it worked; a failure is said in the dialog (a toast would sit under it).
async function act(control, change) {
  control._busy = true;
  control.disabled = true;
  setText('bh-error', '');
  try {
    await change();
    return true;
  } catch (err) {
    setText('bh-error', sentence(err.message));
    return false;
  } finally {
    control._busy = false;
    control.disabled = false;
    render();
  }
}

async function runTest() {
  if (await confirmDialog('Run a battery self-test?', 'The UPS runs on battery for 10 seconds, then returns to mains. Mains stays connected, so your equipment keeps power if the battery struggles.', 'Start Test')) {
    act($('bh-run-test'), () => source.command('test.quick'));
  }
}

async function changeInstalled() {
  const r = getState().battery.report;
  const installed = await askDate({ title: 'When did this battery go in?', ok: 'Save', value: r.installed || ymd(new Date()), min: '2000-01-01' });
  if (installed) {
    act($('bh-installed'), () => source.battery.save({ installed, auto_test: !!r.auto_test }));
  }
}

// The server sets both settings at once, so the date always goes along.
async function setAutoTest(e) {
  const sw = e.currentTarget, on = sw.checked;
  const r = getState().battery.report;
  const body = { auto_test: on };
  if (r.installed) body.installed = r.installed;
  if (!(await act(sw, () => source.battery.save(body)))) sw.checked = !on;
}

async function replace() {
  const yearAgo = new Date();
  yearAgo.setFullYear(yearAgo.getFullYear() - 1);
  const installed = await askDate({
    title: 'New battery?',
    body: 'This starts the counts again: cycles, outages, time on battery, self-tests and the voltage-drop baseline. The monthly self-test setting stays as it is.',
    ok: 'Replace battery', danger: true, value: ymd(new Date()), min: ymd(yearAgo),
  });
  if (installed) act($('bh-replace'), () => source.battery.replaced(installed));
}

/** Asks for the day a battery went in: YYYY-MM-DD, or null for Cancel. */
function askDate({ title, body = '', ok, danger = false, value, min }) {
  const dlg = $('bhd-dialog');
  const input = $('bhd-date');
  setText('bhd-title', title);
  setText('bhd-body', body);
  $('bhd-body').hidden = !body;
  input.min = min;
  input.max = ymd(new Date());
  input.value = value;
  setText('bhd-error', '');
  const okBtn = $('bhd-ok');
  okBtn.textContent = ok;
  okBtn.className = `btn primary${danger ? ' danger' : ''}`;
  return new Promise((resolve) => {
    const done = (v) => {
      okBtn.onclick = $('bhd-cancel').onclick = dlg.onclose = null;
      if (dlg.open) dlg.close();
      resolve(v);
    };
    okBtn.onclick = () => {
      if (!input.value || input.value < input.min || input.value > input.max) {
        setText('bhd-error', `Pick a day from ${date(day(input.min))} to today.`);
        return;
      }
      done(input.value);
    };
    $('bhd-cancel').onclick = () => done(null);
    dlg.onclose = () => done(null);
    dlg.showModal();
  });
}
