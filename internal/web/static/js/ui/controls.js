// UPS controls, settings and the dialogs they use. Dangerous actions
// (shutdown, restart) always end in an explicit confirmation modal.
import { $, num, setText, toast } from './core.js';
import { openEventsDialog } from './events.js';
import { openLogs } from './logs.js';
import { openHistoryDialog } from './charts.js';

const THRESHOLDS = [10, 15, 20, 25, 30, 40, 50];

export function confirmDialog(title, body, okLabel, danger = false) {
  const dlg = $('confirm-dialog');
  setText('cd-title', title);
  setText('cd-body', body);
  const ok = $('cd-ok');
  ok.textContent = okLabel;
  ok.className = `btn primary${danger ? ' danger' : ''}`;
  return new Promise((resolve) => {
    const done = (v) => {
      ok.onclick = $('cd-cancel').onclick = dlg.onclose = null;
      if (dlg.open) dlg.close();
      resolve(v);
    };
    ok.onclick = () => done(true);
    $('cd-cancel').onclick = () => done(false);
    dlg.onclose = () => done(false);
    dlg.showModal();
  });
}

function fillThresholds(sel, current) {
  const values = THRESHOLDS.includes(current) ? THRESHOLDS : [...THRESHOLDS, current].sort((a, b) => a - b);
  if (sel.options.length !== values.length) {
    sel.textContent = '';
    for (const v of values) sel.append(new Option(`${v}%`, v));
  }
  if (document.activeElement !== sel) sel.value = String(current);
}

/** Button states and labels that depend on the UPS state. */
export function updateControls(state) {
  const on = state.connected;
  const cfg = state.settings;
  if (cfg) {
    fillThresholds($('low-threshold'), cfg.low_battery_percent);
    setText('shutdown-cfg-desc', cfg.shutdown_mac
      ? `Shuts this Mac down after ${cfg.shutdown_after_minutes} min on battery`
      : 'Off. Choose when to shut down this Mac');
  }
  $('low-threshold').disabled = !cfg;

  const test = $('test-btn');
  test.disabled = !on || (!state.battery.testRunning && state.mode !== 'ac');
  test.textContent = state.battery.testRunning ? 'Cancel Test' : 'Start Test';
  setText('test-desc', state.battery.testRunning ? 'Test in progress' : state.mode === 'ac' ? 'Runs on battery for 10 seconds' : 'Available on mains power only');

  const sd = $('shutdown-btn');
  sd.disabled = !on;
  if (on && (state.shutdownPending || state.mode === 'standby')) {
    sd.textContent = state.shutdownPending ? 'Cancel' : 'Turn On';
    sd.className = 'btn';
    setText('shutdown-desc', state.shutdownPending ? 'Shutdown is counting down' : 'Outlets are off');
  } else {
    sd.textContent = 'Shutdown';
    sd.className = 'btn danger';
    setText('shutdown-desc', 'Turn off connected equipment after a delay');
  }
  $('restart-btn').disabled = !on || state.shutdownPending;
}

export function wireControls(source, getState) {
  const run = async (id, params, ok) => {
    try {
      const r = await source.command(id, params);
      toast(r && r.sent ? `${ok}` : 'Already set; nothing to send');
      return true;
    } catch (err) {
      toast(err.message, true);
      return false;
    }
  };

  // Alarm on/off: the UPS beeper setting.
  const sw = $('alarm-switch');
  sw.addEventListener('change', async () => {
    const want = sw.checked;
    sw._busy = true;
    sw.disabled = true;
    const ok = await run(want ? 'beeper.on' : 'beeper.off', {}, want ? 'UPS alarm turned on' : 'UPS alarm turned off');
    if (!ok) sw.checked = !want;
    sw._busy = false;
    sw.disabled = false;
  });

  // Mute: silences the current alarm until it clears.
  $('mute-btn').addEventListener('click', () => {
    const muted = getState().alarm.muted;
    run(muted ? 'alarm.unmute' : 'alarm.mute', {}, muted ? 'Alarm unmuted' : 'Alarm muted until the condition clears');
  });

  $('low-threshold').addEventListener('change', async (e) => {
    try {
      await source.saveSettings({ ...getState().settings, low_battery_percent: Number(e.target.value) });
      toast(`Low battery alarm set to ${e.target.value}%`);
    } catch (err) {
      toast(err.message, true);
    }
  });

  // Self-test.
  $('test-btn').addEventListener('click', async () => {
    if (getState().battery.testRunning) {
      run('test.cancel', {}, 'Self-test cancelled');
      return;
    }
    if (await confirmDialog('Run a battery self-test?', 'The UPS runs on battery for 10 seconds, then returns to mains. Mains stays connected, so your equipment keeps power if the battery struggles.', 'Start Test')) {
      run('test.quick', {}, 'Self-test started');
    }
  });

  // Graceful shutdown and restart share one dialog, then a confirmation.
  const openPowerDialog = (restart) => {
    setText('sd-title', restart ? 'Restart UPS outlets' : 'Graceful shutdown');
    setText('sd-body', restart
      ? 'The outlets turn off when the delay ends, then back on after the restart delay (once mains is present).'
      : 'Connected equipment keeps power until the delay ends, giving it time to shut down.');
    document.querySelector(`input[name="sd-after"][value="${restart ? 'restart' : 'off'}"]`).checked = true;
    $('sd-restart-min').value = '1';
    setText('sd-error', '');
    $('sd-dialog').showModal();
  };
  $('shutdown-btn').addEventListener('click', async () => {
    const s = getState();
    if (s.shutdownPending) {
      run('shutdown.cancel', {}, 'Shutdown cancelled');
    } else if (s.mode === 'standby') {
      if (await confirmDialog('Turn the outlets on?', 'Power is restored to everything plugged into the UPS.', 'Turn On')) {
        run('shutdown.cancel', {}, 'Outlets turning on');
      }
    } else {
      openPowerDialog(false);
    }
  });
  $('restart-btn').addEventListener('click', () => openPowerDialog(true));
  $('sd-cancel').addEventListener('click', () => $('sd-dialog').close());
  $('sd-ok').addEventListener('click', async () => {
    const minutes = Number($('sd-delay').value);
    const restart = document.querySelector('input[name="sd-after"]:checked').value === 'restart';
    const after = restart ? Number($('sd-restart-min').value) : 0;
    if (restart && !(Number.isInteger(after) && after >= 1 && after <= 9999)) {
      setText('sd-error', 'Restart delay must be a whole number from 1 to 9,999 minutes.');
      return;
    }
    const delay = $('sd-delay').selectedOptions[0].textContent;
    $('sd-dialog').close();
    const what = restart
      ? `turn off in ${delay}, then back on after ${num(after)} min`
      : `turn off in ${delay} and stay off until you turn them on`;
    const title = restart ? 'Restart the UPS outlets?' : 'Shut down the UPS outlets?';
    if (await confirmDialog(title, `The outlets will ${what}. Everything plugged into them, including this Mac's charger, loses power.`, restart ? 'Yes, restart' : 'Yes, shut down', true)) {
      run('shutdown', { minutes, restore_after: after }, restart ? 'Restart scheduled' : 'Shutdown scheduled');
    }
  });

  // Updates (settings dialog). Only the live service can update itself.
  let updating = false;
  const showUpdate = (text, { url = '', canApply = false } = {}) => {
    setText('up-status', text);
    const notes = $('up-notes');
    notes.hidden = !url;
    if (url) notes.href = url;
    $('up-apply').hidden = !canApply;
  };
  const resetUpdate = () => {
    if (updating) return;
    const v = getState().device.appVersion;
    showUpdate(v ? `You have PowerPilot ${v}.` : '');
    setText('up-error', '');
  };
  $('up-section').hidden = !source.checkUpdate;
  $('up-check').addEventListener('click', async () => {
    const btn = $('up-check');
    btn.disabled = true;
    setText('up-error', '');
    showUpdate('Checking for updates…');
    try {
      const st = await source.checkUpdate();
      if (st.available) {
        showUpdate(`${st.latest} is available.${st.blocked ? ` ${st.blocked}` : ''}`, { url: st.url, canApply: !st.blocked });
      } else if (st.blocked) {
        showUpdate(st.blocked);
      } else {
        showUpdate(`${st.current} is the latest version.`, { url: st.url });
      }
    } catch (err) {
      resetUpdate();
      setText('up-error', err.message);
    } finally {
      btn.disabled = false;
    }
  });
  $('up-apply').addEventListener('click', async () => {
    updating = true;
    $('up-apply').disabled = $('up-check').disabled = true;
    setText('up-error', '');
    showUpdate('Downloading and installing the update…');
    try {
      const r = await source.applyUpdate();
      // The live source reloads the page once the service is back on it.
      showUpdate(`Installed ${r.version}. PowerPilot is restarting; this page reloads by itself.`);
    } catch (err) {
      updating = false;
      $('up-apply').disabled = $('up-check').disabled = false;
      resetUpdate();
      setText('up-error', err.message);
    }
  });

  // Shutdown configuration (settings dialog).
  const openSettings = () => {
    const cfg = getState().settings;
    if (!cfg) return;
    resetUpdate();
    $('st-notify').checked = cfg.notify;
    fillThresholds($('st-low'), cfg.low_battery_percent);
    $('st-low').value = String(cfg.low_battery_percent);
    $('st-shutdown').checked = cfg.shutdown_mac;
    $('st-after').value = cfg.shutdown_after_minutes;
    $('st-ah').value = cfg.battery_ah;
    $('st-keep').value = cfg.log_retention_days;
    setText('st-error', '');
    $('settings-dialog').showModal();
  };
  $('settings-btn').addEventListener('click', openSettings);
  $('shutdown-cfg-btn').addEventListener('click', openSettings);
  $('st-cancel').addEventListener('click', () => $('settings-dialog').close());
  $('st-ok').addEventListener('click', async () => {
    try {
      await source.saveSettings({
        ...getState().settings,
        notify: $('st-notify').checked,
        low_battery_percent: Number($('st-low').value),
        shutdown_mac: $('st-shutdown').checked,
        shutdown_after_minutes: Number($('st-after').value),
        battery_ah: Number($('st-ah').value),
        log_retention_days: Number($('st-keep').value),
      });
      $('settings-dialog').close();
      toast('Settings saved');
    } catch (err) {
      setText('st-error', err.message);
    }
  });

  $('refresh-btn').addEventListener('click', (e) => {
    const btn = e.currentTarget;
    btn.classList.remove('spin');
    void btn.offsetWidth;
    btn.classList.add('spin');
    source.refresh().catch((err) => toast(err.message, true));
  });
  // With logs on disk, "View all" browses them; mock data has only memory.
  $('events-all').addEventListener('click', () => (source.logs ? openLogs('events') : openEventsDialog()));
  $('ed-close').addEventListener('click', () => $('events-dialog').close());
  $('history-open').addEventListener('click', openHistoryDialog);
  $('hd-close').addEventListener('click', () => $('history-dialog').close());
}
