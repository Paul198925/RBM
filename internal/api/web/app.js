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
  for (const child of children.flat()) {
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
  if (opts.method && opts.method !== 'GET') opts.headers['X-RBM-Request'] = '1';
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

// fmtPrice shows what a pool pays per day, for the unit of hashrate that
// gives a readable number.
function fmtPrice(perHash) {
  if (!perHash) return '-';
  let i = 0;
  let value = perHash;
  while (value < 1e-7 && i < UNITS.length - 1) { value *= 1000; i++; }
  return value.toFixed(8) + ' / ' + UNITS[i].replace('/s', '');
}

function fmtWatts(value) { return value ? Math.round(value) + ' W' : '-'; }

function fmtAgo(iso) {
  if (!iso) return '-';
  let s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  const d = Math.floor(s / 86400); s -= d * 86400;
  const hrs = Math.floor(s / 3600); s -= hrs * 3600;
  const m = Math.floor(s / 60);
  if (d) return d + 'd ' + hrs + 'h';
  if (hrs) return hrs + 'h ' + m + 'm';
  return m + 'm';
}

function fmtUntil(iso) {
  const s = Math.round((new Date(iso).getTime() - Date.now()) / 1000);
  if (s <= 0) return 'now';
  const hrs = Math.floor(s / 3600);
  const m = Math.ceil((s - hrs * 3600) / 60);
  return hrs ? hrs + 'h ' + m + 'm' : m + 'm';
}

function coinLabel(pool, coin) { return coin ? pool + ' : ' + coin : pool + ' (pool picks the coin)'; }

// ------------------------------------------------------------------ state

const state = {
  page: 'dashboard',
  status: null,
  devices: [],
  offers: [],
  config: null,   // as the server has it
  draft: null,    // what the configuration page is editing
  meta: null,
  configTab: 'general',
  log: [],
  minerLogs: [],
  minerLog: { name: '', text: '' },
  balances: { balances: [] },
  activity: [],
  benchmarks: [],
  benchSelected: new Set(),
  benchFilter: { miner: '', algorithm: '', group: '', status: '' },
  benchSort: { key: 'miner', dir: 1 },
  offerFilter: '',
  offerMineOnly: true,
};

const PAGES = { '': 'dashboard', miners: 'miners', benchmarks: 'benchmarks', pools: 'pools', activity: 'activity', config: 'config', logs: 'logs' };

function fiat(btc) {
  const s = state.status;
  if (!s || !s.btc_price || !btc) return '';
  return (btc * s.btc_price).toFixed(2) + ' ' + s.currency;
}

function fmtGB(mib) { return (mib / 1024).toFixed(1) + ' GB'; }

function fmtDuration(seconds) {
  seconds = Math.max(0, Math.round(seconds));
  const d = Math.floor(seconds / 86400);
  const hrs = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d) return d + 'd ' + hrs + 'h ' + m + 'm';
  if (hrs) return hrs + 'h ' + m + 'm';
  return m + 'm';
}

function totals() {
  let profit = 0, power = 0, mining = 0;
  for (const g of state.status.groups) {
    if (g.state !== 'idle') { mining++; power += g.power_watts; }
    profit += g.profit_btc_per_day || 0;
  }
  return { profit, power, mining };
}

// --------------------------------------------------------------- top bar

function topItem(label, value) {
  return h('div', { class: 'item' }, h('div', { class: 'label' }, label), h('div', { class: 'value' }, value));
}

function renderTopbar() {
  const s = state.status;
  const bar = document.getElementById('topbar');
  if (!s) { bar.replaceChildren(); return; }
  const t = totals();
  const paused = s.system.paused;
  const memory = s.system.memory;
  bar.replaceChildren(
    topItem('Profit per day', fmtBTC(t.profit) === '-' ? '-' : fmtBTC(t.profit) + ' BTC' + (fiat(t.profit) ? '  =  ' + fiat(t.profit) : '')),
    topItem('Power', fmtWatts(t.power)),
    topItem('Mining', t.mining + ' of ' + s.groups.length + ' groups'),
    memory ? topItem('Free memory', fmtGB(memory.commit_free_mib) + ' of ' + fmtGB(memory.commit_limit_mib)) : null,
    topItem('Uptime', fmtDuration((Date.now() - new Date(s.system.started).getTime()) / 1000)),
    h('div', { class: 'spacer' }),
    h('button', { class: paused ? 'primary' : 'warn', onclick: () => setPaused(!paused),
      title: paused ? 'Start the miners again' : 'Stop all miners, keep rbm running' }, paused ? 'Resume mining' : 'Pause mining'));
}

async function setPaused(paused) {
  try {
    await api('/api/pause', { method: 'POST', body: { paused } });
    toast(paused ? 'Mining is paused' : 'Mining continues');
    state.status.system.paused = paused;
    renderTopbar();
    setTimeout(refreshStatus, 1500);
  } catch (e) { toast(e.message, true); }
}

// ------------------------------------------------------------- dashboard

function renderDashboard() {
  const s = state.status;
  if (!s) return h('div', { class: 'empty' }, 'Loading ...');

  const { profit, power, mining } = totals();
  const memory = s.system.memory;
  const cards = h('div', { class: 'cards' },
    card('Device groups mining', mining + ' of ' + s.groups.length),
    card('Expected profit per day', fmtBTC(profit) === '-' ? '-' : fmtBTC(profit) + ' BTC', fiat(profit)),
    card('Power draw', fmtWatts(power), state.config && state.config.general.power.price_per_kwh > 0 ? '' : 'electricity is not counted'),
    card('Bitcoin price', s.btc_price ? s.btc_price.toFixed(0) + ' ' + s.currency : '-', s.btc_price ? '' : 'not known yet'),
    memory ? card('Memory left to reserve', fmtGB(memory.commit_free_mib), 'of ' + fmtGB(memory.commit_limit_mib) + ' (RAM and page file); RAM free: ' + fmtGB(memory.free_mib)) : null);

  const groups = s.groups.length
    ? h('div', { class: 'groups' }, s.groups.map(renderGroup))
    : h('div', { class: 'empty' }, 'No device group is active.');

  const tempClass = c => 'num' + (c >= 85 ? ' hot' : c >= 78 ? ' warm' : '');
  const val = (v, unit) => (v ? Math.round(v) + unit : '-');
  const devices = h('div', { class: 'scroll' }, h('table', {},
    h('thead', {}, h('tr', {}, h('th', {}, 'ID'), h('th', {}, 'Model'),
      h('th', { class: 'num' }, 'Temp'), h('th', { class: 'num' }, 'Max'), h('th', { class: 'num' }, 'Fan'),
      h('th', { class: 'num' }, 'Power'), h('th', { class: 'num' }, 'Limit'), h('th', { class: 'num' }, 'Load'),
      h('th', { class: 'num' }, 'Memory used'), h('th', { class: 'num' }, 'Clock'), h('th', { class: 'num' }, 'Mem clock'))),
    h('tbody', {}, state.devices.map(d => {
      const t = d.telemetry;
      if (!t) {
        return h('tr', {}, h('td', {}, d.id), h('td', {}, d.model || '-'),
          h('td', { class: 'num', colspan: '9' }, d.threads ? d.threads + ' threads' : '-'));
      }
      return h('tr', {}, h('td', {}, d.id), h('td', {}, d.model || '-'),
        h('td', { class: tempClass(t.temperature_c) }, val(t.temperature_c, ' °C')),
        h('td', { class: tempClass(d.max_temperature_c) }, val(d.max_temperature_c, ' °C')),
        h('td', { class: 'num' }, val(t.fan_percent, ' %')),
        h('td', { class: 'num' }, val(t.power_watts, ' W')),
        h('td', { class: 'num' }, val(t.power_limit_watts, ' W')),
        h('td', { class: 'num' }, Math.round(t.utilization_percent) + ' %'),
        h('td', { class: 'num' }, Math.round(t.memory_used_mib) + ' / ' + d.memory_mib + ' MiB'),
        h('td', { class: 'num' }, val(t.clock_mhz, ' MHz')),
        h('td', { class: 'num' }, val(t.memory_clock_mhz, ' MHz')));
    }))));

  return h('div', {}, h('h1', {}, 'Dashboard'), cards, h('h2', {}, 'Device groups'), groups,
    h('h2', {}, 'Devices'), devices, renderBalances());
}

function renderBalances() {
  const info = state.balances;
  const out = [h('h2', {}, 'Balance at the pool')];
  if (info.error) out.push(h('p', { class: 'help' }, 'Could not be updated: ' + info.error));
  if (!info.balances.length) {
    out.push(h('div', { class: 'empty' }, info.error ? 'No balance known.' : 'Nothing yet. The pool is asked every ten minutes once a wallet is set.'));
    return out;
  }
  for (const b of info.balances) {
    const amount = v => v.toFixed(8).replace(/0+$/, '').replace(/[.]$/, '') + ' ' + b.currency;
    out.push(h('div', { class: 'cards' },
      card('Unpaid at ' + b.pool, amount(b.unpaid), 'confirmed ' + amount(b.balance) + ', not yet exchanged ' + amount(b.unsold)),
      card('Paid in the last 24 hours', amount(b.paid_24h)),
      card('Earned in total', amount(b.total), new Date(b.updated).getFullYear() > 2000 ? 'updated ' + fmtAgo(b.updated) + ' ago' : '')));
    if (b.payouts.length) {
      out.push(h('h2', {}, 'Payouts', h('small', {}, b.pool)));
      out.push(h('div', { class: 'scroll' }, h('table', {},
        h('thead', {}, h('tr', {}, h('th', {}, 'Date'), h('th', { class: 'num' }, 'Amount'), h('th', {}, 'Transaction'))),
        h('tbody', {}, b.payouts.slice(0, 20).map(p => h('tr', {},
          h('td', {}, new Date(p.time).toLocaleString()),
          h('td', { class: 'num' }, amount(p.amount)),
          h('td', {}, p.tx)))))));
    }
  }
  return out;
}

// -------------------------------------------------------------- activity

function renderActivity() {
  const runs = state.activity;
  const head = [h('h1', {}, 'Activity'),
    h('p', { class: 'help' }, 'Every run of a miner program in the last three days, newest first. "Earned" is an estimate from the measured hashrate and the pool\'s price at the time, not what the pool has credited.'),
    h('div', { class: 'toolbar' }, h('a', { class: 'button', href: '/api/activity.csv' }, 'Download as CSV'),
      h('span', { class: 'help' }, runs.length + ' runs'))];
  if (!runs.length) return h('div', {}, head, h('div', { class: 'empty' }, 'No miner has finished a run yet.'));

  const rows = runs.map(r => {
    const minutes = (new Date(r.ended) - new Date(r.started)) / 60000;
    const sat = r.earned_btc * 1e8;
    return h('tr', {},
      h('td', {}, h('span', { class: 'badge ' + r.end.replace(/ /g, '') }, r.end)),
      h('td', {}, new Date(r.started).toLocaleString()),
      h('td', { class: 'num' }, minutes.toFixed(1)),
      h('td', {}, r.group), h('td', {}, r.miner),
      h('td', {}, r.algorithm + (r.benchmark ? '  (benchmark)' : '')),
      h('td', {}, r.coin ? r.pool + ' : ' + r.coin : r.pool),
      h('td', { class: 'num' }, fmtHash(r.hashrate)),
      h('td', { class: 'num' }, sat ? sat.toFixed(2) + ' sat' : '-'),
      h('td', { class: 'num' }, r.energy_kwh ? r.energy_kwh.toFixed(3) + ' kWh' : '-'),
      h('td', { class: 'num' }, r.energy_kwh > 0.0005 && sat ? (sat / r.energy_kwh).toFixed(0) : '-'),
      h('td', { class: 'num' }, r.accepted + ' / ' + r.rejected));
  });
  return h('div', {}, head, h('div', { class: 'scroll' }, h('table', {},
    h('thead', {}, h('tr', {}, h('th', {}, 'End'), h('th', {}, 'Started'), h('th', { class: 'num' }, 'Minutes'),
      h('th', {}, 'Group'), h('th', {}, 'Miner'), h('th', {}, 'Algorithm'), h('th', {}, 'Pool : coin'),
      h('th', { class: 'num' }, 'Hashrate'), h('th', { class: 'num' }, 'Earned'), h('th', { class: 'num' }, 'Energy'),
      h('th', { class: 'num' }, 'sat/kWh'), h('th', { class: 'num' }, 'Shares ok / bad'))),
    h('tbody', {}, rows))));
}

function card(label, value, sub) {
  return h('div', { class: 'card' }, h('div', { class: 'label' }, label), h('div', { class: 'value' }, value), sub ? h('div', { class: 'sub' }, sub) : null);
}

function renderGroup(g) {
  const idle = g.state === 'idle';
  return h('div', { class: 'group' },
    h('header', {}, h('strong', {}, g.name), h('span', { class: 'badge ' + g.state }, g.state)),
    h('div', { class: 'devices' }, g.devices.join(', ')),
    idle ? null : h('div', { class: 'what' }, h('b', {}, g.algorithm), ' with ', h('b', {}, g.miner), ' at ', coinLabel(g.pool, g.coin)),
    idle ? null : h('dl', {},
      h('dt', {}, 'Hashrate'), h('dd', {}, fmtHash(g.hashrate)),
      h('dt', {}, 'Power'), h('dd', {}, fmtWatts(g.power_watts)),
      h('dt', {}, 'Shares (ok / rejected / invalid)'), h('dd', {}, g.accepted + ' / ' + g.rejected + ' / ' + g.invalid),
      h('dt', {}, 'Profit per day'), h('dd', {}, g.state === 'benchmarking' ? 'measuring' : fmtBTC(g.profit_btc_per_day) + ' BTC' + (fiat(g.profit_btc_per_day) ? '  =  ' + fiat(g.profit_btc_per_day) : '')),
      h('dt', {}, 'Running for'), h('dd', {}, fmtAgo(g.since))),
    h('div', { class: 'reason' }, g.reason || ''));
}

// ---------------------------------------------------------------- miners

function renderMiners() {
  const s = state.status;
  if (!s) return h('div', { class: 'empty' }, 'Loading ...');
  const out = [h('h1', {}, 'Miners'),
    h('p', { class: 'help' }, 'Everything each device group could mine right now, best first. A miner on an algorithm is measured once; that benchmark counts for every coin and pool.')];
  if (!s.groups.length) out.push(h('div', { class: 'empty' }, 'Nothing is running.'));

  for (const g of s.groups) {
    out.push(h('h2', {}, g.name, h('small', {}, g.devices.join(', '))));
    const rows = g.candidates.slice().sort((a, b) =>
      (b.benchmarked - a.benchmarked) || (b.profit_btc_per_day - a.profit_btc_per_day) || a.id.localeCompare(b.id));
    if (!rows.length) {
      out.push(h('div', { class: 'empty' }, g.reason || 'No candidates.'));
    } else {
      out.push(h('div', { class: 'scroll' }, h('table', {},
        h('thead', {}, h('tr', {},
          h('th', {}, 'Miner'), h('th', {}, 'Algorithm'), h('th', {}, 'Pool : coin'), h('th', {}, 'State'),
          h('th', { class: 'num' }, 'Hashrate'), h('th', { class: 'num' }, 'Power'), h('th', { class: 'num' }, 'Fee'),
          h('th', { class: 'num' }, 'Pool pays per day'), h('th', { class: 'num' }, 'Revenue BTC/day'), h('th', { class: 'num' }, 'Profit BTC/day'), h('th', {}, ''))),
        h('tbody', {}, rows.map(c => {
          const running = c.id === g.running;
          const stateBadge = running ? h('span', { class: 'badge ' + g.state }, g.state)
            : c.benchmarked ? h('span', { class: 'badge' }, 'measured') : h('span', { class: 'badge todo' }, 'needs benchmark');
          // a benchmark keeps what it has measured across restarts
          const progress = c.benchmarked ? '' : '  ' + Math.round(c.measured_seconds) + ' of ' + Math.round(s.benchmark_seconds) + ' s';
          return h('tr', { class: running ? 'running' : '' },
            h('td', {}, c.miner), h('td', {}, c.algorithm), h('td', {}, c.coin ? c.pool + ' : ' + c.coin : c.pool), h('td', {}, stateBadge, progress),
            h('td', { class: 'num' }, c.benchmarked ? fmtHash(c.hashrate) : '-'),
            h('td', { class: 'num' }, fmtWatts(c.power_watts)),
            h('td', { class: 'num' }, c.fee_percent + ' %'),
            h('td', { class: 'num' }, fmtPrice(c.price)),
            h('td', { class: 'num' }, c.benchmarked ? fmtBTC(c.revenue_btc_per_day) : '-'),
            h('td', { class: 'num ' + (c.profit_btc_per_day > 0 ? 'pos' : c.profit_btc_per_day < 0 ? 'neg' : '') }, c.benchmarked ? fmtBTC(c.profit_btc_per_day) : '-'),
            h('td', {}, c.benchmarked ? h('button', { class: 'small', title: 'Forget the measured hashrate and measure again', onclick: () => resetBenchmark(c.hash_key) }, 'Benchmark again') : null));
        })))));
    }
    if (g.disabled.length) {
      out.push(h('h2', {}, 'Taken out after failures', h('small', {}, g.name)));
      out.push(h('div', { class: 'scroll' }, h('table', {},
        h('thead', {}, h('tr', {}, h('th', {}, 'Miner / devices / algorithm'), h('th', {}, 'Tried again in'), h('th', {}, ''))),
        h('tbody', {}, g.disabled.map(d => h('tr', {},
          h('td', {}, d.hash_key.replace(/^hashrate\//, '')),
          h('td', {}, fmtUntil(d.until)),
          h('td', {}, h('button', { class: 'small', onclick: () => enableCandidate(d.hash_key) }, 'Try again now'))))))));
    }
  }
  return h('div', {}, out);
}

async function resetBenchmark(hashKey) {
  if (!confirm('Forget the measured hashrate of ' + hashKey.replace(/^hashrate\//, '') + ' and benchmark it again?')) return;
  try {
    await api('/api/benchmarks/reset', { method: 'POST', body: { hash_key: hashKey } });
    toast('Benchmark will be repeated');
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

// ------------------------------------------------------------ benchmarks

const BENCH_STATUS = {
  'ok': 'measured',
  'outdated': 'measured with an older version of the miner',
  'benchmarking': 'being measured right now',
  'pending': 'not measured yet',
  'failed': 'taken out after failures',
  'no offer': 'the pool pays nothing for it at the moment, so it cannot be measured',
  'no fit': 'the card has too little memory for it',
  'excluded': 'switched off in the configuration',
};

function benchRows() {
  const f = state.benchFilter;
  const has = (value, needle) => !needle || value.toLowerCase().includes(needle.trim().toLowerCase());
  const { key, dir } = state.benchSort;
  return state.benchmarks
    .filter(b => has(b.miner, f.miner) && has(b.algorithm, f.algorithm) && has(b.group, f.group) && (!f.status || b.status === f.status))
    .sort((a, b) => {
      const x = a[key] === undefined || a[key] === null ? '' : a[key];
      const y = b[key] === undefined || b[key] === null ? '' : b[key];
      const order = typeof x === 'number' && typeof y === 'number' ? x - y : String(x).localeCompare(String(y));
      return dir * order || a.hash_key.localeCompare(b.hash_key);
    });
}

function renderBenchmarks() {
  const all = state.benchmarks;
  const count = status => all.filter(b => b.status === status).length;
  // only what was measured or has failed can be measured again
  const resettable = b => b.measured_seconds > 0 || b.status === 'failed';
  const measured = all.filter(resettable).length;

  const action = (title, text, label, cls, n, run) => h('div', { class: 'action' },
    h('h3', {}, title), h('p', {}, text),
    h('div', { class: 'go' }, h('button', { class: cls, disabled: n === 0, onclick: run }, label + (n ? '  (' + n + ')' : ''))));
  const actions = h('div', { class: 'actions' },
    action('Benchmark updated miners',
      'Forget the figures that were measured with an older version of a miner program than the one installed now. A new version can be faster or slower.',
      'Benchmark updated miners', 'primary', count('outdated'),
      () => resetBenchmarks({ which: 'outdated' }, 'Measure the ' + count('outdated') + ' benchmarks of updated miners again?')),
    action('Benchmark failed miners',
      'Let the miners back in that were taken out after crashes or a benchmark without hashrate, and measure them again. Watch them: what failed once may fail again.',
      'Benchmark failed miners', 'primary', count('failed'),
      () => resetBenchmarks({ which: 'failed' }, 'Try the ' + count('failed') + ' failed benchmarks again?')),
    action('Reset all benchmarks',
      'Forget everything that was measured. Do this after changing hardware, drivers or overclocking. Measuring everything again takes hours, and until then nothing is mined for profit.',
      'Benchmark all miners', 'danger', measured,
      () => resetBenchmarks({ which: 'all' }, 'Really forget all ' + measured + ' measurements? Measuring everything again takes hours.')));

  const rows = benchRows();
  const visible = rows.filter(resettable).map(b => b.hash_key);
  const selected = visible.filter(k => state.benchSelected.has(k));
  const sortable = (label, key, cls) => h('th', { class: 'sortable ' + (cls || ''), onclick: () => {
    state.benchSort = { key, dir: state.benchSort.key === key ? -state.benchSort.dir : 1 };
    render();
  } }, label + (state.benchSort.key === key ? (state.benchSort.dir > 0 ? '  ▲' : '  ▼') : ''));
  const filter = key => h('input', { type: 'search', value: state.benchFilter[key], 'data-filter': key,
    oninput: e => { state.benchFilter[key] = e.target.value; rerenderKeepingFocus(e.target); } });

  const table = h('div', { class: 'scroll' }, h('table', {},
    h('thead', {},
      h('tr', {},
        h('th', { class: 'check' }, h('input', { type: 'checkbox', title: 'Select all shown',
          checked: visible.length > 0 && selected.length === visible.length,
          onchange: e => { for (const k of visible) e.target.checked ? state.benchSelected.add(k) : state.benchSelected.delete(k); render(); } })),
        sortable('Name', 'miner'), sortable('Algorithm', 'algorithm'), sortable('Device', 'group'),
        sortable('Speed', 'hashrate', 'num'), h('th', { class: 'num' }, 'Rejected'), sortable('Power', 'power_watts', 'num'),
        sortable('Status', 'status'), sortable('Date', 'updated'), h('th', {}, 'Log')),
      h('tr', { class: 'filters' },
        h('th', {}), h('th', {}, filter('miner')), h('th', {}, filter('algorithm')), h('th', {}, filter('group')),
        h('th', {}), h('th', {}), h('th', {}),
        h('th', {}, h('select', { onchange: e => { state.benchFilter.status = e.target.value; render(); } },
          h('option', { value: '' }, 'all'),
          Object.keys(BENCH_STATUS).map(s => h('option', { value: s, selected: state.benchFilter.status === s }, s)))),
        h('th', {}), h('th', {}))),
    h('tbody', {}, rows.map(b => {
      let status = b.status;
      if (b.status === 'pending' && b.measured_seconds > 0) status = 'pending  ' + Math.round(b.measured_seconds) + ' of ' + Math.round(state.status.benchmark_seconds) + ' s';
      if (b.status === 'benchmarking') status = 'benchmarking  ' + Math.round(b.measured_seconds) + ' of ' + Math.round(state.status.benchmark_seconds) + ' s';
      if (b.status === 'failed' && b.failed_until) status = 'failed, again in ' + fmtUntil(b.failed_until);
      if (b.status === 'outdated') status = 'outdated (v' + b.measured_version + ')';
      return h('tr', { class: b.status === 'benchmarking' ? 'running' : '' },
        h('td', { class: 'check' }, resettable(b) ? h('input', { type: 'checkbox', checked: state.benchSelected.has(b.hash_key),
          onchange: e => { e.target.checked ? state.benchSelected.add(b.hash_key) : state.benchSelected.delete(b.hash_key); render(); } }) : null),
        h('td', {}, b.miner, h('small', { class: 'help' }, '  ' + b.version)),
        h('td', {}, b.algorithm),
        h('td', { title: b.devices.join(', ') }, b.group),
        h('td', { class: 'num' }, fmtHash(b.hashrate)),
        h('td', { class: 'num' + (b.rejected_percent > 5 ? ' hot' : '') }, b.rejected_percent === undefined ? '-' : b.rejected_percent.toFixed(2).replace(/[.]?0+$/, '') + ' %'),
        h('td', { class: 'num' }, fmtWatts(b.power_watts)),
        h('td', {}, h('span', { class: 'badge ' + b.status.replace(/ /g, ''), title: BENCH_STATUS[b.status] || '' }, status)),
        h('td', {}, b.updated ? new Date(b.updated).toLocaleString() : '-'),
        h('td', {}, b.log ? h('a', { href: '#/logs', title: b.log, onclick: () => { state.minerLog = { name: b.log, text: '' }; loadMinerLog(b.log); } }, 'log') : ''));
    }))));

  return h('div', {}, h('h1', {}, 'Manage Miner Benchmarks'), actions,
    h('h2', {}, 'Benchmark status', h('small', {}, rows.length + ' of ' + all.length + ' shown')),
    h('div', { class: 'toolbar' },
      h('button', { class: 'primary', disabled: selected.length === 0,
        onclick: () => resetBenchmarks({ hash_keys: selected }, 'Measure the ' + selected.length + ' selected benchmarks again?') },
        'Benchmark selected' + (selected.length ? '  (' + selected.length + ')' : '')),
      h('span', { class: 'help' }, 'A miner on an algorithm is measured once per device group; the figure counts for every coin and pool. The list is brought up to date once per round.')),
    all.length ? table : h('div', { class: 'empty' }, 'Nothing yet. The list appears with the first round of the engine.'));
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

// ----------------------------------------------------------------- pools

function minedAlgorithms() {
  const set = new Set();
  for (const m of (state.meta ? state.meta.miners : [])) for (const a of m.algorithms) set.add(a);
  return set;
}

function renderPools() {
  const mine = minedAlgorithms();
  const filter = state.offerFilter.trim().toLowerCase();
  const excluded = new Set(state.config ? state.config.pools.zpool.exclude_coins : []);
  const rows = state.offers
    .filter(o => !state.offerMineOnly || mine.has(o.algorithm))
    .filter(o => !filter || (o.algorithm + ' ' + o.coin + ' ' + o.coin_name).toLowerCase().includes(filter))
    .sort((a, b) => a.algorithm.localeCompare(b.algorithm) || b.price - a.price);

  const toolbar = h('div', { class: 'toolbar' },
    h('input', { type: 'search', placeholder: 'Filter by algorithm or coin', value: state.offerFilter,
      oninput: e => { state.offerFilter = e.target.value; rerenderKeepingFocus(e.target); } }),
    h('label', {}, h('input', { type: 'checkbox', checked: state.offerMineOnly,
      onchange: e => { state.offerMineOnly = e.target.checked; render(); } }), ' only algorithms a miner program supports'),
    h('span', { class: 'help' }, rows.length + ' offers'));

  const table = rows.length ? h('div', { class: 'scroll' }, h('table', {},
    h('thead', {}, h('tr', {},
      h('th', {}, 'Algorithm'), h('th', {}, 'Coin'), h('th', {}, 'Pool'),
      h('th', { class: 'num' }, 'Pays per day'), h('th', { class: 'num' }, 'Pool hashrate'), h('th', { class: 'num' }, 'Workers'),
      h('th', { class: 'num' }, 'Pool fee'), h('th', {}, ''))),
    h('tbody', {}, rows.map(o => h('tr', {},
      h('td', {}, o.algorithm),
      h('td', {}, o.coin ? o.coin + (o.coin_name ? '  (' + o.coin_name + ')' : '') : 'pool picks the coin'),
      h('td', {}, o.pool),
      h('td', { class: 'num' }, fmtPrice(o.price)),
      h('td', { class: 'num' }, fmtHash(o.hashrate)),
      h('td', { class: 'num' }, o.workers),
      h('td', { class: 'num' }, o.fee_percent + ' %'),
      h('td', {}, o.coin && !excluded.has(o.coin)
        ? h('button', { class: 'small', title: 'Never mine this coin on its own', onclick: () => excludeCoin(o.coin) }, 'Exclude coin') : null))))))
    : h('div', { class: 'empty' }, state.offers.length ? 'No offer matches.' : 'No offers yet. They appear once the engine has asked the pool.');

  return h('div', {}, h('h1', {}, 'Pools'),
    h('p', { class: 'help' }, 'What zpool.ca pays right now. "Pool picks the coin" is the pool\'s own port for an algorithm, where it switches between coins itself; the other rows mine exactly one coin. Coins nobody is mining at the pool are not offered.'),
    toolbar, table);
}

async function excludeCoin(coin) {
  if (!confirm('Never mine ' + coin + ' as a single coin? You can undo this under Configuration > Pools.')) return;
  try {
    const cfg = JSON.parse(JSON.stringify(state.config));
    cfg.pools.zpool.exclude_coins.push(coin);
    await api('/api/config', { method: 'PUT', body: cfg });
    toast(coin + ' excluded');
    await loadConfig();
    render();
  } catch (e) { toast(e.message, true); }
}

// --------------------------------------------------------- configuration

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
  return field(label, help, h('select', { onchange: e => setPath(state.draft, path, e.target.value) },
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

const CONFIG_TABS = [
  ['general', 'General', 'config.yaml'],
  ['pools', 'Pools', 'pools.yaml'],
  ['miners', 'Miners', 'miners.yaml'],
  ['algorithms', 'Algorithms', 'algorithms.yaml'],
  ['devices', 'Devices', 'devices.yaml'],
];

function renderConfig() {
  if (!state.draft || !state.meta) return h('div', { class: 'empty' }, 'Loading ...');
  const tab = CONFIG_TABS.find(t => t[0] === state.configTab) || CONFIG_TABS[0];
  const tabs = h('div', { class: 'tabs' }, CONFIG_TABS.map(t =>
    h('button', { class: t[0] === tab[0] ? 'active' : '', onclick: () => { state.configTab = t[0]; render(); } }, t[1])));

  const body = { general: configGeneral, pools: configPools, miners: configMiners, algorithms: configAlgorithms, devices: configDevices }[tab[0]]();
  const error = h('span', { class: 'error', id: 'config-error' });
  const savebar = h('div', { class: 'savebar' },
    h('button', { class: 'primary', onclick: saveConfig }, 'Save and apply'),
    h('button', { onclick: () => { state.draft = JSON.parse(JSON.stringify(state.config)); render(); } }, 'Discard changes'),
    error,
    h('span', { class: 'note' }, 'Saving writes ' + state.meta.config_dir + ' and restarts the miners with the new settings.'));

  return h('div', {}, h('h1', {}, 'Configuration'), tabs,
    h('p', { class: 'help' }, 'File: ', h('code', {}, tab[2])),
    h('div', { class: 'form' }, body), savebar);
}

function configGeneral() {
  return [
    h('div', { class: 'section' }, h('h3', {}, 'Rig'),
      textField('Worker name', 'general.worker', 'Names this rig at the pools. Letters, digits, - and _.'),
      textField('Data directory', 'general.data_dir', 'Miner programs, logs and the statistics database are kept here.')),
    h('div', { class: 'section' }, h('h3', {}, 'Electricity'),
      numberField('Price per kWh', 'general.power.price_per_kwh', 'With 0, electricity is not counted. Otherwise the cost of the measured power draw is taken off the earnings.', '0.01'),
      textField('Currency', 'general.power.currency', 'Three letter code, e.g. EUR or USD. Earnings are shown in it, and the price above is in it.')),
    h('div', { class: 'section' }, h('h3', {}, 'Switching'),
      textField('Round length', 'general.switching.interval', 'How often hashrates are stored and the choice of miner is reconsidered. 30s to 1h, e.g. 1m.'),
      numberField('Hysteresis in percent', 'general.switching.hysteresis_percent', 'Another candidate must earn this much more before the running miner is stopped for it.'),
      textField('Minimum run time', 'general.switching.min_run_time', 'A started miner is left alone for at least this long, e.g. 5m.'),
      textField('Benchmark time', 'general.switching.benchmark_time', 'Measured mining time a miner needs before its hashrate is trusted, e.g. 3m.'),
      boolField('Mine at a loss', 'general.switching.mine_at_loss', 'Keep mining when electricity costs more than is earned.')),
    h('div', { class: 'section' }, h('h3', {}, 'CPU mining'),
      boolField('Mine with the CPU', 'general.cpu.enabled', 'Off by default: it makes the machine slow for everything else, and the GPU miners need some CPU time themselves.'),
      numberField('Assumed power draw in watts', 'general.cpu.watts', 'Used for the electricity cost; a CPU cannot be measured the way a GPU can.'),
      numberField('Threads', 'general.cpu.threads', '0 lets the miner decide.', '1')),
    h('div', { class: 'section' }, h('h3', {}, 'Memory'),
      numberField('Keep free, in GB', 'general.memory.min_free_ram_gb', 'Main memory (RAM and page file together) that is left for everything else. A miner whose algorithm needs more than the rest is not started. This is what keeps two miners from building a large DAG at the same time and running the machine out of memory.', '0.5'),
      numberField('Keep free on every graphics card, in GB', 'general.memory.reserved_vram_gb', 'Graphics memory that is left alone, for example for the desktop.', '0.1')),
    h('div', { class: 'section' }, h('h3', {}, 'Web interface'),
      textField('Listen address', 'general.api.listen', 'host:port. 127.0.0.1 is only reachable from this machine. A change takes effect after rbm is restarted. Empty turns the interface off.'),
      textField('Password', 'general.api.password', 'Without a password the interface only works on this machine. With one, every access needs it (the user name does not matter). Use it only on a network you trust: the connection is not encrypted.', 'password')),
  ];
}

function configPools() {
  const currencies = state.meta.payout_currencies.map(c => ({
    value: c.symbol, text: c.symbol + ' - ' + c.name + (c.notice ? '  (!)' : '') }));
  const chosen = state.meta.payout_currencies.find(c => c.symbol === state.draft.pools.zpool.currency);
  const currencyHelp = 'zpool pays everything out in this one coin, whatever is mined.' +
    (chosen && chosen.minimum_payout ? ' Minimum payout: ' + chosen.minimum_payout + ' ' + chosen.symbol + '.' : '') +
    (chosen && chosen.notice ? ' Notice from zpool: ' + chosen.notice : '');
  return [
    h('div', { class: 'section' }, h('h3', {}, 'zpool.ca'),
      boolField('Use this pool', 'pools.zpool.enabled', ''),
      currencies.length
        ? selectField('Payout coin', 'pools.zpool.currency', currencyHelp, currencies)
        : textField('Payout coin', 'pools.zpool.currency', 'zpool could not be asked for its list; enter the symbol, e.g. BTC, LTC or RVN.'),
      textField('Wallet address', 'pools.zpool.wallet', 'Must be an address of the payout coin. rbm cannot check that; a wrong address is only noticed by the pool.'),
      selectField('Region', 'pools.zpool.region', 'The stratum servers to connect to.', state.meta.zpool_regions.map(r => ({ value: r, text: r }))),
      boolField('TLS', 'pools.zpool.tls', 'Encrypt the connection to the pool where it has a port for it.')),
    h('div', { class: 'section' }, h('h3', {}, 'What to mine there'),
      boolField('Algorithm ports', 'pools.zpool.algo_switching', 'Mine on the pool\'s port for an algorithm and let the pool pick the coin. It may switch every few minutes; for algorithms with a DAG (KawPoW and relatives) every switch rebuilds it, which costs time and needs a lot of memory.'),
      boolField('Single coins', 'pools.zpool.coin_mining', 'Mine exactly one coin at a time, chosen by what it pays. No DAG switching, but a single coin often pays less than the algorithm port.'),
      listField('Only these coins', 'pools.zpool.coins', 'Symbols separated by commas. Empty allows every coin the pool offers.'),
      listField('Never these coins', 'pools.zpool.exclude_coins', 'Symbols separated by commas.')),
  ];
}

function minerDraft(name) {
  if (!state.draft.miners[name]) state.draft.miners[name] = { disabled: false, exclude_algorithms: [], extra_args: [] };
  return state.draft.miners[name];
}

function configMiners() {
  return state.meta.miners.map(m => {
    minerDraft(m.name);
    const base = 'miners.' + m.name;
    return h('div', { class: 'section' },
      h('h3', {}, m.name + ' ' + m.version, h('small', {}, '  ' + m.vendor + ', fee ' + m.fee_percent + ' % unless an algorithm differs')),
      field('Use this miner', '', h('input', { type: 'checkbox', checked: !minerDraft(m.name).disabled,
        onchange: e => { minerDraft(m.name).disabled = !e.target.checked; } })),
      field('Algorithms', 'Unchecked algorithms are not mined with this program.', toggleList(m.algorithms, base + '.exclude_algorithms')),
      field('Extra arguments', 'Added to the command line, separated by spaces. Example for Rigel: --temp-limit tc[80-85]',
        h('input', { type: 'text', value: minerDraft(m.name).extra_args.join(' '),
          oninput: e => { minerDraft(m.name).extra_args = e.target.value.split(/\s+/).filter(Boolean); } })));
  });
}

function configAlgorithms() {
  const all = Array.from(minedAlgorithms()).sort();
  return [
    h('div', { class: 'section' }, h('h3', {}, 'Algorithms'),
      field('Mine these', 'Unchecked algorithms are never mined, whatever the miner program.', toggleList(all, 'algorithms.exclude')),
      listField('Only these', 'algorithms.only', 'If this list is not empty, nothing else is mined. Names separated by commas.')),
  ];
}

function configDevices() {
  const gpus = state.devices.filter(d => d.kind === 'gpu').map(d => d.id);
  const names = {};
  for (const d of state.devices) names[d.id] = d.id + '  ' + (d.model || '');
  return [
    h('div', { class: 'section' }, h('h3', {}, 'Graphics cards'),
      gpus.length
        ? field('Mine with', 'Unchecked cards stay out of mining. Cards of the same model mine together with one miner program.', toggleList(gpus, 'devices.exclude', id => names[id]))
        : h('div', { class: 'help' }, 'No NVIDIA card was found.')),
    h('div', { class: 'section' }, h('h3', {}, 'CPU'),
      boolField('Mine with the CPU', 'general.cpu.enabled', 'The same setting as under General.')),
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

// ------------------------------------------------------------------ logs

function renderLogs() {
  const engineLog = h('pre', { class: 'log', id: 'engine-log' }, state.log.join('\n') || 'Nothing logged yet.');
  const select = h('select', { onchange: e => loadMinerLog(e.target.value) },
    h('option', { value: '' }, state.minerLogs.length ? 'Choose a miner log ...' : 'No miner logs yet'),
    state.minerLogs.map(f => h('option', { value: f.name, selected: f.name === state.minerLog.name },
      f.name + '   (' + Math.max(1, Math.round(f.size / 1024)) + ' kB)')));
  const out = h('div', {}, h('h1', {}, 'Logs'),
    h('h2', {}, 'rbm'), engineLog,
    h('h2', {}, 'Miner programs'),
    h('div', { class: 'toolbar' }, select,
      h('button', { onclick: () => loadMinerLog(state.minerLog.name), disabled: !state.minerLog.name }, 'Reload')),
    state.minerLog.name ? h('pre', { class: 'log', id: 'miner-log' }, state.minerLog.text || '(empty)') : null);
  // show the newest lines
  setTimeout(() => { for (const id of ['engine-log', 'miner-log']) { const el = document.getElementById(id); if (el) el.scrollTop = el.scrollHeight; } }, 0);
  return out;
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

async function refreshStatus() {
  const conn = document.getElementById('conn');
  try {
    state.status = await api('/api/status');
    conn.textContent = 'connected';
    conn.className = 'conn';
  } catch (e) {
    conn.textContent = 'no connection to rbm';
    conn.className = 'conn down';
    return;
  }
  const banner = document.getElementById('banner');
  banner.hidden = !state.status.waiting;
  banner.textContent = state.status.waiting ? 'Not mining: ' + state.status.waiting : '';

  renderTopbar();
  try {
    if (state.page === 'dashboard') {
      state.devices = await api('/api/devices');
      state.balances = await api('/api/balances');
    }
    if (state.page === 'activity') state.activity = await api('/api/activity');
    if (state.page === 'benchmarks') state.benchmarks = await api('/api/benchmarks');
    if (state.page === 'pools') state.offers = await api('/api/offers');
    if (state.page === 'logs') {
      state.log = await api('/api/log');
      state.minerLogs = await api('/api/minerlogs');
    }
  } catch (e) { /* shown as stale data; the next tick tries again */ }

  // the configuration page is not redrawn under the user's hands, and
  // neither is anything while a text field is being typed into
  const typing = document.activeElement && ['INPUT', 'SELECT'].includes(document.activeElement.tagName);
  if (state.page !== 'config' && !typing) render();
}

async function loadConfig() {
  const res = await api('/api/config');
  state.config = res.config;
  state.meta = res.meta;
  state.draft = JSON.parse(JSON.stringify(res.config));
  document.getElementById('version').textContent = 'v' + res.meta.version;
}

function rerenderKeepingFocus(input) {
  const pos = input.selectionStart;
  const key = input.getAttribute('data-filter');
  render();
  const again = key ? document.querySelector('input[data-filter="' + key + '"]') : document.querySelector('input[type=search]');
  if (again) { again.focus(); again.setSelectionRange(pos, pos); }
}

function render() {
  const view = { dashboard: renderDashboard, miners: renderMiners, benchmarks: renderBenchmarks, pools: renderPools, activity: renderActivity, config: renderConfig, logs: renderLogs }[state.page]();
  document.getElementById('page').replaceChildren(view);
  for (const a of document.querySelectorAll('#nav a')) a.classList.toggle('active', a.dataset.page === state.page);
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
