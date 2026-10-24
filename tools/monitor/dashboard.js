// dashboard.js draws the pr-check monitor's page from monitor.py's JSON.
//
// It polls /state every two seconds (the live run, the Actions poll and
// memory), /history every twenty and /actions-history every sixty. Every
// string from a log or an API is placed with textContent, and chart tooltips
// render as canvas rich text (renderMode "richText"), so no third-party text
// is ever parsed as HTML.
'use strict';

// STATUSES lists each stage status with the CSS color token that draws it.
const STATUSES = ['pending', 'running', 'pass', 'fail', 'not-run', 'ended', 'skipped', 'stopped'];
// LABELS is the word shown beside each status color.
const LABELS = {
  pending: 'pending', running: 'running', pass: 'pass', fail: 'fail', 'not-run': 'not run',
  ended: 'ended (verdict not known yet)', skipped: 'not in this run', stopped: 'stopped',
};

const charts = {};
let liveState = null;
let viewId = 'live';
let viewRun = null;
let historyRuns = [];
let actionsHistory = [];
let lastVerdict = null;

// css returns the current value of a CSS custom property.
function css(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

// statusColor returns the color token of a status.
function statusColor(status) {
  return css('--st-' + (STATUSES.includes(status) ? status : 'pending'));
}

// el builds an element: props.text becomes textContent, props.cls the class,
// any other prop an attribute; children are nodes or strings (as text).
function el(tag, props, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value === undefined || value === null) continue;
    if (key === 'text') node.textContent = String(value);
    else if (key === 'cls') node.className = value;
    else node.setAttribute(key, String(value));
  }
  for (const child of children) {
    if (child === null || child === undefined) continue;
    node.appendChild(typeof child === 'string' ? document.createTextNode(child) : child);
  }
  return node;
}

// clear removes every child of node.
function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
}

// plain makes text safe for ECharts rich text, whose only markup is {style|text}.
function plain(text) {
  return String(text === undefined || text === null ? '' : text).replace(/[{}|]/g, ' ');
}

// chip returns a status chip: a colored dot plus the status in words.
function chip(status, words) {
  const node = el('span', { cls: 'chip', text: words || LABELS[status] || status || 'unknown' });
  node.style.setProperty('--chip', statusColor(status));
  return node;
}

// fmtDur formats seconds as 1h02m, 3m05s or 12s.
function fmtDur(sec) {
  if (sec === null || sec === undefined || isNaN(sec)) return '';
  sec = Math.round(sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  if (h) return h + 'h' + String(m).padStart(2, '0') + 'm';
  if (m) return m + 'm' + String(s).padStart(2, '0') + 's';
  return s + 's';
}

// fmtTime formats epoch seconds as local time, with the date when not today.
function fmtTime(sec) {
  if (!sec) return '';
  const d = new Date(sec * 1000);
  const today = new Date().toDateString() === d.toDateString();
  return today ? d.toLocaleTimeString() : d.toLocaleString();
}

// safeUrl returns url only when it points at github.com over https.
function safeUrl(url) {
  return typeof url === 'string' && url.startsWith('https://github.com/') ? url : null;
}

// chart returns the ECharts instance drawing into the element with id, or
// null when ECharts did not load (the tables still work without it).
function chart(id) {
  if (!window.echarts) return null;
  if (!charts[id]) charts[id] = echarts.init(document.getElementById(id), null, { renderer: 'canvas' });
  return charts[id];
}

// axisStyle returns the shared axis colors: recessive grid and muted labels.
function axisStyle() {
  return {
    axisLine: { lineStyle: { color: css('--border') } },
    axisTick: { lineStyle: { color: css('--border') } },
    axisLabel: { color: css('--text-secondary') },
    splitLine: { lineStyle: { color: css('--grid') } },
  };
}

// tooltipStyle returns the shared tooltip settings (canvas rich text).
function tooltipStyle(extra) {
  return Object.assign({
    renderMode: 'richText',
    backgroundColor: css('--surface-1'),
    borderColor: css('--border'),
    textStyle: { color: css('--text-primary') },
  }, extra || {});
}

// drawGantt draws rows ({label, status, start, end, detail, source}) as a
// timeline into the element with id, open bars running to now.
function drawGantt(id, rows, now) {
  const node = document.getElementById(id);
  node.style.height = Math.max(160, 26 * rows.length + 60) + 'px';
  const c = chart(id);
  if (!c) return;
  c.resize();
  const cats = rows.map((r) => r.label + '  ' + (LABELS[r.status] || r.status).split(' ')[0]);
  const data = [];
  let min = Infinity, max = -Infinity;
  rows.forEach((r, i) => {
    if (r.start === null || r.start === undefined) return;
    const end = r.end === null || r.end === undefined ? now : r.end;
    min = Math.min(min, r.start);
    max = Math.max(max, end);
    data.push({
      value: [i, r.start * 1000, end * 1000],
      itemStyle: { color: statusColor(r.status), borderColor: r.source === 'record' ? css('--text-secondary') : null, borderWidth: r.source === 'record' ? 1 : 0 },
      row: r,
    });
  });
  if (!isFinite(min)) { min = now - 60; max = now; }
  c.setOption({
    animation: false,
    grid: { left: 8, right: 24, top: 8, bottom: 28, containLabel: true },
    xAxis: Object.assign({ type: 'time', min: min * 1000, max: Math.max(max, min + 1) * 1000 }, axisStyle()),
    yAxis: Object.assign({ type: 'category', data: cats, inverse: true }, axisStyle(), { splitLine: { show: false } }),
    tooltip: tooltipStyle({
      formatter: (p) => {
        const r = p.data.row;
        const end = r.end === null || r.end === undefined ? null : r.end;
        return [
          plain(r.label) + ': ' + (LABELS[r.status] || r.status),
          'start ' + fmtTime(r.start) + (end ? ', end ' + fmtTime(end) : ', still open'),
          'length ' + fmtDur((end || now) - r.start) + (r.source === 'record' ? ' (from its record)' : ' (as the monitor saw it)'),
          plain(r.detail || ''),
        ].filter(Boolean).join('\n');
      },
    }),
    series: [{
      type: 'custom',
      encode: { x: [1, 2], y: 0 },
      data: data,
      renderItem: (params, api) => {
        const y = api.value(0);
        const s = api.coord([api.value(1), y]);
        const e = api.coord([api.value(2), y]);
        const h = api.size([0, 1])[1] * 0.62;
        const rect = echarts.graphic.clipRectByRect(
          { x: s[0], y: s[1] - h / 2, width: Math.max(e[0] - s[0], 4), height: h },
          { x: params.coordSys.x, y: params.coordSys.y, width: params.coordSys.width, height: params.coordSys.height });
        return rect && { type: 'rect', shape: Object.assign(rect, { r: 3 }), style: api.style() };
      },
    }],
  }, true);
}

// drawLegend writes the status legend: a swatch and a word for each status.
function drawLegend() {
  const node = document.getElementById('stage-legend');
  clear(node);
  for (const s of STATUSES) {
    const sw = el('span', { cls: 'swatch' });
    sw.style.background = statusColor(s);
    node.appendChild(el('span', {}, sw, LABELS[s]));
  }
  node.appendChild(el('span', { text: 'Outlined bars take their times from pr-check\'s records.' }));
}

// renderRun draws a run (live or saved) into the stage, pass, leg and memory views.
function renderRun(run, now, memory, floor) {
  const verdict = document.getElementById('verdict');
  const meta = document.getElementById('meta');
  const notes = document.getElementById('notes');
  clear(notes);
  if (!run) {
    verdict.textContent = 'no run';
    verdict.style.setProperty('--chip', statusColor('pending'));
    meta.textContent = 'Nothing is running under this monitor. Pick a saved run to view it.';
    drawGantt('gantt', [], now);
    clear(document.getElementById('progress'));
    clear(document.getElementById('legs'));
    clear(document.getElementById('stage-table'));
  } else {
    const v = run.verdict || 'running';
    verdict.textContent = v.toUpperCase() + (run.exit_code !== null && run.exit_code !== undefined ? ' (exit ' + run.exit_code + ')' : '');
    verdict.style.setProperty('--chip', statusColor(v === 'pass' ? 'pass' : v === 'fail' ? 'fail' : v === 'stopped' ? 'stopped' : 'running'));
    const begun = run.started || run.created;
    const parts = [];
    if (run.commit) parts.push('commit ' + run.commit);
    if (run.base) parts.push('against ' + run.base);
    if (begun) parts.push('began ' + fmtTime(begun));
    if (begun) parts.push((run.ended ? 'took ' : 'running for ') + fmtDur((run.ended || now) - begun));
    parts.push(run.trace ? 'stage starts from make --trace' : 'no --trace lines: static checks inferred from their commands');
    meta.textContent = parts.join(' | ');
    const allNotes = (run.notes || []).slice();
    allNotes.push('Times are when the monitor read each line; the log has no timestamps of its own.');
    if (run.attached_late) allNotes.push('Attached late: lines already in the log all show the time they were read.');
    for (const n of allNotes) notes.appendChild(el('p', { cls: 'note', text: n }));
    const stages = run.stages || [];
    drawGantt('gantt', stages.map((s) => ({ label: s.name, status: s.status, start: s.start, end: s.end, detail: s.detail, source: s.source })), now);
    renderStageTable(stages, now);
    renderProgress(stages, now);
    renderLegs(stages, now);
  }
  renderMemory(memory || (run && run.memory) || [], floor);
}

// renderStageTable fills the stage table, the timeline's text twin.
function renderStageTable(stages, now) {
  const table = document.getElementById('stage-table');
  clear(table);
  table.appendChild(el('tr', {}, el('th', { text: 'Stage' }), el('th', { text: 'Status' }), el('th', { text: 'Start' }),
    el('th', { cls: 'num', text: 'Length' }), el('th', { text: 'Times from' }), el('th', { text: 'Detail' })));
  for (const s of stages) {
    table.appendChild(el('tr', {},
      el('td', { text: s.name }), el('td', {}, chip(s.status)), el('td', { text: fmtTime(s.start) }),
      el('td', { cls: 'num', text: s.start ? fmtDur((s.end || now) - s.start) : '' }),
      el('td', { text: s.start ? (s.source === 'record' ? 'record' : 'log') : '' }),
      el('td', { text: s.detail || '' })));
  }
}

// typical returns the median length of stage name over saved runs, or null.
function typical(name) {
  const xs = historyRuns.map((r) => (r.stages || {})[name]).filter((x) => typeof x === 'number').sort((p, q) => p - q);
  if (!xs.length) return null;
  return { median: xs[Math.floor(xs.length / 2)], runs: xs.length };
}

// timing describes a stage's elapsed time and, when history knows it, how
// long the stage usually takes.
function timing(s, now) {
  if (!s.start) return '';
  let text = (s.status === 'running' ? 'running ' : 'took ') + fmtDur((s.end || now) - s.start);
  const t = typical(s.name);
  if (t) text += '; usually about ' + fmtDur(t.median) + ' (median of ' + t.runs + ' saved run' + (t.runs > 1 ? 's' : '') + ')';
  return text;
}

// bar returns a progress bar: determinate when total is known, otherwise
// indeterminate (animated) while running and full once ended.
function bar(label, s, done, total) {
  if (total) return el('progress', { max: total, value: Math.min(done, total), 'aria-label': label });
  if (s.status === 'running') return el('progress', { 'aria-label': label + ' (length not known)' });
  if (s.status === 'pending') return el('progress', { max: 1, value: 0, 'aria-label': label });
  return el('progress', { max: 1, value: 1, 'aria-label': label });
}

// progressRow adds one labeled bar with its status and words.
function progressRow(parent, name, s, done, total, words) {
  parent.appendChild(el('div', { cls: 'row' }, el('strong', { text: name }), bar(name, s, done, total)));
  parent.appendChild(el('div', { cls: 'row' }, el('span', {}, chip(s.status)), el('span', { cls: 'note', text: words })));
}

// renderProgress shows a bar for every stage whose progress can be known,
// and an indeterminate one with elapsed and usual time for the rest.
function renderProgress(stages, now) {
  const node = document.getElementById('progress');
  clear(node);
  const statics = stages.filter((x) => x.group === 'static');
  const finished = statics.filter((x) => !['pending', 'running'].includes(x.status)).length;
  const running = statics.find((x) => x.status === 'running');
  const overall = { name: 'static', status: running ? 'running' : finished === statics.length ? 'pass' : 'pending',
    start: statics.length ? statics[0].start : null, end: null };
  if (statics.some((x) => x.status === 'fail')) overall.status = 'fail';
  progressRow(node, 'static checks', overall, finished, statics.length,
    finished + ' of ' + statics.length + ' make targets done' + (running ? '; now ' + running.name + ', ' + timing(running, now) : ''));
  const chips = el('div', { cls: 'chips' });
  for (const s of statics) chips.appendChild(chip(s.status, s.name));
  node.appendChild(chips);
  for (const s of stages.filter((x) => x.group !== 'static' && x.group !== 'leg')) {
    if (s.status === 'skipped') continue;
    if (s.group === 'pass') {
      const total = s.total || 0;
      const done = s.status === 'pass' ? total : Math.min(s.done || 0, total || s.done || 0);
      let words = total ? (s.done || 0) + ' of ' + total + ' packages reported' : 'package count not known yet';
      if (s.failed) words += ', ' + s.failed + ' failed';
      if (s.status === 'pass' && (s.done || 0) < total) words += ' (the rest print nothing: no tests)';
      progressRow(node, s.name, s, done, total, words + (s.start ? '; ' + timing(s, now) : ''));
    } else {
      progressRow(node, s.name, s, 0, 0, s.start ? timing(s, now) : (s.status === 'pending' ? 'not started' : ''));
    }
  }
}

// renderLegs shows each OS leg: its step bar, status, elapsed time,
// packages and latest line.
function renderLegs(stages, now) {
  const node = document.getElementById('legs');
  clear(node);
  for (const s of stages.filter((x) => x.group === 'leg')) {
    const steps = s.steps || [];
    const next = s.step < steps.length ? steps[s.step] : null;
    let words = steps.length ? 'step ' + s.step + ' of ' + steps.length + (next && s.status === 'running' ? ', now: ' + next : '') : '';
    if (s.total) words += (words ? '; ' : '') + (s.done || 0) + ' of ' + s.total + ' packages' + (s.failed ? ', ' + s.failed + ' failed' : '');
    const stepBar = steps.length ? el('progress', { max: steps.length, value: s.step || 0, 'aria-label': s.name + ' steps done' }) : bar(s.name, s, 0, 0);
    node.appendChild(el('div', { cls: 'leg' },
      el('div', {}, el('strong', { text: s.name + ' ' }), chip(s.status), el('span', { cls: 'note', text: '  ' + timing(s, now) })),
      stepBar,
      el('div', { cls: 'note', text: words + (steps.length ? ' (' + steps.join(', ') + ')' : '') }),
      el('div', { cls: 'note', text: s.detail || '' }),
      el('code', { text: s.last_line || 'no line yet' })));
  }
}

// renderMemory draws MemAvailable with the watchdog's floor as a dashed line.
function renderMemory(samples, floor) {
  const last = samples.length ? samples[samples.length - 1][1] : null;
  document.getElementById('memory-note').textContent = last === null
    ? 'No sample yet.'
    : 'Now ' + last + ' MB available. The watchdog stops a run it started below ' + floor + ' MB.';
  const c = chart('memory');
  if (!c) return;
  c.setOption({
    animation: false,
    grid: { left: 8, right: 24, top: 16, bottom: 28, containLabel: true },
    xAxis: Object.assign({ type: 'time' }, axisStyle()),
    yAxis: Object.assign({ type: 'value', min: 0, name: 'MB', nameTextStyle: { color: css('--text-secondary') } }, axisStyle()),
    tooltip: tooltipStyle({ trigger: 'axis', formatter: (p) => fmtTime(p[0].value[0] / 1000) + '\n' + p[0].value[1] + ' MB available' }),
    series: [{
      type: 'line', showSymbol: false, lineStyle: { width: 2, color: css('--series-1') }, itemStyle: { color: css('--series-1') },
      data: samples.map((s) => [s[0] * 1000, s[1]]),
      markLine: { symbol: 'none', silent: true, lineStyle: { type: 'dashed', color: css('--st-fail') },
        label: { color: css('--text-secondary'), formatter: 'watchdog ' + floor + ' MB' }, data: [{ yAxis: floor }] },
    }],
  }, true);
}

// jobStatus maps an Actions status and conclusion to a stage status.
function jobStatus(status, conclusion) {
  if (status !== 'completed') return status === 'in_progress' ? 'running' : 'pending';
  return {
    success: 'pass', failure: 'fail', timed_out: 'fail', startup_failure: 'fail',
    cancelled: 'stopped', skipped: 'skipped', neutral: 'ended', action_required: 'not-run',
  }[conclusion] || 'ended';
}

// renderActions draws the Actions panel from the latest poll.
function renderActions(acts, now) {
  const status = document.getElementById('actions-status');
  const runs = (acts && acts.runs) || [];
  if (acts && acts.error) {
    status.textContent = 'Actions unavailable: ' + acts.error + '. Everything else keeps working.';
  } else if (acts && acts.polled) {
    status.textContent = 'Branch ' + (acts.branch || '?') + ', ' + runs.length + ' run(s), read ' + fmtTime(acts.polled) + ' (every 60s).';
  } else {
    status.textContent = 'Reading Actions...';
  }
  const pick = document.getElementById('actions-run');
  const keep = pick.value;
  clear(pick);
  for (const r of runs) {
    pick.appendChild(el('option', { value: r.id, text: '#' + r.number + ' ' + r.name + ' (' + (r.conclusion || r.status) + ') ' + r.sha.slice(0, 8) }));
  }
  if (runs.some((r) => String(r.id) === keep)) pick.value = keep;
  const run = runs.find((r) => String(r.id) === pick.value) || runs[0];
  const failed = document.getElementById('actions-failed');
  clear(failed);
  if (run) {
    const jobs = run.jobs || [];
    drawGantt('actions-gantt', jobs.map((j) => ({
      label: (j.failed ? 'FAILED ' : '') + j.name, status: jobStatus(j.status, j.conclusion),
      start: j.start, end: j.end, detail: j.conclusion || j.status, source: 'seen',
    })), now);
    const bad = jobs.filter((j) => j.failed);
    if (bad.length) {
      failed.appendChild(el('span', { cls: 'failed-job', text: 'Failed: ' }));
      bad.forEach((j, i) => {
        const url = safeUrl(j.url);
        failed.appendChild(url ? el('a', { href: url, rel: 'noreferrer', target: '_blank', cls: 'failed-job', text: j.name })
          : el('span', { cls: 'failed-job', text: j.name }));
        if (i < bad.length - 1) failed.appendChild(document.createTextNode(', '));
      });
    } else if (run.jobs_error) {
      failed.textContent = 'Jobs unavailable: ' + run.jobs_error;
    }
  } else {
    drawGantt('actions-gantt', [], now);
  }
  renderJobsTable(run ? run.jobs || [] : [], now);
  renderActionsTable(runs, now);
}

// renderJobsTable lists the selected run's jobs, each with its steps bar.
function renderJobsTable(jobs, now) {
  const table = document.getElementById('actions-jobs');
  clear(table);
  table.appendChild(el('tr', {}, el('th', { text: 'Job' }), el('th', { text: 'Result' }), el('th', { text: 'Steps' }),
    el('th', { cls: 'num', text: 'Length' })));
  for (const j of jobs) {
    const st = jobStatus(j.status, j.conclusion);
    const steps = j.steps_total
      ? el('progress', { max: j.steps_total, value: j.steps_done || 0, 'aria-label': j.name + ' steps done' })
      : bar(j.name, { status: st }, 0, 0);
    table.appendChild(el('tr', {}, el('td', { cls: j.failed ? 'failed-job' : null, text: j.name }),
      el('td', {}, chip(st, j.conclusion || j.status)),
      el('td', {}, steps, el('span', { cls: 'note', text: j.steps_total ? ' ' + (j.steps_done || 0) + ' of ' + j.steps_total : '' })),
      el('td', { cls: 'num', text: j.start ? fmtDur((j.end || now) - j.start) : '' })));
  }
}

// jobsBar shows a run's jobs completed of total.
function jobsBar(r) {
  const jobs = r.jobs || [];
  const done = jobs.filter((j) => j.status === 'completed').length;
  if (!jobs.length) return el('span', { cls: 'note', text: r.jobs_error || 'jobs not read' });
  return el('span', {}, el('progress', { max: jobs.length, value: done, 'aria-label': 'jobs completed' }),
    el('span', { cls: 'note', text: ' ' + done + ' of ' + jobs.length }));
}

// renderActionsTable lists the branch's newest runs.
function renderActionsTable(runs, now) {
  const table = document.getElementById('actions-runs');
  clear(table);
  table.appendChild(el('tr', {}, el('th', { text: 'Run' }), el('th', { text: 'Workflow' }), el('th', { text: 'Event' }),
    el('th', { text: 'Result' }), el('th', { text: 'Jobs done' }), el('th', { text: 'Commit' }), el('th', { text: 'Started' }),
    el('th', { cls: 'num', text: 'Length' })));
  for (const r of runs) {
    const url = safeUrl(r.url);
    const name = url ? el('a', { href: url, rel: 'noreferrer', target: '_blank', text: '#' + r.number }) : el('span', { text: '#' + r.number });
    table.appendChild(el('tr', {}, el('td', {}, name), el('td', { text: r.name }), el('td', { text: r.event }),
      el('td', {}, chip(jobStatus(r.status, r.conclusion), r.conclusion || r.status)),
      el('td', {}, jobsBar(r)),
      el('td', { text: r.sha.slice(0, 8) }),
      el('td', { text: fmtTime(r.start) }), el('td', { cls: 'num', text: r.start ? fmtDur((r.end || now) - r.start) : '' })));
  }
}

// renderHistory fills the saved-runs table and the view picker.
function renderHistory() {
  const table = document.getElementById('history');
  clear(table);
  table.appendChild(el('tr', {}, el('th', { text: 'Began' }), el('th', { text: 'Commit' }), el('th', { text: 'Verdict' }),
    el('th', { cls: 'num', text: 'Length' }), el('th', { text: 'How' }), el('th', { text: '' })));
  for (const r of historyRuns) {
    const btn = el('button', { type: 'button', text: 'View', 'aria-label': 'View the run that began ' + fmtTime(r.started) });
    btn.addEventListener('click', () => { selectView(r.id); window.scrollTo(0, 0); });
    const v = r.verdict || 'unknown';
    table.appendChild(el('tr', {}, el('td', { text: fmtTime(r.started) }), el('td', { text: r.commit || '' }),
      el('td', {}, chip(v === 'pass' ? 'pass' : v === 'fail' ? 'fail' : v === 'stopped' ? 'stopped' : 'ended', v)),
      el('td', { cls: 'num', text: fmtDur(r.seconds) }), el('td', { text: (r.mode || '') + (r.stopped_by ? ', stopped by ' + r.stopped_by : '') }),
      el('td', {}, btn)));
  }
  if (!historyRuns.length) table.appendChild(el('tr', {}, el('td', { colspan: 6, text: 'No saved runs yet.' })));
  const view = document.getElementById('view');
  const keep = viewId;
  clear(view);
  view.appendChild(el('option', { value: 'live', text: liveState && liveState.run ? 'Live (' + liveState.mode + ')' : 'Live (no run)' }));
  for (const r of historyRuns) view.appendChild(el('option', { value: r.id, text: fmtTime(r.started) + ' ' + (r.verdict || '') }));
  view.value = keep;
  drawDurationChart('history-chart', 'stage-pick', historyRuns.slice().reverse().map((r) => ({
    label: fmtTime(r.started), total: r.seconds, parts: r.stages || {},
  })));
}

// drawDurationChart charts one chosen part's duration over runs (oldest
// first); the picker's choices are every part any run has.
function drawDurationChart(chartId, pickId, runs) {
  const pick = document.getElementById(pickId);
  const keep = pick.value || '(whole run)';
  const names = new Set();
  for (const r of runs) Object.keys(r.parts).forEach((n) => names.add(n));
  clear(pick);
  pick.appendChild(el('option', { value: '(whole run)', text: 'the whole run' }));
  for (const n of Array.from(names).sort()) pick.appendChild(el('option', { value: n, text: n }));
  pick.value = Array.from(pick.options).some((o) => o.value === keep) ? keep : '(whole run)';
  const choice = pick.value;
  const values = runs.map((r) => (choice === '(whole run)' ? r.total : r.parts[choice]));
  const c = chart(chartId);
  if (!c) return;
  c.setOption({
    animation: false,
    grid: { left: 8, right: 24, top: 16, bottom: 28, containLabel: true },
    xAxis: Object.assign({ type: 'category', data: runs.map((r) => r.label) }, axisStyle()),
    yAxis: Object.assign({ type: 'value', name: 'seconds', min: 0, nameTextStyle: { color: css('--text-secondary') } }, axisStyle()),
    tooltip: tooltipStyle({ trigger: 'axis', formatter: (p) => plain(p[0].name) + '\n' + plain(choice) + ': ' + (p[0].value === null || p[0].value === undefined ? 'not run' : fmtDur(p[0].value)) }),
    series: [{ type: 'line', connectNulls: false, symbolSize: 8, lineStyle: { width: 2, color: css('--series-1') }, itemStyle: { color: css('--series-1') },
      data: values.map((v) => (v === undefined ? null : v)) }],
  }, true);
}

// renderActionsHistory charts Actions job durations over every run seen.
function renderActionsHistory() {
  const runs = actionsHistory.filter((r) => r.status === 'completed').slice().reverse().map((r) => {
    const parts = {};
    for (const j of r.jobs || []) if (j.start && j.end) parts[j.name] = j.end - j.start;
    return { label: '#' + r.number + ' ' + r.name, total: r.start && r.end ? r.end - r.start : null, parts: parts };
  });
  drawDurationChart('actions-chart', 'job-pick', runs);
}

// selectView switches between the live run and a saved one.
async function selectView(id) {
  viewId = id;
  document.getElementById('view').value = id;
  if (id === 'live') { viewRun = null; draw(); return; }
  try {
    const res = await fetch('/history/' + encodeURIComponent(id));
    viewRun = res.ok ? await res.json() : null;
  } catch (e) { viewRun = null; }
  draw();
}

// draw redraws the run views from whichever run is selected.
function draw() {
  if (!liveState) return;
  if (viewId === 'live') {
    renderRun(liveState.run, liveState.now, liveState.memory, liveState.memory_floor_mb);
  } else if (viewRun) {
    renderRun(viewRun, viewRun.ended || liveState.now, viewRun.memory, liveState.memory_floor_mb);
  }
  renderActions(liveState.actions, liveState.now);
}

// getJson fetches path as JSON, or returns null.
async function getJson(path) {
  try {
    const res = await fetch(path, { cache: 'no-store' });
    return res.ok ? await res.json() : null;
  } catch (e) { return null; }
}

// pollState refreshes the live state, and history when a run's verdict changes.
async function pollState() {
  const s = await getJson('/state');
  if (s) {
    liveState = s;
    const verdict = s.run ? s.run.verdict : null;
    if (verdict !== lastVerdict) { lastVerdict = verdict; pollHistory(); }
    draw();
  }
  setTimeout(pollState, 2000);
}

// pollHistory refreshes the saved runs.
async function pollHistory() {
  const h = await getJson('/history');
  if (h) { historyRuns = h.runs || []; renderHistory(); }
}

// pollActionsHistory refreshes every Actions run seen.
async function pollActionsHistory() {
  const a = await getJson('/actions-history');
  if (a) { actionsHistory = a.runs || []; renderActionsHistory(); }
}

document.addEventListener('DOMContentLoaded', () => {
  if (typeof echarts === 'undefined') {
    document.getElementById('notes').appendChild(el('p', { cls: 'note',
      text: 'ECharts did not load (no network to cdn.jsdelivr.net, or its hash did not match). Tables still update.' }));
  }
  drawLegend();
  document.getElementById('view').addEventListener('change', (e) => selectView(e.target.value));
  document.getElementById('actions-run').addEventListener('change', draw);
  document.getElementById('stage-pick').addEventListener('change', renderHistory);
  document.getElementById('job-pick').addEventListener('change', renderActionsHistory);
  window.addEventListener('resize', () => Object.values(charts).forEach((c) => c.resize()));
  pollState();
  pollHistory();
  pollActionsHistory();
  setInterval(pollHistory, 20000);
  setInterval(pollActionsHistory, 60000);
});
