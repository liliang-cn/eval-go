#!/bin/sh
# Load the env block of one target from a targets file, then exec the given
# command with it. Keeps credentials in the config file and out of shell
# history / process listings.
#   ./run-with-target-env.sh <target-name> <cmd> [args...]
#   TARGETS_FILE=targets.guests.json ./run-with-target-env.sh langgraph <cmd> ...
set -e
TARGETS_FILE="${TARGETS_FILE:-targets.run.json}"
TARGET="$1"; shift
eval "$(python3 - "$TARGET" "$TARGETS_FILE" <<'EOF'
import json, shlex, sys
name = sys.argv[1]
for t in json.load(open(sys.argv[2])):
    if t["name"] == name:
        for k, v in (t.get("env") or {}).items():
            print(f"export {k}={shlex.quote(v)}")
        break
EOF
)"
# The judge reads LLM_BASE_URL/LLM_API_KEY/LLM_MODEL. The superai target names
# them LLM_BASE/LLM_KEY/LLM_MODEL; the superleo target names them
# PLATFORM_BASE_URL/OPENAI_API_KEY/SUPERLEO_MODEL. Mirror whichever is present.
export LLM_BASE_URL="${LLM_BASE_URL:-${LLM_BASE:-$PLATFORM_BASE_URL}}"
export LLM_API_KEY="${LLM_API_KEY:-${LLM_KEY:-$OPENAI_API_KEY}}"
export LLM_MODEL="${LLM_MODEL:-$SUPERLEO_MODEL}"
exec "$@"
