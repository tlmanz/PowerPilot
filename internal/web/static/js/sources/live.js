// Live data source: the local Go service (/api/state + server-sent events).
//
// Every source implements the same interface:
//   start(handlers)  handlers = { state(upsState), history(points), events(list, {replace}), link(ok) }
//   command(id, params) -> Promise<{sent}>
//   saveSettings(settings) -> Promise<settings>
//   refresh() -> Promise
// This one also has saveWindow({width, height}) for PowerPilot's own window,
// checkUpdate() / applyUpdate() for self-updates, and logs for the logs on disk.
// Both have battery.load() / save({installed, auto_test}) / replaced(installed)
// for battery health; each resolves to the report and puts it in the state.
import { fromLive, fromLiveEvent, fromLiveSample } from '../state.js';

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw Object.assign(new Error(data.error || `${res.status} ${res.statusText}`), { status: res.status });
  return data;
}

export function createLiveSource() {
  let on = null;
  let device = null, snap = null, settings = null, lastTest = null, report = null;

  const emit = () => on.state({ ...fromLive(device, snap, settings, lastTest, report), source: 'live' });

  // Battery health comes from the server, through the service, in Remote mode.
  async function battery(method, path, body) {
    report = await api(method, path, body);
    if (device) emit();
    return report;
  }

  async function load() {
    const s = await api('GET', '/api/state');
    // Back after an update on a new version: reload for its dashboard.
    if (device && s.device.app_version !== device.app_version) {
      location.reload();
      return;
    }
    ({ device, snapshot: snap, settings, last_test: lastTest } = s);
    on.history((s.history || []).map(fromLiveSample));
    on.events((s.events || []).map(fromLiveEvent), { replace: true });
    emit();
    battery('GET', '/api/battery').catch(() => {}); // outside Remote mode there's none
  }

  function stream() {
    const es = new EventSource('/api/stream');
    let lost = false;
    es.onmessage = (ev) => {
      const u = JSON.parse(ev.data);
      snap = u.snapshot;
      if (u.settings) settings = u.settings;
      if (u.last_test) {
        // A self-test that just ended changes the battery's health.
        if (u.last_test.ended && u.last_test.ended !== lastTest?.ended) battery('GET', '/api/battery').catch(() => {});
        lastTest = u.last_test;
      }
      if (u.events) on.events(u.events.map(fromLiveEvent));
      if (device) emit();
    };
    es.onerror = () => {
      lost = true;
      on.link(false);
    };
    es.onopen = () => {
      on.link(true);
      // Resync anything missed while the stream was down.
      if (lost) load().catch(() => {});
      lost = false;
    };
  }

  return {
    name: 'live',
    start(handlers) {
      on = handlers;
      load()
        .catch(() => on.link(false))
        .finally(stream);
    },
    command: (id, params = {}) => api('POST', '/api/command', { id, ...params }),
    async saveSettings(next) {
      settings = await api('PUT', '/api/settings', next);
      emit();
      return settings;
    },
    refresh: () => api('POST', '/api/refresh', {}),
    saveWindow: (size) => api('PUT', '/api/window', size),
    checkUpdate: () => api('GET', '/api/update'),
    applyUpdate: () => api('POST', '/api/update', {}),
    logs: {
      days: () => api('GET', '/api/logs'),
      events: (date) => api('GET', `/api/logs/events?date=${date}`),
      readings: (date) => api('GET', `/api/logs/readings?date=${date}`),
      exportURL: (kind, from, to) => `/api/logs/export?kind=${kind}&from=${from}&to=${to}`,
    },
    battery: {
      load: () => battery('GET', '/api/battery'),
      save: (body) => battery('PUT', '/api/battery', body),
      replaced: (installed) => battery('POST', '/api/battery/replaced', { installed }),
    },
  };
}
