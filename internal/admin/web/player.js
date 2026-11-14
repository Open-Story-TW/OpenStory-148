'use strict';
/* One player's equipment window and bags, drawn like the game's own, with
   the operator's edits. Loaded after app.js and uses its helpers. */

const STATS = [
  ['str', '力量'], ['dex', '敏捷'], ['int', '智力'], ['luk', '幸運'], ['hp', 'MaxHP'], ['mp', 'MaxMP'],
  ['watk', '攻擊力'], ['matk', '魔法攻擊力'], ['wdef', '物理防禦力'], ['mdef', '魔法防禦力'],
  ['acc', '命中率'], ['avoid', '迴避率'], ['speed', '移動速度'], ['jump', '跳躍力'],
];
// The game's tab order: equip, use, etc, set-up, cash.
const BAGS = [[1, '裝備'], [2, '消耗'], [4, '其他'], [3, '裝飾'], [5, '特殊']];
// Worn slots as the equipment window lays them out; null is a gap.
const WORN = [
  [[-12, '戒指'], null, [-1, '帽子'], null, [-53, '徽章']],
  [[-13, '戒指'], [-17, '墜飾'], [-2, '臉飾'], null, [-49, '勳章']],
  [[-15, '戒指'], null, [-3, '眼飾'], [-4, '耳環'], [-51, '肩飾']],
  [[-16, '戒指'], [-11, '武器'], [-5, '上衣'], [-9, '披風'], [-10, '盾牌']],
  [[-52, '口袋'], [-50, '腰帶'], [-6, '褲/裙'], [-8, '手套'], null],
  [[-18, '騎寵'], [-19, '鞍'], [-7, '鞋子'], null, [-54, '機器人']],
];
const WEAPONS = {
  130: '單手劍', 131: '單手斧', 132: '單手棍', 133: '短劍', 134: '雙刀', 135: '特殊副手', 136: '手杖', 137: '短杖', 138: '長杖',
  140: '雙手劍', 141: '雙手斧', 142: '雙手棍', 143: '槍', 144: '矛', 145: '弓', 146: '弩', 147: '拳套', 148: '指虎', 149: '火槍',
  152: '雙弩槍', 153: '加農砲',
};
const SPEEDS = { 2: '最快', 3: '比較快', 4: '快', 5: '快', 6: '普通', 7: '慢', 8: '慢', 9: '比較慢' };
const TIERS = { 1: '特殊道具', 2: '稀有道具', 3: '罕見道具', 4: '傳說道具' };
const JOBS = [['初心者', 0], ['劍士', 1], ['法師', 2], ['弓箭手', 4], ['盜賊', 8], ['海盜', 16]];
const ICON_SCALE = 2;

let P = null;          // the character being shown
let pJSON = '';
let pBag = 1;
let pCashWorn = false;
let pBusy = false;
let pPick = null;      // item chosen in the give form
let pEntries = new Map();
let pSearchTimer;

const big = (text) => { try { return BigInt(text).toLocaleString('zh-TW'); } catch { return text; } };
const metaOf = (id) => (P && P.meta[id]) || { id, name: '道具 ' + id, inv_type: Math.floor(id / 1000000), slot_max: 1 };

function playerLink(p) {
  return el('button', { class: 'link player-link', type: 'button', text: p.name, title: '查看裝備與背包', onclick: () => openPlayer(p.id).catch(fail) });
}

function iconNode(meta, scale) {
  const fallback = () => el('span', { class: 'noicon', text: meta.name });
  if (!P || !P.icons) return fallback();
  const img = el('img', { class: 'icon', src: '/api/items/icon?id=' + meta.id, alt: meta.name, draggable: 'false' });
  img.addEventListener('load', () => { img.style.width = img.naturalWidth * (scale || ICON_SCALE) + 'px'; });
  img.addEventListener('error', () => img.replaceWith(fallback()));
  return img;
}

/* ---------- tooltip ---------- */
// WZ text marks highlights as #c...# and line breaks as a literal \n.
function wzText(text) {
  const box = el('p', { class: 'tip-desc' });
  const lines = String(text || '').replace(/\\r/g, '').split(/\\n|\n/);
  lines.forEach((line, i) => {
    if (i) box.append(el('br'));
    const parts = line.split('#');
    parts.forEach((part, j) => {
      if (!part) return;
      if (j % 2 === 1 && part[0] === 'c') box.append(el('b', { text: part.slice(1) }));
      else box.append(part);
    });
  });
  return box;
}
function tipNode(meta, equip, stack) {
  const tip = el('div', { class: 'tip' });
  const plus = equip && equip.scroll_level > 0 ? ` (+${equip.scroll_level})` : '';
  tip.append(el('div', { class: 'tip-name' + (equip && equip.potential_tier > 0 ? ' tier' + equip.potential_tier : ''), text: meta.name + plus }));
  if (equip && equip.potential_tier > 0) tip.append(el('div', { class: 'tip-sub', text: `(${TIERS[equip.potential_tier]})` }));
  const marks = [];
  if (meta.only) marks.push('專屬道具');
  if (meta.trade_block) marks.push('無法交換');
  if (meta.quest) marks.push('任務道具');
  if (marks.length) tip.append(el('div', { class: 'tip-mark', text: marks.join(', ') }));
  const side = el('div', { class: 'tip-side' });
  if (equip) {
    const need = (label, value) => side.append(el('div', null, `${label} : `, el('span', { text: value || 0 })));
    need('裝備等級', meta.req_level); need('需要力量', meta.req_str); need('需要敏捷', meta.req_dex);
    need('需要智力', meta.req_int); need('需要幸運', meta.req_luk); need('需要名聲', meta.req_fame || '-');
  } else {
    if (meta.desc) side.append(wzText(meta.desc));
    if (stack) side.append(el('div', { class: 'tip-count', text: stack.pet ? `寵物「${stack.pet_name}」Lv.${stack.pet_level}` : `數量 : ${stack.quantity}` }));
  }
  tip.append(el('div', { class: 'tip-top' }, el('div', { class: 'tip-icon' }, iconNode(meta, 2)), side));
  if (equip) {
    const job = meta.req_job || 0;
    tip.append(el('div', { class: 'tip-jobs' }, ...JOBS.map(([name, bit]) =>
      el('span', { class: (bit === 0 ? job <= 0 : job === 0 || (job > 0 && (job & bit))) ? 'on' : '', text: name }))));
    const stats = el('div', { class: 'tip-list' });
    const kind = WEAPONS[Math.floor(meta.id / 10000)];
    if (kind) stats.append(el('div', { text: `武器分類 : ${kind}` }));
    if (kind && meta.attack_speed) stats.append(el('div', { text: `攻擊速度 : ${SPEEDS[meta.attack_speed] || meta.attack_speed}` }));
    for (const [key, label] of STATS) if (equip[key]) stats.append(el('div', { text: `${label} : +${equip[key]}` }));
    if (stats.children.length) tip.append(el('div', { class: 'tip-head', text: '裝備數值' }), stats);
    if (!equip.cash && (equip.upgrade_slots || equip.scroll_level || equip.enhance)) {
      const up = el('div', { class: 'tip-list' }, el('div', { text: `可用卷軸次數 : ${equip.upgrade_slots}` }));
      if (equip.scroll_level) up.append(el('div', { text: `已上卷次數 : ${equip.scroll_level}` }));
      if (equip.enhance) up.append(el('div', { text: `星力 : ${equip.enhance}` }));
      tip.append(el('div', { class: 'tip-head', text: '道具強化' }), up);
    }
    if (equip.potential_tier === -1) tip.append(el('div', { class: 'tip-potential' }, el('div', { text: '未鑑定的潛在能力' })));
    else if (equip.potential && equip.potential.length) tip.append(el('div', { class: 'tip-potential' }, ...equip.potential.map((line) => el('div', { text: line }))));
    if (meta.desc) tip.append(wzText(meta.desc));
  }
  const expires = (equip || stack || {}).expires_at;
  if (expires > 0) tip.append(el('div', { class: 'tip-mark', text: '使用期限 : ' + when(expires > 1e12 ? expires : expires * 1000) }));
  return tip;
}
function moveTip(event) {
  const tip = $('player-tip');
  if (tip.hidden) return;
  const pad = 14;
  let x = event.clientX + pad;
  let y = event.clientY + pad;
  if (x + tip.offsetWidth > innerWidth - 4) x = Math.max(4, event.clientX - pad - tip.offsetWidth);
  if (y + tip.offsetHeight > innerHeight - 4) y = Math.max(4, innerHeight - 4 - tip.offsetHeight);
  tip.style.left = x + 'px';
  tip.style.top = y + 'px';
}
function hoverTip(event) {
  const slot = event.target.closest ? event.target.closest('[data-key]') : null;
  const tip = $('player-tip');
  const entry = slot && pEntries.get(slot.dataset.key);
  if (!entry) { tip.hidden = true; tip.dataset.key = ''; return; }
  if (tip.dataset.key !== slot.dataset.key) {
    tip.replaceChildren(tipNode(entry.meta, entry.equip, entry.stack));
    tip.dataset.key = slot.dataset.key;
  }
  tip.hidden = false;
  moveTip(event);
}

/* ---------- windows ---------- */
function slotNode(entry, label) {
  if (!entry) return el('div', { class: 'slot empty' }, label ? el('span', { class: 'slot-label', text: label }) : null);
  const key = (entry.equip ? 'e' : 's') + (entry.equip || entry.stack).id;
  pEntries.set(key, entry);
  const node = el('button', { class: 'slot', type: 'button', 'data-key': key, 'aria-label': entry.meta.name, onclick: () => openEditor(entry) }, iconNode(entry.meta));
  if (entry.stack && !entry.stack.pet && (entry.stack.quantity > 1 || entry.meta.slot_max > 1)) node.append(el('span', { class: 'qty', text: entry.stack.quantity }));
  if ((entry.equip || entry.stack).cash) node.append(el('i', { class: 'cash-mark', title: '點數道具' }));
  return node;
}
function equipWindow() {
  const worn = new Map();
  for (const e of P.equipment) if (e.slot < 0) worn.set(e.slot, e);
  const offset = pCashWorn ? -100 : 0;
  const cells = [];
  const used = new Set();
  for (const row of WORN) for (const cell of row) {
    if (!cell) { cells.push(el('div', { class: 'slot gap' })); continue; }
    const e = worn.get(cell[0] + offset);
    used.add(cell[0] + offset);
    cells.push(slotNode(e ? { equip: e, meta: metaOf(e.item_id) } : null, cell[1]));
  }
  // Anything worn in a slot the layout does not draw must still be visible.
  for (const [slot, e] of worn) if (!used.has(slot) && (slot <= -100) === pCashWorn) cells.push(slotNode({ equip: e, meta: metaOf(e.item_id) }));
  const tab = (cash, text) => el('button', { type: 'button', class: pCashWorn === cash ? 'active' : '', text, onclick: () => { pCashWorn = cash; renderPlayer(); } });
  return el('div', { class: 'mw mw-equip' }, el('div', { class: 'mw-title', text: '裝備欄位' }),
    el('div', { class: 'mw-tabs' }, tab(false, '裝備'), tab(true, '點裝')),
    el('div', { class: 'mw-grid cols5' }, ...cells));
}
function bagWindow() {
  const capacity = P.slots[pBag - 1] || 24;
  const bySlot = new Map();
  if (pBag === 1) for (const e of P.equipment) if (e.slot > 0) bySlot.set(e.slot, { equip: e, meta: metaOf(e.item_id) });
  for (const s of P.items) if (s.inv_type === pBag) bySlot.set(s.slot, { stack: s, meta: metaOf(s.item_id) });
  const cells = [];
  const last = Math.max(capacity, ...bySlot.keys());
  for (let slot = 1; slot <= last; slot++) cells.push(slotNode(bySlot.get(slot)));
  const tabs = BAGS.map(([type, text]) => el('button', { type: 'button', class: pBag === type ? 'active' : '', text, onclick: () => { pBag = type; renderPlayer(); } }));
  return el('div', { class: 'mw mw-bag' }, el('div', { class: 'mw-title', text: '道具欄位' }),
    el('div', { class: 'mw-tabs' }, ...tabs),
    el('div', { class: 'mw-grid cols4 scroll' }, ...cells),
    el('div', { class: 'mw-meso' }, el('span', { class: 'coin', text: '楓幣' }), el('span', { class: 'meso-value', text: big(P.mesos) })),
    el('div', { class: 'mw-foot', text: `${bySlot.size} / ${capacity} 格` }));
}

/* ---------- edits ---------- */
async function send(path, body, done) {
  if (pBusy) return false;
  pBusy = true;
  document.body.classList.add('player-busy');
  try {
    setPlayer(await api(path, { character_id: P.id, ...body }));
    toast(done + (P.online ? '，已同步到遊戲' : '，已寫入存檔'));
    return true;
  } catch (error) {
    fail(error);
    // Nothing was applied, or the outcome is unknown: either way what is on
    // screen may be stale, so show what the character holds now.
    if ($('player-dialog').open) closeEditor();
    api('/api/character?id=' + P.id).then(setPlayer).catch(() => {});
    return false;
  } finally {
    pBusy = false;
    document.body.classList.remove('player-busy');
  }
}
// What the page showed of an item; the server applies an edit only if the
// item is still this.
function seen(entry) {
  if (entry.stack) return { item_id: entry.stack.item_id, quantity: entry.stack.quantity };
  const stats = { upgrade_slots: entry.equip.upgrade_slots, scroll_level: entry.equip.scroll_level };
  for (const [key] of STATS) stats[key] = entry.equip[key];
  return { item_id: entry.equip.item_id, stats };
}
const edit = (body, done) => send('/api/character/inventory', body, done);

function numberField(label, value, max, min) {
  const input = el('input', { type: 'number', min: min ?? 0, max, step: 1, value, required: true });
  return [el('label', null, label, input), input];
}
function closeEditor() { $('player-dialog').close(); }
function openEditor(entry) {
  const dialog = $('player-dialog');
  const item = entry.equip || entry.stack;
  const form = el('form', { class: 'edit-form', method: 'dialog' });
  const actions = el('div', { class: 'row end' });
  let submit = null;
  if (entry.stack && entry.stack.pet) {
    form.append(el('p', { class: 'muted', text: '寵物有自己的名字與成長資料，目前不能從後台修改或刪除。' }));
  } else if (entry.equip) {
    const inputs = {};
    const grid = el('div', { class: 'edit-grid' });
    const add = (key, label, max) => {
      const [row, input] = numberField(label, entry.equip[key], max);
      inputs[key] = input;
      grid.append(row);
    };
    for (const [key, label] of STATS) add(key, label, 32767);
    add('upgrade_slots', '可用卷軸次數', 127);
    add('scroll_level', '已上卷次數 (+N)', 127);
    form.append(el('h3', { text: '修改素質' }), grid, el('p', { class: 'muted small', text: '每項 0 到 32767。潛能與星力不在這裡修改。' }));
    submit = () => {
      const stats = {};
      for (const [key, input] of Object.entries(inputs)) stats[key] = Number(input.value);
      return edit({ kind: 'set_equipment', inv_type: 1, asset_id: item.id, stats, seen: seen(entry) }, `已修改「${entry.meta.name}」`);
    };
  } else {
    const max = Math.max(entry.meta.slot_max || 1, item.quantity);
    const [row, input] = numberField(`數量（1 到 ${max}）`, item.quantity, max, 1);
    form.append(el('h3', { text: '修改數量' }), row);
    submit = () => edit({ kind: 'set_quantity', inv_type: item.inv_type, asset_id: item.id, quantity: Number(input.value), seen: seen(entry) }, `已修改「${entry.meta.name}」的數量`);
  }
  if (submit) {
    let armed = false;
    const remove = el('button', { type: 'button', class: 'link danger', text: '刪除這個道具' });
    remove.addEventListener('click', async () => {
      if (!armed) { armed = true; remove.textContent = '再按一次確定刪除'; return; }
      if (await edit({ kind: 'remove', inv_type: entry.equip ? 1 : item.inv_type, asset_id: item.id, seen: seen(entry) }, `已刪除「${entry.meta.name}」`)) closeEditor();
    });
    actions.append(remove, el('span', { class: 'spacer' }));
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      if (await submit()) closeEditor();
    });
  }
  actions.append(el('button', { type: 'button', text: submit ? '取消' : '關閉', onclick: closeEditor }));
  if (submit) actions.append(el('button', { class: 'primary', text: '儲存' }));
  form.append(actions);
  dialog.replaceChildren(el('div', { class: 'edit' }, tipNode(entry.meta, entry.equip, entry.stack), form));
  $('player-tip').hidden = true;
  dialog.showModal();
}

/* ---------- give ---------- */
function renderPick() {
  const box = $('give-pick');
  if (!pPick) { box.replaceChildren(); box.hidden = true; return; }
  const equipment = pPick.inv_type === 1;
  const max = equipment || pPick.slot_max === 1 ? 96 : 30000;
  const quantity = el('input', { type: 'number', min: 1, max, value: 1, required: true, 'aria-label': '數量' });
  const form = el('form', { class: 'row' }, el('span', { class: 'slot static' }, iconNode(pPick)),
    el('span', { class: 'pick-name' }, pPick.name, el('small', { text: ` ${pPick.id}` })), quantity, el('button', { class: 'primary', text: '給予' }));
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    await edit({ kind: 'give_item', item_id: pPick.id, quantity: Number(quantity.value) }, `已給予「${pPick.name}」× ${quantity.value}`);
  });
  box.replaceChildren(form);
  box.hidden = false;
}
async function searchItems() {
  const query = $('give-query').value.trim();
  const list = $('give-results');
  if (!query) { list.replaceChildren(); return; }
  const data = await api('/api/items/search?q=' + encodeURIComponent(query));
  if ($('give-query').value.trim() !== query) return;
  if (!data.items.length) {
    list.replaceChildren(el('div', { class: 'empty', text: data.named ? '找不到符合的道具' : '服務器沒有設定 WZ 資料，只能用完整的道具編號查詢' }));
    return;
  }
  list.replaceChildren(...data.items.map((meta) => {
    const key = 'g' + meta.id;
    pEntries.set(key, { meta, equip: meta.base || null, stack: null });
    return el('button', { type: 'button', class: 'result', 'data-key': key, disabled: meta.pet, title: meta.pet ? '寵物不能從後台給予' : '',
      onclick: () => { pPick = meta; renderPick(); } },
      el('span', { class: 'slot static' }, iconNode(meta, 1)), el('span', { class: 'pick-name' }, meta.name, el('small', { text: ` ${meta.id}` })),
      el('small', { text: (BAGS.find((b) => b[0] === meta.inv_type) || [0, ''])[1] }));
  }));
}
function moneyRow(label, value, give) {
  const input = el('input', { inputmode: 'numeric', pattern: '-?[0-9]{1,19}', required: true, placeholder: '數量，負數是扣除', 'aria-label': label + '數量' });
  const form = el('form', { class: 'money' }, el('div', { class: 'money-now' }, el('span', { text: label }), el('b', { text: big(value) })),
    el('div', { class: 'row' }, input, el('button', { class: 'primary', text: '給予' })));
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (await give(input.value.trim())) input.value = '';
  });
  return form;
}

/* ---------- page ---------- */
function renderPlayer() {
  pEntries = new Map([...pEntries].filter(([key]) => key[0] === 'g'));
  $('player-tip').hidden = true;
  $('player-tip').dataset.key = '';
  const state = P.online ? el('span', { class: 'pill good', text: `線上・頻道 ${P.channel || '轉換中'}` }) : el('span', { class: 'pill', text: '離線' });
  $('player-head').replaceChildren(
    el('button', { class: 'link', type: 'button', text: '← 玩家列表', onclick: closePlayer }),
    el('strong', { class: 'player-name', text: P.name }), el('span', { class: 'muted', text: `Lv.${P.level}・職業 ${P.job}・帳號 ${P.account}` }), state,
    el('span', { class: 'spacer' }), el('button', { type: 'button', text: '重新整理', onclick: () => openPlayer(P.id).catch(fail) }));
  $('player-notice').textContent = P.notice || (P.online
    ? '玩家在線上：修改會立刻出現在對方的遊戲畫面，不用重新登入。'
    : '玩家離線中：修改直接寫入存檔，下次登入就會看到。');
  $('player-notice').className = P.notice ? 'banner' : 'muted';
  $('player-windows').replaceChildren(equipWindow(), bagWindow());
  $('player-money').replaceChildren(
    moneyRow('楓幣', P.mesos, (amount) => edit({ kind: 'give_mesos', amount }, '已調整楓幣')),
    moneyRow('Gash', P.gash, (amount) => send('/api/character/wallet', { currency: 'gash', amount }, '已調整 Gash')),
    moneyRow('楓點', P.points, (amount) => send('/api/character/wallet', { currency: 'points', amount }, '已調整楓點')));
}
function setPlayer(data) {
  const json = JSON.stringify(data);
  const same = json === pJSON;
  P = data;
  pJSON = json;
  if (!same) renderPlayer();
}
async function openPlayer(id) {
  const data = await api('/api/character?id=' + id);
  const fresh = !P || P.id !== data.id;
  if (fresh) {
    pBag = 1; pCashWorn = false; pPick = null; pJSON = '';
    $('give-query').value = '';
    $('give-results').replaceChildren();
    renderPick();
  }
  setPlayer(data);
  $('player-list').hidden = true;
  $('player-detail').hidden = false;
}
function closePlayer() {
  P = null;
  $('player-detail').hidden = true;
  $('player-list').hidden = false;
  loadPlayers().catch(fail);
}
// A typed-in amount must survive the refresh, so it waits while a field is in use.
const typing = () => { const a = document.activeElement; return a && $('player-detail').contains(a) && a.tagName === 'INPUT' && a.value !== ''; };
setInterval(() => {
  if (P && visible('players') && !pBusy && !$('player-dialog').open && !typing()) {
    api('/api/character?id=' + P.id).then((data) => { if (P && P.id === data.id && !pBusy) setPlayer(data); }).catch(() => {});
  }
}, 3000);

$('player-detail').addEventListener('mouseover', hoverTip);
$('player-detail').addEventListener('mousemove', moveTip);
$('player-detail').addEventListener('mouseleave', () => { $('player-tip').hidden = true; });
$('player-dialog').addEventListener('click', (event) => { if (event.target === $('player-dialog')) closeEditor(); });
$('give-query').addEventListener('input', () => {
  clearTimeout(pSearchTimer);
  pSearchTimer = setTimeout(() => searchItems().catch(fail), 250);
});
$('give-search').addEventListener('submit', (event) => { event.preventDefault(); searchItems().catch(fail); });
