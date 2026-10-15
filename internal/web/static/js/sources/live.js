// Live data source: the local Go service (/api/state + server-sent events).
//
// Every source implements the same interface:
//   start(handlers)  handlers = { state(upsState), history(points), events(list, {replace}), link(ok) }
//   command(id, params) -> Promise<{sent}>
//   saveSettings(settings) -> Promise<settings>
//   refresh() -> Promise
// This one also has saveWindow({width, height}) for PowerPilot's own window.
import { fromLive, fromLiveEvent, fromLiveSample } from '../state.js';

async function api(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `${res.status} ${res.statusText}`);
  return data;
}

export function createLiveSource() {
  let on = null;
  let device = null, snap = null, settings = null, lastTest = null;

  const emit = () => on.state({ ...fromLive(device, snap, settings, lastTest), source: 'live' });

  async function load() {
    const s = await api('GET', '/api/state');
    ({ device, snapshot: snap, settings, last_test: lastTest } = s);
    on.history((s.history || []).map(fromLiveSample));
    on.events((s.events || []).map(fromLiveEvent), { replace: true });
    emit();
  }

  function stream() {
    const es = new EventSource('/api/stream');
    let lost = false;
    es.onmessage = (ev) => {
      const u = JSON.parse(ev.data);
      snap = u.snapshot;
      if (u.settings) settings = u.settings;
      if (u.last_test) lastTest = u.last_test;
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
  };
}
