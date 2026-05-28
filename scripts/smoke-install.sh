#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

run_case() {
  local name="$1"
  local answers="$2"
  local want_claude="$3"
  local want_codex="$4"
  local want_mode="$5"
  local want_auto="$6"
  local want_backend="$7"

  local home
  home="$(mktemp -d)"
  local out
  out="$(mktemp)"
  trap 'chmod -R u+w "$home" 2>/dev/null || true; rm -rf "$home"; rm -f "$out"' RETURN

  printf "%s" "$answers" | DISTILL_TEST_LAUNCHD=1 HOME="$home" go run ./cmd/distill install >"$out"

  HOME_DIR="$home" WANT_CLAUDE="$want_claude" WANT_CODEX="$want_codex" WANT_MODE="$want_mode" WANT_AUTO="$want_auto" WANT_BACKEND="$want_backend" python3 - <<'PY'
import json
import os
from pathlib import Path

home = Path(os.environ["HOME_DIR"])
prefs = json.loads((home / ".distill" / "preferences.json").read_text())

want_claude = os.environ["WANT_CLAUDE"] == "true"
want_codex = os.environ["WANT_CODEX"] == "true"
want_mode = os.environ["WANT_MODE"]
want_auto = os.environ["WANT_AUTO"] == "true"
want_backend = os.environ["WANT_BACKEND"]

assert prefs["watch_claude"] is want_claude, prefs
assert prefs["watch_codex"] is want_codex, prefs
assert prefs["automatic_watch"] is want_auto, prefs
assert prefs["extraction_backend"] == want_backend, prefs
assert prefs["generation_backend"] == "claude", prefs
assert prefs["generation_model"] == "opus", prefs
assert prefs["promotion_mode"] == want_mode, prefs
assert prefs["always_on_path"] == str(home / ".agents" / "AGENTS.md"), prefs
assert prefs["claude_md_path"] == str(home / ".claude" / "CLAUDE.md"), prefs
assert prefs["codex_agents_path"] == str(home / ".codex" / "AGENTS.md"), prefs
assert prefs["skills_dir"] == str(home / ".agents" / "skills"), prefs
assert (home / ".agents" / "skills").is_dir(), prefs
plist = home / "Library" / "LaunchAgents" / "com.msmedes.distill.watch.plist"
if want_auto:
    assert plist.is_file(), plist
else:
    assert not plist.exists(), plist
PY

  echo "ok: $name"
}

cd "$ROOT"

run_case "defaults" $'\n\n\n\n\n\n' true true unified true claude
run_case "codex-separate-manual" $'n\ny\ncodex\nn\n0\nn\n' false true separate false codex
run_case "claude-unified-auto" $'y\nn\nclaude\ny\n0\ny\n' true false unified true claude

echo "install smoke passed"
