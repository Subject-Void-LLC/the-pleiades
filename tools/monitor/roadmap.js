// roadmap.js draws the Roadmap view and the header strip, and switches views.
//
// It reads /roadmap.json (the roadmap model, without item details) once a
// minute, one phase's details on request (/roadmap.json?phase=ID&details=1),
// and the recorded totals from /timeline?kind=roadmap.summary, with the main
// line's own history from /timeline?kind=git.day beneath them. Like
// dashboard.js, whose helpers (el, clear, chip, css, chart, fmtTime,
// axisStyle, tooltipStyle, timeAxis) it uses, it builds every node itself:
// roadmap text is Markdown source, and only its bold and code spans are
// turned into elements, by richText, never by parsing HTML.
'use strict';

// STATE_STATUS maps a phase state to the status color it is drawn with.
const STATE_STATUS = { 'done': 'pass', 'in progress': 'running', 'not started': 'pending' };
// LINE_TOKEN maps a phase state to its dependency line color, as the old roadmap page drew them.
const LINE_TOKEN = { 'done': '--st-pass', 'in progress': '--st-not-run', 'not started': '--st-fail' };
// CLAMP_CHARS is the length past which an item's full text starts folded.
const CLAMP_CHARS = 700;

let roadmapDoc = null;
let roadmapPhase = null;
let roadmapTrend = [];
// gitDays holds the newest git.day line of each day, oldest day first.
let gitDays = [];
// byId maps a phase id to its phase; up maps a phase to the ids it depends on; down the reverse.
let byId = new Map();
let up = new Map();
let down = new Map();
let chainFocus = null;

// stateChip returns a chip for a phase state, in words.
function stateChip(state) {
  return chip(STATE_STATUS[state] || 'pending', state || 'unknown');
}

// pct formats done of total as a percentage.
function pct(done, total) {
  return total ? Math.round((100 * done) / total) + '%' : '';
}

// progressCell returns a table cell holding a bar and its numbers.
function progressCell(done, total, status) {
  const bar = el('progress', { max: total || 1, value: done || 0, 'aria-label': (done || 0) + ' of ' + (total || 0) + ' done' });
  bar.style.setProperty('--bar', statusColor(status));
  return el('td', {}, bar, el('span', { cls: 'note', text: ' ' + (done || 0) + '/' + (total || 0) + ' ' + pct(done, total) }));
}

// inline turns one line's **bold** and `code` spans into elements, the rest into text.
function inline(parent, text) {
  const re = /(\*\*[^*]+\*\*|`[^`]+`)/g;
  let last = 0;
  for (const m of text.matchAll(re)) {
    if (m.index > last) parent.appendChild(document.createTextNode(text.slice(last, m.index)));
    const tok = m[0];
    if (tok.startsWith('**')) {
      const strong = el('strong');
      inline(strong, tok.slice(2, -2));  // a bold span may hold code spans
      parent.appendChild(strong);
    } else {
      parent.appendChild(el('code', { text: tok.slice(1, -1) }));
    }
    last = m.index + tok.length;
  }
  if (last < text.length) parent.appendChild(document.createTextNode(text.slice(last)));
}

// richText renders roadmap Markdown as paragraphs: hard-wrapped lines join,
// a blank line, a list item or a bold lead-in starts a new paragraph.
function richText(text) {
  const box = el('div', { cls: 'item-text' });
  let para = [];
  const flush = () => {
    if (!para.length) return;
    const p = el('p');
    inline(p, para.join(' '));
    box.appendChild(p);
    para = [];
  };
  for (const raw of String(text || '').split('\n')) {
    const line = raw.trim();
    if (!line) { flush(); continue; }
    if (/^([-*+]|\d+\.)\s/.test(line) || (/^\*\*/.test(line) && para.length)) flush();
    para.push(line);
  }
  flush();
  return box;
}

// roadmapReady says whether roadmapDoc holds a roadmap: read, configured, and without error.
function roadmapReady() {
  return Boolean(roadmapDoc) && !roadmapDoc.error && roadmapDoc.configured !== false;
}

// renderStrip writes the header line shown in every view.
function renderStrip() {
  const strip = document.getElementById('strip');
  if (!roadmapReady()) { strip.textContent = ''; return; }
  const s = roadmapDoc.summary || {};
  const rel = s.current_release || {};
  const next = s.next_item || {};
  strip.textContent = (rel.version || 'Roadmap') + ' · ' + (rel.done || 0) + ' of ' + (rel.total || 0) + (next.phase ? ' · next Phase ' + next.phase : '');
  strip.style.setProperty('--chip', statusColor('running'));
}

// indexPhases rebuilds the id and dependency maps from the model.
function indexPhases() {
  byId = new Map();
  up = new Map();
  down = new Map();
  for (const p of roadmapDoc.phases || []) byId.set(String(p.id), p);
  for (const p of roadmapDoc.phases || []) {
    const id = String(p.id);
    for (const pre of p.depends_on || []) {
      if (!up.has(id)) up.set(id, []);
      up.get(id).push(String(pre));
      if (!down.has(String(pre))) down.set(String(pre), []);
      down.get(String(pre)).push(id);
    }
  }
}

// reach returns every phase reachable from id along one direction, cycles included safely.
function reach(map, id) {
  const seen = new Set();
  const stack = [id];
  while (stack.length) {
    for (const next of map.get(stack.pop()) || []) {
      if (!seen.has(next)) { seen.add(next); stack.push(next); }
    }
  }
  seen.delete(id);
  return seen;
}

// renderRoadmap draws every roadmap section from roadmapDoc.
function renderRoadmap() {
  renderStrip();
  const status = document.getElementById('rm-status');
  const tiles = document.getElementById('rm-tiles');
  clear(tiles);
  if (!roadmapDoc) { status.textContent = 'Loading the roadmap.'; return; }
  if (roadmapDoc.configured === false) {
    // A clone without the roadmap: an empty tab, not an error.
    document.getElementById('rm-hero').textContent = 'No roadmap';
    status.textContent = roadmapDoc.note + (roadmapDoc.configure ? ' Its shape: ' + roadmapDoc.configure : '');
    return;
  }
  if (roadmapDoc.error) {
    document.getElementById('rm-hero').textContent = 'No roadmap';
    status.textContent = roadmapDoc.error + (roadmapDoc.configure ? ' Write panels.json as ' + roadmapDoc.configure : '');
    return;
  }
  indexPhases();
  const s = roadmapDoc.summary || {};
  const rel = s.current_release || {};
  status.textContent = 'Read from ' + (roadmapDoc.source || 'the roadmap') + '.';
  document.getElementById('h-rm-summary').textContent = 'Current release ' + (rel.version || '');
  document.getElementById('rm-hero').textContent = (rel.percent || 0) + '%';
  document.getElementById('rm-hero-sub').textContent = (rel.done || 0) + ' of ' + (rel.total || 0) + ' work items across ' +
    (rel.phases || []).length + ' phases';
  const bar = document.getElementById('rm-hero-bar');
  bar.max = rel.total || 1;
  bar.value = rel.done || 0;
  const states = { 'done': 0, 'in progress': 0, 'not started': 0 };
  for (const p of roadmapDoc.phases || []) states[p.state] = (states[p.state] || 0) + 1;
  const spec = (roadmapDoc.spec || {}).summary || {};
  tile(tiles, 'All work items', (s.work_items_done || 0).toLocaleString(), 'of ' + (s.work_items || 0).toLocaleString() + ' done · ' + (s.percent_done || 0) + '%',
    s.work_items_done, s.work_items);
  tile(tiles, 'Open items', (s.open_items || 0).toLocaleString(), (s.process_items_not_counted || 0) + ' process items not counted');
  tile(tiles, 'Phases in progress', states['in progress'], states['done'] + ' done · ' + states['not started'] + ' not started');
  tile(tiles, 'Cite a missing test', s.items_citing_missing_tests, 'items naming a test that is not there');
  tile(tiles, 'Cite a missing file', s.items_citing_missing_files, 'items naming a file that is not there');
  tile(tiles, 'Spec traced', spec.traced || 0, 'of ' + (spec.sections || 0) + ' sections · ' + (spec.not_traced || 0) + ' not traced');
  const next = s.next_item || {};
  const nextNode = document.getElementById('rm-next');
  clear(nextNode);
  nextNode.hidden = !next.phase;
  if (next.phase) {
    const p = byId.get(String(next.phase));
    nextNode.appendChild(el('div', {}, el('span', { cls: 'label', text: 'Next item' }),
      phaseButton(next.phase, p ? p.heading : 'Phase ' + next.phase)));
    const text = String(next.title || '');
    nextNode.appendChild(richText(text.length > 320 ? text.slice(0, 320) + '...' : text));
  }
  renderReleases(s);
  renderQueue(s);
  renderPhaseFilters();
  renderPhases();
  renderProblems(s);
  renderTrace();
  if (roadmapPhase) drawPhaseHeader(roadmapPhase);
}

// phaseButton returns a button that opens a phase's items.
function phaseButton(id, words, cls) {
  const b = el('button', { type: 'button', cls: cls || 'link', text: words || ('Phase ' + id) });
  b.addEventListener('click', () => openPhase(String(id)));
  return b;
}

// depButton returns a chip button for a phase, colored and labelled by its state.
function depButton(id, short) {
  const p = byId.get(String(id));
  const b = el('button', { type: 'button', cls: 'dep', text: short ? String(id) : 'Phase ' + id + (p ? ', ' + p.state : ', no such phase'),
    title: p ? p.heading : 'No phase has this id' });
  b.style.setProperty('--chip', p ? statusColor(STATE_STATUS[p.state] || 'pending') : css('--border'));
  if (p) b.addEventListener('click', () => openPhase(String(id)));
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
    body.appendChild(el('tr', { cls: current ? 'current' : null }, el('th', { scope: 'row', text: v + (current ? ' · current' : '') }),
      el('td', {}, stateChip(r.state)), progressCell(r.done, r.total, STATE_STATUS[r.state] || 'pending'),
      el('td', { cls: 'num', text: (r.phases || []).length })));
  }
  table.appendChild(body);
}

// renderQueue lists the first phases of the suggested walk.
function renderQueue(s) {
  const list = document.getElementById('rm-queue');
  clear(list);
  for (const step of (s.phase_walk || []).slice(0, 30)) {
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
  if (!roadmapReady()) return;
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
    const heading = String(p.heading || '').replace(/^Phase\s+\S+?:\s*/, '');
    body.appendChild(el('tr', { 'data-phase': String(p.id) }, el('th', { scope: 'row' }, phaseButton(p.id, String(p.id), 'pid')),
      el('td', { text: heading }), el('td', { text: p.version || '' }), el('td', {}, stateChip(p.state)),
      progressCell(p.done, p.total, STATE_STATUS[p.state] || 'pending'), el('td', { text: (p.depends_on || []).join(', ') })));
  }
  table.appendChild(body);
  drawDepLines();
}

// depLinesOn reports whether the dependency lines are shown.
function depLinesOn() {
  return document.getElementById('rm-deps').checked;
}

// drawDepLines draws an arc from each visible phase to each visible phase
// that depends on it, in the gutter left of the table, as the old page did.
function drawDepLines() {
  const wrap = document.getElementById('rm-phases-wrap');
  const svg = document.getElementById('rm-dep-lines');
  const on = depLinesOn();
  wrap.classList.toggle('on', on);
  document.getElementById('rm-deps-note').hidden = !on;
  while (svg.firstChild) svg.removeChild(svg.firstChild);
  if (!on || document.getElementById('view-roadmap').hidden) return;
  const NS = 'http://www.w3.org/2000/svg';
  const top = wrap.getBoundingClientRect().top;
  const rows = new Map();
  for (const tr of wrap.querySelectorAll('tbody tr')) {
    const r = tr.getBoundingClientRect();
    rows.set(tr.dataset.phase, r.top - top + r.height / 2);
  }
  svg.setAttribute('height', String(wrap.scrollHeight));
  const defs = document.createElementNS(NS, 'defs');
  for (const [state, token] of Object.entries(LINE_TOKEN)) {
    const marker = document.createElementNS(NS, 'marker');
    marker.setAttribute('id', 'arrow-' + state.replace(' ', '-'));
    for (const [k, v] of [['viewBox', '0 0 10 10'], ['refX', '9'], ['refY', '5'], ['markerWidth', '5'], ['markerHeight', '5'], ['orient', 'auto']]) {
      marker.setAttribute(k, v);
    }
    const tip = document.createElementNS(NS, 'path');
    tip.setAttribute('d', 'M0,0 L10,5 L0,10 z');
    tip.setAttribute('fill', css(token));
    marker.appendChild(tip);
    defs.appendChild(marker);
  }
  svg.appendChild(defs);
  const x = 62;
  for (const [dep, pres] of up) {
    for (const pre of pres) {
      if (!rows.has(pre) || !rows.has(dep)) continue;
      const y1 = rows.get(pre);
      const y2 = rows.get(dep);
      // Longer spans bulge further into the gutter, so nested arcs stay apart.
      const bulge = Math.min(58, 12 + Math.abs(y2 - y1) * 0.08);
      const state = (byId.get(pre) || {}).state || 'not started';
      const path = document.createElementNS(NS, 'path');
      path.setAttribute('d', 'M ' + x + ' ' + y1 + ' C ' + (x - bulge) + ' ' + y1 + ', ' + (x - bulge) + ' ' + y2 + ', ' + x + ' ' + y2);
      path.setAttribute('stroke', css(LINE_TOKEN[state] || '--st-fail'));
      path.setAttribute('marker-end', 'url(#arrow-' + state.replace(' ', '-') + ')');
      path.dataset.pre = pre;
      path.dataset.dep = dep;
      svg.appendChild(path);
    }
  }
  highlightChain(chainFocus);
}

// highlightChain lights the whole chain through id, upstream and downstream, and fades the rest.
function highlightChain(id) {
  chainFocus = id;
  const wrap = document.getElementById('rm-phases-wrap');
  const chain = id && depLinesOn() && (up.has(id) || down.has(id)) ? new Set([id, ...reach(up, id), ...reach(down, id)]) : null;
  wrap.classList.toggle('chain-active', !!chain);
  for (const tr of wrap.querySelectorAll('tbody tr')) {
    tr.classList.toggle('in-chain', !!chain && chain.has(tr.dataset.phase));
    tr.classList.toggle('chain-focus', !!chain && tr.dataset.phase === id);
  }
  const svg = document.getElementById('rm-dep-lines');
  for (const p of Array.from(svg.querySelectorAll('path[data-pre]'))) {
    const lit = !!chain && chain.has(p.dataset.pre) && chain.has(p.dataset.dep);
    p.classList.toggle('on', lit);
    if (lit) svg.appendChild(p);
  }
}

// byOrder sorts phase ids by their position in the roadmap.
function byOrder(a, b) {
  const ids = Array.from(byId.keys());
  return ids.indexOf(a) - ids.indexOf(b);
}

// drawPhaseHeader writes an opened phase's heading, facts and dependency links.
function drawPhaseHeader(id) {
  const p = byId.get(String(id));
  document.getElementById('h-rm-phase').textContent = p ? p.heading : 'Phase ' + id;
  document.getElementById('rm-phase-meta').textContent = p
    ? [p.version, p.state, p.done + ' of ' + p.total + ' done',
      p.open_gates && p.open_gates.length ? 'open gates: ' + p.open_gates.join(', ') : ''].filter(Boolean).join(' · ')
    : '';
  const links = document.getElementById('rm-phase-links');
  clear(links);
  if (!p) return;
  const own = String(p.id);
  const row = (label, ids) => {
    if (!ids.length) return;
    const chips = el('div', { cls: 'chips' });
    ids.forEach((x) => chips.appendChild(depButton(x)));
    links.appendChild(el('span', { cls: 'deps-label', text: label }));
    links.appendChild(chips);
  };
  row('Depends on', (p.depends_on || []).map(String));
  row('Needed by', (down.get(own) || []).slice().sort(byOrder));
  const sections = ((p.implements || {}).sections || []).map((x) => x.id + ' ' + x.title);
  if (sections.length) {
    links.appendChild(el('span', { cls: 'deps-label', text: 'Implements' }));
    links.appendChild(el('div', { cls: 'chips', text: sections.join('; ') }));
  }
  const upstream = Array.from(reach(up, own)).sort(byOrder);
  const downstream = Array.from(reach(down, own)).sort(byOrder);
  if (upstream.length > (p.depends_on || []).length || downstream.length > (down.get(own) || []).length) {
    const chainBox = el('details', {}, el('summary', { text: 'Whole chain: ' + upstream.length + ' upstream, ' + downstream.length + ' downstream' }));
    const list = (label, ids) => {
      if (!ids.length) return;
      const ul = el('ul', { cls: 'chain-list chips' });
      ids.forEach((x) => ul.appendChild(el('li', {}, depButton(x))));
      chainBox.appendChild(el('p', { cls: 'note', text: label }));
      chainBox.appendChild(ul);
    };
    list('Everything this phase waits on, directly or through another phase:', upstream);
    list('Everything that waits on this phase:', downstream);
    links.appendChild(el('span', { cls: 'deps-label', text: 'Chain' }));
    links.appendChild(chainBox);
  }
}

// openPhase shows one phase's items, then fetches their full text.
async function openPhase(id) {
  const p = byId.get(String(id));
  if (location.hash !== '#roadmap') showView('roadmap', false);
  roadmapPhase = id;
  drawPhaseHeader(id);
  drawItems(p ? p.items : [], false);
  document.getElementById('h-rm-phase').focus();
  highlightChain(String(id));
  const status = document.getElementById('rm-phase-status');
  status.textContent = 'Loading the full text of Phase ' + id + '.';
  const doc = await getJson('/roadmap.json?phase=' + encodeURIComponent(id) + '&details=1');
  if (roadmapPhase !== id) return;
  const full = doc && !doc.error ? (doc.phases || []).find((x) => String(x.id) === String(id)) : null;
  if (full) { drawItems(full.items, true); status.textContent = ''; }
  else status.textContent = 'The full text could not be read' + (doc && doc.error ? ': ' + doc.error : '.');
}

// drawItems lists a phase's items; withDetails shows each item's full text, folded when long.
function drawItems(items, withDetails) {
  const list = document.getElementById('rm-items');
  clear(list);
  for (const item of items || []) {
    const word = item.kind === 'withdrawn' ? 'Withdrawn' : (item.completed ? 'Done' : 'Open');
    const text = withDetails && item.details ? item.details : item.title || '';
    const body = richText(text);
    const li = el('li', {}, el('span', { cls: 'badge ' + word.toLowerCase(), text: word }), body);
    const meta = el('div', { cls: 'item-meta' });
    if (item.kind && item.kind !== 'work') meta.appendChild(el('span', { text: item.kind }));
    if (item.gate) meta.appendChild(chip(item.completed ? 'pass' : 'pending', 'gate: ' + item.gate));
    if ((item.missing_tests || []).length) meta.appendChild(el('span', { cls: 'failed-job', text: 'missing tests: ' + item.missing_tests.join(', ') }));
    if ((item.missing_files || []).length) meta.appendChild(el('span', { cls: 'failed-job', text: 'missing files: ' + item.missing_files.join(', ') }));
    if (meta.firstChild) li.appendChild(meta);
    if (withDetails && text.length > CLAMP_CHARS) {
      body.classList.add('clamped');
      const more = el('button', { type: 'button', cls: 'more', text: 'Show all', 'aria-expanded': 'false' });
      more.addEventListener('click', () => {
        const open = body.classList.toggle('clamped') === false;
        more.textContent = open ? 'Show less' : 'Show all';
        more.setAttribute('aria-expanded', String(open));
      });
      li.appendChild(more);
    }
    list.appendChild(li);
  }
}

// renderProblems lists what the roadmap itself reports as inconsistent.
function renderProblems(s) {
  const box = document.getElementById('rm-problems');
  clear(box);
  // Phases left open in a release that already closed share one box; the rest get one each.
  const closed = new Map();
  for (const p of s.version_problems || []) {
    const m = p.kind === 'closed' && /ships in (\S+), which closed/.exec(p.detail || '');
    if (m) {
      if (!closed.has(m[1])) closed.set(m[1], []);
      closed.get(m[1]).push(p.phase);
    } else {
      box.appendChild(el('div', { cls: 'warnbox' }, el('span', {}, phaseButton(p.phase), ' ' + p.detail)));
    }
  }
  for (const [version, phases] of closed) {
    const chips = el('div', { cls: 'chips' });
    phases.forEach((id) => chips.appendChild(depButton(id, true)));
    box.appendChild(el('div', { cls: 'warnbox' }, el('span', {}, el('strong', { text: phases.length + ' phases ship in ' + version }),
      ', which closed when the release line moved on, but still have open work items.'), chips));
  }
  const unknown = s.unknown_dependencies || [];
  if (unknown.length) {
    const line = el('span');
    unknown.forEach((d, i) => {
      if (i) line.appendChild(document.createTextNode('; '));
      line.appendChild(phaseButton(d.phase));
      line.appendChild(document.createTextNode(' depends on unknown Phase ' + d.depends_on));
    });
    box.appendChild(el('div', { cls: 'warnbox' }, line));
  }
  if (!box.firstChild) box.appendChild(el('p', { cls: 'note', text: 'None reported.' }));
}

// renderTrace shows the specification trace and the security attestation totals.
function renderTrace() {
  const dl = document.getElementById('rm-trace');
  clear(dl);
  const spec = (roadmapDoc.spec || {}).summary || {};
  const sec = (roadmapDoc.security || {}).summary || {};
  tile(dl, 'Spec traced', spec.traced || 0, 'of ' + (spec.sections || 0) + ' sections', spec.traced, spec.sections);
  tile(dl, 'Controls', sec.controls || 0, 'mapped to SDLC stages');
  tile(dl, 'In progress', sec.in_progress || 0, (sec.drafted_awaiting_review || 0) + ' drafted for review');
  tile(dl, 'Attested', sec.attested || 0, (sec.open || 0) + ' still open', sec.attested, sec.controls);
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
    grid: { left: 56, right: 56, top: 28, bottom: 32 },
    tooltip: tooltipStyle({ trigger: 'axis' }),
    legend: { top: 0, textStyle: { color: css('--text-secondary') } },
    xAxis: timeAxis('rm-trend', rows[0].t * 1000 - 3600000, Math.max(Date.now(), rows[rows.length - 1].t * 1000)),
    yAxis: [Object.assign({ type: 'value', name: 'all' }, axis), Object.assign({ type: 'value', name: 'release' }, axis)],
    series: [
      { name: 'Work items done', type: 'line', step: 'end', symbol: 'circle', symbolSize: 6, data: rows.map((r) => [r.t * 1000, r.done]), color: css('--cat-1') },
      { name: 'Current release done', type: 'line', step: 'end', symbol: 'triangle', symbolSize: 7, yAxisIndex: 1, data: rows.map((r) => [r.t * 1000, r.rel]), color: css('--cat-2') },
    ],
  });
}

// renderGitTrend draws what git holds for every day since the first commit:
// the roadmap's totals start when the daemon began recording, git does not.
function renderGitTrend() {
  const note = document.getElementById('rm-git-note');
  const rows = gitDays.map((l) => Object.assign({ t: l.t, day: l.src }, l.data || {}));
  const last = rows[rows.length - 1];
  note.textContent = rows.length
    ? rows.length + ' day(s) with a commit since ' + rows[0].day + ': ' + last.commits_total + ' commits, ' + last.merged_prs_total + ' merged pull requests'
      + (last.methods != null ? ', ' + last.methods_implemented + ' of ' + last.methods + ' catalog methods implemented' : '') + ', as of this clone\'s last fetch.'
    : 'Nothing recorded yet: the daemon reads the main line\'s history when it starts.';
  const table = document.getElementById('rm-git-table');
  clear(table);
  table.appendChild(el('thead', {}, el('tr', {}, el('th', { scope: 'col', text: 'Day (UTC)' }), el('th', { scope: 'col', cls: 'num', text: 'Commits' }),
    el('th', { scope: 'col', cls: 'num', text: 'Commits so far' }), el('th', { scope: 'col', cls: 'num', text: 'Merged pull requests' }),
    el('th', { scope: 'col', cls: 'num', text: 'Methods implemented' }), el('th', { scope: 'col', cls: 'num', text: 'Methods listed' }))));
  const body = el('tbody');
  for (const r of rows.slice().reverse()) {
    body.appendChild(el('tr', {}, el('td', { text: r.day }), el('td', { cls: 'num', text: r.commits }), el('td', { cls: 'num', text: r.commits_total }),
      el('td', { cls: 'num', text: r.merged_prs_total }), el('td', { cls: 'num', text: r.methods_implemented == null ? '' : r.methods_implemented }),
      el('td', { cls: 'num', text: r.methods == null ? '' : r.methods })));
  }
  table.appendChild(body);
  const c = chart('rm-git');
  if (!c || !rows.length) return;
  const axis = axisStyle();
  const day = (v) => new Date(v).toLocaleDateString([], { month: 'short', day: 'numeric' });
  const line = (name, key, color, symbol, yAxisIndex) => ({ name, type: 'line', symbol, symbolSize: 5, showSymbol: rows.length < 40, yAxisIndex,
    data: rows.filter((r) => r[key] != null).map((r) => [r.t * 1000, r[key]]), color: css(color) });
  c.setOption({
    animation: false,
    grid: { left: 56, right: 56, top: 28, bottom: 32 },
    tooltip: tooltipStyle({ trigger: 'axis' }),
    legend: { top: 0, textStyle: { color: css('--text-secondary') } },
    xAxis: Object.assign(timeAxis('rm-git', rows[0].t * 1000, Math.max(Date.now(), last.t * 1000)),
      { axisLabel: { color: css('--text-secondary'), hideOverlap: true, formatter: day } }),
    yAxis: [Object.assign({ type: 'value', name: 'commits' }, axis), Object.assign({ type: 'value', name: 'pull requests, methods', nameTextStyle: { align: 'right' } }, axis, { splitLine: { show: false } })],
    series: [
      line('Commits', 'commits_total', '--cat-1', 'circle', 0),
      line('Merged pull requests', 'merged_prs_total', '--cat-2', 'triangle', 1),
      line('Methods implemented', 'methods_implemented', '--cat-3', 'rect', 1),
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
  try {
    const res = await fetch('/timeline?kind=git.day&limit=5000', { cache: 'no-store' });
    const text = res.ok ? await res.text() : '';
    // A day still gaining commits is written again: keep each day's last line.
    const byDay = new Map();
    for (const l of text.split('\n').filter(Boolean)) { try { const o = JSON.parse(l); byDay.set(o.src, o); } catch (e) { /* a truncated line */ } }
    gitDays = Array.from(byDay.values()).sort((a, b) => (a.src < b.src ? -1 : 1));
  } catch (e) { gitDays = []; }
  if (!document.getElementById('view-roadmap').hidden) { renderTrend(); renderGitTrend(); }
}

// showView shows one view, marks its link current and, when asked, moves focus to it.
function showView(name, focus) {
  const views = { gate: 'Gate', actions: 'Actions', roadmap: 'Roadmap', security: 'Security' };
  if (!views[name]) name = 'gate';
  for (const v of Object.keys(views)) {
    document.getElementById('view-' + v).hidden = v !== name;
    const link = document.getElementById('nav-' + v);
    if (v === name) link.setAttribute('aria-current', 'page'); else link.removeAttribute('aria-current');
  }
  document.title = views[name] + ' - Status page';
  const title = document.getElementById('view-title');
  title.textContent = views[name];
  if (name === 'roadmap') { renderTrend(); renderGitTrend(); drawDepLines(); }
  if (name === 'actions' && typeof renderActionsHistory === 'function') renderActionsHistory();
  if (name === 'security' && typeof renderSecurityTrend === 'function') renderSecurityTrend();
  Object.values(charts).forEach((c) => c.resize());
  if (focus) title.focus();
}

document.addEventListener('DOMContentLoaded', () => {
  const deps = document.getElementById('rm-deps');
  try { deps.checked = localStorage.getItem('rm-deps') === '1'; } catch (e) { /* storage blocked: lines start hidden */ }
  if (new URLSearchParams(location.search).get('deps') === '1') deps.checked = true;
  deps.addEventListener('change', () => {
    try { localStorage.setItem('rm-deps', deps.checked ? '1' : '0'); } catch (e) { /* storage blocked */ }
    drawDepLines();
  });
  window.addEventListener('hashchange', () => showView(location.hash.replace('#', '') || 'gate', true));
  showView(location.hash.replace('#', '') || 'gate', false);
  for (const id of ['rm-state', 'rm-version']) document.getElementById(id).addEventListener('change', renderPhases);
  document.getElementById('rm-search').addEventListener('input', renderPhases);
  const table = document.getElementById('rm-phases');
  const enter = (e) => {
    const tr = e.target.closest && e.target.closest('tbody tr');
    const id = tr ? tr.dataset.phase : null;
    if (id !== chainFocus) highlightChain(id);
  };
  table.addEventListener('mouseover', enter);
  table.addEventListener('focusin', enter);
  table.addEventListener('mouseleave', () => highlightChain(roadmapPhase ? String(roadmapPhase) : null));
  let pending = false;
  new ResizeObserver(() => {
    if (pending) return;
    pending = true;
    requestAnimationFrame(() => { drawDepLines(); pending = false; });
  }).observe(document.getElementById('rm-phases-wrap'));
  pollRoadmap();
  pollTrend();
  setInterval(pollRoadmap, 60000);
  setInterval(pollTrend, 60000);
});
