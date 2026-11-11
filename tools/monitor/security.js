// security.js draws the Security tab and the header's security pill.
//
// It reads /security.json once a minute (the daemon rebuilds it at most every
// thirty seconds) and the recorded totals from /timeline?kind=security.summary.
// The Pending panel leads the tab: every group the daemon derived, most
// urgent first, so nothing waiting on someone sits only in a table. Like
// dashboard.js and roadmap.js, whose helpers (el, clear, chip, tile, css,
// chart, fmtTime, statusColor, richText, openPhase) it uses, it builds every
// node itself and never parses HTML.
'use strict';

// SEC_STATE maps a control's state to the status color it is drawn with.
const SEC_STATE = { 'attested': 'pass', 'in progress': 'running', 'open': 'fail', 'not applicable': 'skipped' };
// URGENT are the pending groups drawn as warnings and opened; the rest are calmer.
const URGENT = { problem: 'bad', scanner: 'bad', overdue: 'bad', signoff: 'urgent', open: 'urgent', due_soon: 'urgent', unproven: 'urgent', waiver: 'urgent' };

let securityDoc = null;
let securityTrend = [];
let securityControl = null;

// secStatus returns the status color name of a control state.
function secStatus(state) {
  if (String(state || '').startsWith('drafted')) return 'not-run';
  return SEC_STATE[state] || 'pending';
}

// secControls returns the attestation's controls, or none.
function secControls() {
  return ((securityDoc && securityDoc.attestation) || {}).controls || [];
}

// controlButton returns a button that opens a control's statements.
function controlButton(id, words) {
  const b = el('button', { type: 'button', cls: 'link', text: words || id });
  b.addEventListener('click', () => openControl(id));
  return b;
}

// renderSecurity draws every Security section from securityDoc.
function renderSecurity() {
  const doc = securityDoc;
  const pill = document.getElementById('sec-pill');
  if (!doc || doc.error) {
    document.getElementById('sec-status').textContent = doc && doc.error ? doc.error : 'Loading.';
    return;
  }
  const att = doc.attestation;
  const sum = (att && att.summary) || {};
  const pend = doc.pending_total || 0;
  pill.textContent = pend ? 'Security · ' + pend + ' pending' : 'Security · nothing pending';
  pill.style.setProperty('--chip', statusColor(pend ? 'not-run' : 'pass'));
  const hero = document.getElementById('sec-hero');
  const tiles = document.getElementById('sec-tiles');
  clear(tiles);
  if (!att) {
    hero.textContent = 'No attestation';
    document.getElementById('sec-hero-sub').textContent = 'Configure the roadmap panel to read SECURITY_ATTESTATION.md.';
  } else {
    hero.textContent = (sum.attested || 0) + ' of ' + (sum.controls || 0);
    const next = att.next_review;
    document.getElementById('sec-hero-sub').textContent = 'controls attested' +
      (next ? ' · next review ' + next.date + ' (' + next.control + ')' : ' · no control signed yet');
    const bar = document.getElementById('sec-hero-bar');
    bar.max = sum.controls || 1;
    bar.value = sum.attested || 0;
    document.getElementById('sec-status').textContent = 'Read from ' + (att.source || 'the attestation') + '.';
  }
  const vulns = (doc.scans || {}).vulns;
  tile(tiles, 'Pending', pend, pend ? 'need someone; listed below' : 'nothing waits');
  tile(tiles, 'Awaiting sign-off', sum.drafted_awaiting_review || 0, 'drafted, waiting for an owner');
  tile(tiles, 'Open controls', sum.open || 0, 'no statement done yet');
  tile(tiles, 'In progress', sum.in_progress || 0, (sum.review_overdue || 0) + ' reviews overdue');
  tile(tiles, 'govulncheck', vulns ? (vulns.failed ? 'failed' : vulns.conclusion) : 'no run', vulns ? 'newest scheduled scan ' + fmtTime(vulns.end) : 'the vulns job has not run');
  tile(tiles, 'Unreleased fixes', (doc.unreleased || []).length, 'security changes waiting for a release');
  if (vulns && vulns.failed) tiles.children[4].querySelector('dd').classList.add('failed-job');
  renderPending(doc);
  renderControlTable();
  renderScans(doc);
  renderFrameworks(att);
  renderWaivers(doc);
  renderUnreleased(doc);
  if (securityControl) openControl(securityControl, false);
}

// renderPending draws each pending group: urgent ones open as warnings, the rest folded.
function renderPending(doc) {
  const box = document.getElementById('sec-pending');
  clear(box);
  const groups = doc.pending || [];
  document.getElementById('sec-pending-note').textContent = doc.pending_total
    ? doc.pending_total + ' item(s) waiting on someone, most urgent first'
    : 'nothing waits on anyone';
  for (const g of groups) {
    const tone = URGENT[g.group];
    const list = el('ul');
    for (const item of g.items.slice(0, 60)) {
      const isControl = secControls().some((c) => c.id === item.ref);
      const head = isControl ? controlButton(item.ref, item.title)
        : (safeUrl(item.ref) ? el('a', { href: safeUrl(item.ref), rel: 'noreferrer', target: '_blank', text: item.title }) : el('strong', { text: item.title }));
      list.appendChild(el('li', {}, head, el('span', { cls: 'detail', text: ' · ' + item.detail })));
    }
    if (g.items.length > 60) list.appendChild(el('li', { text: (g.items.length - 60) + ' more' }));
    const head = [el('span', { text: g.title }), el('span', { cls: 'count', text: String(g.count) })];
    // Urgent groups stay open; the long calm ones (in progress, unreleased) start folded.
    if (tone) {
      box.appendChild(el('div', { cls: 'pend ' + tone, role: 'group', 'aria-label': g.title }, el('div', { cls: 'pend-head' }, ...head), list));
    } else {
      box.appendChild(el('details', { cls: 'pend' }, el('summary', {}, ...head), list));
    }
  }
  if (!groups.length) box.appendChild(el('p', { cls: 'note', text: 'Nothing is pending.' }));
}

// renderControlTable lists the controls under their life cycle stage, filtered.
function renderControlTable() {
  const att = securityDoc && securityDoc.attestation;
  const table = document.getElementById('sec-controls');
  clear(table);
  if (!att) return;
  const state = document.getElementById('sec-state').value;
  const words = document.getElementById('sec-search').value.trim().toLowerCase();
  table.appendChild(el('thead', {}, el('tr', {}, el('th', { scope: 'col', text: 'Control' }), el('th', { scope: 'col', text: 'Title' }),
    el('th', { scope: 'col', text: 'State' }), el('th', { scope: 'col', text: 'Statements' }), el('th', { scope: 'col', text: 'Review due' }))));
  const body = el('tbody');
  for (const stage of att.stages || []) {
    const rows = (att.controls || []).filter((c) => c.stage === stage.number &&
      (!state || String(c.state).startsWith(state)) &&
      (!words || (c.id + ' ' + c.title + ' ' + (c.references || []).join(' ')).toLowerCase().includes(words)));
    if (!rows.length) continue;
    body.appendChild(el('tr', { cls: 'sect' }, el('td', { colspan: 5, text: String(stage.heading || '').replace(/\s*\(.*\)$/, '') })));
    for (const c of rows) {
      const bar = el('progress', { max: c.statements_total || 1, value: c.statements_done || 0, 'aria-label': c.statements_done + ' of ' + c.statements_total + ' statements done' });
      bar.style.setProperty('--bar', statusColor(secStatus(c.state)));
      body.appendChild(el('tr', { cls: c.id === securityControl ? 'current' : null }, el('th', { scope: 'row' }, controlButton(c.id)),
        el('td', { text: c.title }), el('td', {}, chip(secStatus(c.state), c.state)),
        el('td', {}, bar, el('span', { cls: 'note', text: ' ' + c.statements_done + '/' + c.statements_total })),
        el('td', { text: c.review_due || '' })));
    }
  }
  table.appendChild(body);
}

// openControl shows one control: its references, the phases that implement it, and its statements.
function openControl(id, focus) {
  const c = secControls().find((x) => x.id === id);
  if (!c) return;
  securityControl = id;
  if (location.hash !== '#security') showView('security', false);
  document.getElementById('h-sec-control').textContent = c.id + ': ' + c.title;
  document.getElementById('sec-control-meta').textContent = [c.state, c.statements_done + ' of ' + c.statements_total + ' statements done',
    c.signed_on ? 'signed ' + c.signed_on : 'not signed', c.review_due ? 'review due ' + c.review_due : '',
    c.review_every_days ? 'reviewed every ' + c.review_every_days + ' days' : ''].filter(Boolean).join(' · ');
  const links = document.getElementById('sec-control-links');
  clear(links);
  if ((c.references || []).length) {
    links.appendChild(el('span', { cls: 'deps-label', text: 'Maps to' }));
    links.appendChild(el('div', { cls: 'chips', text: c.references.join(' · ') }));
  }
  if ((c.implemented_by || []).length) {
    const chips = el('div', { cls: 'chips' });
    for (const impl of c.implemented_by) {
      const b = el('button', { type: 'button', cls: 'dep', text: 'Phase ' + impl.phase + ', ' + impl.status + (impl.proven ? ', proven' : ', not proven') });
      b.style.setProperty('--chip', statusColor(impl.proven ? 'pass' : impl.status === 'done' ? 'not-run' : 'pending'));
      if (typeof openPhase === 'function') b.addEventListener('click', () => openPhase(String(impl.phase)));
      chips.appendChild(b);
    }
    links.appendChild(el('span', { cls: 'deps-label', text: 'Implemented by' }));
    links.appendChild(chips);
  }
  if ((c.problems || []).length) {
    links.appendChild(el('span', { cls: 'deps-label', text: 'Problems' }));
    links.appendChild(el('div', { cls: 'warnbox', text: c.problems.map((p) => (typeof p === 'string' ? p : p.detail || JSON.stringify(p))).join('; ') }));
  }
  const list = document.getElementById('sec-statements');
  clear(list);
  for (const item of c.items || []) {
    const word = item.completed ? 'Done' : 'Open';
    list.appendChild(el('li', {}, el('span', { cls: 'badge ' + word.toLowerCase(), text: word }), richText(item.title || '')));
  }
  renderControlTable();
  if (focus !== false) document.getElementById('h-sec-control').focus();
}

// renderScans shows the pinned scanner versions and the newest scheduled scans.
function renderScans(doc) {
  const pins = doc.pins || {};
  const dl = document.getElementById('sec-pins');
  clear(dl);
  tile(dl, 'gosec', pins.gosec || '-', 'Makefile pin');
  tile(dl, 'govulncheck', pins.govulncheck || '-', 'Makefile pin');
  tile(dl, 'Go', pins.go || '-', 'go.mod language version');
  tile(dl, 'Toolchain', pins.toolchain || '-', 'go.mod toolchain');
  const table = document.getElementById('sec-scans');
  clear(table);
  table.appendChild(el('thead', {}, el('tr', {}, el('th', { scope: 'col', text: 'Scheduled job' }), el('th', { scope: 'col', text: 'Result' }),
    el('th', { scope: 'col', text: 'Branch' }), el('th', { scope: 'col', text: 'Finished' }))));
  const body = el('tbody');
  for (const [name, what] of [['vulns', 'govulncheck against the advisory database'], ['nightly', 'the strict full test suite']]) {
    const j = (doc.scans || {})[name];
    const url = j && safeUrl(j.url);
    body.appendChild(el('tr', {}, el('th', { scope: 'row' }, url ? el('a', { href: url, rel: 'noreferrer', target: '_blank', text: name }) : el('span', { text: name }),
      el('div', { cls: 'note', text: what })),
      el('td', {}, j ? chip(j.failed ? 'fail' : 'pass', j.conclusion) : chip('pending', 'no run recorded')),
      el('td', { text: j ? j.branch : '' }), el('td', { text: j ? fmtTime(j.end) : '' })));
  }
  table.appendChild(body);
}

// renderFrameworks lists each framework the controls reference, and how far each is attested.
function renderFrameworks(att) {
  const table = document.getElementById('sec-frameworks');
  clear(table);
  table.appendChild(el('thead', {}, el('tr', {}, el('th', { scope: 'col', text: 'Framework' }), el('th', { scope: 'col', cls: 'num', text: 'Referenced' }),
    el('th', { scope: 'col', cls: 'num', text: 'Drafted' }), el('th', { scope: 'col', cls: 'num', text: 'Attested' }), el('th', { scope: 'col', cls: 'num', text: 'Not yet' }))));
  const body = el('tbody');
  for (const f of (att && att.frameworks) || []) {
    body.appendChild(el('tr', {}, el('th', { scope: 'row', text: f.framework }), el('td', { cls: 'num', text: f.referenced }),
      el('td', { cls: 'num', text: f.drafted }), el('td', { cls: 'num', text: f.attested }), el('td', { cls: 'num', text: f.not_yet })));
  }
  table.appendChild(body);
}

// renderWaivers lists every gosec waiver and skip, stale ones first.
function renderWaivers(doc) {
  const all = (doc.waivers || []).concat((doc.skips || []).map((x) => Object.assign({}, x, { rule: 'skip' })));
  all.sort((a, b) => (b.stale ? 1 : 0) - (a.stale ? 1 : 0));
  const stale = all.filter((w) => w.stale).length;
  document.getElementById('sec-waivers-note').textContent = (doc.waivers || []).length + ' waiver(s), ' + (doc.skips || []).length +
    ' skip(s); ' + (stale ? stale + ' no longer match the tree' : 'every one still matches the tree');
  const table = document.getElementById('sec-waivers');
  clear(table);
  table.appendChild(el('thead', {}, el('tr', {}, el('th', { scope: 'col', text: 'Rule' }), el('th', { scope: 'col', text: 'Where' }),
    el('th', { scope: 'col', text: 'Why it is waived' }))));
  const body = el('tbody');
  for (const w of all) {
    const why = el('td', { style: 'white-space: normal; max-width: 80ch' });
    if (w.stale) why.appendChild(el('div', { cls: 'failed-job', text: 'Stale: ' + w.stale }));
    if (w.reason && w.reason.length > w.short.length) {
      why.appendChild(el('details', {}, el('summary', { text: w.short }), richText(w.reason)));
    } else {
      why.appendChild(el('span', { text: w.reason || 'no reason written' }));
    }
    body.appendChild(el('tr', {}, el('th', { scope: 'row', text: w.rule || '' }), el('td', { cls: 'mono', text: w.file + (w.line ? ':' + w.line : '') }), why));
  }
  table.appendChild(body);
}

// renderUnreleased lists the security changelog fragments waiting for a release.
function renderUnreleased(doc) {
  const list = document.getElementById('sec-unreleased');
  clear(list);
  for (const f of doc.unreleased || []) {
    list.appendChild(el('li', {}, el('span', { cls: 'badge', text: f.added ? new Date(f.added * 1000).toLocaleDateString([], { month: 'short', day: 'numeric' }) : 'new' }),
      el('div', {}, el('div', { cls: 'mono muted', text: f.name }), richText(f.summary))));
  }
  if (!list.firstChild) list.appendChild(el('li', { text: 'None: every security change has shipped.' }));
}

// renderSecurityTrend charts attested controls and pending items over the recorded history.
function renderSecurityTrend() {
  const rows = securityTrend.map((l) => ({ t: l.t, attested: ((l.data || {}).attestation || {}).attested, pending: (l.data || {}).pending }));
  document.getElementById('sec-trend-note').textContent = rows.length ? rows.length + ' recorded change(s) since ' + fmtTime(rows[0].t)
    : 'recorded each time a total changes';
  const c = chart('sec-trend');
  if (!c || !rows.length || document.getElementById('view-security').hidden) return;
  c.resize();
  c.setOption({
    animation: false,
    grid: { left: 48, right: 24, top: 28, bottom: 32 },
    tooltip: tooltipStyle({ trigger: 'axis' }),
    legend: { top: 0, textStyle: { color: css('--text-secondary') } },
    xAxis: timeAxis('sec-trend', rows[0].t * 1000 - 3600000, Math.max(Date.now(), rows[rows.length - 1].t * 1000)),
    yAxis: Object.assign({ type: 'value', min: 0 }, axisStyle()),
    series: [
      { name: 'Controls attested', type: 'line', step: 'end', symbol: 'circle', symbolSize: 6, data: rows.map((r) => [r.t * 1000, r.attested]), color: css('--st-pass') },
      { name: 'Pending', type: 'line', step: 'end', symbol: 'triangle', symbolSize: 7, data: rows.map((r) => [r.t * 1000, r.pending]), color: css('--st-not-run') },
    ],
  });
}

// pollSecurity rereads the model.
async function pollSecurity() {
  securityDoc = await getJson('/security.json');
  renderSecurity();
}

// pollSecurityTrend rereads the recorded totals.
async function pollSecurityTrend() {
  try {
    const res = await fetch('/timeline?kind=security.summary&limit=5000', { cache: 'no-store' });
    const text = res.ok ? await res.text() : '';
    securityTrend = text.split('\n').filter(Boolean).map((l) => { try { return JSON.parse(l); } catch (e) { return null; } }).filter(Boolean);
  } catch (e) { securityTrend = []; }
  renderSecurityTrend();
}

document.addEventListener('DOMContentLoaded', () => {
  document.getElementById('sec-state').addEventListener('change', renderControlTable);
  document.getElementById('sec-search').addEventListener('input', renderControlTable);
  pollSecurity();
  pollSecurityTrend();
  setInterval(pollSecurity, 60000);
  setInterval(pollSecurityTrend, 60000);
});
