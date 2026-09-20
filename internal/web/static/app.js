'use strict';
const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];

const state = {
  obs: [], certs: [], scenarios: [], results: [],
  current: null, draft: null, affected: [],
};

function toast(msg, kind = 'info') {
  const d = document.createElement('div');
  d.className = 'toast ' + kind;
  d.textContent = msg;
  $('#toast').appendChild(d);
  setTimeout(() => d.remove(), 5200);
}

async function api(method, path, body, raw = false) {
  const opts = { method, headers: {} };
  if (method !== 'GET') {
    const rid = (window.__rid || (window.__rid = crypto.randomUUID()));
    opts.headers['X-Request-Id'] = rid + ':' + path + ':' + (window.__ridN = (window.__ridN || 0) + 1);
  }
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (raw) return res;
  let data = null;
  try { data = await res.json(); } catch (_) {}
  if (!res.ok) {
    const e = new Error((data && data.error && data.error.message) || res.statusText);
    e.status = res.status; e.body = data;
    throw e;
  }
  return data;
}

// ---------- tabs ----------
$$('#tabs button').forEach(b => b.onclick = () => {
  $$('#tabs button').forEach(x => x.classList.remove('active'));
  $$('.tab').forEach(x => x.classList.remove('active'));
  b.classList.add('active');
  $('#tab-' + b.dataset.tab).classList.add('active');
  if (b.dataset.tab === 'scn') renderScenarioTab();
  if (b.dataset.tab === 'cmp') renderCompareSelects();
  if (b.dataset.tab === 'events') loadEvents();
});

// ---------- observations ----------
async function loadObs() {
  const d = await api('GET', '/api/observations');
  state.obs = d.observations || [];
  $('#obs-table tbody').innerHTML = (state.obs||[]).map(o =>
    `<tr><td>${o.id}</td><td>${fmt(o.value)}</td><td>${esc(o.unit)}</td><td>${fmt(o.std_uncertainty)}</td><td>${o.distribution}</td><td>${o.nu ?? '∞'}</td></tr>`).join('');
}
$('#obs-form').onsubmit = async (e) => {
  e.preventDefault();
  const f = e.target;
  const body = {
    id: f.id.value.trim() || undefined,
    value: num(f.value), unit: f.unit.value.trim(),
    std_uncertainty: num(f.std_uncertainty), distribution: f.distribution.value,
    nu: f.nu.value === '' ? undefined : num(f.nu),
  };
  try {
    await api('POST', '/api/observations', body);
    toast('观测已保存（原始输入集合）', 'ok');
    f.reset();
    loadObs();
  } catch (err) { showErr(err); }
};

// ---------- certificates ----------
async function loadCerts() {
  const d = await api('GET', '/api/certificates');
  state.certs = d.certificates || [];
  const now = Date.now();
  $('#cert-table tbody').innerHTML = (state.certs||[]).map(c => {
    const expired = new Date(c.valid_until).getTime() < now;
    const revoked = !!c.revoked_at;
    const cls = revoked ? 'expired' : (expired ? 'expired' : 'ok');
    const txt = revoked ? '已注销/被替换' : (expired ? '已到期' : '有效');
    return `<tr><td>${c.id}</td><td>${c.revision}</td><td>${esc(c.valid_from)}<br>→ ${esc(c.valid_until)}</td>
    <td><span class="badge ${cls}">${txt}</span></td></tr>`;
  }).join('');
  const opts = state.certs.map(c => `<option value="${c.id}">${c.id} rev${c.revision}</option>`).join('');
  $('#cert-replace [name=old]').innerHTML = opts;
  $$('#node-form [name=certificate_id]').forEach(s => s.innerHTML = '<option value="">（无）</option>' + opts);
}
$('#cert-form').onsubmit = async (e) => {
  e.preventDefault();
  const f = e.target;
  try {
    await api('POST', '/api/certificates', certBody(f));
    toast('证书已登记', 'ok'); loadCerts();
  } catch (err) { showErr(err); }
};
$('#cert-replace').onsubmit = async (e) => {
  e.preventDefault();
  const f = e.target;
  try {
    const d = await api('POST', '/api/certificates/' + encodeURIComponent(f.old.value) + '/replace', certBody(f));
    state.affected = d.affected;
    renderAffected(d.affected);
    toast('已安装新证书版本，旧证书注销；请选择重算方式', 'info');
    loadCerts();
  } catch (err) { showErr(err); }
};
function certBody(f) {
  return {
    slope: num(f.slope), intercept: num(f.intercept),
    slope_u: num(f.slope_u), intercept_u: num(f.intercept_u),
    slope_intercept_r: num(f.slope_intercept_r), nu: num(f.nu),
    valid_from: f.valid_from.value, valid_until: f.valid_until.value,
  };
}
function renderAffected(list) {
  if (!list.length) { $('#affected-box').classList.add('hidden'); return; }
  $('#affected-box').classList.remove('hidden');
  $('#affected-list').innerHTML = list.map(a =>
    `<label class="inline"><input type="checkbox" class="aff-chk" value="${a.result_id}" ${a.frozen ? 'disabled' : ''}>
     <span>${a.result_id} · 方案 ${a.scenario_id} · <span class="badge ${a.status}">${a.status}</span>
     ${a.frozen ? '<span class="badge frozen">已冻结</span>' : ''}<br><small>${esc(a.reason)}</small></span></label>`).join('');
}
$('#recompute-selected').onclick = async () => {
  const ids = $$('.aff-chk:checked').map(x => x.value);
  if (!ids.length) return toast('请先勾选结果');
  for (const id of ids) {
    try { await api('POST', '/api/results/' + id + '/recompute'); toast(id + ' 已重算', 'ok'); }
    catch (err) { showErr(err); return; }
  }
  refreshAfterRecompute();
};
$('#recompute-batch').onclick = async () => {
  const ids = $$('.aff-chk').filter(x => !x.disabled).map(x => x.value);
  if (!ids.length) return toast('没有可批量重算的草稿结果');
  try {
    const d = await api('POST', '/api/results/recompute-batch', { result_ids: ids });
    toast(`原子批次完成：${d.count} 个结果同时落盘（要么全部，要么零个）`, 'ok');
    refreshAfterRecompute();
  } catch (err) { showErr(err); }
};
async function refreshAfterRecompute() {
  await loadResults(); await renderScenarioTab();
}

// ---------- helpers ----------
function num(el) { return el.value === '' ? 0 : parseFloat(el.value); }
function fmt(x, d = 6) {
  if (x === null || x === undefined || Number.isNaN(x)) return '—';
  if (Math.abs(x) >= 1e6 || (x !== 0 && Math.abs(x) < 1e-4)) return x.toExponential(4);
  return Number(x.toFixed(d)).toString();
}
function esc(s) { return String(s ?? '').replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])); }
function showErr(err) {
  console.error(err);
  const probs = err.body && err.body.problems;
  if (probs && probs.length) {
    toast(err.message + '\n' + probs.map(p => `• [${p.code}] ${p.target}${p.edge ? ' 边 ' + p.edge : ''}: ${p.detail}`).join('\n'), 'err');
  } else {
    toast(`[${err.status || ''}] ${err.message}`, 'err');
  }
}

// ---------- scenario tab ----------
async function loadResults() {
  const d = await api('GET', '/api/results');
  state.results = (d.results || []).slice().sort((a, b) => (a.created_at||'').localeCompare(b.created_at||''));
}

async function renderScenarioTab() {
  await Promise.all([loadObs(), loadCerts(), loadResults()]);
  const d = await api('GET', '/api/scenarios');
  state.scenarios = d.scenarios || [];
  $('#scn-list').innerHTML = (state.scenarios||[]).map(sc =>
    `<div class="scn-item ${state.current === sc.id ? 'active' : ''}" data-id="${sc.id}">
       <b>${esc(sc.name)}</b> <small>${sc.id}</small>
       ${sc.frozen ? '<span class="badge frozen">已冻结</span>' : '<span class="badge ok">草稿</span>'}
     </div>`).join('');
  $$('.scn-item').forEach(el => el.onclick = () => selectScenario(el.dataset.id));
  if (state.current) selectScenario(state.current, true);
}

$('#scn-new').onsubmit = async (e) => {
  e.preventDefault();
  const f = e.target;
  try {
    const d = await api('POST', '/api/scenarios', {
      name: f.name.value, coverage_p: parseFloat(f.coverage_p.value || '0.95'),
      nodes: [], edges: [], groups: [], pairs: [],
    });
    state.current = d.scenario.id;
    renderScenarioTab();
  } catch (err) { showErr(err); }
};

function emptyDraft(sc) {
  return {
    name: sc.name, coverage_p: sc.coverage_p || 0.95,
    nodes: JSON.parse(JSON.stringify(sc.nodes || [])),
    edges: JSON.parse(JSON.stringify(sc.edges || [])),
    groups: JSON.parse(JSON.stringify(sc.groups || [])),
    pairs: JSON.parse(JSON.stringify(sc.pairs || [])),
  };
}

function selectScenario(id, keepDraft) {
  state.current = id;
  const sc = state.scenarios.find(x => x.id === id);
  if (!keepDraft || !state.draft || state.draft.__id !== id) {
    state.draft = emptyDraft(sc);
    state.draft.__id = id;
  }
  $('#scn-head').innerHTML = `
    <h2>${esc(sc.name)} <small>${sc.id}</small>
      ${sc.frozen ? '<span class="badge frozen">已冻结 · 只读，可复制</span>' : '<span class="badge ok">草稿</span>'}
      ${sc.latest_result_id ? `<span class="badge pending">最新结果 ${sc.latest_result_id}</span>` : ''}
    </h2>`;
  $('#scn-editor').classList.toggle('hidden', sc.frozen);
  renderGraph(sc, state.draft);
  renderEditorLists(sc);
  renderLatestResult(sc);
  $$('.scn-item').forEach(el => el.classList.toggle('active', el.dataset.id === id));
}

function renderEditorLists(sc) {
  const d = state.draft;
  $('#node-list').innerHTML = d.nodes.map((n, i) =>
    `<li><span title="${esc(JSON.stringify(n))}">${n.id} · ${n.kind} · ${esc(n.unit)}</span>
     <button data-i="${i}" class="del-node">×</button></li>`).join('');
  $$('.del-node').forEach(b => b.onclick = () => {
    const n = d.nodes[b.dataset.i];
    d.nodes = d.nodes.filter(x => x !== n);
    d.edges = d.edges.filter(e => e.from !== n.id && e.to !== n.id);
    selectScenario(sc.id, true);
  });
  $('#edge-list').innerHTML = d.edges.map((e, i) =>
    `<li><span>${e.from} → ${e.to}${e.role ? ' [' + e.role + ']' : ''}</span>
     <button data-i="${i}" class="del-edge">×</button></li>`).join('');
  $$('.del-edge').forEach(b => b.onclick = () => { d.edges.splice(b.dataset.i, 1); selectScenario(sc.id, true); });
  $('#group-list').innerHTML = d.groups.map((g, i) =>
    `<li><span>${g.id}: ρ=${g.correlation} {${(g.members || []).join(',')}}</span>
     <button data-i="${i}" class="del-group">×</button></li>`).join('');
  $$('.del-group').forEach(b => b.onclick = () => { d.groups.splice(b.dataset.i, 1); selectScenario(sc.id, true); });
}

$('#node-form').onsubmit = (e) => {
  e.preventDefault();
  const f = e.target, sc = state.scenarios.find(x => x.id === state.current);
  const node = { id: f.id.value.trim(), kind: f.kind.value, unit: f.unit.value.trim() };
  if (f.weights.value.trim()) node.weights = f.weights.value.split(',').map(x => parseFloat(x.trim()));
  if (f.reference_leaf.value.trim()) node.reference_leaf = f.reference_leaf.value.trim();
  if (f.certificate_id.value) node.certificate_id = f.certificate_id.value;
  if (state.draft.nodes.some(n => n.id === node.id)) return toast('节点 ID 重复');
  state.draft.nodes.push(node);
  selectScenario(sc.id, true);
};
$('#edge-form').onsubmit = (e) => {
  e.preventDefault();
  const f = e.target, sc = state.scenarios.find(x => x.id === state.current);
  const edge = { from: f.from.value.trim(), to: f.to.value.trim() };
  if (f.role.value) edge.role = f.role.value;
  state.draft.edges.push(edge);
  selectScenario(sc.id, true);
};
$('#group-form').onsubmit = (e) => {
  e.preventDefault();
  const f = e.target, sc = state.scenarios.find(x => x.id === state.current);
  state.draft.groups.push({ id: f.id.value.trim(), correlation: parseFloat(f.correlation.value),
    members: f.members.value.split(',').map(x => x.trim()).filter(Boolean) });
  selectScenario(sc.id, true);
};
$('#pair-form').onsubmit = (e) => {
  e.preventDefault();
  const f = e.target, sc = state.scenarios.find(x => x.id === state.current);
  state.draft.pairs.push({ a: f.a.value.trim(), b: f.b.value.trim(), correlation: parseFloat(f.correlation.value) });
  selectScenario(sc.id, true);
  toast('两两相关已加入本地草稿（随保存提交）');
};

$('#btn-save-scn').onclick = async () => {
  const sc = state.scenarios.find(x => x.id === state.current);
  const d = state.draft;
  try {
    await api('PUT', '/api/scenarios/' + sc.id, {
      name: d.name, nodes: d.nodes, edges: d.edges, groups: d.groups, pairs: d.pairs, coverage_p: d.coverage_p,
    });
    toast('方案已保存（关键校验仍在服务端进行）', 'ok');
    renderScenarioTab();
  } catch (err) { showErr(err); }
};

$('#asof-toggle').onchange = (e) => $('#asof').classList.toggle('hidden', !e.target.checked);

$('#btn-compute').onclick = async () => {
  const sc = state.scenarios.find(x => x.id === state.current);
  const d = state.draft;
  // save first so server sees the same graph, then compute
  try {
    await api('PUT', '/api/scenarios/' + sc.id, {
      name: d.name, nodes: d.nodes, edges: d.edges, groups: d.groups, pairs: d.pairs, coverage_p: d.coverage_p,
    });
  } catch (err) { return showErr(err); }
  const body = { confirmed: false };
  if ($('#asof-toggle').checked && $('#asof').value) body.as_of = $('#asof').value;
  try {
    const r = await api('POST', `/api/scenarios/${sc.id}/compute`, body);
    await loadResults();
    renderResult(r.result_id, r.status);
  } catch (err) { showProblems(err); }
};

$('#btn-copy').onclick = async () => {
  const sc = state.scenarios.find(x => x.id === state.current);
  const name = prompt('新方案名称', sc.name + ' (相关性调整副本)');
  if (!name) return;
  // adjust correlations in UI before copying? server accepts groups/pairs overrides:
  try {
    const d = await api('POST', `/api/scenarios/${sc.id}/copy`, {
      name, groups: state.draft.groups, pairs: state.draft.pairs,
    });
    state.current = d.scenario.id;
    toast('已复制，可自由调整相关性', 'ok');
    renderScenarioTab();
  } catch (err) { showErr(err); }
};

$('#btn-freeze').onclick = async () => {
  const sc = state.scenarios.find(x => x.id === state.current);
  try {
    await api('POST', `/api/scenarios/${sc.id}/freeze`);
    toast('方案与结果已冻结；证书到期后仍可复现', 'ok');
    renderScenarioTab();
  } catch (err) { showErr(err); }
};

function showProblems(err) {
  const ps = err.body && err.body.problems || [];
  let html = `<div class="problem"><b>[${esc(err.body?.error?.code || 'error')}] ${esc(err.message)}</b></div>`;
  html += ps.map(p =>
    `<div class="problem"><b>[${esc(p.code)}]</b> 目标 <code>${esc(p.target)}</code>` +
    (p.edge ? ` 边 <code>${esc(p.edge)}</code>` : '') + ` — ${esc(p.detail)}</div>`).join('');
  $('#result-panel').innerHTML = html;
  toast('服务端拒绝给出数字：共 ' + ps.length + ' 个定位问题', 'err');
}

// ---------- graph layout (layered DAG) ----------
function renderGraph(sc, draft) {
  const svg = $('#graph');
  const obsIds = new Set(state.obs.map(o => o.id));
  const nodes = [...state.obs].filter(o => isUsed(o.id, draft)).map(o => ({ id: o.id, kind: 'leaf', unit: o.unit }));
  draft.nodes.forEach(n => nodes.push({ id: n.id, kind: n.kind, unit: n.unit, cert: n.certificate_id }));
  const byId = Object.fromEntries(nodes.map(n => [n.id, n]));
  const edges = draft.edges.slice();

  // layers by longest path
  const layer = {};
  nodes.forEach(n => layer[n.id] = byId[n.id] && obsIds.has(n.id) ? 0 : -1);
  let guard = 0;
  let changed = true;
  while (changed && guard++ < 1000) {
    changed = false;
    for (const e of edges) {
      if (layer[e.to] < (layer[e.from] ?? -2) + 1) {
        layer[e.to] = (layer[e.from] ?? -2) + 1;
        changed = true;
      }
    }
  }
  const layers = {};
  nodes.forEach(n => { const l = Math.max(0, layer[n.id] || 0); (layers[l] ||= []).push(n); });
  const layerKeys = Object.keys(layers).map(Number).sort((a, b) => a - b);
  const W = 1100, rowH = 92;
  const colW = Math.max(190, (W - 120) / Math.max(1, layerKeys.length - 1));
  const pos = {};
  layerKeys.forEach((lk, li) => {
    const arr = layers[lk];
    arr.forEach((n, i) => {
      const y = 40 + i * rowH;
      pos[n.id] = { x: 60 + li * colW, y };
    });
  });
  const H = Math.max(360, ...layerKeys.map(l => 40 + layers[l].length * rowH + 40));
  svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
  let s = `<defs><marker id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto">
    <path d="M0,0 L0,6 L8,3 z" fill="#7a8294"/></marker></defs>`;
  for (const e of edges) {
    const a = pos[e.from], b = pos[e.to];
    if (!a || !b) {
      s += `<text x="80" y="20" fill="#b00020">悬空边: ${esc(e.from)} → ${esc(e.to)}</text>`;
      continue;
    }
    const x1 = a.x + 120, y1 = a.y + 18, x2 = b.x, y2 = b.y + 18;
    s += `<path class="edge-line" d="M${x1},${y1} C${(x1+x2)/2},${y1} ${(x1+x2)/2},${y2} ${x2-6},${y2}"/>`;
    if (e.role) s += `<text class="edge-label" x="${(x1+x2)/2}" y="${(y1+y2)/2 - 4}">${esc(e.role)}</text>`;
  }
  for (const n of nodes) {
    const p = pos[n.id];
    if (!p) continue;
    const cls = `node-rect ${n.kind}${sc.frozen ? ' frozen' : ''}`;
    s += `<g><rect class="${cls}" x="${p.x}" y="${p.y}" width="120" height="40" rx="6"/>
      <text class="node-text" x="${p.x + 8}" y="${p.y + 17}"><tspan font-weight="700">${esc(n.id)}</tspan></text>
      <text class="node-text" x="${p.x + 8}" y="${p.y + 32}">${esc(n.kind)} · ${esc(n.unit || '')}</text></g>`;
  }
  svg.innerHTML = s;
}

function isUsed(id, draft) {
  return draft.edges.some(e => e.from === id) || draft.nodes.some(n => n.reference_leaf === id);
}

// ---------- result rendering ----------
async function renderLatestResult(sc) {
  if (!sc.latest_result_id) { $('#result-panel').innerHTML = '<p class="kv">尚无结果，编辑后点击“计算”。</p>'; return; }
  renderResult(sc.latest_result_id);
}

async function renderResult(id, statusHint) {
  try {
    const d = await api('GET', '/api/results/' + id);
    const r = d.result, out = r.output;
    if (r.status === 'pending') {
      const nodes = Object.values(out.results).filter(n => n.pending_expired_certificates?.length || n.revoked_certs?.length);
      $('#result-panel').innerHTML = `<div class="pending-box">
        <b>状态：等待确认（waiting_confirmation）</b>
        <div>结果 ${r.id} 依赖的证书已到期/注销，系统不给出覆盖区间。</div>
        <ul>${nodes.map(n => `<li>${n.id}: 过期证书 ${(n.pending_expired_certificates||[]).join(',')} ${(n.revoked_certs||[]).length ? '注销 '+(n.revoked_certificates||[]).join(',') : ''}</li>`).join('')}</ul>
        <button class="primary" id="btn-confirm">我确认在知情情况下使用过期证书，重算并出具数字</button>
      </div>`;
      $('#btn-confirm').onclick = async () => {
        try {
          const c = await api('POST', '/api/results/' + r.id + '/confirm');
          toast('已确认并重算', 'ok');
          renderResult(c.result.id);
        } catch (err) { showErr(err); }
      };
      return;
    }
    const nodeIds = out.order.filter(id => out.results[id].kind !== 'leaf');
    let html = `<h2>结果 ${r.id} <span class="badge ok">${r.status}</span></h2>`;
    for (const id of nodeIds) {
      const n = out.results[id];
      html += `<div class="card"><h3>${esc(id)} <small>(${n.kind}, ${esc(n.unit)})</small></h3>
        <div class="kv">
          <b>值 y</b>${fmt(n.display_value)} ${esc(n.unit)}<br>
          <b>合成标准 u_c</b>${fmt(n.display_u)}<br>
          <b>有效自由度 ν_eff</b>(n.nu_eff == null ? '∞' : fmt(n.nu_eff, 3))<br>
          <b>覆盖因子 k</b>${fmt(n.k, 4)}（p=${n.coverage_p}，${esc(out.rule_versions.welch_satterthwaite)} / ${esc(out.rule_versions.propagation)}）<br>
          <b>约 ${Math.round(n.coverage_p*100)}% 区间</b>[${fmt(n.interval_low)}, ${fmt(n.interval_high)}] ${esc(n.unit)}<br>
          <b>节点公式版本</b>${esc(n.formula)}
        </div>
        <div class="grid2">
          <div><h4>贡献率（占 u_c²）</h4>${bars(n.contributions.map(c => ({ label: c.kind === 'pair' ? `${c.source}~${c.with}` : c.source, v: c.share })))}</div>
          <div><h4>敏感性排序 |c_i|</h4>${bars(n.sensitivities.map(x => ({ label: x.source, v: x.sensitivity })), true)}</div>
        </div>
      </div>`;
    }
    html += `<details class="card"><summary>单位换算路径与规则版本（审计）</summary><pre>${esc(JSON.stringify({ rule_versions: out.rule_versions, unit_paths: out.unit_paths.slice(0, 40) }, null, 2))}</pre></details>`;
    $('#result-panel').innerHTML = html;
  } catch (err) { showErr(err); }
}

function bars(items, signed) {
  const max = Math.max(1e-12, ...items.map(x => Math.abs(x.v)));
  return items.slice(0, 12).map(x => {
    const pct = Math.abs(x.v) / max * 100;
    const neg = x.v < 0 ? ' neg' : '';
    const text = signed ? fmt(x.v, 4) : (x.v * 100).toFixed(1) + '%';
    return `<div class="bar-row"><span class="bar-label" title="${esc(x.label)}">${esc(x.label)}</span>
      <span class="bar-track"><span class="bar-fill${neg}" style="width:${pct}%"></span></span>
      <span class="bar-val">${text}</span></div>`;
  }).join('');
}

// ---------- compare ----------
async function renderCompareSelects() {
  await loadResults();
  const opts = state.results.map(r => `<option value="${r.id}">${r.id} · ${r.scenario_id} · ${r.status}</option>`).join('');
  $('#cmp-a').innerHTML = opts;
  $('#cmp-b').innerHTML = opts;
}
$('#cmp-run').onclick = async () => {
  try {
    const d = await api('POST', `/api/results/${$('#cmp-a').value}/diff/${$('#cmp-b').value}`);
    $('#cmp-result').innerHTML = `<div class="card"><h2>并排结论变化</h2>
      <table><thead><tr><th>节点</th><th>值 A</th><th>值 B</th><th>Δ(B−A)</th><th>u_A</th><th>u_B</th><th>状态</th></tr></thead>
      <tbody>${d.nodes.map(n => `<tr><td>${esc(n.node)}</td><td>${fmt(n.value_a)}</td><td>${fmt(n.value_b)}</td>
      <td><b>${fmt(n.delta)}</b></td><td>${fmt(n.u_a)}</td><td>${fmt(n.u_b)}</td>
      <td><span class="badge ${n.status_a}">${n.status_a}</span> → <span class="badge ${n.status_b}">${n.status_b}</span></td></tr>`).join('')}</tbody></table></div>`;
  } catch (err) { showErr(err); }
};

// ---------- audit ----------
$('#audit-export').onclick = () => {
  const id = $('#audit-result-id').value.trim();
  if (!id) return toast('填写结果 ID');
  window.open('/api/results/' + id + '/audit', '_blank');
};
$('#audit-import').onclick = async () => {
  const f = $('#audit-file').files[0];
  if (!f) return toast('选择 .tar 审计包');
  const store = $('#audit-store').checked;
  try {
    const res = await fetch('/api/audit/import?store=' + store, {
      method: 'POST', body: f, headers: { 'X-Request-Id': 'import:' + f.name + ':' + f.size + ':' + f.lastModified },
    });
    const data = await res.json();
    if (!res.ok) return showErr({ status: res.status, body: data, message: data.error?.message });
    $('#audit-report').textContent = JSON.stringify(data, null, 2);
    toast(data.all_match ? '哈希与数值全部一致' : '存在差异，见报告', data.all_match ? 'ok' : 'err');
  } catch (err) { showErr(err); }
};

// ---------- events ----------
async function loadEvents() {
  const d = await api('GET', '/api/events');
  $('#events-table tbody').innerHTML = d.events.slice().reverse().map(e =>
    `<tr><td>${e.seq}</td><td>${esc(e.event)}</td><td>${esc(e.id)}</td>
     <td><code>${esc(e.request_id || '')}</code></td><td>${esc(e.time)}</td></tr>`).join('');
}
$('#events-refresh').onclick = loadEvents;

// ---------- boot ----------
(async function init() {
  await loadObs(); await loadCerts(); await loadResults();
  {
    const d = await api('GET', '/api/scenarios');
    state.scenarios = d.scenarios || [];
  }
  const want = location.hash.slice(1);
  if (want) {
    const b = document.querySelector('#tabs button[data-tab="' + want + '"]');
    if (b) b.click();
  }
  if (want === 'scn') {
    await renderScenarioTab();
    const items = document.querySelectorAll('.scn-item');
    if (items.length) items[items.length - 1].click();
    // ensure the latest result is rendered
    const scs = state.scenarios;
    if (scs.length) {
      const sc = scs[scs.length - 1];
      if (sc.latest_result_id) renderResult(sc.latest_result_id);
    }
  }
})();
