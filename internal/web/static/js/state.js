// The single UPS state object every UI component renders from. Data sources
// (the live service, the mock generator, or anything added later) produce this
// shape; components never read source-specific data.

/** @returns {UPSState} */
export function emptyState() {
  return {
    source: 'live',
    connected: false,
    error: '',
    updatedAt: null, // Date
    device: {
      name: 'UPS', brand: '', model: '', connection: '', protocol: '', type: '',
      ratedVA: 0, ratedW: 0, nominalVoltage: null, nominalFrequency: null, appVersion: '',
    },
    // ac | battery | bypass | fault | standby (outlets off) | test | offline
    mode: 'offline',
    status: 'Disconnected',
    onBatterySec: null, // how long the current outage has lasted
    input: { voltage: null, frequency: null },
    output: { voltage: null, loadPercent: null, watts: null, va: null, powerFactor: null },
    battery: {
      percent: null, voltage: null, nominalVoltage: null,
      state: 'unknown', // charging | discharging | full | low | unknown
      health: 'unknown', // good | check | replace | testing | unknown
      runtimeMinutes: null, lowThreshold: 20, lastTest: null, testRunning: false,
      report: null, // powerpilot-server's battery health, once loaded (GET /api/battery)
    },
    temperature: null, // °C; null when the UPS doesn't report it
    fanSpeed: null, // RPM
    avr: false,
    // enabled: beeper on; muted: silenced until the condition clears
    alarm: { enabled: false, muted: false, active: false, level: 'none', message: '' },
    shutdownPending: false,
    runtimeCurve: [], // [{load, minutes}]
    settings: null,
  };
}

const STATUS = {
  ac: 'Online', battery: 'On Battery', bypass: 'Bypass', fault: 'Fault',
  standby: 'Outlets Off', test: 'Self-test', offline: 'Disconnected',
};

export function statusLabel(mode) {
  return STATUS[mode] || mode;
}

/** Derives alarm level and message from the readings. */
export function deriveAlarm({ onBattery, batteryLow, fault, overload, enabled, muted }) {
  let level = 'none', message = '';
  if (fault) [level, message] = ['critical', 'UPS fault'];
  else if (batteryLow) [level, message] = ['critical', 'Battery low'];
  else if (overload) [level, message] = ['critical', 'Overload'];
  else if (onBattery) [level, message] = ['warning', 'Running on battery'];
  return { enabled, muted, active: level !== 'none', level, message };
}

/** The Health badge: the server's verdict once its report is loaded, else the last self-test. */
export function healthFrom(lastTest, report, testRunning = false) {
  if (testRunning || (lastTest && lastTest.result === 'running')) return 'testing';
  if (report) return ['good', 'check', 'replace'].includes(report.health) ? report.health : 'unknown';
  if (!lastTest) return 'unknown';
  return { passed: 'good', failed: 'replace' }[lastTest.result] || 'unknown';
}

/** Maps the Go service's /api/state (or a stream update) to the UPS state. */
export function fromLive(device, snap, settings, lastTest, report = null) {
  const s = emptyState();
  const st = snap && snap.connected ? snap.status : null;
  const r = snap && snap.ratings;
  s.device = {
    name: `${device.brand} ${device.model}`.trim(),
    brand: device.brand, model: device.model,
    connection: device.connection, protocol: device.protocol,
    type: st ? (st.line_interactive ? 'Line-interactive' : 'Online') : '',
    ratedVA: device.rated_va, ratedW: device.rated_w,
    nominalVoltage: r ? r.voltage : null, nominalFrequency: r ? r.frequency : null,
    appVersion: device.app_version || '',
  };
  s.settings = settings;
  s.updatedAt = snap && snap.updated_at && !snap.updated_at.startsWith('0001') ? new Date(snap.updated_at) : null;
  s.battery.lowThreshold = settings ? settings.low_battery_percent : 20;
  s.battery.lastTest = lastTest || null;
  s.battery.report = report;
  s.battery.health = healthFrom(lastTest, report, !!(st && st.test_running));
  if (!st) {
    s.error = (snap && snap.error) || '';
    return s;
  }

  s.connected = true;
  // A set AVR bit means bypass on online UPSes but boost/buck on line-interactive ones.
  const mode = { line: 'ac', battery: 'battery', test: 'test', fault: 'fault', standby: 'standby' }[snap.mode] || 'ac';
  s.mode = mode === 'ac' && st.avr_active && !st.line_interactive ? 'bypass' : mode;
  s.status = statusLabel(s.mode);
  s.onBatterySec = st.on_battery ? snap.on_battery_sec ?? 0 : null;
  s.avr = st.avr_active && st.line_interactive;
  s.input = { voltage: st.input_voltage, frequency: st.frequency };
  s.output = {
    voltage: st.output_voltage, loadPercent: st.load_percent,
    watts: snap.load_w, va: snap.load_va, powerFactor: null,
  };
  s.battery = {
    ...s.battery,
    percent: snap.charge ?? null,
    voltage: st.battery_voltage,
    nominalVoltage: r ? r.battery_voltage : null,
    state: snap.battery_state || 'unknown',
    runtimeMinutes: snap.runtime_min ?? null,
    testRunning: st.test_running,
  };
  s.temperature = st.temperature ?? null;
  s.alarm = deriveAlarm({
    onBattery: st.on_battery, batteryLow: st.battery_low || snap.battery_state === 'low',
    fault: st.fault, overload: st.load_percent >= 100,
    enabled: st.beeper_on || !!snap.alarm_muted, muted: !!snap.alarm_muted,
  });
  s.shutdownPending = st.shutdown_active;
  s.runtimeCurve = (snap.runtime_curve || []).map((p) => ({ load: p.load, minutes: p.min }));
  return s;
}

/** Maps one history sample to a chart point. */
export function fromLiveSample(p) {
  return {
    t: p.t, inputVoltage: p.in, outputVoltage: p.out, load: p.load,
    battery: p.charge ?? null, temperature: null,
  };
}

export function fromLiveEvent(e) {
  return { time: new Date(e.time), kind: e.kind || 'info', level: e.level, message: e.message };
}

/** Chart point for the current state. */
export function pointFromState(s) {
  return {
    t: s.updatedAt ? s.updatedAt.getTime() : Date.now(),
    inputVoltage: s.input.voltage, outputVoltage: s.output.voltage, load: s.output.loadPercent,
    battery: s.battery.percent, temperature: s.temperature,
  };
}
