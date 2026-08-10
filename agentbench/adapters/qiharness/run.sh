#!/bin/sh
# agentbench exec-target wrapper for the qi-harness (Qi language) adapter.
#
#   EVAL_INPUT="..." ./adapters/qiharness/run.sh
#
# Two jobs:
#   1. keep stdout clean — `qi` prints compile diagnostics and qi-harness still
#      has a legacy trace sink that writes to stdout, so the whole run is sent to
#      stderr and the protocol line is read back out of a file.
#   2. resolve the framework — the qi import resolver only finds a *local* package
#      by scanning ancestor directories for a dir whose qi.toml declares that
#      package name, so `Harness` is a symlink created here (override with
#      QIHARNESS_DIR).
set -e

adapter_dir=$(cd "$(dirname "$0")" && pwd)
: "${QIHARNESS_DIR:=$adapter_dir/../../../../../../dev/lang/qilang/qi-harness}"

if [ ! -f "$QIHARNESS_DIR/qi.toml" ]; then
    printf '{"output":"RUN ERROR: qi-harness not found at %s (set QIHARNESS_DIR)","tool_calls":[]}\n' "$QIHARNESS_DIR"
    exit 0
fi
ln -sfn "$QIHARNESS_DIR" "$adapter_dir/Harness"

# Compile once; `qi run` would re-invoke LLVM for every one of the 50 tasks.
bin="$adapter_dir/bin/qiharness-agent"
if [ ! -x "$bin" ] || [ "$adapter_dir/qiharness_agent.qi" -nt "$bin" ]; then
    mkdir -p "$adapter_dir/bin"
    staging="$adapter_dir/bin/.build.$$"
    if qi compile "$adapter_dir/qiharness_agent.qi" -o "$staging" >&2 2>&2; then
        mv -f "$staging" "$bin"
        rm -f "$adapter_dir/qiharness_agent.o"   # qi compile drops an object file next to the source
    else
        rm -f "$staging"
        printf '{"output":"RUN ERROR: qi compile failed","tool_calls":[]}\n'
        exit 0
    fi
fi

: "${QIHARNESS_HOME:=$PWD/.qi-home}"
mkdir -p "$QIHARNESS_HOME"
export QIHARNESS_HOME

work=$(mktemp -d "${TMPDIR:-/tmp}/qiharness.XXXXXX")
QIB_OUT="$work/out.json"
QIB_CALLS="$work/calls.log"
export QIB_OUT QIB_CALLS
: > "$QIB_CALLS"

"$bin" >&2 2>&2 || true

if [ -s "$QIB_OUT" ]; then
    cat "$QIB_OUT"
    echo
else
    # the process died before it could write the protocol line — still report the
    # tool calls it had made, so a crash is distinguishable from a no-tool run.
    QIB_CALLS="$QIB_CALLS" python3 -c '
import json, os
calls = []
try:
    with open(os.environ["QIB_CALLS"], encoding="utf-8") as fh:
        calls = [{"name": line.strip()} for line in fh if line.strip()]
except Exception:
    pass
print(json.dumps({"output": "RUN ERROR: the qi adapter produced no protocol line",
                  "tool_calls": calls}, ensure_ascii=False))
'
fi

rm -rf "$work"
