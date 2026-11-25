#!/usr/bin/env bash
# PreToolUse hook: an issue or PR comment the agent posts starts with "Agent:" or
# "Agent, for the human:", so it can be told apart from the human's on the shared
# account (CLAUDE.md, "What the history records"). Blocks with exit 2 otherwise.
# Covers `gh issue comment`, `gh pr comment` and `gh pr review`; `gh api` and the
# web UI are not covered. A body the hook can't check is refused, not let through.
input=$(cat)

refuse() {
  echo "$1 Start every comment you post with \"Agent:\" for your own words, or \"Agent, for the human:\" when it records the human's decision or answer (CLAUDE.md, \"What the history records\")." >&2
  exit 2
}

if ! command -v jq >/dev/null; then
  [[ "$input" =~ gh[^\"]*(comment|review) ]] && refuse "jq is missing, so the comment can't be checked: install jq."
  exit 0
fi
cmd=$(jq -r '.tool_input.command // empty' <<<"$input")
[[ "$cmd" == *gh* ]] || exit 0

ws=$'[ \t\n]'
# gh in command position, so a commit message that mentions the command passes.
start="(^|[;&|(]|"$'\n'")[ "$'\t'"]*"
prefix="(command$ws+|env$ws+([^ ]+=[^ ]*$ws+)*)?([^ ;&|()]*/)?"
comment_re="${start}${prefix}gh$ws+(issue$ws+comment|pr$ws+comment|pr$ws+review)($ws|\$)"
[[ "$cmd" =~ $comment_re ]] || exit 0
sub=${BASH_REMATCH[5]}

# The gh call's own arguments: what follows the subcommand, up to the next command.
args=" ${cmd#*"${BASH_REMATCH[0]}"}"
args=${args%%[;|&]*}

marked() {
  local body=$1
  body="${body#"${body%%[![:space:]]*}"}"
  [[ "$body" == "Agent:"* || "$body" == "Agent, for the human:"* ]]
}

# A flag's value: in double quotes, in single quotes, or up to the next whitespace.
dq='"([^"]*)"'
sq="'([^']*)'"
bare="([^\"' "$'\t\n'"]+)"
file_re="$ws(--body-file|-F)(=|$ws+)($dq|$sq|$bare)"
if [[ "$args" =~ $file_re ]]; then
  path=${BASH_REMATCH[4]}${BASH_REMATCH[5]}${BASH_REMATCH[6]}
  [[ "$path" == - ]] && refuse "The body comes from stdin, so it can't be checked: write it to a file and pass --body-file <path>."
  if [[ "$path" != /* ]]; then
    dir=$(jq -r '.cwd // empty' <<<"$input")
    path="${dir:-.}/$path"
  fi
  [[ -r "$path" ]] || refuse "Can't read the body file $path, so it can't be checked: write it first, and pass a literal path (no variables or ~)."
  marked "$(cat "$path")" || refuse "The body file $path doesn't start with an Agent line."
  exit 0
fi

body_re="$ws(--body|-b)(=|$ws+)(\"|'|\\\$')?"
if [[ "$args" =~ $body_re ]]; then
  rest=${args#*"${BASH_REMATCH[0]}"}
  marked "$rest" || refuse "The body doesn't start with an Agent line."
  exit 0
fi

# gh pr review --approve or --request-changes without a body posts no text.
[[ "$sub" == pr*review ]] && exit 0
refuse "No body given: pass --body-file <path>."
