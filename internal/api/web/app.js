'use strict';

// ---------------------------------------------------------------- helpers

// h builds a DOM element. Strings become text nodes, so nothing that comes
// from a pool or a miner is ever read as HTML.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === false || value === null || value === undefined) continue;
    if (key === 'class') el.className = value;
    else if (key.startsWith('on')) el.addEventListener(key.slice(2), value);
    else if (key === 'checked' || key === 'disabled' || key === 'selected' || key === 'hidden') el[key] = Boolean(value);
    else if (key === 'value') el.value = value;
    else el.setAttribute(key, value);
  }
  for (const child of children.flat(Infinity)) {
    if (child === null || child === undefined || child === false) continue;
    el.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return el;
}

async function api(path, options) {
  const opts = Object.assign({ headers: {} }, options);
  if (opts.body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(opts.body);
  }
  if (opts.method && opts.method !== 'GET') opts.headers['X-Hashsteer-Request'] = '1';
  const res = await fetch(path, opts);
  if (!res.ok) {
    let message = res.status + ' ' + res.statusText;
    try { message = (await res.json()).error || message; } catch (e) { /* not json */ }
    throw new Error(message);
  }
  if (res.status === 204) return null;
  const type = res.headers.get('Content-Type') || '';
  return type.includes('json') ? res.json() : res.text();
}

function toast(message, isError) {
  const el = document.getElementById('toast');
  el.textContent = message;
  el.className = 'toast' + (isError ? ' error' : '');
  el.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => { el.hidden = true; }, isError ? 7000 : 3000);
}

const UNITS = ['H/s', 'kH/s', 'MH/s', 'GH/s', 'TH/s', 'PH/s'];

function fmtHash(value) {
  if (!value) return '-';
  let i = 0;
  while (value >= 1000 && i < UNITS.length - 1) { value /= 1000; i++; }
  return value.toFixed(2) + ' ' + UNITS[i];
}

function fmtBTC(value) {
  if (value === 0 || value === undefined || value === null) return '-';
  return value.toFixed(8);
}

function fmtSat(btc) { return btc ? (btc * 1e8).toFixed(2) + ' sat' : '-'; }
function fmtWatts(value) { return value ? Math.round(value) + ' W' : '-'; }
function fmtGB(mib) { return (mib / 1024).toFixed(1) + ' GB'; }
function fmtDate(iso) { return iso && new Date(iso).getFullYear() > 2000 ? new Date(iso).toLocaleString() : '-'; }
function fmtCoin(value, currency) { return value.toFixed(8).replace(/0+$/, '').replace(/[.]$/, '') + ' ' + currency; }

function fmtDuration(seconds) {
  seconds = Math.max(0, Math.round(seconds));
  const d = Math.floor(seconds / 86400);
  const hrs = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d) return d + 'd ' + hrs + 'h ' + m + 'm';
  if (hrs) return hrs + 'h ' + m + 'm';
  if (m) return m + 'm';
  return seconds + 's';
}

// the time since a moment; '' where there is none
function fmtAgo(iso) { return iso && new Date(iso).getFullYear() > 2000 ? fmtDuration((Date.now() - new Date(iso).getTime()) / 1000) : ''; }

function fmtUntil(iso) {
  const s = (new Date(iso).getTime() - Date.now()) / 1000;
  return s <= 0 ? 'now' : fmtDuration(s + 59);
}

// A hash key names miner, devices and algorithm: hashrate/rigel/GPU#00/kawpow
function parseKey(hashKey) {
  const parts = hashKey.split('/');
  return { miner: parts[1] || '', devices: parts[2] || '', algorithm: parts[3] || '' };
}

// ------------------------------------------------------------ data table

// Every table can be sorted by a click on a column head and filtered by the
// fields below the heads. What is chosen is kept per table while the page is
// redrawn with fresh figures.
const tables = {};

// dataTable builds a table.
//   columns: { key, label, num, value(row), cell(row), filter, cls(row) }
//     value gives what is sorted and filtered by (default row[key]),
//     cell what is shown (default the value),
//     filter is 'text' (default for text columns), a list of choices, or false.
//   opts: { sort: {key, dir}, rowClass(row), empty, select: {id(row), set, can(row)} }
// It returns the element and the rows as shown.
function dataTable(id, columns, rows, opts) {
  opts = opts || {};
  const t = tables[id] || (tables[id] = { sort: opts.sort || { key: columns[0].key, dir: 1 }, filters: {} });
  const value = (col, row) => (col.value ? col.value(row) : row[col.key]);
  const norm = v => (v === undefined || v === null ? '' : v);

  let shown = rows.filter(row => columns.every(col => {
    const f = t.filters[col.key];
    if (!f) return true;
    const v = String(norm(value(col, row)));
    return Array.isArray(col.filter) ? v === f : v.toLowerCase().includes(f.trim().toLowerCase());
  }));
  const sortCol = columns.find(c => c.key === t.sort.key);
  if (sortCol) {
    shown = shown.slice().sort((a, b) => {
      const x = norm(value(sortCol, a)), y = norm(value(sortCol, b));
      const order = typeof x === 'number' && typeof y === 'number' ? x - y : String(x).localeCompare(String(y), undefined, { numeric: true });
      return t.sort.dir * order;
    });
  }

  const sel = opts.select;
  const selectable = sel ? shown.filter(r => !sel.can || sel.can(r)).map(sel.id) : [];
  const head = h('tr', {},
    sel ? h('th', { class: 'check' }, h('input', { type: 'checkbox', title: 'Select all shown',
      checked: selectable.length > 0 && selectable.every(k => sel.set.has(k)),
      onchange: e => { for (const k of selectable) e.target.checked ? sel.set.add(k) : sel.set.delete(k); render(); } })) : null,
    columns.map(col => h('th', { class: 'sortable' + (col.num ? ' num' : ''), onclick: () => {
      t.sort = { key: col.key, dir: t.sort.key === col.key ? -t.sort.dir : 1 };
      render();
    } }, col.label + (t.sort.key === col.key ? (t.sort.dir > 0 ? '  ▲' : '  ▼') : ''))));

  const hasFilters = columns.some(c => c.filter !== false && (c.filter || !c.num));
  const filters = hasFilters ? h('tr', { class: 'filters' },
    sel ? h('th', {}) : null,
    columns.map(col => {
      const kind = col.filter === undefined ? (col.num ? false : 'text') : col.filter;
      if (!kind) return h('th', {});
      if (Array.isArray(kind)) {
        return h('th', {}, h('select', { onchange: e => { t.filters[col.key] = e.target.value; render(); } },
          h('option', { value: '' }, 'all'),
          kind.map(o => h('option', { value: o, selected: t.filters[col.key] === o }, o))));
      }
      return h('th', {}, h('input', { type: 'search', value: t.filters[col.key] || '', 'data-filter': id + ':' + col.key,
        oninput: e => { t.filters[col.key] = e.target.value; render(); } }));
    })) : null;

  const body = shown.map(row => h('tr', { class: opts.rowClass ? opts.rowClass(row) : '' },
    sel ? h('td', { class: 'check' }, !sel.can || sel.can(row) ? h('input', { type: 'checkbox', checked: sel.set.has(sel.id(row)),
      onchange: e => { e.target.checked ? sel.set.add(sel.id(row)) : sel.set.delete(sel.id(row)); render(); } }) : null) : null,
    columns.map(col => {
      const content = col.cell ? col.cell(row) : norm(value(col, row));
      return h('td', { class: (col.num ? 'num ' : '') + (col.cls ? col.cls(row) : '') }, content === '' ? '-' : content);
    })));

  const el = rows.length || !opts.empty
    ? h('div', { class: 'scroll' }, h('table', {}, h('thead', {}, head, filters),
      h('tbody', {}, body.length ? body : h('tr', {}, h('td', { colspan: String(columns.length + (sel ? 1 : 0)), class: 'help' }, 'Nothing matches the filters.')))))
    : h('div', { class: 'empty' }, opts.empty);
  return { el, rows: shown };
}

// ------------------------------------------------------------------ state

const state = {
  page: 'dashboard',
  status: null,
  devices: [],
  offers: [],
  config: null,   // as the server has it
  draft: null,    // what the setup pages are editing
  meta: null,
  log: [],
  minerLogs: [],
  minerLog: { name: '', text: '' },
  dependencies: null,   // what the miner programs need from the system; null until asked for
  installing: '',
  activeLogs: [],   // the logs of the miner programs that run right now
  balances: { balances: [], wallets: [] },
  activity: [],
  totals: [],
  benchmarks: [],
  benchSelected: new Set(),
  update: null,
  shared: { entries: [] },
  sharedMineOnly: true,
  sharedSearch: '',
  sharedOpen: new Set(),    // card|algorithm whose slower miners are shown
  sharedClosed: new Set(),  // cards that are folded away
  updateAt: 0,
  offerMineOnly: true,
};

const PAGES = {
  '': 'dashboard', console: 'console', devices: 'devices',
  bestpools: 'bestpools', allpools: 'allpools', earnings: 'earnings', totals: 'totals', payouts: 'payouts',
  miners: 'miners', benchmarks: 'benchmarks', activity: 'activity', shared: 'shared', watchdog: 'watchdog',
  setup: 'setup', setupwallets: 'setupwallets', setupcoins: 'setupcoins', setuppools: 'setuppools', setupminers: 'setupminers', setupalgorithms: 'setupalgorithms', setupdevices: 'setupdevices',
  // names of earlier versions
  pools: 'allpools', config: 'setup', logs: 'console',
};

function fiat(btc) {
  const s = state.status;
  if (!s || !s.btc_price || !btc) return '';
  return (btc * s.btc_price).toFixed(2) + ' ' + s.currency;
}

function totals() {
  let profit = 0, power = 0, mining = 0;
  for (const g of state.status.groups) {
    if (g.state !== 'idle') { mining++; power += g.power_watts; }
    profit += g.profit_btc_per_day || 0;
  }
  return { profit, power, mining };
}

function workerName() { return state.config ? state.config.general.worker : ''; }
// The coin a pool pays in: zpool converts everything into one, the coin
// pools pay what is mined.
function payoutCoin(pool, coin) {
  if (pool === 'zpool') return state.config ? state.config.pools.zpool.currency : '';
  return coin || '';
}

// --------------------------------------------------------------- top bar

function topItem(label, value) {
  return h('div', { class: 'item' }, h('div', { class: 'label' }, label), h('div', { class: 'value' }, value));
}

function pauseButton() {
  const paused = state.status.system.paused;
  return h('button', { class: paused ? 'primary' : 'warn', onclick: () => setPaused(!paused),
    title: paused ? 'Start the miners again' : 'Stop all miners, keep hashsteer running' }, paused ? 'Resume' : 'Pause');
}

function renderTopbar() {
  const s = state.status;
  const bar = document.getElementById('topbar');
  if (!s) { bar.replaceChildren(); return; }
  const t = totals();
  const memory = s.system.memory;
  bar.replaceChildren(
    topItem('Total profit', fmtBTC(t.profit) === '-' ? '-' : fmtBTC(t.profit) + ' BTC/day' + (fiat(t.profit) ? '  =  ' + fiat(t.profit) + '/day' : '')),
    topItem('@Power', fmtWatts(t.power)),
    memory ? topItem('Free memory', fmtGB(memory.commit_free_mib) + ' of ' + fmtGB(memory.commit_limit_mib)) : null,
    topItem('Uptime', fmtDuration((Date.now() - new Date(s.system.started).getTime()) / 1000)),
    h('div', { class: 'spacer' }),
    pauseButton());
}

async function setPaused(paused) {
  try {
    await api('/api/pause', { method: 'POST', body: { paused } });
    toast(paused ? 'Mining is paused' : 'Mining continues');
    state.status.system.paused = paused;
    renderTopbar();
    render();
    setTimeout(refreshStatus, 1500);
  } catch (e) { toast(e.message, true); }
}

// ---------------------------------------------------------------- update

// updateBox says that a newer release exists, and offers to install it.
function updateBox() {
  const u = state.update;
  if (!u || (!u.available && !u.downloaded)) return null;
  const notes = u.page ? h('a', { href: u.page, target: '_blank', rel: 'noopener' }, 'What is new') : null;
  if (u.downloaded) {
    return h('div', { class: 'eta' },
      h('div', { class: 'main' }, 'hashsteer ' + u.downloaded + ' is fetched and checked.'),
      h('div', { class: 'help' }, u.restartable ? 'hashsteer restarts to let it take over.' : 'It takes over the next time hashsteer is started with start.bat or start.sh.'));
  }
  return h('div', { class: 'eta' },
    h('div', { class: 'title' }, h('div', { class: 'main' }, 'hashsteer ' + u.latest + ' is available. This is ' + u.current + '.'),
      h('button', { class: 'primary', disabled: u.busy, onclick: installUpdate }, u.busy ? 'Fetching ...' : 'Update now'), notes),
    h('div', { class: 'help' }, 'The release is fetched from GitHub and checked against the checksum GitHub lists for it. ' +
      (u.restartable ? 'Then hashsteer stops its miners and restarts with the new version.' : 'It then waits for the next start: this hashsteer was not started with start.bat or start.sh, so it cannot restart itself.')),
    u.error ? h('div', { class: 'help error' }, u.error) : null);
}

async function loadUpdate(force) {
  if (!force && Date.now() - state.updateAt < 60000) return;
  state.updateAt = Date.now();
  try { state.update = await api('/api/update'); } catch (e) { /* the next try comes by itself */ }
}

async function installUpdate() {
  const u = state.update;
  if (!confirm('Update hashsteer to ' + u.latest + ' now?' + (u.restartable ? ' The miners stop and hashsteer restarts.' : ''))) return;
  state.update = Object.assign({}, u, { busy: true });
  render();
  try {
    state.update = await api('/api/update/install', { method: 'POST' });
    toast(state.update.restartable ? 'Fetched - hashsteer restarts' : 'Fetched - it takes over at the next start');
  } catch (e) {
    toast('Not updated: ' + e.message, true);
    await loadUpdate(true);
  }
  render();
}

async function checkUpdate() {
  try {
    state.update = await api('/api/update/check', { method: 'POST' });
    state.updateAt = Date.now();
    toast(state.update.available ? 'hashsteer ' + state.update.latest + ' is available' : 'This is the newest version');
  } catch (e) { toast(e.message, true); }
  render();
}

// ------------------------------------------------------------- dashboard

function deviceColumns(withSelection) {
  const t = d => d.telemetry || {};
  const temp = c => (c >= 85 ? 'hot' : c >= 78 ? 'warm' : '');
  const num = (v, unit) => (v ? Math.round(v) + unit : '');
  const excluded = new Set(state.config ? state.config.devices.exclude : []);
  const mines = d => (d.kind === 'cpu' ? Boolean(state.config && state.config.general.cpu.enabled) : !excluded.has(d.id));
  return [
    { key: 'id', label: 'Name' },
    { key: 'vendor', label: 'Vendor', value: d => d.vendor || (d.kind === 'cpu' ? 'CPU' : '') },
    { key: 'model', label: 'Model' },
    withSelection ? { key: 'mines', label: 'Mines', value: d => (mines(d) ? 'yes' : 'no'), filter: ['yes', 'no'] } : null,
    { key: 'mem', label: 'Mem', num: true, value: d => d.memory_mib || 0, cell: d => (d.memory_mib ? Math.round(d.memory_mib / 1024) + ' GB' : d.threads ? d.threads + ' threads' : '') },
    withSelection ? { key: 'used', label: 'Mem used', num: true, value: d => t(d).memory_used_mib || 0, cell: d => (d.telemetry ? Math.round(t(d).memory_used_mib) + ' MiB' : '') } : null,
    { key: 'temp', label: '°C', num: true, value: d => t(d).temperature_c || 0, cell: d => num(t(d).temperature_c, ' °C'), cls: d => temp(t(d).temperature_c) },
    { key: 'max', label: 'max °C', num: true, value: d => d.max_temperature_c || 0, cell: d => num(d.max_temperature_c, ' °C'), cls: d => temp(d.max_temperature_c) },
    { key: 'fan', label: 'Fan', num: true, value: d => t(d).fan_percent || 0, cell: d => num(t(d).fan_percent, ' %') },
    withSelection ? { key: 'load', label: 'Load', num: true, value: d => t(d).utilization_percent || 0, cell: d => (d.telemetry ? Math.round(t(d).utilization_percent) + ' %' : '') } : null,
    { key: 'power', label: 'Power', num: true, value: d => t(d).power_watts || 0, cell: d => num(t(d).power_watts, ' W') },
    { key: 'clock', label: 'Clock', num: true, value: d => t(d).clock_mhz || 0, cell: d => num(t(d).clock_mhz, ' MHz') },
    { key: 'memclk', label: 'Memclk', num: true, value: d => t(d).memory_clock_mhz || 0, cell: d => num(t(d).memory_clock_mhz, ' MHz') },
    // the power limit, as a share of what the card comes with
    { key: 'pl', label: 'PL', num: true, value: d => (t(d).power_default_watts ? 100 * t(d).power_limit_watts / t(d).power_default_watts : 0),
      cell: d => (t(d).power_default_watts ? Math.round(100 * t(d).power_limit_watts / t(d).power_default_watts) + ' %' : '') },
  ].filter(Boolean);
}

function balanceCards() {
  const info = state.balances;
  if (!info.balances.length) {
    return h('div', { class: 'empty' }, info.error ? 'No balance known: ' + info.error : 'Nothing yet. The pool is asked every ten minutes once a wallet is set.');
  }
  return h('div', { class: 'cards' }, info.balances.map(b => h('div', { class: 'card balance' },
    h('h3', {}, b.pool),
    h('div', { class: 'main' }, fmtCoin(b.unpaid, b.currency)),
    h('dl', {},
      h('dt', {}, 'Worth'), h('dd', {}, fiat(b.value_btc) || '-'),
      h('dt', {}, 'Confirmed'), h('dd', {}, fmtCoin(b.balance, b.currency)),
      h('dt', {}, 'Pending'), h('dd', {}, fmtCoin(b.unsold, b.currency)),
      h('dt', {}, 'Paid in 24 h'), h('dd', {}, fmtCoin(b.paid_24h, b.currency)),
      h('dt', {}, 'Earned in total'), h('dd', {}, fmtCoin(b.total, b.currency)),
      h('dt', {}, 'Updated'), h('dd', {}, fmtAgo(b.updated) ? fmtAgo(b.updated) + ' ago' : '-')))));
}

// What has arrived at the wallets themselves, as block explorers tell it.
function walletCards() {
  const info = state.balances;
  const wallets = info.wallets || [];
  if (!wallets.length && !info.wallet_error) return null;
  return [h('h2', {}, 'Wallets'),
    info.wallet_error ? h('p', { class: 'help' }, 'Could not be updated: ' + info.wallet_error) : null,
    h('div', { class: 'cards' }, wallets.map(w => h('div', { class: 'card balance', title: w.address + ', as ' + w.explorer + ' tells it' },
      h('h3', {}, w.currency + ' wallet'),
      h('div', { class: 'main' }, fmtCoin(w.balance, w.currency)),
      h('dl', {},
        h('dt', {}, 'Worth'), h('dd', {}, fiat(w.value_btc) || '-'),
        h('dt', {}, 'Address'), h('dd', {}, w.address.length > 12 ? w.address.slice(0, 5) + '...' + w.address.slice(-4) : w.address),
        h('dt', {}, 'Unconfirmed'), h('dd', {}, w.pending ? fmtCoin(w.pending, w.currency) : '-'),
        h('dt', {}, 'Updated'), h('dd', {}, fmtAgo(w.updated) ? fmtAgo(w.updated) + ' ago' : '-')))))];
}

function renderDashboard() {
  const s = state.status;
  if (!s) return h('div', { class: 'empty' }, 'Loading ...');

  const running = s.groups.filter(g => g.state !== 'idle');
  const idle = s.groups.filter(g => g.state === 'idle');
  const benched = g => { const c = g.candidates.find(x => x.id === g.running); return c && c.benchmarked ? c.hashrate : 0; };
  const guessed = g => { const c = g.candidates.find(x => x.id === g.running); return Boolean(c && c.estimated); };
  const miners = dataTable('running', [
    { key: 'miner', label: 'Name' },
    { key: 'pool', label: 'Pool' },
    { key: 'coin', label: 'Coin', cell: g => g.coin || 'pool picks' },
    { key: 'paid', label: 'Paid', value: g => payoutCoin(g.pool, g.coin) },
    { key: 'name', label: 'Devices', cell: g => h('span', { title: g.devices.join(', ') }, g.name) },
    { key: 'profit', label: 'Profit', num: true, value: g => g.profit_btc_per_day,
      cell: g => (g.state === 'benchmarking' ? 'benchmarking' : fmtBTC(g.profit_btc_per_day) + (fiat(g.profit_btc_per_day) ? '  (' + fiat(g.profit_btc_per_day) + ')' : '')) },
    { key: 'power_watts', label: 'Power', num: true, cell: g => fmtWatts(g.power_watts) },
    { key: 'algorithm', label: 'Algo' },
    { key: 'hashrate', label: 'Speed', num: true, cell: g => fmtHash(g.hashrate) },
    { key: 'benched', label: 'Benched', num: true, value: benched, cell: g => fmtHash(benched(g)) + (guessed(g) ? '  (est.)' : '') },
    { key: 'shares', label: 'A/R/S', num: true, value: g => g.accepted, cell: g => g.accepted + '/' + g.rejected + '/' + g.invalid },
    { key: 'since', label: 'Active', num: true, value: g => -new Date(g.since).getTime(), cell: g => fmtAgo(g.since) },
  ], running, { empty: s.system.paused ? 'Mining is paused.' : 'No miner is running.', sort: { key: 'name', dir: 1 } });

  const memory = s.system.memory;
  const system = h('div', { class: 'cards' },
    memory ? card('RAM', fmtGB(memory.total_mib - memory.free_mib) + ' / ' + fmtGB(memory.total_mib),
      Math.round(100 * (1 - memory.free_mib / memory.total_mib)) + ' % in use') : null,
    memory ? card('Memory with page file', fmtGB(memory.commit_limit_mib - memory.commit_free_mib) + ' / ' + fmtGB(memory.commit_limit_mib),
      fmtGB(memory.commit_free_mib) + ' left for miners to reserve') : null,
    card('Bitcoin price', s.btc_price ? s.btc_price.toFixed(0) + ' ' + s.currency : '-', s.btc_price ? '' : 'not known yet'),
    card('Electricity', state.config && state.config.general.power.price_per_kwh > 0
      ? state.config.general.power.price_per_kwh + ' ' + state.config.general.power.currency + ' / kWh' : 'not counted'));

  return h('div', {},
    h('h1', {}, 'Dashboard'),
    updateBox(),
    benchmarkEta(state.benchmarks, Math.round(s.benchmark_seconds), true),
    h('div', { class: 'title' }, h('h2', {}, 'Running Miners on ' + workerName()), pauseButton()),
    miners.el,
    idle.length ? [h('h2', {}, 'Idle'), dataTable('idle', [
      { key: 'name', label: 'Devices', cell: g => h('span', { title: g.devices.join(', ') }, g.name), filter: false },
      { key: 'reason', label: 'Why', filter: false, cls: () => 'wrap' },
    ], idle).el] : null,
    h('h2', {}, 'Device Status'), dataTable('devstatus', deviceColumns(false).map(c => Object.assign({ filter: false }, c)), state.devices).el,
    h('h2', {}, 'System Information'), system,
    h('h2', {}, 'Balances'),
    state.balances.error && state.balances.balances.length ? h('p', { class: 'help' }, 'Could not be updated: ' + state.balances.error) : null,
    balanceCards(),
    walletCards());
}

function card(label, value, sub) {
  return h('div', { class: 'card' }, h('div', { class: 'label' }, label), h('div', { class: 'value' }, value), sub ? h('div', { class: 'sub' }, sub) : null);
}

// --------------------------------------------------------------- devices

function renderDevices() {
  return h('div', {}, h('h1', {}, 'Devices'),
    h('p', { class: 'lead' }, 'The devices hashsteer found. Which of them mine is set under Setup Devices.'),
    dataTable('devices', deviceColumns(true), state.devices, { empty: 'No device was found.' }).el);
}

// ----------------------------------------------------------------- pools

function minedAlgorithms() {
  const set = new Set();
  for (const m of (state.meta ? state.meta.miners : [])) for (const a of m.algorithms) set.add(a);
  return set;
}

function poolColumns(withAction) {
  const pools = state.config ? state.config.pools : null;
  const excluded = o => Boolean(pools) && poolSettings(pools, o.pool).exclude_coins.includes(o.coin);
  const ratio = o => (o.actual_24h > 0 && o.price_24h > 0 ? o.actual_24h / o.price_24h : 0);
  return [
    { key: 'algorithm', label: 'Algorithm' },
    { key: 'pool', label: 'Name' },
    { key: 'coin', label: 'Coin', cell: o => (o.coin ? h('span', { title: o.coin_name }, o.coin) : 'pool picks') },
    { key: 'price', label: 'BTC/GH/Day', num: true, cell: o => (o.price * 1e9).toFixed(10) },
    // what the pool really paid over a day against what it estimated
    { key: 'ratio', label: 'Act/Est', num: true, value: ratio, cell: o => (ratio(o) ? ratio(o).toFixed(3) : '') },
    { key: 'hashrate', label: 'Hashrate', num: true, cell: o => fmtHash(o.hashrate) },
    { key: 'workers', label: 'Workers', num: true },
    { key: 'last', label: 'Last (min)', num: true, value: o => o.last_block_seconds || 0, cell: o => (o.last_block_seconds ? (o.last_block_seconds / 60).toFixed(1) : '') },
    { key: 'blocks_24h', label: 'Blocks/24h', num: true, cell: o => o.blocks_24h || '' },
    { key: 'fee_percent', label: 'Fee', num: true, cell: o => o.fee_percent + ' %' },
    { key: 'host', label: 'Host', cell: o => (o.host ? o.host + ':' + o.port : ''), cls: () => 'mono' },
    { key: 'updated', label: 'Last Updated', filter: false, value: o => new Date(o.updated).getTime(), cell: o => (fmtAgo(o.updated) ? fmtAgo(o.updated) + ' ago' : '') },
    withAction ? { key: 'action', label: '', filter: false, value: () => '',
      cell: o => (o.coin && !excluded(o) ? h('button', { class: 'small', title: 'Do not mine this coin at this pool', onclick: () => excludeCoin(o.pool, o.coin) }, 'Exclude') : '') } : null,
  ].filter(Boolean);
}

// The settings of a pool, by the name its offers carry.
function poolSettings(pools, name) {
  const coinPool = (state.meta ? state.meta.coin_pools : []).find(p => p.name === name);
  return coinPool ? pools[coinPool.key] : pools.zpool;
}

function poolToolbar(count) {
  return h('div', { class: 'toolbar' },
    h('label', {}, h('input', { type: 'checkbox', checked: state.offerMineOnly,
      onchange: e => { state.offerMineOnly = e.target.checked; render(); } }), ' only algorithms a miner program supports'),
    h('span', { class: 'help' }, count + ' shown'));
}

function offersToShow() {
  const mine = minedAlgorithms();
  return state.offers.filter(o => !state.offerMineOnly || mine.has(o.algorithm));
}

function renderBestPools() {
  const best = new Map();
  for (const o of offersToShow()) if (!best.has(o.algorithm) || o.price > best.get(o.algorithm).price) best.set(o.algorithm, o);
  const t = dataTable('bestpools', poolColumns(false), Array.from(best.values()),
    { empty: 'No offers yet. They appear once the engine has asked the pool.' });
  return h('div', {}, h('h1', {}, 'Best Pools'),
    h('p', { class: 'lead' }, 'For each algorithm, the offer that pays most right now. A miner is not sent there blindly: what it earns depends on its own speed, and the price counts as an hourly average.'),
    poolToolbar(t.rows.length), t.el);
}

function renderAllPools() {
  const t = dataTable('allpools', poolColumns(true), offersToShow(),
    { empty: 'No offers yet. They appear once the engine has asked the pool.' });
  return h('div', {}, h('h1', {}, 'All Pools'),
    h('p', { class: 'lead' }, 'Everything the enabled pools pay for right now. "Pool picks" is zpool\'s own port for an algorithm, where it switches between coins itself; the other rows mine exactly one coin. Coins nobody is mining at a pool are not offered. zpool reports its prices itself; for the pools that pay in the mined coin they are worked out from the coin\'s price, its block reward and the hashrate of its network.'),
    poolToolbar(t.rows.length), t.el);
}

async function excludeCoin(pool, coin) {
  if (!confirm('Stop mining ' + coin + ' at ' + pool + '? You can undo this under Setup Pools.')) return;
  try {
    const cfg = JSON.parse(JSON.stringify(state.config));
    poolSettings(cfg.pools, pool).exclude_coins.push(coin);
    await api('/api/config', { method: 'PUT', body: cfg });
    toast(coin + ' excluded');
    await loadConfig();
    render();
  } catch (e) { toast(e.message, true); }
}

function renderEarnings() {
  const info = state.balances;
  const amount = key => ({ key, label: { balance: 'Balance', unsold: 'Pending', unpaid: 'Total', paid_24h: 'Paid 24h', total: 'Earned' }[key], num: true,
    cell: b => fmtCoin(b[key], b.currency) });
  return h('div', {}, h('h1', {}, 'Pool Earnings'),
    h('p', { class: 'lead' }, 'What the pools hold for your wallets, as they report it. "Balance" is confirmed and waits for the next payout. "Pending" is mined but not final yet: at zpool not exchanged into the payout coin, at the other pools in blocks that still have to mature.'),
    info.error ? h('p', { class: 'help' }, 'Could not be updated: ' + info.error) : null,
    dataTable('earnings', [
      { key: 'pool', label: 'Name' }, { key: 'currency', label: 'Currency' },
      amount('balance'), amount('unsold'), amount('unpaid'),
      { key: 'value_btc', label: 'Worth', num: true, filter: false, cell: b => fiat(b.value_btc) || '-' },
      amount('paid_24h'), amount('total'),
      { key: 'updated', label: 'Last Updated', filter: false, value: b => new Date(b.updated).getTime(), cell: b => fmtDate(b.updated) },
    ], info.balances, { empty: 'Nothing yet. The pools are asked every ten minutes once a wallet is set.' }).el);
}

function renderPayouts() {
  const rows = [];
  for (const b of state.balances.balances) for (const p of b.payouts) rows.push(Object.assign({ pool: b.pool, currency: b.currency }, p));
  return h('div', {}, h('h1', {}, 'Pool Payouts'),
    h('p', { class: 'lead' }, 'The payouts the pools report for your wallets.'),
    dataTable('payouts', [
      { key: 'time', label: 'Date', filter: false, value: p => new Date(p.time).getTime(), cell: p => fmtDate(p.time) },
      { key: 'amount', label: 'Amount', num: true, cell: p => p.amount.toFixed(10) },
      { key: 'currency', label: 'Currency' },
      { key: 'pool', label: 'Name' },
      { key: 'tx', label: 'Txid', cls: () => 'mono' },
    ], rows, { empty: 'No payout is known yet.', sort: { key: 'time', dir: -1 } }).el);
}

function renderTotals() {
  const s = state.status;
  const price = state.config ? state.config.general.power.price_per_kwh : 0;
  // electricity in BTC, with today's bitcoin price
  const cost = t => (price > 0 && s && s.btc_price ? t.energy_kwh * price / s.btc_price : 0);
  const rows = state.totals.slice();
  if (rows.length > 1) {
    rows.push(rows.reduce((sum, t) => ({ pool: 'TOTAL', runs: sum.runs + t.runs, earned_btc: sum.earned_btc + t.earned_btc,
      energy_kwh: sum.energy_kwh + t.energy_kwh, minutes: sum.minutes + t.minutes,
      started: sum.started < t.started ? sum.started : t.started, updated: sum.updated > t.updated ? sum.updated : t.updated })));
  }
  return h('div', {}, h('h1', {}, 'Pool Calculated Totals'),
    h('p', { class: 'lead' }, 'What the miner runs of the last 30 days add up to. "Profit" is estimated from measured hashrate and pool price; compare it with what the pool really paid (Pool Earnings) to see how far its estimates can be trusted.'),
    dataTable('totals', [
      { key: 'pool', label: 'Poolname', filter: false },
      { key: 'earned_btc', label: 'Profit', num: true, cell: t => fmtBTC(t.earned_btc) + (fiat(t.earned_btc) ? '  (' + fiat(t.earned_btc) + ')' : '') },
      { key: 'cost', label: 'Cost', num: true, value: cost, cell: t => (cost(t) ? fmtBTC(cost(t)) + '  (' + fiat(cost(t)) + ')' : '') },
      { key: 'energy_kwh', label: 'Power', num: true, cell: t => t.energy_kwh.toFixed(3) + ' kWh' },
      { key: 'minutes', label: 'Minutes', num: true, cell: t => t.minutes.toFixed(2) },
      { key: 'runs', label: 'Runs', num: true },
      { key: 'started', label: 'Started', filter: false, cell: t => fmtDate(t.started) },
      { key: 'updated', label: 'Updated', filter: false, cell: t => fmtDate(t.updated) },
    ], rows, { empty: 'No miner has finished a run yet.' }).el);
}

// ---------------------------------------------------------------- miners

function renderMiners() {
  const s = state.status;
  if (!s) return h('div', { class: 'empty' }, 'Loading ...');
  const rows = [];
  for (const g of s.groups) for (const c of g.candidates) rows.push(Object.assign({ group: g, running: c.id === g.running }, c));
  const status = c => (c.running ? c.group.state : c.estimated ? 'estimated' : c.benchmarked ? 'measured' : 'needs benchmark');
  const satPerWatt = c => (c.benchmarked && c.power_watts > 0 ? c.profit_btc_per_day * 1e8 / c.power_watts : 0);
  const t = dataTable('miners', [
    { key: 'miner', label: 'Name' },
    { key: 'devices', label: 'Devices', value: c => c.group.name, cell: c => h('span', { title: c.group.devices.join(', ') }, c.group.name) },
    { key: 'profit_btc_per_day', label: 'Profit', num: true, cell: c => (c.benchmarked ? fmtBTC(c.profit_btc_per_day) : ''),
      cls: c => (c.profit_btc_per_day > 0 ? 'pos' : c.profit_btc_per_day < 0 ? 'neg' : '') },
    { key: 'revenue_btc_per_day', label: 'Revenue', num: true, cell: c => (c.benchmarked ? fmtBTC(c.revenue_btc_per_day) : '') },
    { key: 'power_watts', label: 'Power', num: true, cell: c => fmtWatts(c.power_watts) },
    { key: 'satw', label: 'sat/W', num: true, value: satPerWatt, cell: c => (satPerWatt(c) ? satPerWatt(c).toFixed(2) : '') },
    { key: 'algorithm', label: 'Algorithm' },
    { key: 'pool', label: 'Pool' },
    { key: 'coin', label: 'Coin', cell: c => c.coin || 'pool picks' },
    { key: 'hashrate', label: 'Speed', num: true, cell: c => (c.benchmarked ? fmtHash(c.hashrate) : '') },
    { key: 'fee_percent', label: 'Fee', num: true, cell: c => c.fee_percent + ' %' },
    { key: 'status', label: 'Status', value: status, filter: ['mining', 'benchmarking', 'measured', 'estimated', 'needs benchmark'],
      cell: c => h('span', { class: 'badge ' + (c.running ? c.group.state : c.estimated ? 'estimated' : c.benchmarked ? '' : 'todo'),
        title: c.estimated ? 'The hashrate was measured on another machine with the same card. It is replaced by this machine\'s own once this is mined.' : '' }, status(c) + (c.running && c.estimated ? ', estimated' : '')) },
  ], rows, { empty: s.system.paused ? 'Mining is paused.' : 'Nothing to mine right now.', sort: { key: 'profit_btc_per_day', dir: -1 },
    rowClass: c => (c.running ? 'running' : '') });
  return h('div', {}, h('h1', {}, 'Active Miners'),
    h('p', { class: 'lead' }, 'Everything the device groups could mine right now, with what it would bring in BTC per day. The running ones are highlighted. Speed and power come from the benchmarks; a figure counts for every coin and pool.'),
    t.el);
}

// ------------------------------------------------------------ benchmarks

const BENCH_STATUS = {
  'ok': 'measured',
  'outdated': 'measured with an older version of the miner',
  'benchmarking': 'being measured right now',
  'pending': 'not measured yet',
  'failed': 'its benchmark came to nothing; it is tried again after a wait that grows with every failure',
  'gave up': 'its benchmark failed three times; it is not tried again until the miner program has a new version or you reset it',
  'no offer': 'the pool pays nothing for it at the moment, so it cannot be measured',
  'estimated': 'not measured here: the figure comes from another machine with the same card and is replaced once this is mined',
  'no fit': 'the card has too little memory for it',
  'excluded': 'switched off in the configuration',
};

function renderBenchmarks() {
  const all = state.benchmarks;
  const count = status => all.filter(b => b.status === status).length;
  // only what was measured or has failed can be measured again
  const resettable = b => b.measured_seconds > 0 || b.status === 'failed' || b.status === 'gave up';
  const failed = () => count('failed') + count('gave up');
  const measured = all.filter(resettable).length;
  const goal = state.status ? Math.round(state.status.benchmark_seconds) : 0;

  const action = (title, text, label, cls, n, run) => h('div', { class: 'action' },
    h('h3', {}, title), h('p', {}, text),
    h('div', { class: 'go' }, h('button', { class: cls, disabled: n === 0, onclick: run }, label + (n ? '  (' + n + ')' : ''))));
  const actions = h('div', { class: 'actions' },
    action('Benchmark Updated Miners',
      'Forget the figures that were measured with an older version of a miner program than the one installed now. A new version can be faster or slower.',
      'Benchmark Updated Miners', 'primary', count('outdated'),
      () => resetBenchmarks({ which: 'outdated' }, 'Measure the ' + count('outdated') + ' benchmarks of updated miners again?')),
    action('Benchmark Failed Miners',
      'Let the miners back in whose benchmark came to nothing, also the ones that were given up after three failures, and measure them again with a clean record. Watch them: what failed once may fail again.',
      'Benchmark Failed Miners', 'primary', failed(),
      () => resetBenchmarks({ which: 'failed' }, 'Try the ' + failed() + ' failed benchmarks again?')),
    action('Reset All Benchmarks',
      'Forget everything that was measured. Do this after changing hardware, drivers or overclocking. Measuring everything again takes hours, and until then nothing is mined for profit.',
      'Benchmark All Miners', 'danger', measured,
      () => resetBenchmarks({ which: 'all' }, 'Really forget all ' + measured + ' measurements? Measuring everything again takes hours.')));

  const statusText = b => {
    if (b.status === 'pending' && b.measured_seconds > 0) return 'pending  ' + Math.round(b.measured_seconds) + ' of ' + goal + ' s';
    if (b.status === 'benchmarking') return 'benchmarking  ' + Math.round(b.measured_seconds) + ' of ' + goal + ' s';
    if (b.status === 'failed' && b.failed_until) return 'failed' + (b.failures > 1 ? ' ' + b.failures + ' times' : '') + ', again in ' + fmtUntil(b.failed_until);
    if (b.status === 'gave up') return 'gave up after ' + b.failures + ' failures';
    if (b.status === 'outdated') return 'outdated (v' + b.measured_version + ')';
    return b.status;
  };
  const t = dataTable('benchmarks', [
    { key: 'miner', label: 'Name', cell: b => [b.miner, h('small', { class: 'help' }, '  ' + b.version)] },
    { key: 'algorithm', label: 'Algorithm' },
    { key: 'group', label: 'Device', cell: b => h('span', { title: b.devices.join(', ') }, b.group) },
    { key: 'hashrate', label: 'Speed', num: true, cell: b => fmtHash(b.hashrate) },
    { key: 'rejected_percent', label: 'Rejected', num: true, value: b => (b.rejected_percent === undefined ? -1 : b.rejected_percent),
      cell: b => (b.rejected_percent === undefined ? '' : b.rejected_percent.toFixed(2).replace(/[.]?0+$/, '') + ' %'), cls: b => (b.rejected_percent > 5 ? 'hot' : '') },
    { key: 'power_watts', label: 'Power', num: true, cell: b => fmtWatts(b.power_watts) },
    { key: 'status', label: 'Status', filter: Object.keys(BENCH_STATUS),
      cell: b => [h('span', { class: 'badge ' + b.status.replace(/ /g, ''), title: BENCH_STATUS[b.status] || '' }, statusText(b)),
        b.fail_reason ? h('div', { class: 'reason' }, b.fail_reason) : null], cls: b => (b.fail_reason ? 'wrap' : '') },
    { key: 'updated', label: 'Date', filter: false, value: b => (b.updated ? new Date(b.updated).getTime() : 0), cell: b => fmtDate(b.updated) },
    { key: 'log', label: 'Log', filter: false, value: b => b.log || '',
      cell: b => (b.log ? h('a', { href: '#/console', title: b.log, onclick: () => { loadMinerLog(b.log); } }, 'log') : '') },
  ], all, { empty: 'Nothing yet. The list appears with the first round of the engine.',
    rowClass: b => (b.status === 'benchmarking' ? 'running' : ''),
    select: { id: b => b.hash_key, set: state.benchSelected, can: resettable } });

  const selected = t.rows.filter(resettable).map(b => b.hash_key).filter(k => state.benchSelected.has(k));
  return h('div', {}, h('h1', {}, 'Manage Miner Benchmarks'), actions,
    benchmarkEta(all, goal),
    h('div', { class: 'title' }, h('h2', {}, 'Benchmark Status'),
      h('button', { class: 'primary', disabled: selected.length === 0,
        onclick: () => resetBenchmarks({ hash_keys: selected }, 'Measure the ' + selected.length + ' selected benchmarks again?') },
        'Benchmark selected' + (selected.length ? '  (' + selected.length + ')' : '')),
      h('span', { class: 'help' }, t.rows.length + ' of ' + all.length + ' shown, brought up to date once per round')),
    t.el);
}

// What a benchmark takes besides the measured time: the miner starts, and
// its first 90 seconds do not count.
const BENCH_OVERHEAD = 120;

// benchmarkEta says roughly how long the open benchmarks will take. The
// device groups measure side by side, so the slowest one sets the time.
function benchmarkEta(all, goal, brief) {
  const groups = new Map();
  for (const b of all) {
    if (b.status !== 'pending' && b.status !== 'benchmarking') continue;
    const g = groups.get(b.group) || { name: b.group, count: 0, seconds: 0 };
    g.count++;
    // a benchmark under way has its start behind it
    g.seconds += Math.max(0, goal - b.measured_seconds) + (b.status === 'benchmarking' && b.measured_seconds > 0 ? 0 : BENCH_OVERHEAD);
    groups.set(b.group, g);
  }
  if (!groups.size || !goal) return null;
  const list = Array.from(groups.values()).sort((a, b) => b.seconds - a.seconds);
  const total = list.reduce((sum, g) => sum + g.count, 0);
  const paused = state.status && state.status.system.paused;
  return h('div', { class: 'eta' },
    h('div', { class: 'main' }, paused
      ? total + ' benchmarks are open. Mining is paused, so nothing is measured.'
      : 'About ' + fmtDuration(list[0].seconds) + ' until all ' + total + ' open benchmarks are measured.'),
    h('div', { class: 'help' }, list.map(g => g.name + ': ' + g.count + ' to go, about ' + fmtDuration(g.seconds)).join('   ·   ')),
    brief ? h('div', { class: 'help' }, 'A rough figure. ', h('a', { href: '#/benchmarks' }, 'Benchmarks'), ' has the list.')
      : h('div', { class: 'help' }, 'A rough figure: it counts ' + Math.round(goal / 60) + ' min of measuring and 2 min of starting per benchmark. Miners that have to be downloaded, build a DAG, wait for memory or fail make it longer. Algorithms without an offer are not counted; they are measured once a pool pays for them.'));
}

async function resetBenchmarks(body, question) {
  if (!confirm(question)) return;
  try {
    await api('/api/benchmarks/reset', { method: 'POST', body });
    state.benchSelected.clear();
    toast('Will be measured again');
    setTimeout(refreshStatus, 2000);
  } catch (e) { toast(e.message, true); }
}

// -------------------------------------------------------------- activity

function renderActivity() {
  const minutes = r => (new Date(r.ended) - new Date(r.started)) / 60000;
  const rejected = r => (r.accepted + r.rejected > 0 ? 100 * r.rejected / (r.accepted + r.rejected) : -1);
  const satKWh = r => (r.energy_kwh > 0.0005 && r.earned_btc ? r.earned_btc * 1e8 / r.energy_kwh : 0);
  const t = dataTable('activity', [
    { key: 'end', label: 'End', filter: ['switched', 'crashed', 'no hashrate', 'paused', 'stopped'],
      cell: r => h('span', { class: 'badge ' + r.end.replace(/ /g, '') }, r.end === 'crashed' || r.end === 'no hashrate' ? r.end : 'ok') },
    { key: 'started', label: 'Started', filter: false, value: r => new Date(r.started).getTime(), cell: r => fmtDate(r.started) },
    { key: 'ended', label: 'Last Seen', filter: false, value: r => new Date(r.ended).getTime(), cell: r => fmtDate(r.ended) },
    { key: 'min', label: 'Min', num: true, value: minutes, cell: r => minutes(r).toFixed(2) },
    { key: 'miner', label: 'Name' },
    { key: 'algorithm', label: 'Algorithms', cell: r => r.algorithm + (r.benchmark ? '  (benchmark)' : '') },
    { key: 'group', label: 'Devices' },
    { key: 'pool', label: 'Pool', value: r => (r.coin ? r.pool + ' : ' + r.coin : r.pool) },
    { key: 'hashrate', label: 'Speed', num: true, cell: r => fmtHash(r.hashrate) },
    { key: 'earned_btc', label: 'Earned', num: true, cell: r => fmtSat(r.earned_btc) },
    { key: 'energy_kwh', label: 'Power', num: true, cell: r => (r.energy_kwh ? r.energy_kwh.toFixed(3) + ' kWh' : '') },
    { key: 'satkwh', label: 'sat/kWh', num: true, value: satKWh, cell: r => (satKWh(r) ? satKWh(r).toFixed(2) : '') },
    { key: 'rejected', label: 'Rejected', num: true, value: rejected, cell: r => (rejected(r) < 0 ? '' : rejected(r).toFixed(2) + ' %') },
  ], state.activity, { empty: 'No miner has finished a run yet.', sort: { key: 'ended', dir: -1 } });
  return h('div', {}, h('h1', {}, 'Active Miners over the past 3 days'),
    h('p', { class: 'lead' }, '"Earned" is an estimate from the measured hashrate and the pool\'s price at the time, not what the pool has credited.'),
    h('div', { class: 'toolbar' }, h('a', { class: 'button', href: '/api/activity.csv' }, 'Download as CSV'),
      h('span', { class: 'help' }, t.rows.length + ' of ' + state.activity.length + ' runs shown')),
    t.el);
}

// ---------------------------------------------------------------- shared

// sharedCards arranges the shared figures the way they are read: by card,
// then by algorithm, with the miner program that earns most first. An
// algorithm some pool pays for right now gets what it would earn in a day.
function sharedCards(entries) {
  const price = new Map();
  for (const o of state.offers || []) {
    if (o.price > (price.get(o.algorithm) || 0)) price.set(o.algorithm, o.price);
  }
  const fee = new Map((state.meta ? state.meta.miners : []).map(m => [m.name, m.fee_percent || 0]));
  // what is left of a hashrate after the miner program's own fee
  const net = e => e.hashrate * (1 - (fee.get(e.miner) || 0) / 100);
  const cards = new Map();
  for (const e of entries) {
    if (!cards.has(e.device)) cards.set(e.device, { device: e.device, mine: false, algos: new Map() });
    const c = cards.get(e.device);
    c.mine = c.mine || e.mine;
    if (!c.algos.has(e.algorithm)) c.algos.set(e.algorithm, []);
    c.algos.get(e.algorithm).push(e);
  }
  return Array.from(cards.values()).map(c => {
    const algos = Array.from(c.algos, ([algorithm, miners]) => {
      miners.sort((x, y) => net(y) - net(x) || x.miner.localeCompare(y.miner));
      const paid = price.get(algorithm) || 0;
      return { algorithm, miners, best: miners[0], earns: paid * net(miners[0]), paid: paid > 0 };
    });
    algos.sort((x, y) => y.earns - x.earns || x.algorithm.localeCompare(y.algorithm));
    return { device: c.device, mine: c.mine, algos };
  }).sort((x, y) => (y.mine - x.mine) || x.device.localeCompare(y.device, undefined, { numeric: true }));
}

function renderShared() {
  const info = state.shared;
  const all = sharedCards(info.entries);
  const hasMine = all.some(c => c.mine);
  const words = (state.sharedSearch || '').trim().toLowerCase().split(/\s+/).filter(Boolean);
  const matches = (c, a) => words.every(w => (c.device + ' ' + a.algorithm + ' ' + a.miners.map(m => m.miner).join(' ')).toLowerCase().includes(w));
  const cards = all.filter(c => !state.sharedMineOnly || !hasMine || c.mine)
    .map(c => Object.assign({}, c, { shown: c.algos.filter(a => matches(c, a)) }))
    .filter(c => c.shown.length > 0);
  const money = btc => fiat(btc) || (btc * 1e8).toFixed(0) + ' sat';

  // ---- the figures at the top
  const algorithms = new Set(info.entries.map(e => e.algorithm));
  const rigs = info.entries.reduce((n, e) => Math.max(n, e.reports), 0);
  const measuredHere = info.entries.filter(e => e.own_hashrate > 0).length;
  let top = null;
  for (const c of all.filter(x => x.mine)) {
    for (const a of c.algos) if (a.paid && (!top || a.earns > top.earns)) top = Object.assign({ device: c.device }, a);
  }
  const stats = h('div', { class: 'cards' },
    card('Cards', all.length, hasMine ? all.filter(c => c.mine).length + ' of them in this rig' : 'none of them in this rig'),
    card('Algorithms', algorithms.size, info.entries.length + ' hashrates in all'),
    card('Rigs', rigs > 0 ? rigs : '-', rigs > 0 ? 'at most, behind one figure' : 'only the figures hashsteer comes with'),
    card('Last fetched', info.fetched ? fmtAgo(info.fetched) + ' ago' : 'never', info.url ? info.url.replace(/^https?:\/\//, '') : 'no share server is set'),
    card('This rig', info.upload ? 'shares' : 'does not share', measuredHere + ' of these measured here'),
    top ? card('Pays best right now', money(top.earns) + '/day', top.algorithm + ' with ' + top.best.miner + ' on one ' + top.device) : null);

  // ---- one block per card
  const diff = e => (e.own_hashrate > 0 ? 100 * (e.own_hashrate - e.hashrate) / e.hashrate : null);
  const minerRow = (c, a, e, first) => {
    const key = c.device + '|' + a.algorithm;
    const open = state.sharedOpen.has(key);
    const more = a.miners.length - 1;
    const d = diff(e);
    return h('tr', { class: first ? '' : 'other' },
      h('td', {}, first ? a.algorithm : ''),
      h('td', {}, e.miner, h('small', { class: 'help' }, '  ' + (e.version || '')),
        first && more > 0 ? h('button', { class: 'small more', title: 'The other miner programs measured for this algorithm',
          onclick: () => { if (open) state.sharedOpen.delete(key); else state.sharedOpen.add(key); render(); } }, open ? 'hide' : '+' + more + ' slower') : null),
      h('td', { class: 'num' }, fmtHash(e.hashrate)),
      h('td', { class: 'num' }, fmtWatts(e.power_watts)),
      h('td', { class: 'num' }, e.power_watts > 0 ? fmtHash(e.hashrate / e.power_watts).replace('/s', '/W') : ''),
      h('td', { class: 'num' }, e.reports > 0 ? e.reports : h('span', { class: 'help', title: 'This figure comes with hashsteer, not from the share server.' }, 'built in')),
      h('td', { class: 'num' }, e.own_hashrate > 0 ? fmtHash(e.own_hashrate) : ''),
      h('td', { class: 'num ' + (d === null ? '' : d >= 0 ? 'pos' : d < -10 ? 'neg' : '') }, d === null ? '' : (d > 0 ? '+' : '') + d.toFixed(1) + ' %'),
      h('td', { class: 'num' + (first && a.paid ? ' earns' : '') }, first ? (a.paid ? money(a.earns) : h('span', { class: 'help', title: 'No enabled pool pays for this algorithm right now.' }, 'no offer')) : ''));
  };
  const block = c => {
    const closed = state.sharedClosed.has(c.device);
    const best = c.algos.find(a => a.paid);
    const rows = [];
    for (const a of c.shown) {
      rows.push(minerRow(c, a, a.best, true));
      if (state.sharedOpen.has(c.device + '|' + a.algorithm)) for (const e of a.miners.slice(1)) rows.push(minerRow(c, a, e, false));
    }
    return h('div', { class: 'sharedcard' },
      h('div', { class: 'head', onclick: () => { if (closed) state.sharedClosed.delete(c.device); else state.sharedClosed.add(c.device); render(); } },
        h('span', { class: 'fold' }, closed ? '+' : '-'),
        h('h2', {}, c.device),
        c.mine ? h('span', { class: 'badge ok' }, 'in this rig') : null,
        h('span', { class: 'help' }, c.algos.length + (c.algos.length === 1 ? ' algorithm' : ' algorithms') +
          (best ? ', best right now: ' + best.algorithm + ' at ' + money(best.earns) + '/day' : ''))),
      closed ? null : h('table', {},
        h('thead', {}, h('tr', {},
          h('th', {}, 'Algorithm'), h('th', {}, 'Fastest miner'), h('th', { class: 'num' }, 'Speed'), h('th', { class: 'num' }, 'Power'),
          h('th', { class: 'num', title: 'Hashes per second for each watt' }, 'per watt'),
          h('th', { class: 'num', title: 'How many rigs the figure is the middle of' }, 'Rigs'),
          h('th', { class: 'num' }, 'Measured here'), h('th', { class: 'num' }, 'Difference'),
          h('th', { class: 'num', title: 'What one card would earn in a day at the best price an enabled pool pays right now, after the fees of pool and miner program, before electricity' }, 'Earns/day'))),
        h('tbody', {}, rows)));
  };

  const where = info.url
    ? [info.fetched ? '' : 'Nothing was fetched from ' + info.url + ' yet; the figures below come with hashsteer. ',
      info.download ? '' : 'Fetching is turned off. ']
    : ['No share server is set; the figures below come with hashsteer. '];
  const shownAlgos = cards.reduce((n, c) => n + c.shown.length, 0);
  return h('div', {},
    h('div', { class: 'title' }, h('h1', {}, 'Shared Hashrates'),
      h('button', { class: 'primary', disabled: !info.url, onclick: refreshShared }, info.upload ? 'Fetch and share now' : 'Fetch now')),
    h('p', { class: 'lead' }, 'What other rigs measured for one card of a model: the middle of what was reported. Per algorithm the fastest miner program is shown, with what it would earn at today\'s prices; the slower ones are a click away. ' +
      (info.used ? 'hashsteer starts from these figures for your cards until it has measured them itself.' : 'They are not used: "Start from estimates" is turned off under Setup.')),
    stats,
    h('p', { class: 'help' }, where.join(''), 'The same card is faster or slower with another driver, clock or power limit; "Measured here" shows what yours does. ', h('a', { href: '#/setup' }, 'Sharing is set under Setup.')),
    h('div', { class: 'toolbar' },
      h('input', { type: 'search', placeholder: 'algorithm, miner, card', value: state.sharedSearch || '',
        oninput: e => { state.sharedSearch = e.target.value; render(); const box = document.querySelector('.toolbar input[type=search]'); if (box) { box.focus(); box.setSelectionRange(box.value.length, box.value.length); } } }),
      hasMine ? h('label', {}, h('input', { type: 'checkbox', checked: state.sharedMineOnly,
        onchange: e => { state.sharedMineOnly = e.target.checked; render(); } }), ' only the cards of this rig') : null,
      h('span', { class: 'help' }, shownAlgos + ' algorithms on ' + cards.length + (cards.length === 1 ? ' card' : ' cards') + ' shown')),
    cards.length ? cards.map(block) : h('div', { class: 'empty' }, info.entries.length ? 'Nothing matches.' : 'No shared hashrates are known.'));
}

// problemLine says that a miner program cannot run, and offers to install
// what it lacks where hashsteer knows how.
function problemLine(p) {
  const busy = state.fixing === p.miner;
  return h('div', { class: 'problem' },
    h('span', {}, p.miner + ' cannot run on this machine: ' + p.reason),
    p.command ? [h('span', {}, ' Install ' + p.package + ' now?'),
      h('button', { class: 'small', disabled: Boolean(state.fixing), title: 'Runs: ' + p.command, onclick: () => fixProblem(p) }, busy ? 'Installing ...' : 'Yes, install'),
      h('code', {}, p.command)] : null);
}

async function fixProblem(p) {
  if (!confirm('Install ' + p.package + ' on this machine?\n\nhashsteer runs:\n' + p.command + '\n\nThis changes the system. On Linux it needs sudo to work without a password; on Windows the system may ask on the desktop whether to allow it.')) return;
  state.fixing = p.miner;
  render();
  try {
    await api('/api/problems/fix', { method: 'POST', body: { miner: p.miner } });
    toast(p.package + ' is installed, ' + p.miner + ' is tried again');
  } catch (e) {
    alert(e.message);
  }
  state.fixing = '';
  await refreshStatus();
  render();
}

async function refreshShared() {
  try {
    await api('/api/shared/refresh', { method: 'POST' });
    state.shared = await api('/api/shared');
    toast('Up to date');
  } catch (e) { toast(e.message, true); }
  render();
}

// -------------------------------------------------------------- watchdog

function renderWatchdog() {
  const s = state.status;
  if (!s) return h('div', { class: 'empty' }, 'Loading ...');
  const rows = [];
  for (const g of s.groups) {
    for (const d of g.disabled || []) rows.push(Object.assign({ group: g.name, hash_key: d.hash_key, out: true, until: d.until, reason: d.reason, failures: d.failures || 0, gave_up: Boolean(d.gave_up) }, parseKey(d.hash_key)));
    for (const c of g.crashes || []) rows.push(Object.assign({ group: g.name, hash_key: c.hash_key, out: false, count: c.count, last: c.last }, parseKey(c.hash_key)));
  }
  const t = dataTable('watchdog', [
    { key: 'miner', label: 'Minername' },
    { key: 'algorithm', label: 'Algorithm' },
    { key: 'group', label: 'Device' },
    { key: 'state', label: 'State', value: r => (r.gave_up ? 'gave up' : r.out ? 'taken out' : 'counting'), filter: ['taken out', 'gave up', 'counting'],
      cls: r => (r.reason ? 'wrap' : ''),
      cell: r => (r.out ? [h('span', { class: 'badge ' + (r.gave_up ? 'gaveup' : 'failed') },
        r.gave_up ? 'gave up after ' + r.failures + ' failed benchmarks'
          : (r.failures ? 'benchmark failed' + (r.failures > 1 ? ' ' + r.failures + ' times' : '') : 'taken out') + ', tried again in ' + fmtUntil(r.until)),
      r.reason ? h('div', { class: 'reason' }, r.reason) : null]
        : h('span', { class: 'badge todo' }, r.count + (r.count === 1 ? ' failure' : ' failures') + ' in the last hour')) },
    { key: 'last', label: 'Last', filter: false, value: r => (r.last ? new Date(r.last).getTime() : 0), cell: r => (r.last ? fmtAgo(r.last) + ' ago' : '') },
    { key: 'action', label: '', filter: false, value: () => '',
      cell: r => (r.out ? h('button', { class: 'small', onclick: () => enableCandidate(r.hash_key) }, 'Try again now') : '') },
  ], rows, { empty: 'No miner has failed lately.' });
  return h('div', {},
    h('div', { class: 'title' }, h('h1', {}, 'Watchdog Timers'),
      h('button', { class: 'primary', disabled: rows.length === 0, onclick: resetWatchdog }, 'Reset Watchdog')),
    h('p', { class: 'lead' }, 'A miner that exits on its own or reports no hashrate for five minutes is restarted. A miner program that cannot start because a library or a driver is missing is taken out at once with all its algorithms, and said so at the top of every page; Reset Watchdog lets it try again once you have installed what it lacks. So is one that reports a hashrate while its graphics cards stand still and no share is accepted for five minutes; that counts as a failed benchmark, and a hashrate stored from such a run is dropped. One that has mined here before sits out an hour after three failures within an hour. A benchmark that comes to nothing is tried again after six hours, and after a second failure after a day. After the third failure it is given up: not tried again at all, until the miner program has a new version or you let it back in here. This is remembered across restarts.'),
    t.el);
}

async function resetWatchdog() {
  try {
    await api('/api/watchdog/reset', { method: 'POST' });
    toast('Watchdog reset');
    setTimeout(refreshStatus, 1500);
  } catch (e) { toast(e.message, true); }
}

async function enableCandidate(hashKey) {
  try {
    await api('/api/candidates/enable', { method: 'POST', body: { hash_key: hashKey } });
    toast('Enabled again');
    setTimeout(refreshStatus, 1500);
  } catch (e) { toast(e.message, true); }
}

// ----------------------------------------------------------------- setup

function getPath(obj, path) { return path.split('.').reduce((o, k) => (o === undefined || o === null ? o : o[k]), obj); }
function setPath(obj, path, value) {
  const keys = path.split('.');
  const last = keys.pop();
  keys.reduce((o, k) => o[k], obj)[last] = value;
}

function field(label, help, control) {
  return h('div', { class: 'field' }, h('label', {}, label), h('div', { class: 'control' }, control), help ? h('div', { class: 'help' }, help) : null);
}

function textField(label, path, help, type) {
  return field(label, help, h('input', { type: type || 'text', value: getPath(state.draft, path),
    oninput: e => setPath(state.draft, path, e.target.value) }));
}

function numberField(label, path, help, step) {
  return field(label, help, h('input', { type: 'number', step: step || 'any', min: '0', value: getPath(state.draft, path),
    oninput: e => setPath(state.draft, path, e.target.value === '' ? 0 : Number(e.target.value)) }));
}

function boolField(label, path, help) {
  return field(label, help, h('input', { type: 'checkbox', checked: getPath(state.draft, path),
    onchange: e => setPath(state.draft, path, e.target.checked) }));
}

function listField(label, path, help) {
  return field(label, help, h('input', { type: 'text', value: (getPath(state.draft, path) || []).join(', '),
    oninput: e => setPath(state.draft, path, e.target.value.split(/[\s,]+/).filter(Boolean)) }));
}

function selectField(label, path, help, options) {
  const current = getPath(state.draft, path);
  const values = options.map(o => o.value);
  if (!values.includes(current)) options = [{ value: current, text: current }].concat(options);
  return field(label, help, h('select', { onchange: e => { setPath(state.draft, path, e.target.value); render(); } },
    options.map(o => h('option', { value: o.value, selected: o.value === current }, o.text))));
}

// toggleList shows one checkbox per item; checked means "not in the list".
function toggleList(items, path, text) {
  const list = getPath(state.draft, path);
  return h('div', { class: 'checks' }, items.map(item => h('label', {},
    h('input', { type: 'checkbox', checked: !list.includes(item), onchange: e => {
      const next = getPath(state.draft, path).filter(x => x !== item);
      if (!e.target.checked) next.push(item);
      setPath(state.draft, path, next);
    } }), text ? text(item) : item)));
}

const SETUP = {
  setup: ['Setup', 'config.yaml', setupGeneral],
  setupwallets: ['Setup Wallets', 'pools.yaml', setupWallets],
  setupcoins: ['Setup Coins', 'pools.yaml', setupCoins],
  setuppools: ['Setup Pools', 'pools.yaml', setupPools],
  setupminers: ['Setup Miners', 'miners.yaml', setupMiners],
  setupalgorithms: ['Setup Algorithms', 'algorithms.yaml', setupAlgorithms],
  setupdevices: ['Setup Devices', 'devices.yaml', setupDevices],
};

function renderSetup() {
  if (!state.draft || !state.meta) return h('div', { class: 'empty' }, 'Loading ...');
  const [title, file, body] = SETUP[state.page];
  const changed = JSON.stringify(state.draft) !== JSON.stringify(state.config);
  const savebar = h('div', { class: 'savebar' },
    h('button', { class: 'primary', onclick: saveConfig }, 'Save and apply'),
    h('button', { onclick: () => { state.draft = JSON.parse(JSON.stringify(state.config)); state.walletRows = null; render(); } }, 'Discard changes'),
    h('span', { class: 'error', id: 'config-error' }),
    h('span', { class: 'note' }, 'Saving writes all setup pages to ' + state.meta.config_dir + ' and restarts the miners with the new settings.'
      + (changed ? ' There are unsaved changes.' : '')));
  return h('div', {}, h('h1', {}, title),
    h('p', { class: 'lead' }, 'File: ', h('code', {}, file)),
    h('div', { class: 'form' + (state.page === 'setupcoins' ? ' wide' : '') }, body()), savebar);
}

// setupDependencies lists what the miner programs need from the system and
// whether it is there. It changes nothing by being looked at.
function setupDependencies() {
  const list = state.dependencies;
  const check = async () => {
    try { state.dependencies = await api('/api/dependencies'); } catch (e) { toast(e.message, true); }
    render();
  };
  if (list === null) {
    // asked for once when the page is first shown
    state.dependencies = [];
    check();
  }
  const install = async need => {
    if (!confirm('Install ' + need.package + ' on this machine?\n\nhashsteer runs:\n' + need.command + '\n\nThis changes the system. On Linux it needs sudo to work without a password; on Windows the system may ask on the desktop whether to allow it.')) return;
    state.installing = need.library;
    render();
    try {
      state.dependencies = await api('/api/dependencies/install', { method: 'POST', body: { library: need.library } });
      toast(need.package + ' is installed');
    } catch (e) { alert(e.message); }
    state.installing = '';
    await check();
  };
  const needLine = need => h('div', { class: 'need' },
    h('span', { class: 'badge ' + (need.present ? 'ok' : 'failed') }, need.present ? 'found' : 'missing'),
    h('code', {}, need.library),
    need.present ? null
      : need.command ? [h('button', { class: 'small', disabled: Boolean(state.installing), title: 'Runs: ' + need.command, onclick: () => install(need) },
        state.installing === need.library ? 'Installing ...' : 'Install ' + need.package), h('span', { class: 'help' }, need.command)]
        : h('span', { class: 'help' }, need.from_driver ? 'comes with the NVIDIA driver: install or update the driver' : 'hashsteer knows no package for it: install it with the package manager of your system'));
  const missing = (list || []).reduce((n, d) => n + d.needs.filter(x => !x.present).length, 0);
  return h('div', { class: 'section' }, h('h3', {}, 'Dependencies'),
    h('p', { class: 'help' }, 'What the miner programs need from this system besides themselves: libraries they do not bring along. A program that lacks one cannot start and is taken out. Where hashsteer knows the package for a missing library, it installs it when you ask it to here; it installs nothing by itself. Looking at this list changes nothing.'),
    list && list.length ? h('table', { class: 'coins' },
      h('thead', {}, h('tr', {}, h('th', {}, 'Miner program'), h('th', {}, 'For'), h('th', {}, 'Needs from the system'))),
      h('tbody', {}, list.map(d => h('tr', {},
        h('td', { class: 'sym' }, d.miner, h('small', { class: 'help' }, '  ' + d.version)),
        h('td', {}, d.vendor),
        h('td', {}, d.needs.length ? d.needs.map(needLine) : h('span', { class: 'help' }, 'nothing: it brings everything along')))))) : h('div', { class: 'empty' }, 'Checking ...'),
    h('div', { class: 'go' }, h('button', { onclick: check }, 'Check again'),
      h('span', { class: 'help' }, list && list.length ? (missing ? '  ' + missing + (missing === 1 ? ' library is' : ' libraries are') + ' missing.' : '  Everything the miner programs need is there.') : '')));
}

function setupGeneral() {
  return [
    h('div', { class: 'section' }, h('h3', {}, 'Basic Setup'),
      textField('Worker Name', 'general.worker', 'Names this rig at the pools. Letters, digits, - and _.'),
      textField('Data directory', 'general.data_dir', 'Miner programs, logs and the statistics database are kept here.')),
    h('div', { class: 'section' }, h('h3', {}, 'Set Electricity Cost Handling'),
      numberField('Price per kWh', 'general.power.price_per_kwh', 'With 0, electricity is not counted. Otherwise the cost of the measured power draw is taken off the earnings.', '0.01'),
      textField('Currency', 'general.power.currency', 'Three letter code, e.g. EUR or USD. Earnings are shown in it, and the price above is in it.'),
      boolField('Mine at a loss', 'general.switching.mine_at_loss', 'Keep mining when electricity costs more than is earned.')),
    h('div', { class: 'section' }, h('h3', {}, 'Profit Switching'),
      textField('Round length', 'general.switching.interval', 'How often hashrates are stored and the choice of miner is reconsidered. 30s to 1h, e.g. 1m.'),
      numberField('Hysteresis in percent', 'general.switching.hysteresis_percent', 'Another candidate must earn this much more before the running miner is stopped for it.'),
      textField('Minimum run time', 'general.switching.min_run_time', 'A started miner is left alone for at least this long, e.g. 5m.'),
      textField('Benchmark time', 'general.switching.benchmark_time', 'Measured mining time a miner needs before its hashrate is trusted, e.g. 3m.'),
      boolField('Start from estimates', 'general.switching.use_estimates', 'Where hashsteer comes with a hashrate that was measured on another machine with the same card, it mines by that figure at once instead of benchmarking first. What is mined is measured, and the measurement replaces the estimate. The same card can be faster or slower here; turn this off to measure everything yourself before anything is mined for profit.')),
    h('div', { class: 'section' }, h('h3', {}, 'CPU'),
      boolField('Mine with the CPU', 'general.cpu.enabled', 'Off by default: it makes the machine slow for everything else, and the GPU miners need some CPU time themselves.'),
      numberField('CPU Mining Threads', 'general.cpu.threads', '0 lets the miner decide.', '1'),
      numberField('Assumed power draw in watts', 'general.cpu.watts', 'Used for the electricity cost; a CPU cannot be measured the way a GPU can.')),
    h('div', { class: 'section' }, h('h3', {}, 'Memory'),
      numberField('Min. Free RAM, in GB', 'general.memory.min_free_ram_gb', 'Main memory (RAM and page file together) that is left for everything else. A miner whose algorithm needs more than the rest is not started. This is what keeps two miners from building a large DAG at the same time and running the machine out of memory.', '0.5'),
      numberField('Reserved GPU VRAM, in GB', 'general.memory.reserved_vram_gb', 'Graphics memory that is left alone on every card, for example for the desktop.', '0.1')),
    h('div', { class: 'section' }, h('h3', {}, 'Shared hashrates'),
      textField('Server', 'general.sharing.url', 'The address rigs exchange their measured hashrates through. Empty exchanges nothing; the estimates that come with hashsteer are still used.'),
      boolField('Fetch what others measured', 'general.sharing.download', 'Twice a day, fetch the hashrates other rigs measured with the same cards, to start from them. They count from the next start of the miners.'),
      boolField('Share what this rig measured', 'general.sharing.upload', 'Twice a day, send the hashrates measured here: card model, miner program with version, algorithm, hashrate and power draw, under a random number made up for this rig. No wallet, no rig name. Off unless you turn it on.')),
    h('div', { class: 'section' }, h('h3', {}, 'Updates'),
      field('This version', state.update && state.update.latest
        ? (state.update.available ? 'hashsteer ' + state.update.latest + ' is available.' : 'This is the newest version.') : 'GitHub has not been asked yet.',
        [h('code', {}, state.meta.version), ' ', h('button', { class: 'small', onclick: checkUpdate }, 'Check now')]),
      boolField('Look for updates', 'general.updates.check', 'Ask GitHub a few times a day whether there is a newer release, and say so on the dashboard. Nothing is installed by this.'),
      boolField('Install updates by themselves', 'general.updates.auto', 'Fetch and start a newer release without asking, as soon as it is found. Off by default: hashsteer then runs whatever is published as a release of this project, without anyone looking at it first. It only works where hashsteer was started with start.bat or start.sh.')),
    h('div', { class: 'section' }, h('h3', {}, 'Machine\'s localhost API setup'),
      textField('Listen address', 'general.api.listen', 'host:port. 127.0.0.1 is only reachable from this machine. A change takes effect after hashsteer is restarted. Empty turns the interface off.'),
      textField('Password', 'general.api.password', 'Without a password the interface only works on this machine. With one, every access needs it (the user name does not matter). Use it only on a network you trust: the connection is not encrypted.', 'password')),
    setupDependencies()
  ];
}

// walletRows are the lines of the wallet list while it is edited. The
// configuration holds the wallets by coin, which a half-typed line cannot be.
function walletRows() {
  if (!state.walletRows) {
    state.walletRows = Object.entries(state.draft.pools.wallets).map(([coin, address]) => ({ coin, address }));
  }
  return state.walletRows;
}

function storeWallets() {
  const wallets = {};
  for (const row of state.walletRows) {
    const coin = row.coin.trim().toUpperCase();
    if (coin || row.address.trim()) wallets[coin] = row.address.trim();
  }
  state.draft.pools.wallets = wallets;
}

// walletUsers names the pools that use the wallet of a coin, as the draft
// has them set up.
function walletUsers(coin) {
  const pools = state.draft.pools;
  const users = [];
  if (pools.zpool.enabled && pools.zpool.currency === coin && !pools.zpool.wallet) users.push('zpool (all payouts)');
  for (const pool of state.meta.coin_pools) {
    const settings = pools[pool.key];
    if (settings.enabled && pool.coins.some(c => c.symbol === coin) && !settings.exclude_coins.includes(coin)) users.push(pool.title);
  }
  return users;
}

function setupWallets() {
  const known = new Map();
  for (const pool of state.meta.coin_pools) for (const c of pool.coins) known.set(c.symbol, c.name);
  for (const c of state.meta.payout_currencies) if (!known.has(c.symbol)) known.set(c.symbol, c.name);
  const rows = walletRows();
  const have = new Set(rows.map(r => r.coin.trim().toUpperCase()));
  // what the enabled pools could do with a wallet they do not have yet
  const missing = [];
  const pools = state.draft.pools;
  if (pools.zpool.enabled && !pools.zpool.wallet && !have.has(pools.zpool.currency)) missing.push(pools.zpool.currency + ' (zpool pays out in it)');
  for (const pool of state.meta.coin_pools) {
    if (!pools[pool.key].enabled) continue;
    const coins = pool.coins.map(c => c.symbol).filter(s => !have.has(s) && !pools[pool.key].exclude_coins.includes(s));
    if (coins.length) missing.push(coins.join(', ') + ' (' + pool.title + ')');
  }
  const users = row => {
    const coin = row.coin.trim().toUpperCase();
    const list = coin ? walletUsers(coin) : [];
    return list.length ? list.join(', ') : h('span', { class: 'help' }, coin ? 'no enabled pool' : '');
  };
  const line = row => h('tr', {},
    h('td', {}, h('input', { type: 'text', list: 'wallet-coins', placeholder: 'RVN', size: '8', value: row.coin,
      oninput: e => { row.coin = e.target.value; storeWallets(); } })),
    h('td', {}, h('input', { type: 'text', class: 'wide', placeholder: 'address', value: row.address, spellcheck: 'false',
      oninput: e => { row.address = e.target.value; storeWallets(); } })),
    h('td', { class: 'users' }, users(row)),
    h('td', {}, h('button', { class: 'small', onclick: () => { rows.splice(rows.indexOf(row), 1); storeWallets(); render(); } }, 'Remove')));
  return h('div', { class: 'section' }, h('h3', {}, 'Wallets'),
    h('p', { class: 'help' }, 'One payout address per coin, used by every pool that can. ' + state.meta.coin_pools.map(p => p.title).join(' and ') + ' pay every coin to its own wallet and mine the coins that have one here. zpool pays everything to the wallet of its payout coin. hashsteer cannot check an address; a wrong one is only noticed by the pool, or not at all.'),
    rows.length ? h('table', { class: 'wallets' },
      h('thead', {}, h('tr', {}, h('th', {}, 'Coin'), h('th', {}, 'Address'), h('th', {}, 'Used by'), h('th', {}))),
      h('tbody', {}, rows.map(line))) : h('div', { class: 'empty' }, 'No wallet yet.'),
    h('datalist', { id: 'wallet-coins' }, Array.from(known).map(([symbol, name]) => h('option', { value: symbol }, name))),
    h('div', { class: 'go' }, h('button', { onclick: () => { rows.push({ coin: '', address: '' }); render(); } }, 'Add a wallet')),
    missing.length ? h('p', { class: 'help' }, 'The enabled pools could also use a wallet for: ' + missing.join('; ') + '.') : null,
    boolField('Do not ask block explorers for the wallet balances', 'pools.no_wallet_balances', 'The dashboard shows what has arrived at each wallet. For that the address is sent every ten minutes to a public block explorer of its coin, which sees it together with your internet address. Balances are known for ' + (state.meta.wallet_coins || []).join(', ') + '. Tick this to keep the addresses between you and the pools.'));
}

// zpoolWalletField says which wallet zpool pays to. An address of its own
// is only shown where an older configuration still has one that differs.
function zpoolWalletField() {
  const pools = state.draft.pools;
  if (pools.zpool.wallet) {
    return textField('Wallet', 'pools.zpool.wallet', 'An address for zpool alone, from an older configuration. Empty it to use the ' + pools.zpool.currency + ' wallet from Setup Wallets like every other pool.');
  }
  const address = pools.wallets[pools.zpool.currency];
  return field('Wallet', 'zpool pays to the wallet of its payout coin. Wallets are set under Setup Wallets.',
    address ? h('code', {}, address)
      : h('span', { class: 'error' }, 'There is no ' + pools.zpool.currency + ' wallet yet. ', h('a', { href: '#/setupwallets' }, 'Add one')));
}

// setupCoins lists every coin that could be mined as a single coin, with
// what stands in its way, and one switch per coin that holds for all pools.
function setupCoins() {
  const pools = state.draft.pools;
  const mined = minedAlgorithms();
  const coins = new Map();
  const coin = symbol => {
    if (!coins.has(symbol)) coins.set(symbol, { symbol, name: '', algorithms: new Set(), pools: [] });
    return coins.get(symbol);
  };
  for (const pool of state.meta.coin_pools) {
    for (const c of pool.coins) {
      const entry = coin(c.symbol);
      entry.name = entry.name || c.name;
      entry.algorithms.add(c.algorithm);
      const settings = pools[pool.key];
      entry.pools.push({ title: pool.title, usable: settings.enabled && !settings.exclude_coins.includes(c.symbol),
        why: !settings.enabled ? 'pool is off' : settings.exclude_coins.includes(c.symbol) ? 'unchecked at the pool' : '' });
    }
  }
  // what zpool mines as single coins, as far as it is known here
  const atZpool = new Set(pools.zpool.coins);
  for (const symbol of pools.zpool.coins) coin(symbol);
  for (const g of (state.status ? state.status.groups : [])) {
    for (const c of g.candidates || []) {
      if (c.pool === 'zpool' && c.coin) { coin(c.coin).algorithms.add(c.algorithm); atZpool.add(c.coin); }
    }
  }
  for (const entry of coins.values()) {
    const viaZpool = pools.zpool.coin_mining && (!pools.zpool.coins.length || pools.zpool.coins.includes(entry.symbol));
    if (viaZpool && atZpool.has(entry.symbol)) {
      entry.pools.push({ title: 'zpool', usable: pools.zpool.enabled && !pools.zpool.exclude_coins.includes(entry.symbol),
        why: !pools.zpool.enabled ? 'pool is off' : pools.zpool.exclude_coins.includes(entry.symbol) ? 'excluded at the pool' : '', zpool: true });
    }
  }
  // what each coin would earn right now, at best
  const best = new Map();
  for (const g of (state.status ? state.status.groups : [])) {
    for (const c of g.candidates || []) {
      if (!c.coin) continue;
      const have = best.get(c.coin);
      if (!have || c.profit_btc_per_day > have.profit) best.set(c.coin, { profit: c.profit_btc_per_day, group: g.name, pool: c.pool, measured: c.benchmarked });
    }
  }

  const rows = Array.from(coins.values()).sort((a, b) => a.symbol.localeCompare(b.symbol));
  const line = entry => {
    const off = pools.exclude_coins.includes(entry.symbol);
    const usable = entry.pools.filter(p => p.usable);
    // a coin pool pays to the coin's own wallet, zpool to the wallet of its payout coin
    const needsWallet = usable.some(p => !p.zpool);
    const algorithms = Array.from(entry.algorithms);
    const problems = [];
    if (!usable.length) problems.push(entry.pools.length ? 'no pool: ' + entry.pools.map(p => p.title + ' ' + (p.why || 'is usable')).join(', ') : 'no pool offers it');
    if (needsWallet && !pools.wallets[entry.symbol] && !usable.some(p => p.zpool)) problems.push('no wallet');
    if (algorithms.length && !algorithms.some(a => mined.has(a))) problems.push('no miner program for ' + algorithms.join(', '));
    const b = best.get(entry.symbol);
    const state_ = off ? 'switched off here' : problems.length ? problems.join('; ') : b ? 'ready' : 'ready, no offer right now';
    return h('tr', { class: off || problems.length ? 'off' : '' },
      h('td', {}, h('input', { type: 'checkbox', checked: !off, title: 'Mine ' + entry.symbol, onchange: e => {
        const next = state.draft.pools.exclude_coins.filter(x => x !== entry.symbol);
        if (!e.target.checked) next.push(entry.symbol);
        state.draft.pools.exclude_coins = next.sort();
        render();
      } })),
      h('td', { class: 'sym' }, entry.symbol),
      h('td', {}, entry.name),
      h('td', {}, algorithms.join(', ')),
      h('td', {}, pools.wallets[entry.symbol] ? 'set' : usable.length && !needsWallet ? 'not needed' : h('a', { href: '#/setupwallets' }, 'none')),
      h('td', {}, entry.pools.map(p => p.title + (p.usable ? '' : ' (' + p.why + ')')).join(', ') || '-'),
      h('td', {}, b && !off && b.profit > 0 ? (fiat(b.profit) || (b.profit * 1e8).toFixed(0) + ' sat') + '/day on ' + b.group + ' at ' + b.pool + (b.measured ? '' : ' (not measured yet)') : '-'),
      h('td', { class: 'note' }, state_));
  };
  // coins that are switched off but that no pool lists any more stay visible, so they can be switched on again
  const strays = pools.exclude_coins.filter(s => !coins.has(s));
  return h('div', { class: 'section wide' }, h('h3', {}, 'Coins'),
    h('p', { class: 'help' }, 'Every coin that can be mined as a single coin, and what it takes: a pool that offers it and is switched on, a wallet where the pool pays in the coin itself, and a miner program for its algorithm. An unchecked coin is mined at no pool, whatever the lists under Setup Pools say. "Best now" is what the coin would earn at the moment with the saved settings; it is empty until hashsteer mines.'),
    rows.length ? h('table', { class: 'coins' },
      h('thead', {}, h('tr', {}, h('th', {}, 'Mine'), h('th', {}, 'Coin'), h('th', {}, 'Name'), h('th', {}, 'Algorithm'), h('th', {}, 'Wallet'), h('th', {}, 'Pools'), h('th', {}, 'Best now'), h('th', {}, 'State'))),
      h('tbody', {}, rows.map(line))) : h('div', { class: 'empty' }, 'No coin is known yet.'),
    strays.length ? listField('Also switched off', 'pools.exclude_coins', 'Coins that are switched off and that no pool here lists right now. Symbols separated by commas.') : null,
    h('p', { class: 'help' }, pools.zpool.enabled && pools.zpool.algo_switching
      ? 'zpool also has one port per algorithm where the pool itself picks the coin ("pool picks" in the lists). That is not touched by this page: which coin is mined there cannot be chosen.'
      : ''));
}

// setupCoinPool is the section of a pool that pays in the mined coin.
function setupCoinPool(pool) {
  const wallets = state.draft.pools.wallets;
  const base = 'pools.' + pool.key;
  const mined = new Set(minedAlgorithms());
  const note = c => c.symbol + '  ' + c.name + '  (' + c.algorithm + (wallets[c.symbol] ? '' : ', no wallet') + (mined.has(c.algorithm) ? '' : ', no miner program') + ')';
  const bySymbol = {};
  for (const c of pool.coins) bySymbol[c.symbol] = c;
  return h('div', { class: 'section' }, h('h3', {}, pool.title),
    boolField('Use this pool', base + '.enabled', 'It pays in the coin that is mined, to the wallet of that coin from Setup Wallets, and keeps ' + pool.fee_percent + ' %.'),
    pool.regions.length > 1
      ? selectField('Region', base + '.region', 'The stratum servers to connect to.', pool.regions.map(r => ({ value: r, text: r })))
      : null,
    boolField('SSL', base + '.tls', 'Encrypt the connection to the pool.'),
    field('Coins', 'Unchecked coins are not mined there. A coin also needs a wallet and a miner program for its algorithm.',
      toggleList(pool.coins.map(c => c.symbol), base + '.exclude_coins', symbol => note(bySymbol[symbol]))));
}

function setupPools() {
  const currencies = state.meta.payout_currencies.map(c => ({
    value: c.symbol, text: c.symbol + ' - ' + c.name + (c.notice ? '  (!)' : '') }));
  const chosen = state.meta.payout_currencies.find(c => c.symbol === state.draft.pools.zpool.currency);
  const currencyHelp = 'zpool pays everything out in this one coin, whatever is mined.' +
    (chosen && chosen.minimum_payout ? ' Minimum payout: ' + chosen.minimum_payout + ' ' + chosen.symbol + '.' : '') +
    (chosen && chosen.notice ? ' Notice from zpool: ' + chosen.notice : '');
  return [
    state.meta.coin_pools.map(setupCoinPool),
    h('div', { class: 'section' }, h('h3', {}, 'Zpool'),
      boolField('Use this pool', 'pools.zpool.enabled', 'A pool that pays everything out in one coin, whatever is mined.'),
      currencies.length
        ? selectField('Payout coin', 'pools.zpool.currency', currencyHelp, currencies)
        : textField('Payout coin', 'pools.zpool.currency', 'zpool could not be asked for its list; enter the symbol, e.g. BTC, LTC or RVN.'),
      zpoolWalletField(),
      selectField('Region', 'pools.zpool.region', 'The stratum servers to connect to.', state.meta.zpool_regions.map(r => ({ value: r, text: r }))),
      boolField('SSL', 'pools.zpool.tls', 'Encrypt the connection to the pool where it has a port for it.')),
    h('div', { class: 'section' }, h('h3', {}, 'What to mine at Zpool'),
      boolField('Zpool (algorithm ports)', 'pools.zpool.algo_switching', 'Mine on the pool\'s port for an algorithm and let the pool pick the coin. It may switch every few minutes; for algorithms with a DAG (KawPoW and relatives) every switch rebuilds it, which costs time and needs a lot of memory.'),
      boolField('ZpoolCoins (single coins)', 'pools.zpool.coin_mining', 'Mine exactly one coin at a time, chosen by what it pays. No DAG switching, but a single coin often pays less than the algorithm port.'),
      listField('CoinSymbol', 'pools.zpool.coins', 'Mine only these coins as single coins. Symbols separated by commas; empty allows every coin the pool offers.'),
      listField('ExcludeCoinSymbol', 'pools.zpool.exclude_coins', 'Never mine these as single coins. Symbols separated by commas.')),
  ];
}

// minerDraft gives the settings of a miner program, which are only in the
// configuration once something was set for it.
function minerDraft(name, create) {
  const fresh = { disabled: false, exclude_algorithms: [], extra_args: [] };
  if (!state.draft.miners[name] && create) state.draft.miners[name] = fresh;
  return state.draft.miners[name] || fresh;
}

function setupMiners() {
  return state.meta.miners.map(m => {
    const excluded = minerDraft(m.name).exclude_algorithms;
    return h('div', { class: 'section' },
      h('h3', {}, m.name + ' ' + m.version, h('small', { class: 'help' }, '  ' + m.vendor + ', fee ' + m.fee_percent + ' % unless an algorithm differs')),
      field('Use this miner', '', h('input', { type: 'checkbox', checked: !minerDraft(m.name).disabled,
        onchange: e => { minerDraft(m.name, true).disabled = !e.target.checked; } })),
      field('Algorithms', 'Unchecked algorithms are not mined with this program.', h('div', { class: 'checks' }, m.algorithms.map(a => h('label', {},
        h('input', { type: 'checkbox', checked: !excluded.includes(a), onchange: e => {
          const d = minerDraft(m.name, true);
          d.exclude_algorithms = d.exclude_algorithms.filter(x => x !== a);
          if (!e.target.checked) d.exclude_algorithms.push(a);
        } }), a)))),
      field('Params', 'Added to the command line, separated by spaces. Example for Rigel: --temp-limit tc[80-85]',
        h('input', { type: 'text', value: minerDraft(m.name).extra_args.join(' '),
          oninput: e => { minerDraft(m.name, true).extra_args = e.target.value.split(/\s+/).filter(Boolean); } })));
  });
}

function setupAlgorithms() {
  const all = Array.from(minedAlgorithms()).sort();
  return [
    h('div', { class: 'section' }, h('h3', {}, 'Algorithms'),
      field('Mine these', 'Unchecked algorithms are never mined, whatever the miner program.', toggleList(all, 'algorithms.exclude')),
      listField('Only these', 'algorithms.only', 'If this list is not empty, nothing else is mined. Names separated by commas.')),
  ];
}

function setupDevices() {
  const gpus = state.devices.filter(d => d.kind === 'gpu').map(d => d.id);
  const names = {};
  for (const d of state.devices) names[d.id] = d.id + '  ' + (d.model || '');
  return [
    h('div', { class: 'section' }, h('h3', {}, 'NVIDIA'),
      gpus.length
        ? field('Mine with', 'Unchecked cards stay out of mining. Cards of the same model mine together with one miner program.', toggleList(gpus, 'devices.exclude', id => names[id]))
        : h('div', { class: 'help' }, 'No NVIDIA card was found.')),
    h('div', { class: 'section' }, h('h3', {}, 'CPU'),
      boolField('Mine with the CPU', 'general.cpu.enabled', 'The same setting as under Setup.')),
  ];
}

async function saveConfig() {
  const error = document.getElementById('config-error');
  error.textContent = '';
  try {
    await api('/api/config', { method: 'PUT', body: state.draft });
    toast('Saved - the miners restart with the new settings');
    await loadConfig();
    render();
  } catch (e) {
    error.textContent = e.message;
    toast('Not saved: ' + e.message, true);
  }
}

// --------------------------------------------------------------- console

// The colours of the console window, by the code a terminal knows them by.
const TERM_STYLES = { 1: 'b', 2: 'dim', 31: 'red', 32: 'green', 33: 'yellow', 36: 'cyan' };

// termLine turns a line of the console window into elements: its text as
// text, the terminal's colour codes as classes. Nothing in it is taken for
// markup.
function termLine(line) {
  const parts = [];
  let classes = [];
  const pattern = /\x1b\[([0-9;]*)m/g;
  let at = 0;
  const push = text => { if (text) parts.push(classes.length ? h('span', { class: classes.join(' ') }, text) : text); };
  for (let m = pattern.exec(line); m; m = pattern.exec(line)) {
    push(line.slice(at, m.index));
    at = pattern.lastIndex;
    for (const code of (m[1] || '0').split(';')) {
      if (Number(code) === 0) classes = [];
      else if (TERM_STYLES[Number(code)]) classes = classes.concat(TERM_STYLES[Number(code)]);
    }
  }
  push(line.slice(at));
  return h('div', {}, parts.length ? parts : ' ');
}

function renderConsole() {
  const lines = state.consoleLines || [];
  const term = h('pre', { class: 'term' }, lines.length ? lines.map(termLine) : 'The console window has nothing to show yet.');
  const engineLog = h('pre', { class: 'log', id: 'engine-log' }, state.log.join('\n') || 'Nothing logged yet.');
  // the logs of the miner programs that run right now, one below the other
  const active = (state.activeLogs || []).map(l => [
    h('h3', { class: 'minerlog' }, l.group + ': ' + l.miner + ' on ' + l.algorithm,
      h('small', { class: 'help' }, '  ' + (l.name || 'no log file yet'))),
    h('pre', { class: 'log active' }, l.text || (l.name ? '(empty)' : 'The miner program has not written anything yet.'))]);
  // a log that was asked for by name, from the Benchmarks page
  const asked = state.minerLog.name ? [
    h('h3', { class: 'minerlog' }, 'Asked for: ' + state.minerLog.name,
      h('button', { class: 'small', onclick: () => { state.minerLog = { name: '', text: '' }; render(); } }, 'Close')),
    h('pre', { class: 'log active' }, state.minerLog.text || '(empty)')] : null;
  const out = h('div', {}, h('h1', {}, 'Console'),
    h('p', { class: 'lead' }, 'What the window shows that hashsteer runs in, redrawn every few seconds, without the end of the log: the whole log is below, and after it what the miner programs that run right now write themselves.'),
    term,
    h('h2', {}, 'Log'), engineLog,
    h('h2', {}, 'Miner programs'),
    active.length ? active : h('div', { class: 'empty' }, 'No miner program is running.'),
    asked);
  // show the newest lines
  setTimeout(() => { for (const el of document.querySelectorAll('pre.log')) el.scrollTop = el.scrollHeight; }, 0);
  return out;
}

// activeLogFiles names the newest log of every miner program that runs.
// The files are called miner-algorithm-group-date-time.log, and the date
// sorts by time.
function activeLogFiles() {
  const list = [];
  for (const g of (state.status ? state.status.groups : [])) {
    if (g.state === 'idle' || !g.miner) continue;
    const prefix = g.miner + '-' + g.algorithm + '-' + g.name + '-';
    let newest = '';
    for (const f of state.minerLogs) {
      if (f.name.startsWith(prefix) && f.name.length === prefix.length + '20060102-150405.log'.length && f.name > newest) newest = f.name;
    }
    list.push({ group: g.name, miner: g.miner, algorithm: g.algorithm, name: newest, text: '' });
  }
  return list;
}

// loadActiveLogs reads the end of the log of every running miner program.
async function loadActiveLogs() {
  const list = activeLogFiles();
  for (const l of list) {
    if (!l.name) continue;
    try { l.text = await api('/api/minerlogs/' + encodeURIComponent(l.name)); } catch (e) { l.text = '(' + e.message + ')'; }
  }
  state.activeLogs = list;
}

async function loadMinerLog(name) {
  state.minerLog = { name, text: '' };
  if (name) {
    try { state.minerLog.text = await api('/api/minerlogs/' + encodeURIComponent(name)); }
    catch (e) { toast(e.message, true); }
  }
  render();
}

// --------------------------------------------------------------- loading

const VIEWS = {
  dashboard: renderDashboard, console: renderConsole, devices: renderDevices,
  bestpools: renderBestPools, allpools: renderAllPools, earnings: renderEarnings, totals: renderTotals, payouts: renderPayouts,
  miners: renderMiners, benchmarks: renderBenchmarks, activity: renderActivity, shared: renderShared, watchdog: renderWatchdog,
  setup: renderSetup, setupwallets: renderSetup, setupcoins: renderSetup, setuppools: renderSetup, setupminers: renderSetup, setupalgorithms: renderSetup, setupdevices: renderSetup,
};

// what each page needs besides the status
const NEEDS = {
  dashboard: ['devices', 'balances', 'benchmarks'], devices: ['devices'],
  bestpools: ['offers'], allpools: ['offers'], earnings: ['balances'], payouts: ['balances'], totals: ['totals'],
  benchmarks: ['benchmarks'], activity: ['activity'], shared: ['shared', 'offers'], console: ['console', 'log', 'minerlogs'],
};
const SOURCES = { console: 'consoleLines', devices: 'devices', balances: 'balances', offers: 'offers', totals: 'totals', benchmarks: 'benchmarks', activity: 'activity', shared: 'shared', log: 'log', minerlogs: 'minerLogs' };

async function refreshStatus() {
  const conn = document.getElementById('conn');
  try {
    state.status = await api('/api/status');
    conn.textContent = 'connected';
    conn.className = 'conn';
  } catch (e) {
    conn.textContent = 'no connection to hashsteer';
    conn.className = 'conn down';
    return;
  }
  const banner = document.getElementById('banner');
  const notices = (state.status.system.notices || []).slice();
  if (state.status.waiting) notices.unshift('Not mining: ' + state.status.waiting);
  const problems = state.status.system.problems || [];
  banner.hidden = notices.length === 0 && problems.length === 0;
  banner.replaceChildren(...notices.map(text => h('div', {}, text)), ...problems.map(problemLine));
  renderTopbar();
  await loadUpdate(false);

  for (const what of NEEDS[state.page] || []) {
    try { state[SOURCES[what]] = await api('/api/' + what); } catch (e) { /* stale figures stay; the next tick tries again */ }
  }
  if (state.page === 'console') await loadActiveLogs();

  // nothing is redrawn under the user's hands: not the setup pages, and no
  // page while a field is being typed into
  const active = document.activeElement;
  const typing = active && ['INPUT', 'SELECT', 'TEXTAREA'].includes(active.tagName);
  if (!SETUP[state.page] && !typing) render();
}

async function loadConfig() {
  const res = await api('/api/config');
  state.config = res.config;
  state.meta = res.meta;
  state.draft = JSON.parse(JSON.stringify(res.config));
  state.walletRows = null;
  document.getElementById('version').textContent = 'v' + res.meta.version;
  document.getElementById('worker').textContent = 'Worker: ' + res.config.general.worker;
}

// render draws the current page. A filter field that was being typed into
// keeps the cursor.
function render() {
  const active = document.activeElement;
  const filter = active && active.getAttribute ? active.getAttribute('data-filter') : null;
  const pos = filter ? active.selectionStart : 0;

  document.getElementById('page').replaceChildren(VIEWS[state.page]());
  for (const a of document.querySelectorAll('#nav a')) a.classList.toggle('active', a.dataset.page === state.page);

  if (filter) {
    const again = document.querySelector('input[data-filter="' + filter + '"]');
    if (again) { again.focus(); again.setSelectionRange(pos, pos); }
  }
}

async function route() {
  state.page = PAGES[location.hash.replace(/^#\/?/, '')] || 'dashboard';
  render();
  await refreshStatus();
  render();
}

async function start() {
  window.addEventListener('hashchange', route);
  try {
    state.devices = await api('/api/devices');
    await loadConfig();
  } catch (e) { toast(e.message, true); }
  await route();
  setInterval(refreshStatus, 5000);
}

start();
