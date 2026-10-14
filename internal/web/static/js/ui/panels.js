// Header, live metrics, battery status, power flow and alarm controls.
import { $, badge, dateTime, duration, num, pct, setText, tween } from './core.js';

const NOT_REPORTED = 'Not reported by this UPS';

export function updateHeader(state, linkUp) {
  setText('h-name', state.device.name || 'UPS');
  setText('h-app', `PowerPilot${state.device.appVersion ? ` ${state.device.appVersion}` : ''}`);
  const conn = $('h-conn');
  const [cls, text] = !linkUp ? ['off', 'Service unreachable'] : state.connected ? ['on', 'Connected'] : ['off', 'Disconnected'];
  conn.className = `conn ${cls}`;
  setText(conn, text);
  setText('h-updated', state.updatedAt ? dateTime(state.updatedAt, true) : '—');
  $('h-source').hidden = state.source !== 'mock';

  const banner = $('banner');
  if (linkUp && !state.connected) {
    banner.className = 'banner bad';
    setText(banner, `UPS not connected${state.error ? `: ${state.error}` : ''}. Check the USB cable.`);
    banner.hidden = false;
  } else if (state.connected && state.alarm.active) {
    banner.className = `banner ${state.alarm.level === 'critical' ? 'bad' : 'warn'}`;
    setText(banner, `${state.alarm.message}.${state.alarm.muted ? ' Alarm muted.' : ''}`);
    banner.hidden = false;
  } else {
    banner.hidden = true;
  }
}

function metric(key, { value, format, unit, sub, tone, placeholder = '—' }) {
  tween($(`m-${key}`), value, format, placeholder);
  setText(`m-${key}-unit`, value == null ? '' : unit);
  setText(`m-${key}-sub`, sub);
  $(`mt-${key}`).dataset.tone = tone;
}

const BATTERY_WORDS = { charging: 'Charging', discharging: 'Discharging', full: 'Fully charged', low: 'Low', unknown: 'Unknown' };

/** Environment: the readings no other card shows. */
export function updateEnvironment(state) {
  const on = state.connected;
  metric('temp', {
    value: on ? state.temperature : null, format: (v) => num(v, 1), unit: '°C',
    sub: on && state.temperature == null ? NOT_REPORTED : on ? (state.temperature >= 40 ? 'High' : 'Normal') : 'No data',
    tone: !on || state.temperature == null ? 'off' : state.temperature >= 40 ? 'warn' : 'good',
  });
  metric('fan', {
    value: on ? state.fanSpeed : null, format: (v) => num(v), unit: 'RPM',
    sub: on && state.fanSpeed == null ? NOT_REPORTED : on ? 'Normal' : 'No data',
    tone: !on || state.fanSpeed == null ? 'off' : 'good',
  });
}

const CIRC = 2 * Math.PI * 52;

export function updateBattery(battery, state = {}) {
  const on = state.connected !== false && battery.percent != null;
  const fill = $('bt-ring').querySelector('.fill');
  const frac = on ? Math.max(0, Math.min(1, battery.percent / 100)) : 0;
  fill.setAttribute('stroke-dasharray', CIRC.toFixed(2));
  fill.setAttribute('stroke-dashoffset', (CIRC * (1 - frac)).toFixed(2));
  fill.style.visibility = frac > 0 ? 'visible' : 'hidden';
  fill.style.stroke = battery.state === 'low' ? 'var(--critical)' : on && battery.percent < 50 ? 'var(--warning)' : 'var(--good)';
  $('bt-ring').classList.toggle('charging', on && battery.state === 'charging');
  tween($('bt-pct'), on ? battery.percent : null, (v) => pct(v));
  setText('bt-pct-label', on ? 'Remaining' : 'No data');

  const stateTone = { charging: 'info', discharging: 'warn', full: 'good', low: 'bad' }[battery.state] || 'muted';
  badge($('bt-state'), stateTone, on ? BATTERY_WORDS[battery.state] || 'Unknown' : '—');
  $('bt-state-icon').dataset.state = battery.state;
  setText('bt-volt', battery.voltage != null ? `${num(battery.voltage, 1)} V${battery.nominalVoltage ? ` of ${num(battery.nominalVoltage)} V` : ''}` : '—');
  const health = {
    good: ['good', 'Good'], replace: ['bad', 'Replace soon'], testing: ['info', 'Testing…'], unknown: ['muted', 'Run a self-test'],
  }[battery.health] || ['muted', 'Unknown'];
  badge($('bt-health'), health[0], health[1]);
  setText('bt-runtime', on ? duration(battery.runtimeMinutes) : '—');
  setText('bt-test', battery.lastTest && battery.lastTest.started ? `Last test ${dateTime(new Date(battery.lastTest.started))}` : 'No self-test recorded');
}

/** Load card: gauge plus power figures (estimated from load % and the rating). */
export function updateLoad(output, state = {}) {
  const on = state.connected !== false && output.loadPercent != null;
  const device = state.device || {};
  const load = on ? output.loadPercent : null;
  const fill = $('ld-ring').querySelector('.fill');
  const frac = on ? Math.max(0, Math.min(1, load / 100)) : 0;
  fill.setAttribute('stroke-dasharray', CIRC.toFixed(2));
  fill.setAttribute('stroke-dashoffset', (CIRC * (1 - frac)).toFixed(2));
  fill.style.visibility = frac > 0 ? 'visible' : 'hidden';
  fill.style.stroke = load >= 100 ? 'var(--critical)' : load >= 80 ? 'var(--warning)' : 'var(--series)';
  tween($('ld-pct'), load, (v) => pct(v));
  setText('ld-w', on && output.watts != null ? `≈ ${num(output.watts)} W` : '—');
  setText('ld-va', on && output.va != null ? `≈ ${num(output.va)} VA` : '—');
  setText('ld-pf', !on ? '—' : output.powerFactor != null ? num(output.powerFactor, 2) : 'Not reported');
  if (!on) badge($('ld-status'), 'muted', '—');
  else if (load >= 100) badge($('ld-status'), 'bad', 'Overload');
  else if (load >= 80) badge($('ld-status'), 'warn', 'High');
  else badge($('ld-status'), 'good', 'Normal');
  setText('ld-foot', on && output.powerFactor == null ? 'Power figures are estimates from load % and the rating' : '');
}

const FLOW_TEXT = {
  ac: 'Utility power feeds the equipment through the UPS',
  battery: 'Utility power lost: the battery feeds the equipment',
  bypass: 'Bypass: utility power goes straight to the equipment',
  fault: 'UPS fault: check the unit',
  standby: 'Outlets are off',
  test: 'Self-test: running from the battery briefly',
  offline: 'No data from the UPS',
};

// UPS node status text and tone per mode.
const UPS_STATE = {
  ac: ['good', 'Online'], battery: ['warn', 'On Battery'], bypass: ['warn', 'Bypass'],
  fault: ['bad', 'Fault'], standby: ['muted', 'Outlets Off'], test: ['info', 'Self-test'], offline: ['muted', 'Offline'],
};

function fmode(id, active, text) {
  const el = $(id);
  el.classList.toggle('active', active);
  setText(el.querySelector('.fm-state'), text);
}

/** Power flow: Utility → UPS → Equipment, restyled per mode via data-mode. */
export function updatePowerFlow(mode, state = {}) {
  const on = state.connected !== false;
  const m = on ? mode : 'offline';
  $('flow').dataset.mode = m;
  const input = state.input || {};
  const output = state.output || {};
  const mainsLost = m === 'battery';

  setText('pf-in-v', on && input.voltage != null ? `${num(input.voltage)} V` : '—');
  const inState = $('pf-in-state');
  inState.dataset.tone = !on ? 'muted' : mainsLost ? 'bad' : state.avr ? 'info' : 'good';
  const hz = input.frequency != null ? ` · ${num(input.frequency, 1)} Hz` : '';
  setText(inState, !on ? '—' : mainsLost ? 'Failed' : `${state.avr ? 'Regulating' : 'Normal'}${hz}`);
  $('pf-in-icon').setAttribute('href', mainsLost ? '#i-plug-off' : '#i-plug');

  const [tone, label] = UPS_STATE[m] || UPS_STATE.offline;
  const upsState = $('pf-ups-state');
  upsState.dataset.tone = tone;
  setText(upsState, label);

  const outOff = !on || m === 'standby' || m === 'fault';
  setText('pf-out-v', on && output.voltage != null ? `${num(output.voltage)} V` : '—');
  const outState = $('pf-out-load');
  outState.dataset.tone = !on ? 'muted' : outOff ? 'muted' : 'good';
  setText(outState, !on ? '—' : outOff ? 'Off' : 'Normal');

  setText('pf-caption', FLOW_TEXT[m] || '');
  fmode('pf-mode-ac', m === 'ac' || m === 'standby', !on ? '—' : m === 'ac' ? 'Normal' : m === 'standby' ? 'Outlets off' : 'Not Active');
  fmode('pf-mode-bypass', m === 'bypass', !on ? '—' : m === 'bypass' ? 'Active' : 'Not Active');
  fmode('pf-mode-battery', m === 'battery' || m === 'test', !on ? '—' : m === 'battery' ? 'Active' : m === 'test' ? 'Self-test' : 'Not Active');
}

export function updateAlarm(alarm, state = {}) {
  const on = state.connected !== false;
  const sw = $('alarm-switch');
  if (!sw._busy) sw.checked = alarm.enabled;
  sw.disabled = !on || sw._busy;
  setText('alarm-desc', !on ? 'UPS not connected' : alarm.muted ? 'On (muted for the current alarm)' : alarm.enabled ? 'Beeps on power events' : 'Off: the UPS stays silent');

  const mute = $('mute-btn');
  mute.textContent = alarm.muted ? 'Unmute' : 'Mute';
  mute.disabled = !on || !(alarm.muted || (alarm.active && alarm.enabled));
  setText('mute-desc', !on ? 'UPS not connected'
    : alarm.muted ? 'Muted until the condition clears'
    : alarm.active ? 'Silence it until the condition clears' : 'No alarm sounding');
}
