// Converts Legacy scripts/quest/<id>.js start/end state machines into Lua
// sources for scripts/quest/. The legacy control flow is kept statement for
// statement; scripts/legacy/quest_driver.lua supplies `qm` and runs it.
//
// Only a known JavaScript subset and a fixed list of qm calls are accepted.
// Anything else is reported and left unconverted for manual review.
//
//   npm install acorn            (any directory on NODE_PATH)
//   node tools/migrate-quest-scripts.mjs <Legacy scripts/quest dir> [ids...]
import { createRequire } from 'node:module'
import { createHash } from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const acorn = require('acorn')
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const outDir = path.join(root, 'scripts', 'quest')
const manifestPath = path.join(root, 'scripts', 'legacy-quests.json')

// qm call -> [minimum arguments, maximum arguments]. Mirrors quest_driver.lua.
const QM = {
  sendNext: [1, 1], sendNextPrev: [1, 1], sendPrev: [1, 1], sendOk: [1, 1], sendYesNo: [1, 1],
  sendAcceptDecline: [1, 1], askAcceptDecline: [1, 1], sendSimple: [1, 1],
  dispose: [0, 0], safeDispose: [0, 0],
  forceStartQuest: [0, 2], forceCompleteQuest: [0, 1],
  gainItem: [2, 2], gainExp: [1, 1], gainMeso: [1, 1],
  haveItem: [1, 2], canHold: [1, 2],
  getQuestStatus: [1, 1], isQuestActive: [1, 1], isQuestFinished: [1, 1],
  getJob: [0, 0], getQuestCustomData: [0, 0], setQuestCustomData: [1, 1], warp: [1, 2],
  sendNextS: [2, 2], sendNextPrevS: [1, 2], sendOkS: [2, 2], sendPlayerToNpc: [1, 1], PlayerToNpc: [1, 1],
  isQuestCompleted: [1, 1], removeAll: [1, 1],
  // Presentation only: the driver accepts and shows nothing for these.
  summonMsg: [1, 1], playerSummonHint: [1, 1], AranTutInstructionalBubble: [1, 1], evanTutorial: [2, 2],
  showWZEffect: [1, 1], ShowWZEffect: [1, 1], MovieClipIntroUI: [1, 1],
}
const BOOLEAN_QM = new Set(['haveItem', 'canHold', 'isQuestActive', 'isQuestFinished', 'isQuestCompleted'])
const PLAYER = new Set(['getJob', 'getLevel'])
const RESERVED = new Set(['and', 'break', 'do', 'else', 'elseif', 'end', 'false', 'for', 'function', 'goto', 'if', 'in', 'local', 'nil', 'not', 'or', 'repeat', 'return', 'then', 'true', 'until', 'while', 'global',
  'qm', 'npc', 'start', 'finish', 'math', 'string', 'table', 'tostring', 'tonumber', 'pairs', 'ipairs', 'select', 'next', 'error', 'assert', 'pcall'])

class Unsupported extends Error {}
const fail = (node, why) => { throw new Unsupported(`${why} (line ${node?.loc?.start?.line ?? '?'})`) }

function convert(source, id) {
  const ast = acorn.parse(source, { ecmaVersion: 5, locations: true })
  const functions = new Set()
  for (const node of ast.body) if (node.type === 'FunctionDeclaration') functions.add(node.id.name)
  let temp = 0

  const name = (n) => {
    if (n === 'end') return 'finish'
    if (n === 'undefined') return 'nil'
    if (n.startsWith('__')) fail(null, `reserved identifier ${n}`)
    return RESERVED.has(n) && !functions.has(n) ? n + '_' : n
  }
  const text = (value) => '"' + value.replace(/[\\"\x00-\x1f\x7f]/g, (c) => ({ '\\': '\\\\', '"': '\\"', '\n': '\\n', '\r': '\\r', '\t': '\\t' }[c] ?? '\\' + String(c.charCodeAt(0)).padStart(3, '0'))) + '"'
  const qmCall = (node) => node.type === 'CallExpression' && node.callee.type === 'MemberExpression' && !node.callee.computed && node.callee.object.type === 'Identifier' && node.callee.object.name === 'qm' ? node.callee.property.name : null

  const isBoolean = (node) => {
    switch (node.type) {
      case 'LogicalExpression': return true
      case 'UnaryExpression': return node.operator === '!'
      case 'BinaryExpression': return ['==', '===', '!=', '!==', '<', '<=', '>', '>='].includes(node.operator)
      case 'Literal': return typeof node.value === 'boolean'
      case 'CallExpression': return BOOLEAN_QM.has(qmCall(node))
    }
    return false
  }
  const cond = (node) => isBoolean(node) ? expr(node) : `__truthy(${expr(node)})`

  const call = (node) => {
    const args = node.arguments.map(expr)
    const callee = node.callee
    const method = qmCall(node)
    if (method) {
      const arity = QM[method]
      if (!arity) fail(node, `qm.${method} is not supported`)
      if (args.length < arity[0] || args.length > arity[1]) fail(node, `qm.${method} with ${args.length} arguments`)
      return `qm.${method}(${args.join(', ')})`
    }
    if (callee.type === 'MemberExpression' && !callee.computed && callee.object.type === 'CallExpression' && qmCall(callee.object) === 'getPlayer' && callee.object.arguments.length === 0) {
      if (!PLAYER.has(callee.property.name) || args.length) fail(node, `qm.getPlayer().${callee.property.name} is not supported`)
      return `qm.getPlayer().${callee.property.name}()`
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
          case '-': case '*': case '/': case '%': case '<': case '<=': case '>': case '>=': return `(${l} ${node.operator} ${r})`
          case '==': case '===': return `(${l} == ${r})`
          case '!=': case '!==': return `(${l} ~= ${r})`
        }
        return fail(node, `operator ${node.operator}`)
      }
      case 'ConditionalExpression':
        return `(function() if ${cond(node.test)} then return ${expr(node.consequent)} end return ${expr(node.alternate)} end)()`
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
  if (!functions.has('start') && !functions.has('end')) fail(null, 'no start or end function')
  const sha = createHash('sha256').update(source).digest('hex')
  return [
    '-- legacy-quest-state-machine',
    `-- Generated by tools/migrate-quest-scripts.mjs from Legacy scripts/quest/${id}.js.`,
    `-- Original SHA256: ${sha}.`,
    '-- The legacy start/end state machine is kept as written;',
    '-- scripts/legacy/quest_driver.lua supplies qm and commits each call atomically.',
    `__quest = ${id}`,
    ...out,
    '',
  ].join('\n')
}

const [dir, ...only] = process.argv.slice(2)
if (!dir) { console.error('usage: migrate-quest-scripts.mjs <Legacy scripts/quest dir> [ids...]'); process.exit(2) }
const ids = fs.readdirSync(dir).filter((f) => /^\d+\.js$/.test(f)).map((f) => Number(f.slice(0, -3))).filter((id) => !only.length || only.includes(String(id))).sort((a, b) => a - b)
const manifest = [], rejected = []
for (const id of ids) {
  const target = path.join(outDir, `${id}.lua`)
  if (fs.existsSync(target) && !fs.readFileSync(target, 'utf8').startsWith('-- legacy-quest-state-machine')) { rejected.push([id, 'hand-converted source exists']); continue }
  const raw = fs.readFileSync(path.join(dir, `${id}.js`))
  const source = raw.toString('utf8')
  if (!Buffer.from(source, 'utf8').equals(raw)) { rejected.push([id, 'source is not UTF-8']); continue }
  try {
    const lua = convert(source.replace(/^﻿/, ''), id)
    fs.writeFileSync(target, lua)
    manifest.push({ legacy: `quest/${id}.js`, source_sha256: createHash('sha256').update(raw).digest('hex'), lua: `quest/${id}.lua`, lua_sha256: createHash('sha256').update(lua).digest('hex') })
  } catch (error) {
    if (!(error instanceof Unsupported) && !(error instanceof SyntaxError)) throw error
    rejected.push([id, error.message])
  }
}
if (!only.length) fs.writeFileSync(manifestPath, JSON.stringify({ scripts: manifest }, null, 2) + '\n')
console.log(`converted ${manifest.length}, rejected ${rejected.length}`)
for (const [id, why] of rejected) console.log(`  ${id}: ${why}`)
