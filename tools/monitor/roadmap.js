// roadmap.js draws the Roadmap view and the header strip, and switches views.
//
// It reads /roadmap.json (the roadmap model, without item details) once a
// minute, one phase's details on request (/roadmap.json?phase=ID&details=1),
// and the recorded totals from /timeline?kind=roadmap.summary. Like
// dashboard.js, whose helpers (el, clear, chip, css, chart, fmtTime,
// axisStyle, tooltipStyle, timeAxis) it uses, it places every string with
// textContent: roadmap text is Markdown source shown as plain text, never
// parsed as HTML.
'use strict';

// STATE_STATUS maps a phase state to the status color it is drawn with.
const STATE_STATUS = { 'done': 'pass', 'in progress': 'running', 'not started': 'pending' };

let roadmapDoc = null;
let roadmapPhase = null;
let roadmapTrend = [];

// stateChip returns a chip for a phase state, in words.
function stateChip(state) {
  return chip(STATE_STATUS[state] || 'pending', state || 'unknown');
}

// pct formats done of total as a percentage.
function pct(done, total) {
  return total ? Math.round((100 * done) / total) + '%' : '';
}

// tile appends one labelled number to a <dl class="tiles">.
function tile(dl, label, value) {
  dl.appendChild(el('div', {}, el('dt', { text: label }), el('dd', { text: value === undefined || value === null ? '' : value })));
}

// progressCell returns a table cell holding a bar and its numbers.
function progressCell(done, total, status) {
  const bar = el('progress', { max: total || 1, value: done || 0, 'aria-label': (done || 0) + ' of ' + (total || 0) + ' done' });
  bar.style.setProperty('--bar', statusColor(status));
  return el('td', {}, bar, el('span', { cls: 'note', text: ' ' + (done || 0) + '/' + (total || 0) + ' ' + pct(done, total) }));
}

// renderStrip writes the header line shown in every view.
function renderStrip() {
  const strip = document.getElementById('strip');
  if (!roadmapDoc || roadmapDoc.error) { strip.textContent = ''; return; }
  const s = roadmapDoc.summary || {};
  const rel = s.current_release || {};
  const next = s.next_item || {};
  strip.textContent = 'Roadmap ' + (rel.version || '') + ' ' + (rel.done || 0) + '/' + (rel.total || 0) +
    ' (' + (rel.percent || 0) + '%)' + (next.phase ? '; next: Phase ' + next.phase : '');
}

// renderRoadmap draws every roadmap section from roadmapDoc.
function renderRoadmap() {
  renderStrip();
  const status = document.getElementById('rm-status');
  const tiles = document.getElementById('rm-tiles');
  clear(tiles);
  if (!roadmapDoc) { status.textContent = 'Loading the roadmap.'; return; }
  if (roadmapDoc.error) {
    status.textContent = roadmapDoc.error + (roadmapDoc.configure ? ' Write panels.json as ' + roadmapDoc.configure : '');
    return;
  }
  const s = roadmapDoc.summary || {};
  const rel = s.current_release || {};
  status.textContent = 'Read from ' + (roadmapDoc.source || 'the roadmap') + '.';
  tile(tiles, 'Work items done', (s.work_items_done || 0) + ' / ' + (s.work_items || 0) + ' (' + (s.percent_done || 0) + '%)');
  tile(tiles, 'Release ' + (rel.version || ''), (rel.done || 0) + ' / ' + (rel.total || 0) + ' (' + (rel.percent || 0) + '%)');
  tile(tiles, 'Open items', s.open_items);
  tile(tiles, 'Cite a missing test', s.items_citing_missing_tests);
  tile(tiles, 'Cite a missing file', s.items_citing_missing_files);
  const next = s.next_item || {};
  const nextNode = document.getElementById('rm-next');
  clear(nextNode);
  if (next.phase) {
    nextNode.appendChild(document.createTextNode('Next item, Phase ' + next.phase + ': '));
    nextNode.appendChild(el('span', { text: String(next.title || '').slice(0, 300) }));
    nextNode.appendChild(document.createTextNode(' '));
    nextNode.appendChild(phaseButton(next.phase, 'Open Phase ' + next.phase));
  }
  renderReleases(s);
  renderQueue(s);
  renderPhaseFilters();
  renderPhases();
  renderProblems(s);
  renderTrace();
}

// phaseButton returns a button that opens a phase's items.
function phaseButton(id, words) {
  const b = el('button', { type: 'button', cls: 'link', text: words || ('Phase ' + id) });
  b.addEventListener('click', () => openPhase(id));
  return b;
}

// renderReleases draws one row per release in ledger order.
function renderReleases(s) {
  const table = document.getElementById('rm-releases');
  clear(table);
  table.appendChild(el('thead', {}, el('tr', {}, el('th', { scope: 'col', text: 'Release' }),
    el('th', { scope: 'col', text: 'State' }), el('th', { scope: 'col', text: 'Items' }), el('th', { scope: 'col', cls: 'num', text: 'Phases' }))));
  const body = el('tbody');
  const versions = s.versions || {};
  const order = (s.release_ledger || Object.keys(versions)).filter((v) => versions[v]);
  for (const v of order) {
    const r = versions[v];
    const current = v === s.release_line;
    body.appendChild(el('tr', {}, el('th', { scope: 'row', text: v + (current ? ' (current)' : '') }),
      el('td', {}, stateChip(r.state)), progressCell(r.done, r.total, STATE_STATUS[r.state] || 'pending'),
      el('td', { cls: 'num', text: (r.phases || []).length })));
  }
  table.appendChild(body);
}

// renderQueue lists the first phases of the suggested walk.
function renderQueue(s) {
  const list = document.getElementById('rm-queue');
  clear(list);
  for (const step of (s.phase_walk || []).slice(0, 25)) {
    list.appendChild(el('li', {}, phaseButton(step.phase), ' ' + step.open_items + ' open'));
  }
}

// renderPhaseFilters fills the release filter once.
function renderPhaseFilters() {
  const select = document.getElementById('rm-version');
  if (select.options.length > 1) return;
  const s = roadmapDoc.summary || {};
  for (const v of s.release_ledger || []) select.appendChild(el('option', { value: v, text: v }));
}

// renderPhases draws the phase table, filtered by state, release and text.
function renderPhases() {
  if (!roadmapDoc || roadmapDoc.error) return;
  const state = document.getElementById('rm-state').value;
  const version = document.getElementById('rm-version').value;
  const words = document.getElementById('rm-search').value.trim().toLowerCase();
  const rows = (roadmapDoc.phases || []).filter((p) => (!state || p.state === state) && (!version || p.version === version) &&
    (!words || (p.heading || '').toLowerCase().includes(words) || String(p.id).toLowerCase() === words));
  document.getElementById('rm-count').textContent = rows.length + ' of ' + (roadmapDoc.phases || []).length + ' phases shown.';
  const table = document.getElementById('rm-phases');
  clear(table);
  table.appendChild(el('thead', {}, el('tr', {}, el('th', { scope: 'col', text: 'Phase' }), el('th', { scope: 'col', text: 'Heading' }),
    el('th', { scope: 'col', text: 'Release' }), el('th', { scope: 'col', text: 'State' }), el('th', { scope: 'col', text: 'Items' }),
    el('th', { scope: 'col', text: 'Depends on' }))));
  const body = el('tbody');
  for (const p of rows.slice(0, 400)) {
    body.appendChild(el('tr', {}, el('th', { scope: 'row' }, phaseButton(p.id, p.id)), el('td', { text: p.heading }),
      el('td', { text: p.version || '' }), el('td', {}, stateChip(p.state)),
      progressCell(p.done, p.total, STATE_STATUS[p.state] || 'pending'), el('td', { text: (p.depends_on || []).join(', ') })));
  }
  table.appendChild(body);
}

// openPhase shows one phase's items, then fetches their details.
async function openPhase(id) {
  const p = (roadmapDoc && roadmapDoc.phases || []).find((x) => String(x.id) === String(id));
  if (location.hash !== '#roadmap') showView('roadmap', false);
  roadmapPhase = id;
  const heading = document.getElementById('h-rm-phase');
  heading.textContent = p ? p.heading : 'Phase ' + id;
  document.getElementById('rm-phase-meta').textContent = p
    ? [p.version, p.state, p.done + '/' + p.total + ' done', p.depends_on && p.depends_on.length ? 'depends on ' + p.depends_on.join(', ') : '',
      p.open_gates && p.open_gates.length ? 'open gates: ' + p.open_gates.join(', ') : ''].filter(Boolean).join('; ')
    : '';
  drawItems(p ? p.items : [], null);
  heading.focus();
  const status = document.getElementById('rm-phase-status');
  status.textContent = 'Loading the details of Phase ' + id + '.';
  const doc = await getJson('/roadmap.json?phase=' + encodeURIComponent(id) + '&details=1');
  if (roadmapPhase !== id) return;
  const full = doc && !doc.error ? (doc.phases || []).find((x) => String(x.id) === String(id)) : null;
  if (full) { drawItems(full.items, true); status.textContent = 'Details loaded.'; }
  else status.textContent = 'The details could not be read' + (doc && doc.error ? ': ' + doc.error : '.');
}

// drawItems lists a phase's items; withDetails adds each item's full text.
function drawItems(items, withDetails) {
  const list = document.getElementById('rm-items');
  clear(list);
  for (const item of items || []) {
    const done = item.completed;
    const li = el('li', {}, el('span', { cls: 'state' + (done ? ' done' : ''), text: done ? 'Done' : (item.kind === 'withdrawn' ? 'Withdrawn' : 'Open') }),
      el('span', { text: item.title || '' }));
    const extra = [item.kind !== 'work' ? item.kind : '', item.gate ? 'gate: ' + item.gate : '',
      (item.missing_tests || []).length ? 'missing tests: ' + item.missing_tests.join(', ') : '',
      (item.missing_files || []).length ? 'missing files: ' + item.missing_files.join(', ') : ''].filter(Boolean).join('; ');
    if (extra) li.appendChild(el('div', { cls: 'extra', text: extra }));
    if (withDetails && item.details && item.details !== item.title) {
      li.appendChild(el('details', {}, el('summary', { text: 'Full text' }), el('pre', { cls: 'details', text: item.details })));
    }
    list.appendChild(li);
  }
}

// renderProblems lists what the roadmap itself reports as inconsistent.
function renderProblems(s) {
  const list = document.getElementById('rm-problems');
  clear(list);
  for (const p of s.version_problems || []) list.appendChild(el('li', { text: 'Phase ' + p.phase + ' ' + p.detail }));
  for (const d of s.unknown_dependencies || []) list.appendChild(el('li', { text: 'Phase ' + d.phase + ' depends on unknown Phase ' + d.depends_on }));
  if (!list.firstChild) list.appendChild(el('li', { text: 'None reported.' }));
}

// renderTrace shows the specification trace and the security attestation totals.
function renderTrace() {
  const dl = document.getElementById('rm-trace');
  clear(dl);
  const spec = (roadmapDoc.spec || {}).summary || {};
  const sec = (roadmapDoc.security || {}).summary || {};
  tile(dl, 'Spec sections traced', (spec.traced || 0) + ' / ' + (spec.sections || 0));
  tile(dl, 'Spec sections not traced', spec.not_traced);
  tile(dl, 'Security controls', sec.controls);
  tile(dl, 'Controls attested', sec.attested);
  tile(dl, 'Controls in progress', sec.in_progress);
}

// renderTrend draws the recorded roadmap totals over time.
function renderTrend() {
  const note = document.getElementById('rm-trend-note');
  const rows = roadmapTrend.map((l) => ({ t: l.t, done: (l.data || {}).work_items_done, total: (l.data || {}).work_items,
    rel: ((l.data || {}).current_release || {}).done, relName: ((l.data || {}).current_release || {}).version }));
  note.textContent = rows.length ? rows.length + ' recorded change(s), the first ' + fmtTime(rows[0].t) + '.'
    : 'Nothing recorded yet: the daemon records the totals each time they change.';
  const table = document.getElementById('rm-trend-table');
  clear(table);
  table.appendChild(el('thead', {}, el('tr', {}, el('th', { scope: 'col', text: 'When' }), el('th', { scope: 'col', cls: 'num', text: 'Done' }),
    el('th', { scope: 'col', cls: 'num', text: 'Work items' }), el('th', { scope: 'col', text: 'Release' }), el('th', { scope: 'col', cls: 'num', text: 'Release done' }))));
  const body = el('tbody');
  for (const r of rows.slice().reverse()) {
    body.appendChild(el('tr', {}, el('td', { text: fmtTime(r.t) }), el('td', { cls: 'num', text: r.done }), el('td', { cls: 'num', text: r.total }),
      el('td', { text: r.relName || '' }), el('td', { cls: 'num', text: r.rel })));
  }
  table.appendChild(body);
  const c = chart('rm-trend');
  if (!c || !rows.length) return;
  const axis = axisStyle();
  c.setOption({
    animation: false,
    grid: { left: 56, right: 56, top: 24, bottom: 32 },
    tooltip: tooltipStyle({ trigger: 'axis' }),
    legend: { top: 0, textStyle: { color: css('--text-secondary') } },
    xAxis: timeAxis('rm-trend', rows[0].t * 1000 - 3600000, Math.max(Date.now(), rows[rows.length - 1].t * 1000)),
    yAxis: [Object.assign({ type: 'value', name: 'all' }, axis), Object.assign({ type: 'value', name: 'release' }, axis)],
    series: [
      { name: 'Work items done', type: 'line', step: 'end', symbol: 'circle', symbolSize: 5, data: rows.map((r) => [r.t * 1000, r.done]), color: css('--cat-1') },
      { name: 'Current release done', type: 'line', step: 'end', symbol: 'triangle', symbolSize: 6, yAxisIndex: 1, data: rows.map((r) => [r.t * 1000, r.rel]), color: css('--cat-2') },
    ],
  });
}

// pollRoadmap rereads the model; the server caches it until a file changes.
async function pollRoadmap() {
  // Read the body on a 404 too: it says why there is no roadmap and how to configure one.
  let d = null;
  try { d = await (await fetch('/roadmap.json', { cache: 'no-store' })).json(); } catch (e) { d = null; }
  roadmapDoc = d || { error: 'the roadmap could not be read' };
  renderRoadmap();
}

// pollTrend rereads the recorded totals.
async function pollTrend() {
  try {
    const res = await fetch('/timeline?kind=roadmap.summary&limit=5000', { cache: 'no-store' });
    const text = res.ok ? await res.text() : '';
    roadmapTrend = text.split('\n').filter(Boolean).map((l) => { try { return JSON.parse(l); } catch (e) { return null; } }).filter(Boolean);
  } catch (e) { roadmapTrend = []; }
  if (!document.getElementById('view-roadmap').hidden) renderTrend();
}

// showView shows one view, marks its link current and, when asked, moves focus to it.
function showView(name, focus) {
  const views = { gate: 'Gate', roadmap: 'Roadmap' };
  if (!views[name]) name = 'gate';
  for (const v of Object.keys(views)) {
    document.getElementById('view-' + v).hidden = v !== name;
    const link = document.getElementById('nav-' + v);
    if (v === name) link.setAttribute('aria-current', 'page'); else link.removeAttribute('aria-current');
  }
  document.title = views[name] + ' - Status page';
  const title = document.getElementById('view-title');
  title.textContent = views[name];
  if (name === 'roadmap') renderTrend();
  Object.values(charts).forEach((c) => c.resize());
  if (focus) title.focus();
}

document.addEventListener('DOMContentLoaded', () => {
  const fromHash = () => showView(location.hash.replace('#', '') || 'gate', true);
  window.addEventListener('hashchange', fromHash);
  showView(location.hash.replace('#', '') || 'gate', false);
  for (const id of ['rm-state', 'rm-version']) document.getElementById(id).addEventListener('change', renderPhases);
  document.getElementById('rm-search').addEventListener('input', renderPhases);
  pollRoadmap();
  pollTrend();
  setInterval(pollRoadmap, 60000);
  setInterval(pollTrend, 60000);
});
