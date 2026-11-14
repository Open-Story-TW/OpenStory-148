// Converts Legacy scripts/npc/<id>.js start/action state machines into Lua
// sources for scripts/npc/. The legacy control flow is kept statement for
// statement; scripts/legacy/npc_driver.lua supplies `cm` and runs it.
//
// Only a known JavaScript subset and a fixed list of cm calls are accepted.
// Anything else is reported, recorded with its reason in
// scripts/legacy-npcs.json, and left unconverted for manual review, so a GM
// talking to such an NPC is told why it does nothing.
//
// Reviewed (hand-written) NPC sources are never overwritten: only files that
// already carry the legacy marker are replaced.
//
// English dialogue is replaced with the Traditional Chinese text recorded in
// tools/npc-zh.json ({ "<id>": { "<source string>": "<Chinese string>" } }).
// A string without an entry is kept as written.
//
//   npm install acorn            (any directory on NODE_PATH)
//   node tools/migrate-npc-scripts.mjs <Legacy scripts/npc dir> [ids...]
//   node tools/migrate-npc-scripts.mjs --strings <out.json> <Legacy scripts/npc dir>
//       writes the dialogue strings of every convertible script that still
//       have no Chinese text, and changes nothing else.
import { createRequire } from 'node:module'
import { createHash } from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const acorn = require('acorn')
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(root, 'scripts', 'npc')
const manifestPath = path.join(root, 'scripts', 'legacy-npcs.json')
const zhPath = path.join(root, 'tools', 'npc-zh.json')
const MARKER = '-- legacy-npc-script'
// The engine refuses a source above this size; the driver is appended to it.
const MAX_SOURCE = 65536

// cm call -> [minimum arguments, maximum arguments]. Mirrors npc_driver.lua.
const CM = {
  sendNext: [1, 1], sendNextPrev: [1, 1], sendPrev: [1, 1], sendOk: [1, 1], sendYesNo: [1, 1],
  sendAcceptDecline: [1, 1], askAcceptDecline: [1, 1], sendSimple: [1, 1],
  sendNextS: [2, 2], sendNextPrevS: [1, 3], sendOkS: [2, 2], sendPlayerToNpc: [1, 1], PlayerToNpc: [1, 1],
  sendNextNoESC: [1, 2], sendGetText: [1, 1], getText: [0, 0], sendGetNumber: [4, 4],
  dispose: [0, 0], safeDispose: [0, 0],
  gainItem: [2, 3], gainMeso: [1, 1], gainExp: [1, 1], removeAll: [1, 1],
  forceStartQuest: [1, 2], forceCompleteQuest: [1, 1],
  warp: [1, 2], saveLocation: [1, 1], clearSavedLocation: [1, 1], getSavedLocation: [1, 1],
  haveItem: [1, 4], itemQuantity: [1, 1], canHold: [1, 2], getMeso: [0, 0], getMapId: [0, 0],
  getJob: [0, 0], getLevel: [0, 0], getName: [0, 0], getNpc: [0, 0], getPlayerStat: [1, 1],
  getQuestStatus: [1, 1], isQuestActive: [1, 1], isQuestFinished: [1, 1], isQuestCompleted: [1, 1], isQuestStarted: [1, 1],
  getQuestRecord: [1, 1], getPlayer: [0, 0], getChar: [0, 0],
  openShop: [1, 1], sendStorage: [0, 0], playerMessage: [1, 2], isGMS: [0, 0],
  askAvatar: [2, 2], sendStyle: [2, 2], setAvatar: [2, 2], setRandomAvatar: [2, 200],
  getPlayerCount: [1, 1], getChannelNumber: [0, 0], getParty: [0, 0], isLeader: [0, 0], getFame: [0, 0], hasEquipped: [1, 1],
  startQuest: [1, 1], completeQuest: [1, 1], getInfoQuest: [1, 1], updateInfoQuest: [2, 2],
  openNpc: [1, 1], mapMessage: [1, 2], getCurrentTime: [0, 0],
  changeJob: [1, 1], teachSkill: [2, 3], addFame: [1, 1], setHair: [1, 1], setFace: [1, 1], setSkin: [1, 1], gainGachaponItem: [2, 3], saveReturnLocation: [1, 1],
  lockUI: [0, 2], lockKey: [0, 2], EnableUI: [0, 2], DisableUI: [0, 2],
  // Presentation only: the driver accepts and shows nothing for these.
  playSound: [1, 2], showEffect: [1, 2], environmentChange: [1, 2], showWZEffect: [1, 1], ShowWZEffect: [1, 1],
  showMapEffect: [1, 1], summonMsg: [1, 1], playerSummonHint: [1, 1], playMusic: [1, 1], changeMusic: [1, 1],
}
// cm.getPlayer().<call> and cm.getChar().<call>
const PLAYER = {
  getLevel: [0, 0], getJob: [0, 0], getMapId: [0, 0], getMeso: [0, 0], getGender: [0, 0], getName: [0, 0],
  isGM: [0, 0], getGMLevel: [0, 0], dropMessage: [1, 2], itemQuantity: [1, 1], haveItem: [1, 2],
  getHair: [0, 0], getFace: [0, 0], getSkinColor: [0, 0], getFame: [0, 0], getStr: [0, 0], getDex: [0, 0], getInt: [0, 0], getLuk: [0, 0],
  getHp: [0, 0], getMp: [0, 0], getRemainingSp: [0, 0], getRemainingAp: [0, 0], getSubcategory: [0, 0], hasEquipped: [1, 1],
  getParty: [0, 0], changeMap: [1, 2],
  getJobId: [0, 0], getAllSkillLevels: [0, 0], addFame: [1, 1], updateFame: [0, 0], gainSP: [1, 2], resetSP: [1, 1], changeJob: [1, 1],
}
// Methods allowed on values the driver returns (quest records).
const OBJECT = {
  getCustomData: [0, 0], setCustomData: [1, 1],
  // new Date()
  getFullYear: [0, 0], getYear: [0, 0], getMonth: [0, 0], getDate: [0, 0], getDay: [0, 0], getHours: [0, 0], getMinutes: [0, 0], getSeconds: [0, 0], getTime: [0, 0],
}
const STATS = new Set(['LVL', 'GENDER', 'GM', 'ADMIN', 'HAIR', 'FACE', 'SKIN', 'RSP', 'RAP', 'FAME', 'STR', 'DEX', 'INT', 'LUK', 'HP', 'MP', 'MAXHP', 'MAXMP'])
// Legacy SavedLocationType names; npc_driver.lua keeps the same table.
const SAVED = new Set(['FREE_MARKET', 'MULUNG_TC', 'WORLDTOUR', 'FLORINA', 'FISHING', 'RICHIE', 'DONGDONGCHIANG', 'EVENT', 'AMORIA', 'CHRISTMAS', 'ARDENTMILL', 'Gachapon'])
const BOOLEAN_CM = new Set(['haveItem', 'canHold', 'isQuestActive', 'isQuestFinished', 'isQuestCompleted', 'isQuestStarted', 'isGMS', 'isLeader', 'hasEquipped'])
const MATH = { pow: [2, 2], random: [0, 0], floor: [1, 1], ceil: [1, 1], abs: [1, 1], max: [2, 8], min: [2, 8], round: [1, 1] }
const RESERVED = new Set(['and', 'break', 'do', 'else', 'elseif', 'end', 'false', 'for', 'function', 'goto', 'if', 'in', 'local', 'nil', 'not', 'or', 'repeat', 'return', 'then', 'true', 'until', 'while', 'global',
  'cm', 'npc', 'math', 'string', 'table', 'utf8', 'tostring', 'tonumber', 'pairs', 'ipairs', 'select', 'next', 'error', 'assert', 'pcall', 'type'])

// Private-server salons whose dialogue cannot be published in this repository.
const LEFT_OUT = new Set(['9901009', '9930107', '9930108'])

class Unsupported extends Error {}
const fail = (node, why) => { throw new Unsupported(`${why} (line ${node?.loc?.start?.line ?? '?'})`) }
// A string a player reads: it has a Latin word and no Chinese yet.
const needsChinese = (s) => /[A-Za-z]{2}/.test(s.replace(/#[a-zA-Z]\d*#?|\\[rnt]/g, ' ')) && !/[㐀-鿿]/.test(s)

function convert(source, id, zh, strings) {
  const ast = acorn.parse(source, { ecmaVersion: 5, locations: true })
  const functions = new Set()
  for (const node of ast.body) if (node.type === 'FunctionDeclaration') functions.add(node.id.name)
  if (!functions.has('start') && !functions.has('action')) fail(null, 'no start or action function')
  let temp = 0
  // Every name the source declares or assigns. Reading any other name threw a
  // ReferenceError in Legacy, so such a script is left for review.
  const known = new Set(['undefined'])
  const scan = (node) => {
    if (!node || typeof node.type !== 'string') return
    if (node.type === 'VariableDeclarator' && node.id.type === 'Identifier') known.add(node.id.name)
    if (node.type === 'FunctionDeclaration') { known.add(node.id.name); for (const q of node.params) known.add(q.name) }
    if (node.type === 'AssignmentExpression' && node.left.type === 'Identifier') known.add(node.left.name)
    for (const key of Object.keys(node)) {
      const value = node[key]
      if (Array.isArray(value)) value.forEach(scan)
      else if (value && typeof value === 'object' && key !== 'loc') scan(value)
    }
  }
  scan(ast)
  // Strings the script compares or stores are data, not dialogue: a value that
  // appears in such a place is never replaced with Chinese text anywhere.
  const data = new Set()
  const literal = (n) => { if (n && n.type === 'Literal' && typeof n.value === 'string') data.add(n.value) }
  const mark = (node) => {
    if (!node || typeof node.type !== 'string') return
    if (node.type === 'BinaryExpression' && ['==', '===', '!=', '!=='].includes(node.operator)) { literal(node.left); literal(node.right) }
    if (node.type === 'SwitchCase') literal(node.test)
    if (node.type === 'CallExpression' && node.callee.type === 'MemberExpression' && !node.callee.computed &&
      ['equals', 'equalsIgnoreCase', 'indexOf', 'contains', 'updateInfoQuest', 'saveReturnLocation', 'setCustomData', 'forceStartQuest', 'getPlayerStat', 'saveLocation', 'getSavedLocation', 'clearSavedLocation', 'playSound', 'showEffect', 'environmentChange', 'showWZEffect', 'ShowWZEffect', 'showMapEffect', 'playMusic', 'changeMusic'].includes(node.callee.property.name)) node.arguments.forEach(literal)
    for (const key of Object.keys(node)) {
      const value = node[key]
      if (Array.isArray(value)) value.forEach(mark)
      else if (value && typeof value === 'object' && key !== 'loc') mark(value)
    }
  }
  mark(ast)

  const name = (n) => {
    if (n === 'undefined') return 'nil'
    if (n.startsWith('__')) fail(null, `reserved identifier ${n}`)
    return RESERVED.has(n) && !functions.has(n) ? n + '_' : n
  }
  const text = (value) => {
    if (needsChinese(value) && !data.has(value)) {
      if (zh[value] !== undefined) value = zh[value]
      else strings.add(value)
    }
    return '"' + value.replace(/[\\"\x00-\x1f\x7f]/g, (c) => ({ '\\': '\\\\', '"': '\\"', '\n': '\\n', '\r': '\\r', '\t': '\\t' }[c] ?? '\\' + String(c.charCodeAt(0)).padStart(3, '0'))) + '"'
  }
  const isCm = (node) => node.type === 'Identifier' && node.name === 'cm'
  const cmCall = (node) => node.type === 'CallExpression' && node.callee.type === 'MemberExpression' && !node.callee.computed && isCm(node.callee.object) ? node.callee.property.name : null
  const arity = (table, method, args, node, label) => {
    const a = Object.hasOwn(table, method) ? table[method] : null
    if (!a) fail(node, `${label}.${method} is not supported`)
    if (args.length < a[0] || args.length > a[1]) fail(node, `${label}.${method} with ${args.length} arguments`)
  }

  const isBoolean = (node) => {
    switch (node.type) {
      case 'LogicalExpression': return true
      case 'UnaryExpression': return node.operator === '!'
      case 'BinaryExpression': return ['==', '===', '!=', '!==', '<', '<=', '>', '>=', 'instanceof'].includes(node.operator)
      case 'Literal': return typeof node.value === 'boolean'
      case 'CallExpression': return BOOLEAN_CM.has(cmCall(node)) || (node.callee.type === 'MemberExpression' && !node.callee.computed && ['equals', 'equalsIgnoreCase', 'isEmpty', 'contains'].includes(node.callee.property.name))
    }
    return false
  }
  // A + that JavaScript is certain to evaluate as string concatenation.
  const stringy = (node) => {
    switch (node.type) {
      case 'Literal': return typeof node.value === 'string'
      case 'BinaryExpression': return node.operator === '+' && (stringy(node.left) || stringy(node.right))
      case 'ConditionalExpression': return stringy(node.consequent) && stringy(node.alternate)
    }
    return false
  }
  const piece = (node) => {
    if (stringy(node)) return expr(node)
    if (node.type === 'Literal' && typeof node.value === 'number') return text(String(node.value))
    return `__text(${expr(node)})`
  }
  const cond = (node) => isBoolean(node) ? expr(node) : `__truthy(${expr(node)})`

  // Fixed Java chains that have a direct driver equivalent.
  const chain = (node) => {
    const parts = []
    let n = node
    while (n.type === 'CallExpression' && n.callee.type === 'MemberExpression' && !n.callee.computed) {
      parts.unshift([n.callee.property.name, n.arguments])
      n = n.callee.object
    }
    return isCm(n) ? parts : null
  }
  const special = (node) => {
    const parts = chain(node)
    if (!parts) {
      const c = node.callee
      const javaMath = (o) => o.type === 'MemberExpression' && !o.computed && o.property.name === 'Math' && o.object.type === 'MemberExpression' && !o.object.computed &&
        o.object.property.name === 'lang' && o.object.object.type === 'Identifier' && o.object.object.name === 'java'
      if (c.type === 'MemberExpression' && !c.computed && (c.object.type === 'Identifier' && c.object.name === 'Math' || javaMath(c.object))) {
        arity(MATH, c.property.name, node.arguments, node, 'Math')
        const args = node.arguments.map(expr).join(', ')
        if (c.property.name === 'round') return `math.floor(${args} + 0.5)`
        if (c.property.name === 'pow') return `((${expr(node.arguments[0])}) ^ (${expr(node.arguments[1])}))`
        return `math.${c.property.name}(${args})`
      }
      if (c.type === 'Identifier' && c.name === 'parseInt' && node.arguments.length >= 1 && node.arguments.length <= 2) return `__int(${expr(node.arguments[0])})`
      if (c.type === 'Identifier' && (c.name === 'Array' || c.name === 'Array') && !functions.has('Array')) return array(node.arguments, node)
      return null
    }
    const sig = parts.map(([m, a]) => `${m}/${a.length}`).join('.')
    switch (sig) {
      case 'getMap/0.getId/0':
      case 'getPlayer/0.getMap/0.getId/0':
      case 'getChar/0.getMap/0.getId/0':
      case 'getChar/0.getMapId/0':
      case 'getPlayer/0.getMapId/0': return 'cm.getMapId()'
      case 'getClient/0.getChannel/0':
      case 'getPlayer/0.getClient/0.getChannel/0':
      case 'getChar/0.getClient/0.getChannel/0': return 'cm.getChannelNumber()'
      case 'getMap/0.getCharactersSize/0':
      case 'getPlayer/0.getMap/0.getCharactersSize/0': return 'cm.getPlayerCount(cm.getMapId())'
      case 'getMap/1.getCharactersSize/0': return `cm.getPlayerCount(${expr(parts[0][1][0])})`
    }
    return null
  }
  const array = (elements, node) => {
    if (elements.length === 1 && elements[0] && elements[0].type === 'Literal' && typeof elements[0].value === 'number' && node.type !== 'ArrayExpression') fail(node, 'Array(length)')
    if (elements.some((e) => e === null || e.type === 'SpreadElement')) fail(node, 'array hole')
    return `__array(${elements.length}, {${elements.map(expr).join(', ')}})`
  }

  const call = (node) => {
    const fixed = special(node)
    if (fixed) return fixed
    const callee = node.callee
    const method = cmCall(node)
    if (method) {
      arity(CM, method, node.arguments, node, 'cm')
      if (/SavedLocation$|^saveLocation$|^saveReturnLocation$/.test(method)) {
        const where = node.arguments[0]
        if (where.type !== 'Literal' || !SAVED.has(where.value)) fail(node, `saved location ${where.value ?? '(computed)'} is not a Legacy SavedLocationType`)
        return `cm.${method}("${where.value}")`
      }
      if (method === 'getPlayerStat') {
        const stat = node.arguments[0]
        if (stat.type !== 'Literal' || !STATS.has(stat.value)) fail(node, `cm.getPlayerStat(${stat.value ?? 'computed'}) is not supported`)
        return `cm.getPlayerStat("${stat.value}")`
      }
      if (method === 'haveItem' && node.arguments.length > 2) {
        // (id, quantity, checkEquipped, greaterOrEquals): only the "at least" form.
        const exact = node.arguments[3]
        if (node.arguments.length !== 4 || node.arguments[2].type !== 'Literal' || exact.type !== 'Literal' || exact.value !== true) fail(node, 'cm.haveItem with an exact-quantity check')
        return `cm.haveItem(${expr(node.arguments[0])}, ${expr(node.arguments[1])})`
      }
      if (['sendNextPrevS', 'sendNextNoESC'].includes(method) && node.arguments.length === CM[method][1]) {
        // The last argument names another NPC's portrait, which is not shown.
        return `cm.${method}(${node.arguments.slice(0, -1).map(expr).join(', ')})`
      }
      // The third argument is the map name Legacy announced the prize with.
      if (method === 'gainGachaponItem') return `cm.gainGachaponItem(${node.arguments.slice(0, 2).map(expr).join(', ')})`
      if (method === 'gainItem' && node.arguments.length === 3 && !(node.arguments[2].type === 'Literal' && typeof node.arguments[2].value === 'boolean')) fail(node, 'cm.gainItem with a period or owner')
      return `cm.${method}(${node.arguments.map(expr).join(', ')})`
    }
    const args = () => node.arguments.map(expr)
    if (callee.type === 'MemberExpression' && !callee.computed) {
      const prop = callee.property.name
      if (callee.object.type === 'CallExpression' && ['getPlayer', 'getChar'].includes(cmCall(callee.object)) && callee.object.arguments.length === 0) {
        arity(PLAYER, prop, node.arguments, node, 'cm.getPlayer()')
        return `cm.getPlayer().${prop}(${args().join(', ')})`
      }
      if (isCm(callee.object)) fail(node, `cm.${prop} is not supported`)
      if (prop === 'equals' && node.arguments.length === 1) return `(${expr(callee.object)} == ${args()[0]})`
      if (prop === 'push' && node.arguments.length === 1) return `__push(${expr(callee.object)}, ${args()[0]})`
      if (prop === 'toString' && node.arguments.length === 0) return `__add("", ${expr(callee.object)})`
      if (prop === 'equalsIgnoreCase' && node.arguments.length === 1) return `__same(${expr(callee.object)}, ${args()[0]})`
      if (prop === 'indexOf' && node.arguments.length === 1) return `__indexof(${expr(callee.object)}, ${args()[0]})`
      if (prop === 'contains' && node.arguments.length === 1) return `(__indexof(${expr(callee.object)}, ${args()[0]}) ~= -1)`
      if (prop === 'substring' && node.arguments.length >= 1 && node.arguments.length <= 2) return `__substring(${expr(callee.object)}, ${args().join(', ')})`
      if (prop === 'length' && node.arguments.length === 0) return `__len(${expr(callee.object)})`
      if (prop === 'isEmpty' && node.arguments.length === 0) return `(__len(${expr(callee.object)}) == 0)`
      const objectCall = callee.object.type === 'CallExpression' && cmCall(callee.object) === 'getQuestRecord'
      const objectVar = callee.object.type === 'Identifier' && !functions.has(callee.object.name) && !/^[A-Z]/.test(callee.object.name)
      if (objectCall || objectVar) {
        arity(OBJECT, prop, node.arguments, node, 'object')
        return `${expr(callee.object)}.${prop}(${args().join(', ')})`
      }
      fail(node, `call .${prop} is not supported`)
    }
    if (callee.type === 'Identifier' && functions.has(callee.name)) return `${name(callee.name)}(${args().join(', ')})`
    fail(node, `unsupported call${callee.type === 'Identifier' ? ' ' + callee.name : ''}`)
  }

  const expr = (node) => {
    switch (node.type) {
      case 'Literal':
        if (node.value === null) return 'nil'
        if (typeof node.value === 'string') return text(node.value)
        if (typeof node.value === 'number' || typeof node.value === 'boolean') return String(node.value)
        return fail(node, 'unsupported literal')
      case 'Identifier':
        if (node.name === 'cm') fail(node, 'cm used as a value')
        if (!known.has(node.name)) fail(node, `variable ${node.name} is never declared`)
        return name(node.name)
      case 'CallExpression': return call(node)
      case 'NewExpression':
        if (node.callee.type === 'Identifier' && node.callee.name === 'Array') return array(node.arguments, node)
        if (node.callee.type === 'Identifier' && node.callee.name === 'Date' && node.arguments.length === 0) return '__date()'
        return fail(node, `new ${node.callee.name ?? 'object'}`)
      case 'ArrayExpression': return array(node.elements, node)
      case 'UnaryExpression':
        if (node.operator === '!') return `(not ${cond(node.argument)})`
        if (node.operator === '-') return `(-${expr(node.argument)})`
        return fail(node, `unary ${node.operator}`)
      case 'LogicalExpression':
        return `(${cond(node.left)} ${node.operator === '&&' ? 'and' : 'or'} ${cond(node.right)})`
      case 'BinaryExpression': {
        if (node.operator === 'instanceof') {
          if (node.right.type === 'Identifier' && node.right.name === 'Array') return `__isarray(${expr(node.left)})`
          return fail(node, 'operator instanceof')
        }
        if (node.operator === '+' && stringy(node)) return `(${piece(node.left)} .. ${piece(node.right)})`
        const l = expr(node.left), r = expr(node.right)
        const numeric = (n) => n.type === 'Literal' && typeof n.value === 'number'
        switch (node.operator) {
          case '+': return numeric(node.left) && numeric(node.right) ? `(${l} + ${r})` : `__add(${l}, ${r})`
          case '-': case '*': case '/': case '<': case '<=': case '>': case '>=': return `(${l} ${node.operator} ${r})`
          case '%': return `math.fmod(${l}, ${r})`
          case '==': case '===': return `(${l} == ${r})`
          case '!=': case '!==': return `(${l} ~= ${r})`
        }
        return fail(node, `operator ${node.operator}`)
      }
      case 'ConditionalExpression':
        return `(function() if ${cond(node.test)} then return ${expr(node.consequent)} end return ${expr(node.alternate)} end)()`
      case 'MemberExpression':
        if (node.computed) return `${expr(node.object)}[${expr(node.property)}]`
        if (node.property.name === 'length') return `__len(${expr(node.object)})`
        return fail(node, `property ${node.property.name}`)
    }
    return fail(node, `expression ${node.type}`)
  }

  const assign = (node) => {
    const value = expr(node.right)
    if (node.left.type === 'MemberExpression' && node.left.computed) {
      const object = expr(node.left.object), index = expr(node.left.property)
      switch (node.operator) {
        case '=': return `__set(${object}, ${index}, ${value})`
        case '+=': return `__set(${object}, ${index}, __add(${object}[${index}], ${value}))`
        case '-=': case '*=': return `__set(${object}, ${index}, ${object}[${index}] ${node.operator[0]} ${value})`
      }
      return fail(node, `assignment ${node.operator}`)
    }
    if (node.left.type !== 'Identifier') fail(node, 'assignment target')
    const target = name(node.left.name)
    switch (node.operator) {
      case '=': return `${target} = ${value}`
      case '+=': return stringy(node.right) ? `${target} = __text(${target}) .. ${value}` : `${target} = __add(${target}, ${value})`
      case '-=': case '*=': case '/=': return `${target} = ${target} ${node.operator[0]} ${value}`
      case '%=': return `${target} = math.fmod(${target}, ${value})`
    }
    return fail(node, `assignment ${node.operator}`)
  }
  const each = (node, pad) => {
    if (node.type === 'SequenceExpression') return node.expressions.flatMap((e) => each(e, pad))
    if (node.type === 'ConditionalExpression') {
      return [`${pad}if ${cond(node.test)} then`, ...each(node.consequent, pad + '    '), `${pad}else`, ...each(node.alternate, pad + '    '), `${pad}end`]
    }
    const line = simple(node)
    return line === null ? [] : [pad + line]
  }
  const simple = (node) => {
    switch (node.type) {
      case 'CallExpression':
        if (node.callee.type === 'Identifier' && node.callee.name === 'importPackage') return null
        return call(node)
      case 'AssignmentExpression': return assign(node)
      case 'UpdateExpression':
        if (node.argument.type !== 'Identifier') fail(node, 'update target')
        return `${name(node.argument.name)} = ${name(node.argument.name)} ${node.operator[0]} 1`
    }
    return fail(node, `statement expression ${node.type}`)
  }

  // loop is null outside a loop, or { label, used } for the nearest one.
  const block = (nodes, pad, loop) => nodes.flatMap((n) => stmt(n, pad, loop))
  const body = (node, pad, loop) => node.type === 'BlockStatement' ? block(node.body, pad, loop) : stmt(node, pad, loop)
  const declare = (d, pad) => {
    if (d.id.type !== 'Identifier') fail(d, 'destructuring')
    return d.init ? [`${pad}${name(d.id.name)} = ${expr(d.init)}`] : []
  }

  const stmt = (node, pad, loop) => {
    switch (node.type) {
      case 'EmptyStatement': return []
      case 'BlockStatement': return block(node.body, pad, loop)
      case 'ExpressionStatement': return each(node.expression, pad)
      case 'VariableDeclaration': return node.declarations.flatMap((d) => declare(d, pad))
      case 'ReturnStatement':
        return [`${pad}do return${node.argument ? ' ' + expr(node.argument) : ''} end`]
      case 'BreakStatement':
        if (!loop) fail(node, 'break outside a loop or a switch tail')
        if (loop.inSwitch) fail(node, 'break inside a nested switch')
        return [pad + 'break']
      case 'ContinueStatement':
        if (!loop) fail(node, 'continue outside a loop')
        loop.used = true
        return [`${pad}goto ${loop.label}`]
      case 'IfStatement': {
        const lines = [`${pad}if ${cond(node.test)} then`, ...body(node.consequent, pad + '    ', loop)]
        let rest = node.alternate
        while (rest && rest.type === 'IfStatement') {
          lines.push(`${pad}elseif ${cond(rest.test)} then`, ...body(rest.consequent, pad + '    ', loop))
          rest = rest.alternate
        }
        if (rest) lines.push(`${pad}else`, ...body(rest, pad + '    ', loop))
        lines.push(pad + 'end')
        return lines
      }
      case 'WhileStatement': {
        const inner = { label: `__c${++temp}`, used: false }
        const lines = [`${pad}while ${cond(node.test)} do`, ...body(node.body, pad + '    ', inner)]
        if (inner.used) lines.push(`${pad}    ::${inner.label}::`)
        lines.push(pad + 'end')
        return lines
      }
      case 'ForStatement': {
        const inner = { label: `__c${++temp}`, used: false }
        const lines = []
        if (node.init) lines.push(...(node.init.type === 'VariableDeclaration' ? stmt(node.init, pad, loop) : each(node.init, pad)))
        lines.push(`${pad}while ${node.test ? cond(node.test) : 'true'} do`, ...body(node.body, pad + '    ', inner))
        if (inner.used) lines.push(`${pad}    ::${inner.label}::`)
        if (node.update) lines.push(...each(node.update, pad + '    '))
        lines.push(pad + 'end')
        return lines
      }
      case 'SwitchStatement': {
        const key = `__s${++temp}`
        const lines = [`${pad}local ${key} = ${expr(node.discriminant)}`]
        const within = loop ? { label: loop.label, inSwitch: true, get used() { return loop.used }, set used(v) { loop.used = v } } : null
        let tests = [], first = true, fallback = null
        node.cases.forEach((c, index) => {
          if (c.test) tests.push(`${key} == ${expr(c.test)}`)
          else tests.push(null)
          if (c.consequent.length === 0 && index < node.cases.length - 1) return
          let statements = [...c.consequent]
          if (statements.length === 1 && statements[0].type === 'BlockStatement') statements = [...statements[0].body]
          const last = statements[statements.length - 1]
          if (last && last.type === 'BreakStatement') statements.pop()
          else if (index < node.cases.length - 1 && !(last && last.type === 'ReturnStatement')) fail(c, 'switch case falls through')
          const inner = block(statements, pad + '    ', within)
          if (tests.includes(null)) {
            if (tests.length > 1 || fallback) fail(c, 'default shares a switch case')
            fallback = inner
          } else {
            lines.push(`${pad}${first ? 'if' : 'elseif'} ${tests.join(' or ')} then`, ...inner)
            first = false
          }
          tests = []
        })
        if (first) {
          if (!fallback) fail(node, 'switch without cases')
          return [...lines, ...fallback.map((l) => l.slice(4))]
        }
        if (fallback) lines.push(`${pad}else`, ...fallback)
        lines.push(pad + 'end')
        return lines
      }
      case 'TryStatement':
        // A host refusal ends the conversation (the GM sees why); the legacy
        // catch only printed the Java exception, so the handler is dropped.
        if (node.finalizer) fail(node, 'try/finally')
        return block(node.block.body, pad, loop)
    }
    return fail(node, `statement ${node.type}`)
  }

  const locals = (node, found = new Set()) => {
    if (!node || typeof node.type !== 'string') return found
    if (node.type === 'FunctionDeclaration' || node.type === 'FunctionExpression') return found
    if (node.type === 'VariableDeclaration') for (const d of node.declarations) if (d.id.type === 'Identifier') found.add(name(d.id.name))
    for (const key of Object.keys(node)) {
      const value = node[key]
      if (Array.isArray(value)) value.forEach((v) => locals(v, found))
      else if (value && typeof value === 'object' && key !== 'loc') locals(value, found)
    }
    return found
  }

  // Top-level statements run when Legacy evaluated the file, with cm already
  // bound; here they wait in __init until the driver has defined cm.
  const init = [], out = []
  for (const node of ast.body) {
    switch (node.type) {
      case 'EmptyStatement': break
      case 'VariableDeclaration':
        for (const d of node.declarations) init.push(...declare(d, '    '))
        break
      case 'ExpressionStatement': {
        init.push(...each(node.expression, '    '))
        break
      }
      case 'FunctionDeclaration': {
        const params = node.params.map((p) => name(p.name))
        const declared = [...locals(node.body)].filter((n) => !params.includes(n))
        if (declared.length > 150) fail(node, 'too many local variables')
        out.push('', `function ${name(node.id.name)}(${params.join(', ')})`)
        if (declared.length) out.push(`    local ${declared.join(', ')}`)
        out.push(...block(node.body.body, '    ', null), 'end')
        break
      }
      default: fail(node, `top-level ${node.type}`)
    }
  }
  const sha = createHash('sha256').update(source).digest('hex')
  return [
    MARKER,
    `-- Generated by tools/migrate-npc-scripts.mjs from Legacy scripts/npc/${id}.js.`,
    `-- Original SHA256: ${sha}.`,
    '-- The legacy start/action state machine is kept as written;',
    '-- scripts/legacy/npc_driver.lua supplies cm and commits each call atomically.',
    `__npc = ${id}`,
    'function __init()',
    ...init,
    'end',
    ...out,
    '',
  ].join('\n')
}

let argv = process.argv.slice(2), stringsOut = null
if (argv[0] === '--strings') { stringsOut = argv[1]; argv = argv.slice(2) }
const [dir, ...only] = argv
if (!dir) { console.error('usage: migrate-npc-scripts.mjs [--strings out.json] <Legacy scripts/npc dir> [ids...]'); process.exit(2) }
const driverBytes = fs.statSync(path.join(root, 'scripts', 'legacy', 'npc_driver.lua')).size
const zhAll = fs.existsSync(zhPath) ? JSON.parse(fs.readFileSync(zhPath, 'utf8')) : {}
const ids = fs.readdirSync(dir).filter((f) => /^\d+\.js$/.test(f)).map((f) => f.slice(0, -3)).filter((id) => !only.length || only.includes(id)).sort((a, b) => a - b)
const manifest = [], rejected = [], untranslated = {}
for (const id of ids) {
  const target = path.join(outDir, `${id}.lua`)
  if (fs.existsSync(target) && !fs.readFileSync(target, 'utf8').startsWith(MARKER)) continue // reviewed source
  const raw = fs.readFileSync(path.join(dir, `${id}.js`))
  let source = raw.toString('utf8')
  if (!Buffer.from(source, 'utf8').equals(raw)) {
    try { source = new TextDecoder('big5', { fatal: true }).decode(raw) } catch { rejected.push([id, 'source is neither UTF-8 nor Big5']); continue }
  }
  try {
    if (LEFT_OUT.has(id)) throw new Unsupported('left out of this repository')
    const strings = new Set()
    const lua = convert(source.replace(/^﻿/, ''), id, zhAll[id] ?? {}, strings)
    if (Buffer.byteLength(lua) + driverBytes + 1 > MAX_SOURCE) throw new Unsupported('converted source is larger than the engine accepts')
    if (strings.size) untranslated[id] = [...strings]
    if (stringsOut) continue
    fs.writeFileSync(target, lua)
    manifest.push({ legacy: `npc/${id}.js`, source_sha256: createHash('sha256').update(raw).digest('hex'), lua: `npc/${id}.lua`, lua_sha256: createHash('sha256').update(lua).digest('hex') })
  } catch (error) {
    if (!(error instanceof Unsupported) && !(error instanceof SyntaxError)) throw error
    if (!stringsOut && fs.existsSync(target)) fs.unlinkSync(target)
    rejected.push([id, error.message])
  }
}
if (stringsOut) {
  fs.writeFileSync(stringsOut, JSON.stringify(untranslated, null, 1) + '\n')
  console.log(`${Object.keys(untranslated).length} scripts, ${Object.values(untranslated).reduce((n, s) => n + s.length, 0)} strings without Chinese text`)
  process.exit(0)
}
if (!only.length) {
  const pending = rejected.map(([id, reason]) => ({ legacy: `npc/${id}.js`, reason }))
  fs.writeFileSync(manifestPath, JSON.stringify({ scripts: manifest, pending }, null, 2) + '\n')
}
console.log(`converted ${manifest.length}, rejected ${rejected.length}, with English left ${Object.keys(untranslated).length}`)
const reasons = new Map()
for (const [, why] of rejected) { const k = why.replace(/ \(line .*\)$/, ''); reasons.set(k, (reasons.get(k) ?? 0) + 1) }
for (const [why, n] of [...reasons].sort((a, b) => b[1] - a[1])) console.log(`  ${n}\t${why}`)
