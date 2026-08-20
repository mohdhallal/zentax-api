#!/usr/bin/env bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

CLAUDE_EXE=$(command -v claude 2>/dev/null || true)
AI_PLUGIN="go-boilerplate@ezra-plugins"
AI_PLUGIN_PATH=""
SCRIPT_DIR="$(git rev-parse --show-toplevel)/scripts"
source "$SCRIPT_DIR/claude-run.sh"

# ── Helpers ──────────────────────────────────────────────────

do_push() {
    local branch upstream
    branch=$(git rev-parse --abbrev-ref HEAD)
    upstream=$(git rev-parse --abbrev-ref --symbolic-full-name "@{u}" 2>/dev/null || true)

    echo -e "${CYAN}${BOLD}Pushing...${NC}"
    if [ -z "$upstream" ]; then
        git push --set-upstream origin "$branch" "$@"
    else
        git push "$@"
    fi
}

find_enabled_plugin_path() {
    "$CLAUDE_EXE" plugin list --json 2>/dev/null | awk -v plugin_id="$AI_PLUGIN" '
        /^[[:space:]]*\{/ {
            id = ""
            enabled = 0
            install_path = ""
        }
        /"id":[[:space:]]*/ {
            value = $0
            sub(/^[^:]*:[[:space:]]*"/, "", value)
            sub(/",?[[:space:]]*$/, "", value)
            id = value
        }
        /"enabled":[[:space:]]*true/ {
            enabled = 1
        }
        /"installPath":[[:space:]]*/ {
            value = $0
            sub(/^[^:]*:[[:space:]]*"/, "", value)
            sub(/",?[[:space:]]*$/, "", value)
            install_path = value
        }
        /^[[:space:]]*\}/ && id == plugin_id && enabled && install_path != "" {
            print install_path
            exit
        }
    '
}

# Run an AI skill, show diff of affected files, prompt user to accept/reject.
# Usage: ai_review_step "Label" "/plugin:skill" "skills/name/SKILL.md" "no_change_marker" "commit message" file1 [file2 ...]
ai_review_step() {
    local label="$1" skill_cmd="$2" skill_path="$3" no_change_marker="$4" commit_msg="$5"
    shift 5
    local files=("$@")

    if [ ! -f "$AI_PLUGIN_PATH/$skill_path" ]; then
        echo -e "${YELLOW}  $skill_cmd not found in $AI_PLUGIN — skipping${NC}"
        return 0
    fi

    local start_len=${#label}
    local start_pad=4
    local start_width=$((start_len + start_pad))
    local start_border
    start_border=$(printf '─%.0s' $(seq 1 "$start_width"))

    echo ""
    echo -e "${CYAN}${BOLD}┌${start_border}┐${NC}"
    printf "${CYAN}${BOLD}│  %-${start_len}s  │${NC}\n" "$label"
    echo -e "${CYAN}${BOLD}└${start_border}┘${NC}"
    echo ""

    RESULT=$(claude_run "$CLAUDE_EXE" "$skill_cmd") || true

    # Skill reported nothing to do
    if echo "$RESULT" | grep -q "^${no_change_marker}"; then
        echo -e "${GREEN}  no changes needed.${NC}"
        return 0
    fi

    # Check if any tracked file was modified or any new file was created
    local has_changes="no"
    for f in "${files[@]}"; do
        if [ -f "$f" ]; then
            local tracked
            tracked=$(git ls-files --error-unmatch "$f" 2>/dev/null && echo "yes" || echo "no")
            if [ "$tracked" = "no" ]; then
                has_changes="yes"
                break
            elif ! git diff --quiet "$f" 2>/dev/null; then
                has_changes="yes"
                break
            fi
        fi
    done

    if [ "$has_changes" = "no" ]; then
        echo -e "${GREEN}  no changes needed.${NC}"
        return 0
    fi

    # Show diff
    local title="$label — Proposed Changes"
    local title_len=${#title}
    local pad=4  # 2 spaces each side
    local box_width=$((title_len + pad))
    local border
    border=$(printf '─%.0s' $(seq 1 "$box_width"))

    echo ""
    echo -e "${CYAN}${BOLD}┌${border}┐${NC}"
    printf "${CYAN}${BOLD}│  %-${title_len}s  │${NC}\n" "$title"
    echo -e "${CYAN}${BOLD}└${border}┘${NC}"
    echo ""
    for f in "${files[@]}"; do
        [ -f "$f" ] || continue
        local tracked
        tracked=$(git ls-files --error-unmatch "$f" 2>/dev/null && echo "yes" || echo "no")
        if [ "$tracked" = "no" ]; then
            git diff --color --no-index /dev/null "$f" || true
        elif ! git diff --quiet "$f" 2>/dev/null; then
            git diff --color "$f"
        fi
    done
    echo ""
    echo -e "${CYAN}${BOLD}${border}${NC}"

    # Prompt
    echo -e "${YELLOW}${BOLD}Apply changes?${NC}"
    echo -e "  ${GREEN}[y]${NC} Yes — commit and continue"
    echo -e "  ${RED}[n]${NC} No  — revert and continue"
    echo -e "  ${YELLOW}[a]${NC} Abort — revert all, do not push"
    echo ""
    read -r -p "  Choice [y/n/a]: " choice

    case "${choice:-n}" in
        [yY])
            for f in "${files[@]}"; do
                [ -f "$f" ] && git add "$f"
            done
            git commit -m "$commit_msg"
            echo -e "${GREEN}${BOLD}  ✅ Committed.${NC}"
            ;;
        [nN])
            for f in "${files[@]}"; do
                if [ -f "$f" ]; then
                    local tracked
                    tracked=$(git ls-files --error-unmatch "$f" 2>/dev/null && echo "yes" || echo "no")
                    if [ "$tracked" = "yes" ]; then
                        git checkout -- "$f"
                    else
                        rm -f "$f"
                    fi
                fi
            done
            echo -e "${YELLOW}  Reverted. Continuing without these changes.${NC}"
            ;;
        [aA])
            for f in "${files[@]}"; do
                if [ -f "$f" ]; then
                    local tracked
                    tracked=$(git ls-files --error-unmatch "$f" 2>/dev/null && echo "yes" || echo "no")
                    if [ "$tracked" = "yes" ]; then
                        git checkout -- "$f"
                    else
                        rm -f "$f"
                    fi
                fi
            done
            echo -e "${RED}  Aborted. Nothing pushed.${NC}"
            exit 1
            ;;
        *)
            for f in "${files[@]}"; do
                if [ -f "$f" ]; then
                    local tracked
                    tracked=$(git ls-files --error-unmatch "$f" 2>/dev/null && echo "yes" || echo "no")
                    if [ "$tracked" = "yes" ]; then
                        git checkout -- "$f"
                    else
                        rm -f "$f"
                    fi
                fi
            done
            echo -e "${YELLOW}  Unrecognized choice. Reverted.${NC}"
            ;;
    esac
}

# ── Preflight ────────────────────────────────────────────────

if [ -z "$CLAUDE_EXE" ]; then
    echo -e "${YELLOW}claude CLI not found — pushing without AI steps.${NC}"
    do_push "$@"
    exit 0
fi

AI_PLUGIN_PATH=$(find_enabled_plugin_path || true)
if [ -z "$AI_PLUGIN_PATH" ]; then
    echo -e "${YELLOW}$AI_PLUGIN is not installed and enabled — pushing without AI steps.${NC}"
    do_push "$@"
    exit 0
fi

# ── Step 1: Changelog ───────────────────────────────────────

ai_review_step \
    "📋 AI Updating Changelog" \
    "/go-boilerplate:update-changelog" \
    "skills/update-changelog/SKILL.md" \
    "NO_CHANGES" \
    "docs: update CHANGELOG.md" \
    "CHANGELOG.md"

# ── Step 2: Documentation ───────────────────────────────────

ai_review_step \
    "📖 AI Updating Docs (README.md/CLAUDE.md)" \
    "/go-boilerplate:update-docs" \
    "skills/update-docs/SKILL.md" \
    "NO_DOC_CHANGES" \
    "docs: update CLAUDE.md and README.md" \
    "CLAUDE.md" "README.md"

# ── Push ─────────────────────────────────────────────────────

do_push "$@"
