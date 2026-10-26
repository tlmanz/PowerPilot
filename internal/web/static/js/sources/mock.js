// Mock data source: a simulated UPS with the same interface as the live source.
// Open the dashboard with ?source=mock (optionally &scenario=ac|battery|bypass|fault).
// By default it cycles through mains, a 50-second outage and recovery.
import { deriveAlarm, emptyState, healthFrom, statusLabel } from '../state.js';

const RATED_W = 1200;
const PACK_V = 24;
const BATTERY_AH = 9;
const DAY = 86400e3;

// Same Peukert estimate the service uses, so both sources look alike.
function runtime(chargePct, loadW, ah = BATTERY_AH) {
  if (loadW <= 0) return null;
  const amps = loadW / (PACK_V * 0.85);
  const hours = 20 * Math.pow(ah / (amps * 20), 1.15);
  return Math.min((hours * 60 * chargePct) / 100, 480);
}

const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
const walk = (v, step, lo, hi) => clamp(v + (Math.random() - 0.5) * 2 * step, lo, hi);

export function createMockSource({ scenario = 'cycle', interval = 2000 } = {}) {
  let on = null;
  const started = Date.now();
  const sim = {
    input: 231, output: 230, load: 42, battery: 78, temp: 32, fan: 1200, pf: 0.84,
    beeper: true, muted: false, testLeft: 0, shutdownIn: null, restoreIn: null, outletsOff: false,
    mode: null, alarmLevel: 'none', lowNotified: false, lastTest: null,
    settings: { low_battery_percent: 20, notify: true, shutdown_mac: false, shutdown_after_minutes: 5, battery_ah: BATTERY_AH, log_retention_days: 90 },
  };
  // Battery health as powerpilot-server reports it, with the mobile demo's numbers.
  const battery = { installed: new Date(Date.now() - 1170 * DAY).toISOString().slice(0, 10), autoTest: true, replacedAt: null, report: null };

  function batteryReport() {
    const now = Date.now();
    const ageDays = battery.installed ? Math.max(0, Math.floor((now - new Date(`${battery.installed}T00:00`)) / DAY)) : undefined;
    const known = { installed: battery.installed || undefined, age_days: ageDays, battery_ah: sim.settings.battery_ah, auto_test: battery.autoTest };
    if (battery.replacedAt) {
      return { ...known, counting_since: battery.replacedAt.toISOString(), cycles: 0, ah: 0, outages: 0, battery_seconds: 0, tests: [], health: 'unknown', reasons: ['No self-test yet.'] };
    }
    const test = (at, low) => ({ time: new Date(at).toISOString(), result: 'passed', seconds: 10, load: 9, volts_before: 27.1, volts_low: low });
    const tests = [];
    if (sim.lastTest && sim.lastTest.result === 'passed' && sim.lastTest.ended) tests.push(test(sim.lastTest.ended, 25.3));
    for (const [ago, low] of [[5, 25.3], [35, 25.5], [65, 25.6], [95, 25.8], [125, 25.8], [155, 25.9]]) tests.push(test(now - ago * DAY, low));
    const baseline = tests[tests.length - 1];
    const drop = (t) => (t.volts_before - t.volts_low).toFixed(1);
    return {
      ...known, counting_since: new Date(now - 160 * DAY).toISOString(),
      cycles: 1.32, ah: 1.32 * sim.settings.battery_ah, outages: 4, battery_seconds: 3720,
      deepest_charge: 62, deepest_at: new Date(now - 40 * DAY).toISOString(),
      baseline, tests, health: 'check',
      reasons: [
        `The voltage drop during a self-test has grown from ${drop(baseline)} V to ${drop(tests[0])} V.`,
        ...(ageDays >= 1095 ? [`The battery is ${(ageDays / 365).toFixed(1)} years old.`] : []),
      ],
    };
  }

  async function reportBattery(change) {
    if (change) {
      await new Promise((r) => setTimeout(r, 250));
      change();
    }
    battery.report = batteryReport();
    step();
    return battery.report;
  }

  function event(kind, level, message) {
    on.events([{ time: new Date(), kind, level, message }]);
  }

  function scenarioMode(elapsed) {
    if (scenario !== 'cycle') return { ac: 'ac', battery: 'battery', bypass: 'bypass', fault: 'fault' }[scenario] || 'ac';
    const t = elapsed % 240;
    return t >= 150 && t < 200 ? 'battery' : 'ac';
  }

  function step() {
    const dt = interval / 1000;
    if (sim.shutdownIn != null) {
      sim.shutdownIn -= dt;
      if (sim.shutdownIn <= 0) {
        sim.shutdownIn = null;
        sim.outletsOff = true;
        event('shutdown', 'warn', 'Outlets turned off');
      }
    }
    if (sim.outletsOff && sim.restoreIn != null) {
      sim.restoreIn -= dt;
      if (sim.restoreIn <= 0) {
        sim.restoreIn = null;
        sim.outletsOff = false;
        event('shutdown', 'good', 'Outlets turned back on');
      }
    }
    if (sim.testLeft > 0) {
      sim.testLeft -= dt;
      if (sim.testLeft <= 0) {
        sim.testLeft = 0;
        sim.lastTest = { ...sim.lastTest, result: 'passed', ended: new Date().toISOString() };
        event('self_test', 'good', 'UPS self-test completed (passed)');
        if (battery.report) battery.report = batteryReport();
      }
    }

    const base = scenarioMode((Date.now() - started) / 1000);
    const mode = sim.outletsOff ? 'standby' : sim.testLeft > 0 && base === 'ac' ? 'test' : base;
    const onBattery = mode === 'battery';
    const mainsPresent = mode !== 'battery';

    sim.input = mainsPresent ? walk(sim.input, 0.8, 224, 238) : 0;
    sim.output = mode === 'standby' || mode === 'fault' ? 0 : walk(sim.output, 0.4, 228.5, 231.5);
    sim.load = mode === 'standby' || mode === 'fault' ? 0 : walk(sim.load, 1.5, 30, 55);
    sim.battery = onBattery || mode === 'test' ? clamp(sim.battery - 0.35, 0, 100) : clamp(sim.battery + 0.08, 0, 100);
    sim.temp = walk(sim.temp + (sim.load - 42) * 0.002, 0.15, 28, 38);
    sim.fan = Math.round(walk(1000 + (sim.temp - 28) * 40, 15, 900, 1500));
    sim.pf = walk(sim.pf, 0.005, 0.8, 0.9);

    // Transitions → events, like the service raises.
    if (mode !== sim.mode) {
      if (mode === 'battery') {
        sim.batterySince = Date.now();
        event('utility_lost', 'alert', 'Utility power lost');
        event('on_battery', 'warn', 'Running on battery');
      } else if (sim.mode === 'battery') {
        event('utility_restored', 'good', 'Utility power restored');
        event('battery_charging', 'info', 'Battery charging');
        sim.lowNotified = false;
      }
      if (mode === 'fault') event('fault', 'alert', 'The UPS reports a fault');
      if (mode === 'bypass') event('info', 'warn', 'UPS switched to bypass');
      sim.mode = mode;
    }
    const low = onBattery && sim.battery < sim.settings.low_battery_percent;
    if (low && !sim.lowNotified) {
      sim.lowNotified = true;
      event('low_battery', 'alert', 'Battery low');
    }
    const overload = sim.load >= 100;
    const alarm = deriveAlarm({ onBattery, batteryLow: low, fault: mode === 'fault', overload, enabled: sim.beeper || sim.muted, muted: sim.muted });
    if (sim.muted && !alarm.active) {
      sim.muted = false;
      sim.beeper = true;
      alarm.muted = false;
      event('alarm_unmuted', 'info', 'Alarm unmuted: the alarm condition cleared');
    }

    on.state(buildState(mode, alarm, low));
  }

  function buildState(mode, alarm, low) {
    const s = emptyState();
    const watts = (sim.load / 100) * RATED_W;
    s.source = 'mock';
    s.connected = true;
    s.updatedAt = new Date();
    s.device = {
      name: 'Prolink PRO2000SFCU', brand: 'Prolink', model: 'PRO2000SFCU',
      connection: 'Mock data', protocol: 'Simulated', type: 'Line-interactive',
      ratedVA: 2000, ratedW: RATED_W, nominalVoltage: 230, nominalFrequency: 50,
    };
    s.mode = mode;
    s.status = statusLabel(mode);
    s.onBatterySec = mode === 'battery' ? (Date.now() - sim.batterySince) / 1000 : null;
    s.input = { voltage: sim.input, frequency: mode === 'battery' ? null : 50 + (Math.random() - 0.5) * 0.2 };
    s.output = { voltage: sim.output, loadPercent: sim.load, watts, va: watts / sim.pf, powerFactor: sim.pf };
    const charging = mode !== 'battery' && mode !== 'test' && sim.battery < 99.5;
    s.battery = {
      percent: sim.battery, voltage: 21 + sim.battery * 0.055, nominalVoltage: PACK_V,
      state: low ? 'low' : mode === 'battery' || mode === 'test' ? 'discharging' : charging ? 'charging' : 'full',
      health: healthFrom(sim.lastTest, battery.report, sim.testLeft > 0),
      runtimeMinutes: runtime(sim.battery, Math.max(watts, 12), sim.settings.battery_ah),
      lowThreshold: sim.settings.low_battery_percent, lastTest: sim.lastTest, testRunning: sim.testLeft > 0,
      report: battery.report,
    };
    s.temperature = sim.temp;
    s.fanSpeed = sim.fan;
    s.alarm = alarm;
    s.shutdownPending = sim.shutdownIn != null;
    s.settings = sim.settings;
    s.runtimeCurve = Array.from({ length: 20 }, (_, i) => {
      const load = (i + 1) * 5;
      return { load, minutes: runtime(sim.battery, (load / 100) * RATED_W, sim.settings.battery_ah) };
    });
    return s;
  }

  function seedHistory() {
    const now = Date.now();
    return Array.from({ length: 120 }, (_, i) => {
      const t = now - (120 - i) * 30000;
      const k = i / 120;
      return {
        t,
        inputVoltage: 231 + Math.sin(k * 9) * 2.5 + (Math.random() - 0.5),
        outputVoltage: 230 + (Math.random() - 0.5) * 0.8,
        load: 40 + Math.sin(k * 5) * 6 + (Math.random() - 0.5) * 3,
        battery: Math.min(100, 70 + k * 8),
        temperature: 31 + Math.sin(k * 4) * 1.5,
      };
    });
  }

  const commands = {
    'beeper.on': () => { sim.beeper = true; sim.muted = false; event('alarm', 'info', 'UPS alarm turned on'); },
    'beeper.off': () => { sim.beeper = false; sim.muted = false; event('alarm', 'info', 'UPS alarm turned off'); },
    'alarm.mute': () => {
      if (!['battery', 'fault'].includes(sim.mode)) throw new Error('No alarm is sounding; use UPS Alarm to turn it off');
      sim.beeper = false;
      sim.muted = true;
      event('alarm_muted', 'info', 'Alarm muted until the current condition clears');
    },
    'alarm.unmute': () => { sim.beeper = true; sim.muted = false; event('alarm_unmuted', 'info', 'Alarm unmuted'); },
    'test.quick': () => {
      if (sim.mode !== 'ac') throw new Error('Self-tests can only run on mains power');
      sim.testLeft = 10;
      sim.lastTest = { kind: 'test.quick', started: new Date().toISOString(), result: 'running' };
      event('self_test', 'info', 'Started a 10-second battery self-test');
    },
    'test.cancel': () => {
      sim.testLeft = 0;
      sim.lastTest = { ...sim.lastTest, result: 'cancelled' };
      event('self_test', 'info', 'Cancelled the battery self-test');
    },
    shutdown: (p) => {
      sim.shutdownIn = p.minutes * 60;
      sim.restoreIn = p.restore_after ? p.restore_after * 60 : null;
      event('shutdown', 'warn', `Outlets will turn off in ${p.minutes} min${p.restore_after ? ` and restart after ${p.restore_after} min` : ' and stay off'}`);
    },
    'shutdown.cancel': () => {
      sim.shutdownIn = null;
      sim.outletsOff = false;
      event('shutdown', 'info', 'Cancelled outlet shutdown / turned outlets on');
    },
  };

  return {
    name: 'mock',
    start(handlers) {
      on = handlers;
      on.history(seedHistory());
      on.events([{ time: new Date(), kind: 'connection', level: 'good', message: 'Connected to the mock UPS' }], { replace: true });
      on.link(true);
      battery.report = batteryReport();
      step();
      setInterval(step, interval);
    },
    async command(id, params = {}) {
      if (!commands[id]) throw new Error(`Unknown command ${id}`);
      commands[id](params);
      step();
      return { ok: true, sent: '(mock)' };
    },
    async saveSettings(next) {
      sim.settings = { ...next };
      event('info', 'info', 'Settings updated');
      step();
      return sim.settings;
    },
    async refresh() {
      step();
    },
    battery: {
      load: () => reportBattery(),
      save: (body) => reportBattery(() => {
        battery.installed = body.installed || '';
        battery.autoTest = !!body.auto_test;
      }),
      replaced: (installed) => reportBattery(() => {
        battery.installed = installed;
        battery.replacedAt = new Date();
      }),
    },
  };
}
