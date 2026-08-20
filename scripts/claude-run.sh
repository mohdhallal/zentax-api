#!/usr/bin/env bash
#
# claude-run.sh — Stream full Claude CLI activity to the terminal in real time.
#
# PROBLEM:
#   Scripts like pre-commit and push.sh invoke `claude -p` and capture its output
#   into a variable: RESULT=$(claude -p --output-format text ...). This means ALL
#   stdout is swallowed into the variable — the terminal shows nothing while Claude
#   works. The user stares at a frozen screen for 30-60s with no feedback on what
#   the AI agent is doing (reading files, running lint, etc.).
#
# SOLUTION:
#   Use `--output-format stream-json` instead of `--output-format text`. This emits
#   one JSON event per line as Claude works. We pipe the stream through a parser that:
#     - Extracts tool_use events and prints them to STDERR (⚡ Tool: description)
#     - Extracts assistant reasoning/thinking and prints to STDERR (💭 text)
#     - Extracts tool results (truncated) and prints to STDERR (📎 output)
#     - Extracts the final "result" event and prints it to STDOUT so the calling
#       script captures only the final text output in its variable.
#     - On completion, erases all streamed activity lines from the terminal using
#       cursor save/restore + erase-below. Activity is a live progress indicator,
#       not permanent log.
#
#   Stream JSON requires --verbose flag (Claude CLI requirement).
#   Falls back to plain `--output-format text` if jq is not installed.
#
# USAGE:
#   source scripts/claude-run.sh
#   RESULT=$(claude_run /path/to/claude [claude args...])
#   # While running, terminal shows:  💭 Let me check the staged changes...
#   #                                  ⚡ Bash: Show staged diff
#   #                                  📎 diff --git a/modules/users/...
#   #                                  💭 The code looks good...
#   # On completion, activity lines disappear.
#   # Variable gets: final text response only

_claude_parse_stream() {
    set +x 2>/dev/null
    # Save cursor position at start
    printf '\033[s' >&2
    local etype tname tdesc atext toutput
    while IFS= read -r line; do
        etype="$(printf '%s' "$line" | jq -r '.type // empty' 2>/dev/null)" || continue
        case "$etype" in
            assistant)
                # Tool calls
                tname="$(printf '%s' "$line" | jq -r '[.message.content[]? | select(.type == "tool_use")][0].name // empty' 2>/dev/null)" || true
                if [ -n "$tname" ]; then
                    tdesc="$(printf '%s' "$line" | jq -r '
                        [.message.content[]? | select(.type == "tool_use")][0] |
                        (.input.description // .input.file_path // (.input.command // "" | split("\n")[0])) // ""
                    ' 2>/dev/null)" || true
                    if [ -n "$tdesc" ]; then
                        printf '\033[0;36m  ⚡ %s: %s\033[0m\n' "$tname" "$tdesc" >&2
                    else
                        printf '\033[0;36m  ⚡ %s\033[0m\n' "$tname" >&2
                    fi
                fi

                # Reasoning / thinking text
                atext="$(printf '%s' "$line" | jq -r '[.message.content[]? | select(.type == "text")][0].text // empty' 2>/dev/null)" || true
                if [ -n "$atext" ]; then
                    local truncated="${atext:0:200}"
                    if [ ${#atext} -gt 200 ]; then
                        truncated="${truncated}..."
                    fi
                    printf '\033[0;90m  💭 %s\033[0m\n' "$truncated" >&2
                fi
                ;;
            user)
                # Tool results
                toutput="$(printf '%s' "$line" | jq -r '[.message.content[]? | select(.type == "tool_result")][0].content // empty' 2>/dev/null)" || true
                if [ -n "$toutput" ]; then
                    local trunc_out="${toutput:0:150}"
                    if [ ${#toutput} -gt 150 ]; then
                        trunc_out="${trunc_out}..."
                    fi
                    trunc_out="$(printf '%s' "$trunc_out" | tr '\n' ' ')"
                    printf '\033[0;90m  📎 %s\033[0m\n' "$trunc_out" >&2
                fi
                ;;
            result)
                # Restore cursor to saved position, erase everything below
                printf '\033[u\033[J' >&2
                printf '%s' "$line" | jq -r '.result // empty' 2>/dev/null || true
                ;;
        esac
    done

    # Safety: clear if stream ended without result event
    printf '\033[u\033[J' >&2
}

claude_run() {
    local claude_exe="$1"
    shift

    if ! command -v jq &>/dev/null; then
        "$claude_exe" -p --output-format text "$@" || true
        return
    fi

    "$claude_exe" -p --verbose --output-format stream-json "$@" 2>/dev/null | _claude_parse_stream
}
