// Entry point: picks a data source, holds the single UPS state object, and
// renders every component from it. Use ?source=mock for simulated data.
import { emptyState } from './state.js';
import { createLiveSource } from './sources/live.js';
import { createMockSource } from './sources/mock.js';
import { updateUPSDisplay } from './ui/display.js';
import { updateAlarm, updateBattery, updateEnvironment, updateHeader, updateLoad, updatePowerFlow } from './ui/panels.js';
import { addEvent, renderEvents, setEvents } from './ui/events.js';
import { renderCharts, seedCharts, updateCharts } from './ui/charts.js';
import { updateControls, wireControls } from './ui/controls.js';

const params = new URLSearchParams(location.search);
const source = params.get('source') === 'mock'
  ? createMockSource({ scenario: params.get('scenario') || 'cycle' })
  : createLiveSource();

let state = emptyState();
let linkUp = true;

export const getState = () => state;

/** Replaces the UPS state and re-renders every component from it. */
export function updateUPSState(next) {
  state = next;
  updateHeader(state, linkUp);
  updateUPSDisplay(state);
  updateEnvironment(state);
  updateBattery(state.battery, state);
  updateLoad(state.output, state);
  updatePowerFlow(state.mode, state);
  updateAlarm(state.alarm, state);
  updateControls(state);
  updateCharts(state);
  document.body.classList.toggle('stale', !linkUp);
}

wireControls(source, getState);
updateUPSState(state);
source.start({
  state: updateUPSState,
  history: seedCharts,
  events: (list, { replace = false } = {}) => (replace ? setEvents(list) : list.forEach(addEvent)),
  link: (ok) => {
    linkUp = ok;
    updateUPSState(state);
  },
});

// For integrations and debugging from the console.
window.UPSMonitor = { getState, updateUPSState, addEvent, source };

let resizeTimer;
new ResizeObserver(() => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => {
    renderCharts();
    renderEvents();
  }, 100);
}).observe(document.querySelector('.app'));
