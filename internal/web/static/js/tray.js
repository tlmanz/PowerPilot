// The menu-bar panel (macOS): the latest reading at a glance. It uses the
// dashboard's data sources (?source=mock&scenario=battery to preview an
// outage) and asks the app, through WebKit's "powerpilot" message handler, to
// open the dashboard, post a summary notification, quit, or fit the panel.
import { emptyState } from './state.js';
import { createLiveSource } from './sources/live.js';
import { createMockSource } from './sources/mock.js';

const params = new URLSearchParams(location.search);
const source = params.get('source') === 'mock'
  ? createMockSource({ scenario: params.get('scenario') || 'cycle' })
  : createLiveSource();
const native = window.webkit?.messageHandlers?.powerpilot;
const post = (msg) => native?.postMessage(msg);

const $ = (id) => document.getElementById(id);
const setText = (id, text) => { $(id).textContent = text; };

let state = emptyState();
let linkUp = true;

const pct = (v) => `${v.toFixed(1)}%`;
const volts = (v) => `${v.toFixed(1)} V`;
const watts = (v) => `${Math.round(v).toLocaleString('en-US')} W`;

// Minutes as the service's notifications say them: "12 min", "1 h 5 min".
function duration(min) {
  const m = Math.floor(min);
  if (m < 1) return 'under a minute';
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60).toLocaleString('en-US');
  return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
}

// tone is the panel's color: good on mains, warn on battery, bad for alerts.
function tone(s) {
  if (!linkUp || !s.connected) return 'off';
  if (s.mode === 'fault' || s.battery.state === 'low' || s.output.loadPercent >= 100) return 'bad';
  if (['battery', 'test', 'standby', 'bypass'].includes(s.mode)) return 'warn';
  return 'good';
}

// headline and sub say what's happening, like the tray icon's tooltip.
function headline(s) {
  if (!linkUp) return ['Service not running', 'Start PowerPilot to see the UPS'];
  if (!s.connected) return ['UPS not connected', s.error || 'Waiting for the UPS'];
  const b = s.battery;
  if (s.mode === 'fault') return ['UPS fault', 'Check the UPS and its display'];
  if (s.output.loadPercent >= 100) return ['UPS overloaded', 'Equipment draws more than the UPS is rated for'];
  if (b.state === 'low') return ['Battery low', `Below your ${b.lowThreshold}% alert level`];
  if (s.mode === 'test') return ['Self-test running', 'The UPS is briefly on battery'];
  if (s.mode === 'standby') return ['Outlets off', 'The UPS has turned its outlets off'];
  if (s.mode === 'battery') {
    const title = s.onBatterySec >= 60 ? `On battery for ${duration(s.onBatterySec / 60)}` : 'On battery';
    return [title, 'Mains power is out'];
  }
  if (s.mode === 'bypass') return ['On bypass', 'Mains passes straight through'];
  return ['On mains power', b.state === 'charging' ? 'Battery charging' : b.state === 'full' ? 'Battery fully charged' : 'Protected'];
}

function render() {
  const s = state;
  const t = tone(s);
  const b = s.battery;
  document.body.classList.toggle('stale', !linkUp);

  setText('device', s.device.name && s.device.name !== 'UPS' ? s.device.name : 'UPS monitor');
  const pill = $('pill');
  pill.className = `pill ${t}`;
  pill.textContent = !linkUp ? 'Offline' : s.connected ? s.status : 'Disconnected';

  // The battery ring.
  const ring = $('ring');
  ring.className = `ring ${t}`;
  const c = 2 * Math.PI * 52;
  const level = s.connected && b.percent != null ? b.percent : 0;
  // A full ring is drawn whole: meeting round caps would leave a seam.
  $('ring-value').setAttribute('stroke-dasharray', level >= 100 ? 'none' : `${(c * level) / 100} ${c}`);
  $('ring-value').style.opacity = level > 0 ? 1 : 0; // a round cap would leave a dot
  const charge = s.connected && b.percent != null;
  setText('pct', charge ? b.percent.toFixed(1) : '—');
  $('pct-unit').hidden = !charge;

  const [title, sub] = headline(s);
  setText('headline', title);
  setText('sub', sub);
  // No runtime to speak of while the UPS isn't powering anything.
  const runtime = s.connected && b.runtimeMinutes != null && !['fault', 'standby'].includes(s.mode);
  $('runtime').hidden = !runtime;
  if (runtime) {
    setText('runtime-text', s.mode === 'battery'
      ? `About ${duration(b.runtimeMinutes)} left`
      : `${duration(b.runtimeMinutes)} of backup at this load`);
  }

  // The banner: the alarm, an outage, or a lost connection.
  const alarm = s.alarm;
  let banner = null;
  if (!linkUp) banner = ['off', "Can't reach the PowerPilot service. Retrying…"];
  else if (s.connected && alarm.active) banner = [alarm.level === 'critical' ? 'bad' : 'warn', alarm.muted ? `${alarm.message} · alarm muted` : alarm.message];
  else if (s.shutdownPending) banner = ['warn', 'The UPS will turn its outlets off shortly'];
  $('banner').hidden = !banner;
  if (banner) {
    $('banner').className = `banner ${banner[0]}`;
    setText('banner-text', banner[1]);
  }
  $('mute').hidden = !(linkUp && s.connected && alarm.active && alarm.enabled && !alarm.muted);

  // The readings.
  const o = s.output, i = s.input;
  const on = linkUp && s.connected;
  setText('load', on && o.loadPercent != null ? pct(o.loadPercent) : '—');
  setText('load-sub', on && o.watts != null ? `${watts(o.watts)} of ${watts(s.device.ratedW)}` : ' ');
  const bar = $('load-bar');
  bar.style.width = `${on ? Math.min(o.loadPercent || 0, 100) : 0}%`;
  bar.className = o.loadPercent >= 90 ? 'bad' : o.loadPercent >= 70 ? 'warn' : '';

  const mains = on && i.voltage > 20;
  $('input-icon').setAttribute('href', on && !mains ? '#i-plug-off' : '#i-plug');
  setText('input', on && i.voltage != null ? volts(i.voltage) : '—');
  setText('input-sub', !on ? ' ' : mains && i.frequency != null ? `${i.frequency.toFixed(1)} Hz` : 'No mains power');
  setText('output', on && o.voltage != null ? volts(o.voltage) : '—');
  setText('output-sub', !on ? ' '
    : s.mode === 'standby' ? 'Outlets off'
    : o.voltage < 20 ? 'No output'
    : s.avr ? 'Voltage regulated (AVR)' : 'To your equipment');
  setText('battv', on && b.voltage != null ? volts(b.voltage) : '—');
  setText('battv-sub', on && b.nominalVoltage ? `${b.nominalVoltage.toFixed(0)} V pack` : ' ');

  const time = s.updatedAt ? s.updatedAt.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit', second: '2-digit' }) : '';
  setText('updated', [time && `Updated ${time}`, s.device.appVersion].filter(Boolean).join(' · '));
}

$('mute').addEventListener('click', async (e) => {
  const btn = e.currentTarget;
  btn.disabled = true;
  try {
    await source.command('alarm.mute');
  } catch (err) {
    setText('banner-text', err.message);
  } finally {
    btn.disabled = false;
  }
});
$('summary').addEventListener('click', () => post({ action: 'summary' }));
$('open').addEventListener('click', () => (native ? post({ action: 'open' }) : open('/', '_blank')));
$('quit').addEventListener('click', () => post({ action: 'quit' }));
// Outside the app (a browser preview) there's nothing to ask for these.
$('summary').hidden = $('quit').hidden = !native && !params.has('preview');

// The panel fits its content: the popover takes the page's height.
let lastHeight = 0;
new ResizeObserver(() => {
  const h = Math.ceil($('panel').getBoundingClientRect().height);
  if (h !== lastHeight) post({ action: 'size', height: (lastHeight = h) });
}).observe($('panel'));

render();
source.start({
  state: (next) => { state = next; render(); },
  history: () => {},
  events: () => {},
  link: (ok) => { linkUp = ok; render(); },
});
