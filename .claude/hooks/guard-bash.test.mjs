// Run: node --test .claude/hooks/guard-bash.test.mjs
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const hook = fileURLToPath(new URL('./guard-bash.mjs', import.meta.url))
const root = path.resolve(path.dirname(hook), '..', '..')

// run feeds the hook the JSON Claude Code sends and returns its exit code.
function run(command, cwd = root) {
  const r = spawnSync('node', [hook], { input: JSON.stringify({ tool_name: 'Bash', tool_input: { command }, cwd }), encoding: 'utf8' })
  return { code: r.status, stderr: r.stderr }
}

const blocked = [
  ['sed -i', "sed -i 's/a/b/' backend/x.go"],
  ['sed -i.bak', "sed -i.bak 's/a/b/' x"],
  ['sed -Ei', "sed -Ei 's/a/b/' x"],
  ['sed --in-place', "sed --in-place 's/a/b/' x"],
  ['sed -i after a pipe', "ls | sed -n 1p; sed -i 's/a/b/' x"],
  ['perl -pi', "perl -pi -e 's/a/b/' x"],
  ['perl -i', "perl -i -pe 's/a/b/' x"],
  ['perl -pi.bak', "perl -pi.bak -e 's/a/b/' x"],
  ['heredoc into backend', "cat > backend/x.go <<'EOF'\npackage main\nEOF"],
  ['heredoc via python, backend named', "python - <<'EOF'\nopen('backend/a.go','w')\nEOF"],
  ['heredoc without a redirect from backend cwd', "python - <<EOF\nprint(1)\nEOF", path.join(root, 'backend')],
  ['> into backend', 'echo hi > backend/x.go'],
  ['>> into frontend', 'echo hi >> frontend/rentals/x.ts'],
  ['> relative, cwd in backend', 'echo hi > x.go', path.join(root, 'backend')],
  ['> absolute backslash path', 'echo hi > C:\\Users\\itsco\\Desktop\\willcoll\\frontend\\a.ts'],
  ['> git-bash style path', `echo hi > /c/Users/itsco/Desktop/willcoll/backend/a.go`],
  ['1> into backend', 'echo hi 1> backend/x'],
  ['tee into backend', 'echo hi | tee backend/x.go'],
  ['> unresolvable target', 'echo hi > $OUT'],
  ['git add', 'git add .'],
  ['git commit', 'git commit -m "x"'],
  ['git push', 'git push origin main'],
  ['git checkout', 'git checkout main'],
  ['git restore', 'git restore file'],
  ['git reset', 'git reset --hard'],
  ['git stash', 'git stash'],
  ['git merge', 'git merge x'],
  ['git rebase', 'git rebase main'],
  ['git rm', 'git rm file'],
  ['git mv', 'git mv a b'],
  ['git -C dir add', 'git -C backend add x'],
  ['git -c k=v commit', 'git -c user.name=x commit -m y'],
  ['git after &&', 'go build ./... && git add -A'],
  ['git in sh -c', 'bash -c "git commit -m x"'],
  ['git switch', 'git switch main'],
]

const allowed = [
  ['git status', 'git status'],
  ['git diff', 'git diff --stat HEAD~1'],
  ['git log', 'git log --oneline -5'],
  ['git show', 'git show HEAD'],
  ['git check-ignore', 'git check-ignore -v .env'],
  ['git describe', 'git describe --tags'],
  ['git ls-files', 'git ls-files | head'],
  ['git -C dir status', 'git -C backend status'],
  ['git log grep mentioning add', 'git log --grep="git add" --oneline'],
  ['go build', 'cd backend && go build ./...'],
  ['make test', 'make test/race'],
  ['sed read-only', "sed -n '1,5p' backend/x.go"],
  ['perl read-only', "perl -ne 'print if /x/' backend/x.go"],
  ['2>&1', 'go vet ./... 2>&1 | head'],
  ['> /dev/null', 'go build ./... > /dev/null 2>&1'],
  ['2>/dev/null', 'ls backend 2>/dev/null'],
  ['> to the OS tmp dir', 'echo hi > /tmp/out.txt'],
  ['> to a scratchpad', 'echo hi > /c/Users/itsco/AppData/Local/Temp/claude/x/scratchpad/out.txt'],
  ['> to $TMPDIR', 'echo hi > $TMPDIR/out.txt'],
  ['> outside backend/frontend', 'echo hi > docs/note.txt'],
  ['> quoted arrow text', 'grep "a > b" backend/x.go'],
  ['heredoc outside backend/frontend', 'cat > /tmp/x.txt <<EOF\nhello\nEOF'],
  ['git add text in a quoted echo', 'echo "git add ."'],
]

for (const [name, command, cwd] of blocked) {
  test(`blocks: ${name}`, () => {
    const r = run(command, cwd)
    assert.equal(r.code, 2, `expected exit 2, got ${r.code}; stderr: ${r.stderr}`)
    assert.match(r.stderr, /Blocked by/)
  })
}

for (const [name, command, cwd] of allowed) {
  test(`allows: ${name}`, () => {
    const r = run(command, cwd)
    assert.equal(r.code, 0, `expected exit 0, got ${r.code}; stderr: ${r.stderr}`)
  })
}

test('unparseable input never blocks', () => {
  const r = spawnSync('node', [hook], { input: 'not json', encoding: 'utf8' })
  assert.equal(r.status, 0)
})
