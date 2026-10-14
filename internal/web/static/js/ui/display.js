// UPS visualization: the static product image with a live HTML display
// overlaid on its glass panel (see .lcd in style.css for the placement).
import { $, num, setIcon, setText, tween } from './core.js';

const MODE_TEXT = {
  ac: ['AC MODE', 'ONLINE'],
  battery: ['BATTERY MODE', 'ON BATTERY'],
  bypass: ['BYPASS', 'ONLINE'],
  fault: ['FAULT', 'CHECK UPS'],
  standby: ['OUTLETS OFF', 'STANDBY'],
  test: ['SELF-TEST', 'TESTING'],
  offline: ['NO SIGNAL', 'DISCONNECTED'],
};

/** Lights n of the five segments in a bar. */
function segments(el, value, tone) {
  const lit = value == null ? 0 : Math.max(value > 0 ? 1 : 0, Math.ceil(value / 20));
  el.dataset.tone = tone;
  [...el.children].forEach((seg, i) => seg.classList.toggle('on', i < lit));
}

function iconState(id, state, name) {
  const el = $(id);
  el.dataset.state = state;
  if (name) setIcon(el.querySelector('svg'), name);
}

export function updateUPSDisplay(state) {
  const lcd = $('lcd');
  const { mode, alarm, battery, output, input } = state;
  lcd.dataset.mode = mode;
  lcd.dataset.level = alarm.level;
  lcd.dataset.connected = String(state.connected);

  const on = state.connected;
  tween($('lcd-in'), on ? input.voltage : null, (v) => num(v), '---');
  tween($('lcd-out'), on ? output.voltage : null, (v) => num(v), '---');
  tween($('lcd-batt'), on ? battery.percent : null, (v) => num(v, 1), '--');
  tween($('lcd-load'), on ? output.loadPercent : null, (v) => num(v, 1), '--');
  tween($('lcd-temp'), on ? state.temperature : null, (v) => num(v), '--');
  $('lcd-temp-field').hidden = state.temperature == null;

  const pctTone = battery.percent == null ? 'off' : battery.state === 'low' || battery.percent < battery.lowThreshold ? 'bad' : battery.percent < 50 ? 'warn' : 'good';
  segments($('lcd-batt-segs'), on ? battery.percent : null, pctTone);
  $('lcd-batt-segs').classList.toggle('charging', on && battery.state === 'charging');
  const loadTone = output.loadPercent >= 100 ? 'bad' : output.loadPercent >= 80 ? 'warn' : 'good';
  segments($('lcd-load-segs'), on ? output.loadPercent : null, loadTone);
  setText('lcd-charge-dir', !on ? '' : battery.state === 'charging' ? 'CHG ▲' : battery.state === 'discharging' || battery.state === 'low' ? 'DIS ▼' : 'FULL');

  // Status icons: power, mains, battery, alarm.
  const powerTone = { ac: 'good', bypass: 'warn', battery: 'warn', test: 'info', fault: 'bad' }[mode] || 'off';
  iconState('lcd-ico-power', on ? powerTone : 'off');
  const mains = on && mode !== 'battery' && (input.voltage || 0) > 0;
  iconState('lcd-ico-ac', mains ? 'good' : on ? 'bad' : 'off');
  iconState('lcd-ico-batt', !on ? 'off' : mode === 'battery' || mode === 'test' ? 'warn' : battery.state === 'charging' ? 'info' : 'dim');
  if (alarm.muted) iconState('lcd-ico-alarm', 'dim', 'bell-off');
  else if (alarm.active) iconState('lcd-ico-alarm', alarm.level === 'critical' ? 'bad blink' : 'warn blink', 'alert');
  else iconState('lcd-ico-alarm', on && !alarm.enabled ? 'dim' : 'off', on && !alarm.enabled ? 'bell-off' : 'alert');

  const [modeText, statusText] = MODE_TEXT[on ? mode : 'offline'] || MODE_TEXT.offline;
  setText('lcd-mode', modeText);
  setText('lcd-status', alarm.active && on ? alarm.message.toUpperCase() : statusText);

  // Device details.
  const d = state.device;
  setText('ui-rating', d.ratedVA ? `${num(d.ratedVA)} VA / ${num(d.ratedW)} W` : '—');
  setText('ui-nominal', d.nominalVoltage ? `${num(d.nominalVoltage)} V · ${num(d.nominalFrequency)} Hz${battery.nominalVoltage ? ` · ${num(battery.nominalVoltage)} V battery` : ''}` : '—');
  setText('ui-type', d.type || '—');
  setText('ui-conn', d.connection || '—');
}
