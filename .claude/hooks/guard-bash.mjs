// PreToolUse hook for the Bash tool (registered in .claude/settings.json).
//
// Blocks, with exit code 2 and the reason on stderr:
//   1. in-place edits: sed -i / --in-place, perl -i / -pi
//   2. heredocs, and > / >> / tee, that write into backend/ or frontend/
//      (/dev/null, /dev/std*, and scratch/tmp directories are fine)
//   3. state-changing git: add commit push checkout restore reset stash merge
//      rebase rm mv (CLAUDE.md "Git / commit policy"), plus switch pull
//      cherry-pick revert clean apply am, which change repo state the same way
//
// Read-only git (status, diff, log, show, ...) and every other command pass.
// Edit files with the Edit/Write tools instead. See CLAUDE.md "Bash guard hook".
//
// The check is `evaluate(command, cwd)`; this file runs it on the hook's stdin
// JSON only when executed directly, so the tests can import it.

import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = norm(path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..'))
const guarded = ['backend', 'frontend'].map((d) => `${repoRoot}/${d}`)

const GIT_BLOCKED = new Set([
  'add', 'commit', 'push', 'checkout', 'restore', 'reset', 'stash', 'merge', 'rebase', 'rm', 'mv',
  'switch', 'pull', 'cherry-pick', 'revert', 'clean', 'apply', 'am',
])
// git options that take a separate value, so the subcommand is not the next word.
const GIT_OPTS_WITH_VALUE = new Set(['-C', '-c', '--git-dir', '--work-tree', '--namespace', '--exec-path'])

// norm turns a path into a comparable form: forward slashes, drive letter
// upper-cased, Git Bash "/c/..." turned into "C:/...", no trailing slash.
function norm(p) {
  p = p.replace(/\\/g, '/')
  p = p.replace(/^\/([a-zA-Z])\//, (_, d) => `${d.toUpperCase()}:/`)
  p = p.replace(/^([a-z]):/, (_, d) => `${d.toUpperCase()}:`)
  return p.replace(/\/+$/, '')
}

function resolveTarget(target, cwd) {
  if (target.startsWith('~')) target = os.homedir() + target.slice(1)
  target = norm(target)
  if (!/^[A-Z]:\//.test(target) && !target.startsWith('/')) target = `${norm(cwd)}/${target}`
  return norm(path.posix.normalize(target))
}

function isScratch(p) {
  const lower = p.toLowerCase()
  const tmp = norm(os.tmpdir()).toLowerCase()
  return lower.startsWith(tmp + '/') || /\/(scratch|scratchpad|tmp|temp)\//.test(lower + '/')
}

function underGuarded(p) {
  const lower = p.toLowerCase()
  return guarded.some((g) => lower === g.toLowerCase() || lower.startsWith(g.toLowerCase() + '/'))
}

// writeBlocked says whether a redirect/tee target is a forbidden write.
function writeBlocked(target, cwd) {
  if (['/dev/null', '/dev/stderr', '/dev/stdout', '/dev/tty'].includes(target)) return false
  if (/^\$\{?(TMP|TMPDIR|TEMP|SCRATCH)\b/.test(target)) return false
  if (/[$`]/.test(target)) return true // cannot be resolved: refuse
  const resolved = resolveTarget(target, cwd)
  if (isScratch(resolved)) return false
  return underGuarded(resolved)
}

// stripHeredocs removes heredoc bodies and reports the delimiters found.
function stripHeredocs(command) {
  const lines = command.split('\n')
  const out = []
  let found = false
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    out.push(line)
    const m = line.match(/(?<!<)<<(?!<)-?\s*(['"]?)([A-Za-z_][\w]*)\1/)
    if (!m) continue
    found = true
    const end = m[2]
    while (i + 1 < lines.length && lines[i + 1].trim() !== end) i++
    i++ // the delimiter line
  }
  return { text: out.join('\n'), found }
}

// splitQuotes blanks quoted text (so a quoted ">" or "git add" is not seen as
// shell syntax) and returns the quoted payloads for recursive checks of
// sh -c "..." / eval "...".
function splitQuotes(text) {
  let plain = ''
  const payloads = []
  let quote = null
  let cur = ''
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if (quote) {
      if (ch === quote) {
        payloads.push(cur)
        plain += 'Q'
        quote = null
        cur = ''
      } else if (quote === '"' && ch === '\\' && i + 1 < text.length) {
        cur += text[++i]
      } else cur += ch
    } else if (ch === '"' || ch === "'") {
      quote = ch
    } else plain += ch
  }
  return { plain, payloads }
}

const SEGMENT_SPLIT = /\n|;|&&|\|\||\|(?!\|)|&(?!>)/

function gitSubcommand(words) {
  for (let i = 0; i < words.length; i++) {
    const w = words[i]
    if (!w.startsWith('-')) return w
    if (GIT_OPTS_WITH_VALUE.has(w)) i++
  }
  return null
}

export function evaluate(command, cwd = process.cwd(), depth = 0) {
  const { text: noBodies, found: hasHeredoc } = stripHeredocs(command)

  if (hasHeredoc && (/(^|[^\w])(backend|frontend)([^\w]|$)/i.test(command) || underGuarded(norm(cwd)))) {
    return 'a heredoc in a command that touches backend/ or frontend/ is blocked; write files with the Write or Edit tool'
  }

  const { plain, payloads } = splitQuotes(noBodies)

  if (/\bsed\b[^|;&\n]*?\s(-[a-zA-Z]*i[a-zA-Z.]*|--in-place\b)/.test(plain)) {
    return 'sed -i edits files in place; use the Edit tool instead'
  }
  if (/\bperl\b(?:\s+-\S+)*?\s+-[a-zA-Z]*i/.test(plain) || /\bperl\s+-[a-zA-Z]*i/.test(plain)) {
    return 'perl -i / -pi edits files in place; use the Edit tool instead'
  }

  for (const m of plain.matchAll(/(?<![<>\d])(\d*)>>?(?![&>])\s*([^\s;&|<>()]+)/g)) {
    if (m[1] === '2' && m[2].startsWith('&')) continue
    if (writeBlocked(m[2], cwd)) {
      return `redirecting output into ${m[2]} writes a file under backend/ or frontend/; use the Write or Edit tool (scratch/tmp directories and /dev/null are fine)`
    }
  }

  let payloadIndex = 0
  for (const segment of plain.split(SEGMENT_SPLIT)) {
    const words = segment.trim().split(/\s+/).filter(Boolean)
    // Map Q placeholders back to their payloads for -c / eval checks.
    const resolved = words.map((w) => (w === 'Q' ? payloads[payloadIndex++] ?? '' : w))
    const prog = (resolved[0] ?? '').replace(/^.*[\\/]/, '').replace(/\.exe$/, '')

    if (prog === 'tee') {
      for (const w of resolved.slice(1).filter((w) => !w.startsWith('-'))) {
        if (writeBlocked(w, cwd)) return `tee into ${w} writes a file under backend/ or frontend/; use the Write or Edit tool`
      }
    }
    if (prog === 'git') {
      const sub = gitSubcommand(resolved.slice(1))
      if (sub && GIT_BLOCKED.has(sub)) {
        return `git ${sub} changes repo state; the user owns all git state changes (CLAUDE.md). Read-only git (status, diff, log, show, ...) is fine`
      }
    }
    if (depth < 3) {
      const cIndex = ['sh', 'bash', 'zsh'].includes(prog) ? resolved.indexOf('-c') : prog === 'eval' ? 0 : -1
      if (cIndex >= 0 && resolved[cIndex + 1]) {
        const inner = evaluate(resolved.slice(cIndex + 1).join(' '), cwd, depth + 1)
        if (inner) return inner
      }
    }
  }
  return null
}

async function main() {
  let raw = ''
  for await (const chunk of process.stdin) raw += chunk
  let input
  try {
    input = JSON.parse(raw)
  } catch {
    process.exit(0) // not our input: never block on a parse problem
  }
  const command = input?.tool_input?.command
  if (typeof command !== 'string') process.exit(0)
  const reason = evaluate(command, input.cwd || process.cwd())
  if (reason) {
    process.stderr.write(`Blocked by .claude/hooks/guard-bash.mjs: ${reason}\n`)
    process.exit(2)
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main()
}
