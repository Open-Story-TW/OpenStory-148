'use strict';
const $ = (id) => document.getElementById(id);

function el(tag, props, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (key === 'class') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else if (value !== false && value != null) node.setAttribute(key, value === true ? '' : value);
  }
  for (const child of children) if (child != null) node.append(child);
  return node;
}

async function api(path, body) {
  const options = { credentials: 'same-origin', headers: {} };
  if (body !== undefined) {
    options.method = 'POST';
    options.headers = { 'Content-Type': 'application/json', 'X-Maple-Admin': '1' };
    options.body = JSON.stringify(body);
  }
  let response;
  try {
    response = await fetch(path, options);
  } catch {
    throw new Error('連不上後台服務');
  }
  const data = await response.json().catch(() => ({}));
  if (response.status === 401 && !path.startsWith('/api/login')) {
    showLogin();
    throw new Error(data.error || '請先登入');
  }
  if (!response.ok) throw new Error(data.error || '操作失敗');
  return data;
}

let toastTimer;
function toast(message, bad) {
  const node = $('toast');
  node.textContent = message;
  node.className = bad ? 'toast bad' : 'toast';
  node.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { node.hidden = true; }, bad ? 6000 : 3000);
}
const fail = (error) => toast(error.message, true);

function table(headers, rows, empty) {
  if (!rows.length) return el('div', { class: 'table' }, el('div', { class: 'empty', text: empty }));
  const body = el('table', null, el('tr', null, ...headers.map((h) => el('th', { class: h.num ? 'num' : '', text: h.label }))));
  for (const row of rows) {
    body.append(el('tr', null, ...row.map((cell, i) => {
      const td = el('td', { class: headers[i].num ? 'num' : headers[i].wrap ? 'wrap' : '' });
      td.append(cell instanceof Node ? cell : String(cell));
      return td;
    })));
  }
  return el('div', { class: 'table' }, body);
}

/* ---------- sign-in ---------- */
let pendingEmail = '';
function showLogin() {
  $('app').hidden = true;
  $('login').hidden = false;
  setLoginStep('');
}
function setLoginStep(email) {
  pendingEmail = email;
  $('email-row').hidden = !!email;
  $('code-row').hidden = !email;
  $('login-back').hidden = !email;
  $('login-button').textContent = email ? '登入' : '寄認證碼';
  $('login-hint').textContent = email
    ? `如果 ${email} 是管理員，認證碼已經寄出，10 分鐘內有效。`
    : '輸入管理員 email，我們會寄一組 6 位數認證碼給你。';
  $('login-error').textContent = '';
  $('code').value = '';
  (email ? $('code') : $('email')).focus();
}
$('login-back').addEventListener('click', () => setLoginStep(''));
$('login-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const button = $('login-button');
  button.disabled = true;
  try {
    if (!pendingEmail) {
      const email = $('email').value.trim();
      await api('/api/login/request', { email });
      setLoginStep(email);
    } else {
      await api('/api/login/verify', { email: pendingEmail, code: $('code').value.trim() });
      await start();
    }
  } catch (error) {
    $('login-error').textContent = error.message;
  } finally {
    button.disabled = false;
  }
});
$('logout').addEventListener('click', async () => {
  await api('/api/logout', {}).catch(() => {});
  showLogin();
});

/* ---------- tabs ---------- */
const loaders = { overview: loadOverview, settings: loadSettings, players: loadPlayers, notices: loadNotices, logs: loadLogs, gm: loadGM, admins: loadAdmins };
let currentTab = 'overview';
function openTab(name) {
  currentTab = name;
  for (const button of $('tabs').children) button.classList.toggle('active', button.dataset.tab === name);
  for (const key of Object.keys(loaders)) $('tab-' + key).hidden = key !== name;
  loaders[name]().catch(fail);
}
$('tabs').addEventListener('click', (event) => {
  const tab = event.target.dataset.tab;
  if (tab) openTab(tab);
});
// Administrators are rarely changed, so they sit beside the signed-in address
// instead of taking a tab.
$('open-admins').addEventListener('click', () => openTab('admins'));
const visible = (tab) => !$('app').hidden && currentTab === tab && !document.hidden;
setInterval(() => { if (visible('overview')) loadOverview().catch(() => {}); }, 10000);
setInterval(() => {
  if (visible('players') && !$('player-query').value.trim()) loadPlayers().catch(() => {});
  if (visible('logs') && logsAtTop) loadLogs().catch(() => {});
}, 3000);

/* ---------- overview ---------- */
const statusText = { ready: ['運作中', 'good'], starting: ['啟動中', 'warn'], database_unavailable: ['資料庫連不上', 'bad'], stopped: ['未啟動', 'bad'], unknown: ['狀態不明', 'warn'] };
async function loadOverview() {
  const data = await api('/api/status');
  $('stats').replaceChildren(
    el('div', { class: 'stat' }, el('b', { text: data.online }), '線上玩家'),
    el('div', { class: 'stat' }, el('b', { text: data.accounts }), '帳號'),
    el('div', { class: 'stat' }, el('b', { text: data.characters }), '角色'),
  );
  $('services').replaceChildren(...data.services.map((service) => {
    const [label, tone] = statusText[service.status] || statusText.unknown;
    return el('div', { class: 'service' }, el('span', { text: service.label }), el('span', { class: 'pill ' + tone, text: label }));
  }));
  const names = Object.fromEntries(data.worlds.map((world) => [world.id, world.name]));
  $('channels').replaceChildren(table(
    [{ label: '世界' }, { label: '頻道' }, { label: '人數', num: true }],
    data.channels.map((c) => [names[c.world_id] ?? c.world_id, c.channel_id || '轉換中', c.players]),
    '目前沒有玩家在線上',
  ));
}

/* ---------- settings ---------- */
const groups = {
  rates: ['倍率', '儲存後幾秒內生效，不用重啟。'],
  gameplay: ['遊戲功能', '儲存後幾秒內生效，不用重啟。'],
  login: ['登入與角色', '儲存後幾秒內生效，不用重啟；玩家下次回到角色選擇畫面時套用。'],
  trade: ['交易與商店', '儲存後幾秒內生效，不用重啟。玩家下次換地圖或重新登入時，遊戲內顯示的數字會跟著更新。'],
  client: ['登入器介面', '儲存後幾秒內生效，玩家下次換地圖或重新登入時套用，不用重啟。'],
  combat: ['戰鬥與動作', '儲存後幾秒內生效，玩家下次換地圖或重新登入時套用，不用重啟。'],
  protocol: ['突破上限', '這些會改變封包格式：儲存後要重啟服務器，玩家重開遊戲才會生效。登入器會自動跟著服務器，不用再改設定檔。'],
  debug: ['除錯', '儲存後幾秒內生效。'],
};
let settingsState = null;
let settingsGroup = 'rates';
const draft = {};

/* Fee tables are edited as rows of text; a table goes into draft once every
   row is valid, and into feeInvalid while one is not. */
const feeRows = {};
const feeInvalid = new Set();
const feeTry = {};
const RATE_SCALE = 100000n;
const UNIT = { 萬: 1e4, 億: 1e8, 兆: 1e12 };

// "1,000,000", "100萬" and "1.5億" are all amounts; anything else is null.
function parseAmount(text) {
  const match = /^(\d+(?:\.\d+)?)(萬|億|兆)?$/.exec(String(text).replace(/[,\s，]/g, ''));
  if (!match) return null;
  const amount = Number(match[1]) * (UNIT[match[2]] || 1);
  const whole = Math.round(amount);
  return Math.abs(amount - whole) < 1e-6 && whole <= Number.MAX_SAFE_INTEGER ? whole : null;
}
// A percentage with up to three decimals, as parts per 100,000.
function parseRate(text) {
  const match = /^(\d{1,3})(?:\.(\d{1,3}))?$/.exec(String(text).replace(/[%％\s]/g, ''));
  if (!match) return null;
  const rate = Number(match[1]) * 1000 + Number((match[2] || '').padEnd(3, '0'));
  return rate <= 100000 ? rate : null;
}
const rateText = (rate) => String(rate / 1000);
const commas = (amount) => Number(amount).toLocaleString('en-US');
function unitText(amount) {
  if (amount < 10000) return '';
  let rest = amount;
  let out = '';
  for (const [name, size] of [['兆', 1e12], ['億', 1e8], ['萬', 1e4]]) {
    const part = Math.floor(rest / size);
    if (part) out += part + name;
    rest %= size;
  }
  return out + (rest || '');
}
function feeOn(tiers, amount) {
  let rate = 0n;
  for (const tier of tiers) if (amount >= tier.from) rate = BigInt(tier.rate);
  const total = BigInt(amount);
  return { rate: Number(rate), fee: Number(total / RATE_SCALE * rate + (total % RATE_SCALE * rate + RATE_SCALE / 2n) / RATE_SCALE) };
}
function rowsOf(text) {
  return text.split(',').filter(Boolean).map((part) => {
    const [from, rate] = part.split(':');
    return { from: commas(from), rate };
  });
}
// Each row's parsed values and what is wrong with it, if anything.
function checkFees(rows) {
  let last = -1;
  return rows.map((row) => {
    const from = parseAmount(row.from);
    const rate = parseRate(row.rate);
    let error = '';
    if (from === null) error = '金額請填整數，可以寫 100萬、1.5億';
    else if (from <= last) error = '金額要比上一級大';
    else if (rate === null) error = '百分比請填 0 到 100，最多三位小數';
    if (from !== null) last = Math.max(last, from);
    return { from, rate, error };
  });
}
function countPending(group) {
  return settingsState.defs.filter((d) => (!group || d.group === group) && (d.key in draft || feeInvalid.has(d.key))).length;
}
function updateSaveBar() {
  const count = countPending();
  $('settings-save').disabled = count === 0 || feeInvalid.size > 0;
  $('settings-reset').disabled = count === 0;
  $('settings-note').textContent = feeInvalid.size ? '手續費級距有地方要修正，改好才能儲存' : count ? `${count} 項尚未儲存` : '';
  for (const button of $('settings-tabs').children) {
    const pending = countPending(button.dataset.group);
    button.lastChild.hidden = !pending;
    button.lastChild.textContent = pending;
  }
}

function feeEditor(def, row) {
  const key = def.key;
  const saved = settingsState.values[key];
  const rows = feeRows[key] || (feeRows[key] = rowsOf(saved));
  const lines = rows.map((item, index) => {
    const from = el('input', { type: 'text', inputmode: 'numeric', value: item.from, 'aria-label': `第 ${index + 1} 級起算金額`, 'data-fee': `${key}:${index}` });
    const rate = el('input', { type: 'text', inputmode: 'decimal', value: item.rate, 'aria-label': `第 ${index + 1} 級百分比` });
    from.addEventListener('input', () => { item.from = from.value; sync(); });
    rate.addEventListener('input', () => { item.rate = rate.value; sync(); });
    from.addEventListener('change', () => {
      const amount = parseAmount(item.from);
      if (amount !== null) from.value = item.from = commas(amount);
    });
    const remove = el('button', { type: 'button', class: 'link danger', text: '刪除', 'aria-label': `刪除第 ${index + 1} 級`, onclick: () => { rows.splice(index, 1); renderSettings(); } });
    const hint = el('small');
    return { hint, node: el('div', { class: 'tier' },
      el('span', { class: 'no', text: index + 1 }),
      el('label', { class: 'amount' }, '成交金額達', from, '楓幣以上'),
      el('label', { class: 'rate' }, '收', rate, '%'),
      remove, hint) };
  });
  const summary = el('p', { class: 'fee-note' });
  const tryInput = el('input', { type: 'text', inputmode: 'numeric', placeholder: '例如 5000萬', value: feeTry[key] || '', 'aria-label': '試算金額' });
  tryInput.addEventListener('input', () => { feeTry[key] = tryInput.value; sync(); });
  const tryResult = el('span');
  const add = el('button', { type: 'button', text: '＋ 新增一級', onclick: () => {
    const last = checkFees(rows).pop();
    rows.push(last && last.from !== null
      ? { from: commas(Math.min((last.from || 10000) * 10, Number.MAX_SAFE_INTEGER)), rate: last.rate === null ? '' : rateText(last.rate) }
      : { from: '100,000', rate: '1' });
    renderSettings();
    const input = document.querySelector(`[data-fee="${key}:${rows.length - 1}"]`);
    if (input) { input.focus(); input.select(); }
  } });
  const restore = el('button', { type: 'button', class: 'link', text: '填回預設值', onclick: () => { feeRows[key] = rowsOf(def.default); renderSettings(); } });

  function sync() {
    const checked = checkFees(rows);
    const tiers = checked.filter((tier) => !tier.error).map((tier) => ({ from: tier.from, rate: tier.rate }));
    const valid = tiers.length === rows.length && rows.length <= def.max;
    checked.forEach((tier, index) => {
      const { hint, node } = lines[index];
      node.classList.toggle('bad', !!tier.error);
      hint.textContent = tier.error || (unitText(tier.from) ? `${unitText(tier.from)}起，剛好這個金額收 ${commas(feeOn(tiers, tier.from).fee)}` : `剛好這個金額收 ${commas(feeOn(tiers, tier.from).fee)}`);
    });
    const text = tiers.map((tier) => `${tier.from}:${rateText(tier.rate)}`).join(',');
    delete draft[key];
    feeInvalid.delete(key);
    if (!valid) feeInvalid.add(key);
    else if (text !== saved) draft[key] = text;
    row.classList.toggle('changed', key in draft || feeInvalid.has(key));
    add.disabled = rows.length >= def.max;
    restore.hidden = valid && text === def.default;
    summary.textContent = !rows.length ? '目前沒有級距：不收手續費。'
      : valid && tiers[0].from > 0 ? `低於 ${commas(tiers[0].from)} 楓幣不收手續費。${rows.length} / ${def.max} 級。`
        : `${rows.length} / ${def.max} 級。`;
    const amount = parseAmount(feeTry[key] || '');
    if (!(feeTry[key] || '').trim()) tryResult.textContent = '';
    else if (amount === null || !valid) tryResult.textContent = valid ? '請填整數金額' : '先修正上面的級距';
    else {
      const { rate, fee } = feeOn(tiers, amount);
      tryResult.textContent = `手續費 ${commas(fee)}（${rateText(rate)}%），實收 ${commas(amount - fee)}`;
    }
    updateSaveBar();
  }
  sync();
  return el('div', { class: 'fees' }, ...lines.map((line) => line.node), summary,
    el('div', { class: 'fee-actions' }, add, restore),
    el('label', { class: 'fee-try' }, '試算：成交', tryInput, '楓幣', tryResult));
}

function renderSettings() {
  const { defs, values, pending } = settingsState;
  $('settings-tabs').replaceChildren(...Object.entries(groups).map(([group, [title]]) => el('button', {
    type: 'button', role: 'tab', 'data-group': group, 'aria-selected': String(group === settingsGroup),
    class: group === settingsGroup ? 'active' : '', onclick: () => { settingsGroup = group; renderSettings(); },
  }, title, el('span', { class: 'count', title: '尚未儲存' }))));
  const [title, note] = groups[settingsGroup];
  const box = el('div', { class: 'group', role: 'tabpanel' }, el('h3', { text: title }), el('p', { text: note }));
  for (const def of defs.filter((d) => d.group === settingsGroup)) {
    const value = draft[def.key] ?? values[def.key];
    const text = el('div', { class: 'text' }, def.label, el('span', { text: def.help }));
    const row = el('div', { class: 'setting' + (def.key in draft ? ' changed' : '') }, text);
    if (pending[def.key] && !(def.key in draft)) row.append(el('span', { class: 'pill warn', text: '重啟後生效' }));
    const change = (next) => {
      if (next === values[def.key]) delete draft[def.key]; else draft[def.key] = next;
      renderSettings();
    };
    if (def.kind === 'bool') {
      row.append(el('label', { class: 'switch', title: def.label },
        el('input', { type: 'checkbox', checked: value === '1', 'aria-label': def.label, onchange: (e) => change(e.target.checked ? '1' : '0') }), el('i')));
    } else if (def.kind === 'int') {
      row.append(el('input', { type: 'number', min: def.min, max: def.max, value, 'aria-label': def.label, onchange: (e) => change(e.target.value.trim()) }));
    } else if (def.kind === 'fees') {
      row.classList.add('wide');
      row.append(feeEditor(def, row));
    } else {
      row.append(el('input', { type: 'text', value, 'aria-label': def.label, onchange: (e) => change(e.target.value.trim()) }));
    }
    box.append(row);
  }
  $('settings').replaceChildren(box);
  updateSaveBar();
}
function forgetDraft() {
  for (const key of Object.keys(draft)) delete draft[key];
  for (const key of Object.keys(feeRows)) delete feeRows[key];
  feeInvalid.clear();
}
async function loadSettings() {
  settingsState = await api('/api/settings');
  renderSettings();
}
$('settings-reset').addEventListener('click', () => {
  forgetDraft();
  renderSettings();
});
$('settings-save').addEventListener('click', async () => {
  $('settings-save').disabled = true;
  try {
    const needRestart = settingsState.defs.some((d) => !d.live && d.key in draft);
    settingsState = await api('/api/settings', { values: { ...draft } });
    forgetDraft();
    toast(needRestart ? '已儲存。突破上限的項目要重啟服務器才會生效。' : '已儲存，幾秒內生效。');
  } catch (error) {
    fail(error);
  }
  renderSettings();
});
window.addEventListener('beforeunload', (event) => {
  if (settingsState && countPending()) event.preventDefault();
});

/* ---------- players ---------- */
const number = (value) => Number(value).toLocaleString('zh-TW');
const when = (value) => new Date(value).toLocaleString('zh-TW', { dateStyle: 'short', timeStyle: 'medium', hour12: false });
let worlds = [];
const worldName = (id) => (worlds.find((w) => w.id === id) || {}).name ?? `世界 ${id}`;
function gauge(now, max, kind) {
  const bar = el('span', { class: 'bar ' + kind }, el('i'));
  bar.firstChild.style.width = Math.max(0, Math.min(100, max > 0 ? (now / max) * 100 : 0)) + '%';
  return el('span', null, bar, `${now}/${max}`);
}
async function loadPlayers() {
  const query = $('player-query').value.trim();
  if (!query && $('player-scope').value === 'live') return loadLive();
  const data = await api(query ? '/api/players?q=' + encodeURIComponent(query) : '/api/players?all=1');
  const online = data.players.filter((p) => p.online).length;
  $('players-note').textContent = `共 ${data.players.length} 個角色・線上 ${online}・離線 ${data.players.length - online}。`
    + '等級與地圖來自存檔；點角色名稱可以查看並修改裝備與背包，線上、離線都可以。';
  $('players').replaceChildren(table(
    [{ label: '角色' }, { label: '帳號' }, { label: '等級', num: true }, { label: '職業', num: true }, { label: '地圖', num: true }, { label: '位置' }, { label: '' }],
    data.players.map((p) => [
      playerLink(p), p.account, p.level, p.job, p.map_id,
      p.online ? el('span', { class: 'pill good', text: `線上・頻道 ${p.channel_id || '轉換中'}` }) : el('span', { class: 'pill', text: '離線' }),
      p.gm ? el('span', { class: 'pill good', text: 'GM' }) : '',
    ]),
    '找不到符合的角色',
  ));
}
async function loadLive() {
  const data = await api('/api/live');
  worlds = data.worlds;
  const down = worlds.filter((w) => !w.up).map((w) => w.name);
  $('players-note').textContent = `即時狀態，每 3 秒更新・線上 ${data.players.length} 人`
    + (down.length ? `・${down.join('、')} 沒有回應` : '');
  $('players').replaceChildren(table(
    [{ label: '角色' }, { label: '等級', num: true }, { label: '職業', num: true }, { label: '位置' }, { label: '地圖', num: true },
      { label: 'HP' }, { label: 'MP' }, { label: '經驗', num: true }, { label: '楓幣', num: true }, { label: '' }],
    data.players.map((p) => [
      playerLink(p), p.level, p.job, `${worldName(p.world_id)}・頻道 ${p.channel}`,
      p.in_field ? p.map_id : '轉換中',
      gauge(p.hp, p.max_hp, 'hp'), gauge(p.mp, p.max_mp, 'mp'), number(p.exp), number(p.mesos),
      p.gm ? el('span', { class: 'pill good', text: 'GM' }) : '',
    ]),
    '目前沒有玩家在線上',
  ));
}
$('player-scope').addEventListener('change', () => loadPlayers().catch(fail));
$('player-search').addEventListener('submit', (event) => {
  event.preventDefault();
  loadPlayers().catch(fail);
});

/* ---------- notices ---------- */
function scopeText(n) {
  if (n.world_id < 0 && !n.channel && !n.map_id) return '全服務器';
  const parts = [n.world_id < 0 ? '所有世界' : worldName(n.world_id)];
  if (n.channel) parts.push(`頻道 ${n.channel}`);
  if (n.map_id) parts.push(`地圖 ${n.map_id}`);
  return parts.join('・');
}
function renderNoticeForm() {
  const scope = $('notice-scope').value;
  $('notice-world-row').hidden = scope === 'all';
  $('notice-channel-row').hidden = scope !== 'channel' && scope !== 'map';
  $('notice-map-row').hidden = scope !== 'map';
  const world = worlds.find((w) => String(w.id) === $('notice-world').value) || worlds[0];
  const previous = $('notice-channel').value;
  const options = scope === 'map' ? [el('option', { value: '0', text: '所有頻道' })] : [];
  for (let i = 1; i <= (world ? world.channel_count : 1); i++) options.push(el('option', { value: i, text: `頻道 ${i}` }));
  $('notice-channel').replaceChildren(...options);
  if (options.some((o) => o.value === previous)) $('notice-channel').value = previous;
}
async function loadNotices() {
  const [live, data] = await Promise.all([api('/api/live'), api('/api/notices')]);
  worlds = live.worlds;
  const selected = $('notice-world').value;
  $('notice-world').replaceChildren(...worlds.map((w) => el('option', { value: w.id, text: w.name })));
  if (selected) $('notice-world').value = selected;
  renderNoticeForm();
  $('notice-list').replaceChildren(table(
    [{ label: '時間' }, { label: '範圍' }, { label: '內容', wrap: true }, { label: '收到人數', num: true }, { label: '發送人' }],
    data.notices.map((n) => [when(n.at), scopeText(n), n.message, n.delivered, n.actor]),
    '還沒有發過公告',
  ));
}
$('notice-scope').addEventListener('change', renderNoticeForm);
$('notice-world').addEventListener('change', renderNoticeForm);
$('notice-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const scope = $('notice-scope').value;
  const body = { world: -1, channel: 0, map_id: 0, message: $('notice-message').value.trim() };
  if (scope !== 'all') body.world = Number($('notice-world').value);
  if (scope === 'channel' || scope === 'map') body.channel = Number($('notice-channel').value);
  if (scope === 'map') {
    body.map_id = Number($('notice-map').value);
    if (!body.map_id) return toast('請填地圖編號', true);
  }
  $('notice-send').disabled = true;
  try {
    const result = await api('/api/notices', body);
    toast(result.delivered ? `已發送，${result.delivered} 位玩家收到` : '已發送，但範圍內目前沒有玩家');
    $('notice-message').value = '';
    await loadNotices();
  } catch (error) {
    fail(error);
  } finally {
    $('notice-send').disabled = false;
  }
});

/* ---------- logs ---------- */
const kindText = { chat: '一般', chat_buddy: '好友', chat_party: '隊伍', chat_guild: '公會', chat_alliance: '聯盟', party: '組隊', trade: '交易' };
let logRows = [];
let logsAtTop = true;
function logPath(before) {
  const params = new URLSearchParams();
  if ($('log-kind').value) params.set('kind', $('log-kind').value);
  if ($('log-query').value.trim()) params.set('q', $('log-query').value.trim());
  if (before) params.set('before', before);
  return '/api/events?' + params;
}
function renderLogs(more) {
  $('logs').replaceChildren(table(
    [{ label: '時間' }, { label: '種類' }, { label: '角色' }, { label: '位置' }, { label: '內容', wrap: true }],
    logRows.map((e) => [when(e.at), kindText[e.kind] || e.kind, e.name, `頻道 ${e.channel}・地圖 ${e.map_id}`, e.text]),
    '沒有符合的記錄',
  ));
  $('log-more').hidden = !more;
}
async function loadLogs() {
  const data = await api(logPath());
  logRows = data.events;
  logsAtTop = true;
  renderLogs(data.events.length === 100);
}
$('log-search').addEventListener('submit', (event) => {
  event.preventDefault();
  loadLogs().catch(fail);
});
$('log-kind').addEventListener('change', () => loadLogs().catch(fail));
$('log-more').addEventListener('click', async () => {
  try {
    const data = await api(logPath(logRows[logRows.length - 1].id));
    logRows = logRows.concat(data.events);
    logsAtTop = false;
    renderLogs(data.events.length === 100);
  } catch (error) {
    fail(error);
  }
});

/* ---------- GM grants ---------- */
function renderGM(data) {
  $('gm-list').replaceChildren(table(
    [{ label: '角色' }, { label: '帳號' }, { label: '狀態' }, { label: '到期' }, { label: '授權人' }, { label: '' }],
    data.grants.map((g) => [
      g.name, g.account,
      el('span', { class: 'pill ' + (g.enabled ? 'good' : 'bad'), text: g.enabled ? '有效' : '已失效' }),
      g.permanent ? (g.enabled ? el('span', { class: 'pill warn', text: '永久' }) : '—')
        : new Date(g.expires_at).toLocaleString('zh-TW', { dateStyle: 'short', timeStyle: 'short' }),
      g.authorized_by,
      g.enabled ? el('button', { class: 'link danger', text: '撤銷', onclick: () => changeGM(g.name, 'revoke') }) : '',
    ]),
    '還沒有任何 GM 授權',
  ));
}
async function loadGM() {
  renderGMTerm();
  renderGM(await api('/api/gm'));
}
async function changeGM(character, action, hours = 0, permanent = false) {
  try {
    renderGM(await api('/api/gm', { character, action, hours, permanent, reason: '' }));
    toast(action === 'grant' ? `已授權 ${character}${permanent ? '（永久）' : ''}` : `已撤銷 ${character}`);
    return true;
  } catch (error) {
    fail(error);
    return false;
  }
}
function renderGMTerm() {
  const permanent = $('gm-term').value === 'permanent';
  $('gm-hours-row').hidden = permanent;
  $('gm-term-note').textContent = permanent
    ? '永久授權沒有到期時間，要在下面按「撤銷」才會取消。'
    : '限時授權最長 24 小時，到期自動失效。';
}
$('gm-term').addEventListener('change', renderGMTerm);

// Character picker: type part of a character or account name, pick from the list.
let gmPick = null;
let gmOptions = [];
let gmActive = -1;
let gmSearchTimer;
let gmSearchSeq = 0;
function closeGMOptions() {
  $('gm-options').hidden = true;
  $('gm-name').setAttribute('aria-expanded', 'false');
  $('gm-name').removeAttribute('aria-activedescendant');
  gmActive = -1;
}
function setGMActive(index) {
  gmActive = index;
  [...$('gm-options').children].forEach((node, i) => {
    node.classList.toggle('active', i === index);
    node.setAttribute('aria-selected', i === index ? 'true' : 'false');
    if (i === index) node.scrollIntoView({ block: 'nearest' });
  });
  if (index >= 0) $('gm-name').setAttribute('aria-activedescendant', 'gm-option-' + index);
  else $('gm-name').removeAttribute('aria-activedescendant');
}
function pickGM(player) {
  gmPick = player;
  $('gm-name').value = player ? player.name : $('gm-name').value;
  $('gm-submit').disabled = !player;
  closeGMOptions();
}
function renderGMOptions(players) {
  gmOptions = players;
  const list = $('gm-options');
  if (!players.length) {
    list.replaceChildren(el('li', { class: 'none', text: '找不到符合的角色' }));
  } else {
    list.replaceChildren(...players.map((p, i) => el('li', {
      id: 'gm-option-' + i, role: 'option', 'aria-selected': 'false',
      // mousedown, not click: it fires before the input loses focus.
      onmousedown: (event) => { event.preventDefault(); pickGM(p); },
    },
    el('b', { text: p.name }),
    el('span', { class: 'muted', text: `帳號 ${p.account}・Lv ${p.level}` }),
    p.gm ? el('span', { class: 'pill good', text: 'GM' }) : null)));
  }
  list.hidden = false;
  $('gm-name').setAttribute('aria-expanded', 'true');
  setGMActive(players.length ? 0 : -1);
}
async function searchGM() {
  const query = $('gm-name').value.trim();
  const seq = ++gmSearchSeq;
  if (!query) return closeGMOptions();
  try {
    const data = await api('/api/players?q=' + encodeURIComponent(query));
    if (seq === gmSearchSeq && !gmPick) renderGMOptions(data.players.slice(0, 50));
  } catch (error) {
    if (seq === gmSearchSeq) fail(error);
  }
}
$('gm-name').addEventListener('input', () => {
  gmPick = null;
  $('gm-submit').disabled = true;
  clearTimeout(gmSearchTimer);
  gmSearchTimer = setTimeout(searchGM, 200);
});
$('gm-name').addEventListener('focus', () => { if (!gmPick && $('gm-name').value.trim()) searchGM(); });
$('gm-name').addEventListener('blur', closeGMOptions);
$('gm-name').addEventListener('keydown', (event) => {
  const open = !$('gm-options').hidden;
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    if (!open) return void searchGM();
    if (gmOptions.length) setGMActive((gmActive + (event.key === 'ArrowDown' ? 1 : gmOptions.length - 1)) % gmOptions.length);
  } else if (event.key === 'Enter' && open) {
    event.preventDefault();
    if (gmOptions[gmActive]) pickGM(gmOptions[gmActive]);
  } else if (event.key === 'Escape' && open) {
    closeGMOptions();
  }
});
$('gm-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  if (!gmPick) return toast('請先從搜尋結果選一個角色', true);
  const permanent = $('gm-term').value === 'permanent';
  if (await changeGM(gmPick.name, 'grant', permanent ? 0 : Number($('gm-hours').value), permanent)) {
    $('gm-name').value = '';
    pickGM(null);
  }
});

/* ---------- admins ---------- */
function renderAdmins(data) {
  $('admin-list').replaceChildren(table(
    [{ label: 'Email' }, { label: '' }, { label: '' }],
    data.admins.map((a) => [
      a.email,
      a.builtin ? el('span', { class: 'pill good', text: '啟動設定' }) : '',
      a.builtin || a.email === data.me ? '' : el('button', { class: 'link danger', text: '移除', onclick: () => changeAdmin(a.email, 'remove') }),
    ]),
    '沒有管理員',
  ));
}
async function loadAdmins() { renderAdmins(await api('/api/admins')); }
async function changeAdmin(email, action) {
  try {
    renderAdmins(await api('/api/admins', { email, action }));
    toast(action === 'add' ? `已新增 ${email}` : `已移除 ${email}`);
    if (action === 'add') $('admin-email').value = '';
  } catch (error) {
    fail(error);
  }
}
$('admin-form').addEventListener('submit', (event) => {
  event.preventDefault();
  changeAdmin($('admin-email').value.trim(), 'add');
});

/* ---------- start ---------- */
async function start() {
  const me = await api('/api/me');
  $('me').textContent = me.email;
  $('mail-warning').hidden = me.mail_configured;
  $('login').hidden = true;
  $('app').hidden = false;
  openTab('overview');
}
start().catch(() => {});
