#!/usr/bin/env bash
# PreToolUse hook: an issue or PR comment the agent posts starts with "Agent:" or
# "Agent, for the human:", so it can be told apart from the human's on the shared
# account (CLAUDE.md, "What the history records"). Blocks with exit 2 otherwise.
#
# Checks every `gh issue comment`, `gh pr comment` and `gh pr review` in the
# command, wherever it appears (in a chain, after env or xargs, inside bash -c).
# It reads the shell text and isn't a parser, so it errs toward refusing: a body
# it can't check is refused, and so is a commit message that quotes such a
# command. `gh api` and the web UI are not covered.
input=$(cat)

refuse() {
  echo "$1 Start every comment you post with \"Agent:\" for your own words, or \"Agent, for the human:\" when it records the human's decision or answer (CLAUDE.md, \"What the history records\")." >&2
  exit 2
}

ws=$'[ \t\n]'
word_start='(^|[^[:alnum:]_./-]|/)'

if ! command -v jq >/dev/null; then
  re="${word_start}gh( |\\\\n)+(issue|pr)( |\\\\n)+(comment|review)"
  [[ "$input" =~ $re ]] && refuse "jq is missing, so the comment can't be checked: install jq."
  exit 0
fi
cmd=$(jq -r '.tool_input.command // empty' <<<"$input")
[[ "$cmd" == *gh* ]] || exit 0
cmd=${cmd//$'\\\n'/ } # line continuations

marked() {
  local body=$1
  body="${body#"${body%%[![:space:]]*}"}"
  [[ "$body" == "Agent:"* || "$body" == "Agent, for the human:"* ]]
}

# A flag's value: in double quotes, in single quotes, or up to the next whitespace.
dq='"([^"]*)"'
sq="'([^']*)'"
bare="([^\"' "$'\t\n'"]+)"
# Short flags may take their value attached (-F/tmp/b.md, -b"text").
file_re="$ws(--body-file(=|$ws+)|-F(=|$ws+)?)($dq|$sq|$bare)"
body_re="$ws(--body(=|$ws+)|-b(=|$ws+)?)(\"|'|\\\$')?"

# check_call ARGS SUB TAIL: ARGS is what follows the subcommand, up to the next
# command; TAIL is all of it, for when the cut fell inside a quoted value.
check_call() {
  local args=$1 sub=$2 tail=$3 file_at=-1 body_at=-1 path rest dir
  if [[ "$args" =~ $file_re ]]; then
    local m=${BASH_REMATCH[0]}
    path=${BASH_REMATCH[5]}${BASH_REMATCH[6]}${BASH_REMATCH[7]}
    rest=${args%%"$m"*}
    file_at=${#rest}
  fi
  if [[ "$args" =~ $body_re ]]; then
    local b=${BASH_REMATCH[0]}
    rest=${args%%"$b"*}
    body_at=${#rest}
  fi

  # The flag that comes first is the real one; a later match may sit inside its value.
  if ((file_at >= 0 && (body_at < 0 || file_at < body_at))); then
    [[ "$path" == - ]] && refuse "The body comes from stdin, so it can't be checked: write it to a file and pass --body-file <path>."
    if [[ "$path" != /* ]]; then
      [[ "$cmd" =~ (^|[^[:alnum:]_])cd$ws ]] && refuse "The command changes directory, so the relative body file $path can't be found: pass an absolute path."
      dir=$(jq -r '.cwd // empty' <<<"$input")
      path="${dir:-.}/$path"
    fi
    [[ -r "$path" ]] || refuse "Can't read the body file $path, so it can't be checked: write it first, and pass a literal path (no variables or ~)."
    marked "$(cat "$path")" || refuse "The body file $path doesn't start with an Agent line."
    return
  fi
  if ((body_at >= 0)); then
    marked "${args#*"$b"}" || refuse "The body doesn't start with an Agent line."
    return
  fi
  # gh pr review --approve or --request-changes without a body posts no text. If a
  # body flag shows up later in the command, the cut may have hidden it: refuse.
  if [[ "$sub" == pr*review ]]; then
    [[ "$tail" =~ $file_re || "$tail" =~ $body_re ]] && refuse "Can't tell whether this review has a body: run the gh call as a command of its own."
    return
  fi
  refuse "No body given: pass --body-file <path>."
}

call_re="${word_start}gh$ws+(issue$ws+comment|pr$ws+comment|pr$ws+review)($ws|\$)"
rest=$cmd
while [[ "$rest" =~ $call_re ]]; do
  sub=${BASH_REMATCH[2]}
  rest=" ${rest#*"${BASH_REMATCH[0]}"}"
  check_call "${rest%%[;|&]*}" "$sub" "$rest"
done
exit 0
