// Converts Legacy scripts/portal/<name>.js sources into Lua for scripts/portal/.
// The legacy enter(pi) function and its helpers are kept statement for
// statement; scripts/legacy/portal_driver.lua supplies `pi` and runs enter.
//
// Only a known JavaScript subset and a fixed list of pi calls are accepted.
// Anything else is reported, recorded with its reason in
// scripts/legacy-portals.json, and left unconverted for manual review, so a GM
// stepping on such a portal is told why it does nothing.
//
// Reviewed (hand-written or earlier-generated) portal sources are never
// overwritten: only files that already carry the legacy marker are replaced.
//
//   npm install acorn            (any directory on NODE_PATH)
//   node tools/migrate-portal-scripts.mjs <Legacy scripts/portal dir> [names...]
import { createRequire } from 'node:module'
import { createHash } from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const acorn = require('acorn')
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(root, 'scripts', 'portal')
const manifestPath = path.join(root, 'scripts', 'legacy-portals.json')
const MARKER = '-- legacy-portal-script'

// pi call -> [minimum arguments, maximum arguments]. Mirrors portal_driver.lua.
const PI = {
  warp: [1, 2], warpS: [2, 2], playPortalSE: [0, 0], playerMessage: [1, 2],
  getMapId: [0, 0], getPlayer: [0, 0], getPortal: [0, 0], getMap: [1, 1],
  getQuestStatus: [1, 1], isQuestActive: [1, 1], isQuestFinished: [1, 1], isQuestCompleted: [1, 1],
  haveItem: [1, 2], itemQuantity: [1, 1], canHold: [1, 2],
  gainItem: [2, 2], removeAll: [1, 1], gainExp: [1, 1], gainMeso: [1, 1],
  forceStartQuest: [1, 2], forceCompleteQuest: [1, 1],
  getPlayerCount: [1, 1], openNpc: [1, 1],
  getSavedLocation: [1, 1], saveLocation: [1, 1], clearSavedLocation: [1, 1],
  inFreeMarket: [0, 0], inArdentmill: [0, 0],
  getJob: [0, 0], getLevel: [0, 0], getMonsterCount: [0, 0],
  // Presentation only: the driver accepts and shows nothing for these.
  ShowWZEffect: [1, 2], showWZEffect: [1, 2], showInstruction: [1, 3], AranTutInstructionalBubble: [1, 1],
  showEffect: [1, 2], EnableUI: [1, 1], DisableUI: [1, 1], lockUI: [0, 1], unlockUI: [0, 0], environmentChange: [1, 2],
  playSound: [1, 2], showMapEffect: [1, 1], summonMsg: [1, 1],
}
// pi.getPlayer().<call>
const PLAYER = {
  dropMessage: [1, 2], message: [1, 2], getMapId: [0, 0], getJob: [0, 0], getLevel: [0, 0], getGender: [0, 0],
  changeMap: [1, 2], getMeso: [0, 0],
}
// Methods allowed on values the driver returns (map handles, portal handles,
// pi.getPortal()), and on strings for Java equals.
const OBJECT = { getPortal: [1, 1], getId: [0, 0], getName: [0, 0], getPortalName: [0, 0], getMapId: [0, 0] }
// Legacy SavedLocationType names; portal_driver.lua keeps the same table.
const SAVED = new Set(['FREE_MARKET', 'MULUNG_TC', 'WORLDTOUR', 'FLORINA', 'FISHING', 'RICHIE', 'DONGDONGCHIANG', 'EVENT', 'AMORIA', 'CHRISTMAS', 'ARDENTMILL', 'Gachapon'])
const BOOLEAN_PI = new Set(['haveItem', 'canHold', 'isQuestActive', 'isQuestFinished', 'isQuestCompleted'])
const RESERVED = new Set(['and', 'break', 'do', 'else', 'elseif', 'end', 'false', 'for', 'function', 'goto', 'if', 'in', 'local', 'nil', 'not', 'or', 'repeat', 'return', 'then', 'true', 'until', 'while', 'global',
  'pi', 'portal', 'math', 'string', 'table', 'tostring', 'tonumber', 'pairs', 'ipairs', 'select', 'next', 'error', 'assert', 'pcall', 'type'])

class Unsupported extends Error {}
const fail = (node, why) => { throw new Unsupported(`${why} (line ${node?.loc?.start?.line ?? '?'})`) }

function convert(source, file) {
  const ast = acorn.parse(source, { ecmaVersion: 5, locations: true })
  const functions = new Set()
  for (const node of ast.body) if (node.type === 'FunctionDeclaration') functions.add(node.id.name)
  if (!functions.has('enter')) fail(null, 'no enter function')
  const enterNode = ast.body.find((n) => n.type === 'FunctionDeclaration' && n.id.name === 'enter')
  const piName = enterNode.params[0]?.name ?? 'pi'
  let temp = 0

  const name = (n) => {
    if (n === piName) return 'pi'
    if (n === 'undefined') return 'nil'
    if (n.startsWith('__')) fail(null, `reserved identifier ${n}`)
    return RESERVED.has(n) && !functions.has(n) ? n + '_' : n
  }
  const text = (value) => '"' + value.replace(/[\\"\x00-\x1f\x7f]/g, (c) => ({ '\\': '\\\\', '"': '\\"', '\n': '\\n', '\r': '\\r', '\t': '\\t' }[c] ?? '\\' + String(c.charCodeAt(0)).padStart(3, '0'))) + '"'
  const isPi = (node) => node.type === 'Identifier' && (node.name === piName || node.name === 'pi')
  const piCall = (node) => node.type === 'CallExpression' && node.callee.type === 'MemberExpression' && !node.callee.computed && isPi(node.callee.object) ? node.callee.property.name : null
  const arity = (table, method, args, node, label) => {
    const a = table[method]
    if (!a) fail(node, `${label}.${method} is not supported`)
    if (args.length < a[0] || args.length > a[1]) fail(node, `${label}.${method} with ${args.length} arguments`)
  }

  const isBoolean = (node) => {
    switch (node.type) {
      case 'LogicalExpression': return true
      case 'UnaryExpression': return node.operator === '!'
      case 'BinaryExpression': return ['==', '===', '!=', '!==', '<', '<=', '>', '>='].includes(node.operator)
      case 'Literal': return typeof node.value === 'boolean'
      case 'CallExpression': return BOOLEAN_PI.has(piCall(node)) || (node.callee.type === 'MemberExpression' && node.callee.property.name === 'equals')
    }
    return false
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
    return isPi(n) ? parts : null
  }
  const special = (node) => {
    const parts = chain(node)
    if (!parts) {
      const c = node.callee
      if (c.type === 'MemberExpression' && !c.computed && c.object.type === 'Identifier' && c.object.name === 'Math' && ['random', 'floor', 'ceil', 'abs', 'max', 'min'].includes(c.property.name)) {
        return `math.${c.property.name}(${node.arguments.map(expr).join(', ')})`
      }
      return null
    }
    const sig = parts.map(([m, a]) => `${m}/${a.length}`).join('.')
    const arg = (i, j = 0) => expr(parts[i][1][j])
    switch (sig) {
      case 'getClient/0.getChannelServer/0.getMapFactory/0.getMap/1': return `pi.getMap(${arg(3)})`
      case 'getMap/0.getAllMonstersThreadsafe/0.size/0': return 'pi.getMonsterCount()'
      case 'getMap/0.getNumMonsters/0': return 'pi.getMonsterCount()'
      case 'getMap/0.getCharactersSize/0': return 'pi.getPlayerCount(pi.getMapId())'
      case 'getMap/1.getCharactersSize/0': return `pi.getPlayerCount(${arg(0)})`
      case 'getMap/0.getId/0': return 'pi.getMapId()'
      case 'getPlayer/0.getMap/0.getId/0': return 'pi.getMapId()'
      case 'getPlayer/0.getMapId/0': return 'pi.getMapId()'
    }
    return null
  }

  const call = (node) => {
    const fixed = special(node)
    if (fixed) return fixed
    const args = node.arguments.map(expr)
    const callee = node.callee
    const method = piCall(node)
    if (method) {
      arity(PI, method, args, node, 'pi')
      if (/SavedLocation$|^saveLocation$/.test(method)) {
        const where = node.arguments[0]
        if (where.type !== 'Literal' || !SAVED.has(where.value)) fail(node, `saved location ${where.value ?? '(computed)'} is not a Legacy SavedLocationType`)
      }
      return `pi.${method}(${args.join(', ')})`
    }
    if (callee.type === 'MemberExpression' && !callee.computed) {
      const prop = callee.property.name
      if (callee.object.type === 'CallExpression' && piCall(callee.object) === 'getPlayer' && callee.object.arguments.length === 0) {
        arity(PLAYER, prop, args, node, 'pi.getPlayer()')
        return `pi.getPlayer().${prop}(${args.join(', ')})`
      }
      if (isPi(callee.object)) fail(node, `pi.${prop} is not supported`)
      if (prop === 'equals' && args.length === 1) return `(${expr(callee.object)} == ${args[0]})`
      const objectCall = callee.object.type === 'CallExpression' && ['getMap', 'getPortal'].includes(piCall(callee.object))
      const objectVar = callee.object.type === 'Identifier' && !functions.has(callee.object.name)
      if (objectCall || objectVar || (callee.object.type === 'CallExpression' && callee.object.callee.type === 'MemberExpression' && callee.object.callee.property.name === 'getPortal')) {
        arity(OBJECT, prop, args, node, 'object')
        return `${expr(callee.object)}.${prop}(${args.join(', ')})`
      }
      fail(node, `call .${prop} is not supported`)
    }
    if (callee.type === 'Identifier' && functions.has(callee.name)) return `${name(callee.name)}(${args.join(', ')})`
    fail(node, 'unsupported call')
  }

  const expr = (node) => {
    switch (node.type) {
      case 'Literal':
        if (node.value === null) return 'nil'
        if (typeof node.value === 'string') return text(node.value)
        if (typeof node.value === 'number' || typeof node.value === 'boolean') return String(node.value)
        return fail(node, 'unsupported literal')
      case 'Identifier': return name(node.name)
      case 'CallExpression': return call(node)
      case 'UnaryExpression':
        if (node.operator === '!') return `(not ${cond(node.argument)})`
        if (node.operator === '-') return `(-${expr(node.argument)})`
        return fail(node, `unary ${node.operator}`)
      case 'LogicalExpression':
        return `(${cond(node.left)} ${node.operator === '&&' ? 'and' : 'or'} ${cond(node.right)})`
      case 'BinaryExpression': {
        const l = expr(node.left), r = expr(node.right)
        const numeric = (n) => n.type === 'Literal' && typeof n.value === 'number'
        switch (node.operator) {
          case '+': return numeric(node.left) && numeric(node.right) ? `(${l} + ${r})` : `__add(${l}, ${r})`
          case '-': case '*': case '<': case '<=': case '>': case '>=': return `(${l} ${node.operator} ${r})`
          case '/': return `(${l} / ${r})`
          case '%': return `math.fmod(${l}, ${r})`
          case '==': case '===': return `(${l} == ${r})`
          case '!=': case '!==': return `(${l} ~= ${r})`
        }
        return fail(node, `operator ${node.operator}`)
      }
      case 'ConditionalExpression':
        return `(function() if ${cond(node.test)} then return ${expr(node.consequent)} end return ${expr(node.alternate)} end)()`
      case 'MemberExpression':
        return fail(node, `property ${node.property.name ?? '[computed]'}`)
    }
    return fail(node, `expression ${node.type}`)
  }

  const assign = (node) => {
    if (node.left.type !== 'Identifier') fail(node, 'assignment target')
    const target = name(node.left.name), value = expr(node.right)
    switch (node.operator) {
      case '=': return `${target} = ${value}`
      case '+=': return `${target} = __add(${target}, ${value})`
      case '-=': case '*=': return `${target} = ${target} ${node.operator[0]} ${value}`
    }
    return fail(node, `assignment ${node.operator}`)
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

  const block = (nodes, pad, loop) => nodes.flatMap((n) => stmt(n, pad, loop))
  const body = (node, pad, loop) => node.type === 'BlockStatement' ? block(node.body, pad, loop) : stmt(node, pad, loop)

  const stmt = (node, pad, loop) => {
    switch (node.type) {
      case 'EmptyStatement': return []
      case 'BlockStatement': return block(node.body, pad, loop)
      case 'ExpressionStatement': { const line = simple(node.expression); return line === null ? [] : [pad + line] }
      case 'VariableDeclaration':
        return node.declarations.filter((d) => d.init).map((d) => `${pad}${name(d.id.name)} = ${expr(d.init)}`)
      case 'ReturnStatement':
        return [`${pad}do return${node.argument ? ' ' + expr(node.argument) : ''} end`]
      case 'BreakStatement':
        if (!loop) fail(node, 'break outside a loop or a switch tail')
        return [pad + 'break']
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
      case 'WhileStatement':
        return [`${pad}while ${cond(node.test)} do`, ...body(node.body, pad + '    ', true), pad + 'end']
      case 'ForStatement': {
        const lines = []
        if (node.init) lines.push(...(node.init.type === 'VariableDeclaration' ? stmt(node.init, pad, loop) : [pad + simple(node.init)]))
        lines.push(`${pad}while ${node.test ? cond(node.test) : 'true'} do`, ...body(node.body, pad + '    ', true))
        if (node.update) lines.push(pad + '    ' + simple(node.update))
        lines.push(pad + 'end')
        return lines
      }
      case 'SwitchStatement': {
        const key = `__s${++temp}`
        const lines = [`${pad}local ${key} = ${expr(node.discriminant)}`]
        let tests = [], first = true, fallback = null
        node.cases.forEach((c, index) => {
          if (c.test) tests.push(`${key} == ${expr(c.test)}`)
          else tests.push(null)
          if (c.consequent.length === 0 && index < node.cases.length - 1) return
          const statements = [...c.consequent]
          const last = statements[statements.length - 1]
          if (last && last.type === 'BreakStatement') statements.pop()
          else if (index < node.cases.length - 1 && !(last && last.type === 'ReturnStatement')) fail(c, 'switch case falls through')
          const inner = block(statements, pad + '    ', false)
          if (tests.includes(null)) {
            if (tests.length > 1 || fallback) fail(c, 'default shares a switch case')
            fallback = inner
          } else {
            lines.push(`${pad}${first ? 'if' : 'elseif'} ${tests.join(' or ')} then`, ...inner)
            first = false
          }
          tests = []
        })
        if (first) fail(node, 'switch without cases')
        if (fallback) lines.push(`${pad}else`, ...fallback)
        lines.push(pad + 'end')
        return lines
      }
      case 'ContinueStatement': return fail(node, 'continue')
      case 'TryStatement':
        // A host refusal ends the invocation (the GM sees why); the legacy
        // catch only printed the Java exception, so the handler is dropped.
        if (node.finalizer) fail(node, 'try/finally')
        return block(node.block.body, pad, loop)
    }
    return fail(node, `statement ${node.type}`)
  }

  const locals = (node, found = new Set()) => {
    if (!node || typeof node.type !== 'string') return found
    if (node.type === 'FunctionDeclaration' || node.type === 'FunctionExpression') return found
    if (node.type === 'VariableDeclaration') for (const d of node.declarations) found.add(name(d.id.name))
    for (const key of Object.keys(node)) {
      const value = node[key]
      if (Array.isArray(value)) value.forEach((v) => locals(v, found))
      else if (value && typeof value === 'object' && key !== 'loc') locals(value, found)
    }
    return found
  }

  const out = []
  for (const node of ast.body) {
    switch (node.type) {
      case 'EmptyStatement': break
      case 'VariableDeclaration':
        for (const d of node.declarations) {
          if (d.init && d.init.type !== 'Literal' && !(d.init.type === 'UnaryExpression' && d.init.argument.type === 'Literal')) fail(d, 'top-level variable with a computed value')
          if (d.init) out.push(`${name(d.id.name)} = ${expr(d.init)}`)
        }
        break
      case 'ExpressionStatement':
        if (simple(node.expression) !== null) fail(node, 'top-level statement')
        break
      case 'FunctionDeclaration': {
        const params = node.params.map((p) => name(p.name))
        const declared = [...locals(node.body)].filter((n) => !params.includes(n))
        out.push('', `function ${name(node.id.name)}(${params.join(', ')})`)
        if (declared.length) out.push(`    local ${declared.join(', ')}`)
        out.push(...block(node.body.body, '    ', false), 'end')
        break
      }
      default: fail(node, `top-level ${node.type}`)
    }
  }
  const sha = createHash('sha256').update(source).digest('hex')
  return [
    MARKER,
    `-- Generated by tools/migrate-portal-scripts.mjs from Legacy scripts/portal/${file}.`,
    `-- Original SHA256: ${sha}.`,
    '-- The legacy enter(pi) function is kept as written;',
    '-- scripts/legacy/portal_driver.lua supplies pi and runs it.',
    ...out,
    '',
  ].join('\n')
}

const [dir, ...only] = process.argv.slice(2)
if (!dir) { console.error('usage: migrate-portal-scripts.mjs <Legacy scripts/portal dir> [names...]'); process.exit(2) }
const names = fs.readdirSync(dir).filter((f) => f.endsWith('.js')).map((f) => f.slice(0, -3)).filter((n) => !only.length || only.includes(n)).sort()
const manifest = [], rejected = []
for (const name of names) {
  const target = path.join(outDir, `${name}.lua`)
  if (!/^[A-Za-z0-9_]+$/.test(name)) { rejected.push([name, 'file name is not a portal script name']); continue }
  if (fs.existsSync(target) && !fs.readFileSync(target, 'utf8').startsWith(MARKER)) continue // reviewed source
  const raw = fs.readFileSync(path.join(dir, `${name}.js`))
  let source = raw.toString('utf8')
  if (!Buffer.from(source, 'utf8').equals(raw)) {
    // A few Legacy portal files are Korean (EUC-KR) originals.
    try { source = new TextDecoder('euc-kr', { fatal: true }).decode(raw) } catch { rejected.push([name, 'source is neither UTF-8 nor EUC-KR']); continue }
  }
  try {
    const lua = convert(source.replace(/^﻿/, ''), `${name}.js`)
    fs.writeFileSync(target, lua)
    manifest.push({ legacy: `portal/${name}.js`, source_sha256: createHash('sha256').update(raw).digest('hex'), lua: `portal/${name}.lua`, lua_sha256: createHash('sha256').update(lua).digest('hex') })
  } catch (error) {
    if (!(error instanceof Unsupported) && !(error instanceof SyntaxError)) throw error
    if (fs.existsSync(target)) fs.unlinkSync(target)
    rejected.push([name, error.message])
  }
}
if (!only.length) {
  const pending = rejected.map(([name, reason]) => ({ legacy: `portal/${name}.js`, reason }))
  fs.writeFileSync(manifestPath, JSON.stringify({ scripts: manifest, pending }, null, 2) + '\n')
}
console.log(`converted ${manifest.length}, rejected ${rejected.length}`)
const reasons = new Map()
for (const [, why] of rejected) { const k = why.replace(/ \(line .*\)$/, ''); reasons.set(k, (reasons.get(k) ?? 0) + 1) }
for (const [why, n] of [...reasons].sort((a, b) => b[1] - a[1])) console.log(`  ${n}\t${why}`)
