// The Cloud setting (Settings > Cloud): Off, Remote or Publish, against a
// powerpilot-server. Saving restarts the service in the new mode; the page
// reloads once it's back. The token only ever goes to the service.
const $ = (id) => document.getElementById(id);

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

const host = (url) => (url || '').replace(/^https?:\/\//, '');

function describe(st) {
  const trouble = st.error ? ` (${st.error})` : st.connected ? '' : ' (not connected)';
  switch (st.mode) {
    case 'remote':
      return `Remote: showing ${st.device} from ${host(st.url)}${trouble}.`;
    case 'publish':
      return `Publish: sharing this UPS as ${st.device} on ${host(st.url)}${trouble}.`;
  }
  return 'Off: PowerPilot uses the UPS on this computer’s USB.';
}

let current = null;

async function refresh() {
  try {
    current = await api('GET', '/api/cloud');
    $('cl-status').textContent = describe(current);
  } catch (e) {
    $('cl-section').hidden = true; // an older service without the setting
  }
}

const mode = () => document.querySelector('input[name="cl-mode"]:checked')?.value ?? 'off';

function showFields() {
  $('cl-fields').hidden = mode() === 'off';
}

// A pasted pairing link fills in the fields, so it's clear what it holds.
function readLink() {
  try {
    const u = new URL($('cl-link').value.trim());
    if (u.protocol !== 'powerpilot:') return;
    const q = u.searchParams;
    if (q.get('url')) $('cl-url').value = q.get('url');
    if (q.get('device')) $('cl-device').value = q.get('device');
    if (q.get('token')) $('cl-token').value = q.get('token');
  } catch { /* not a link (yet) */ }
}

function open() {
  const st = current ?? { mode: 'off' };
  for (const r of document.querySelectorAll('input[name="cl-mode"]')) r.checked = r.value === st.mode;
  $('cl-link').value = '';
  $('cl-url').value = st.url ?? '';
  $('cl-device').value = st.device ?? '';
  $('cl-token').value = '';
  $('cl-token').placeholder = st.token_set ? 'Saved: leave empty to keep it' : 'From the server’s admin page';
  $('cl-error').textContent = '';
  showFields();
  $('cloud-dialog').showModal();
}

// waitForRestart reloads the page once the service answers again.
async function waitForRestart() {
  await new Promise((r) => setTimeout(r, 1500));
  for (let i = 0; i < 40; i++) {
    try {
      const res = await fetch('/api/state');
      if (res.ok) break;
    } catch { /* still restarting */ }
    await new Promise((r) => setTimeout(r, 500));
  }
  location.reload();
}

async function save() {
  $('cl-error').textContent = '';
  $('cl-ok').disabled = true;
  try {
    await api('PUT', '/api/cloud', {
      mode: mode(),
      link: $('cl-link').value.trim(),
      url: $('cl-url').value.trim(),
      device: $('cl-device').value.trim(),
      token: $('cl-token').value.trim(),
    });
  } catch (e) {
    $('cl-error').textContent = e.message;
    $('cl-ok').disabled = false;
    return;
  }
  $('cl-ok').textContent = 'Restarting…';
  waitForRestart();
}

export function wireCloud() {
  $('settings-btn').addEventListener('click', refresh);
  $('cl-open').addEventListener('click', open);
  $('cl-cancel').addEventListener('click', () => $('cloud-dialog').close());
  $('cl-ok').addEventListener('click', save);
  $('cl-link').addEventListener('input', readLink);
  for (const r of document.querySelectorAll('input[name="cl-mode"]')) r.addEventListener('change', showFields);
  refresh();
}
