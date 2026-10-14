// Recent events list and the "View all" dialog.
import { $, clock, date, dateTime, icon, sameDay } from './core.js';

const MAX_EVENTS = 200;
const KIND_ICON = {
  utility_restored: 'plug', utility_lost: 'plug-off', on_battery: 'battery',
  battery_charging: 'battery-charging', low_battery: 'battery-low', overload: 'gauge',
  self_test: 'pulse', fault: 'alert', alarm: 'bell', alarm_muted: 'bell-off',
  alarm_unmuted: 'bell', connection: 'link', shutdown: 'power', info: 'info',
};

let events = [];

/** Replaces the whole list (e.g. after loading history). */
export function setEvents(list) {
  events = list.slice(-MAX_EVENTS);
  renderEvents();
}

/** Adds one event: {time: Date, kind, level: info|good|warn|alert, message}. */
export function addEvent(event) {
  events.push(event);
  if (events.length > MAX_EVENTS) events.shift();
  renderEvents(true);
}

function row(e, now, fresh) {
  const li = document.createElement('li');
  li.className = `ev lv-${e.level}${fresh ? ' fresh' : ''}`;
  const ico = document.createElement('span');
  ico.className = 'ev-icon';
  ico.append(icon(KIND_ICON[e.kind] || 'info'));
  const msg = document.createElement('span');
  msg.className = 'ev-msg';
  msg.textContent = e.message;
  const time = document.createElement('time');
  time.className = 'ev-time';
  time.dateTime = e.time.toISOString();
  time.textContent = sameDay(e.time, now) ? clock(e.time) : date(e.time);
  time.title = dateTime(e.time, true);
  li.append(ico, msg, time);
  return li;
}

export function renderEvents(freshNewest = false) {
  const list = $('events');
  list.textContent = '';
  const now = new Date();
  const recent = events.slice(-12).reverse();
  if (!recent.length) {
    const li = document.createElement('li');
    li.className = 'ev empty';
    li.textContent = 'No events yet';
    list.append(li);
    return;
  }
  recent.forEach((e, i) => list.append(row(e, now, freshNewest && i === 0)));
  // Keep only what fits; "View all" has the rest.
  while (list.children.length > 1 && list.scrollHeight > list.clientHeight + 1) list.lastChild.remove();
}

export function openEventsDialog() {
  const body = $('events-table');
  body.textContent = '';
  for (const e of events.slice().reverse()) {
    const tr = document.createElement('tr');
    const t = document.createElement('td');
    t.textContent = dateTime(e.time, true);
    const m = document.createElement('td');
    m.className = 'left';
    m.textContent = e.message;
    tr.append(t, m);
    body.append(tr);
  }
  $('events-dialog').showModal();
}
