# shellcheck shell=bash
# Console output helpers shared by every lab script.

if [[ -t 1 ]]; then
  _C_RESET=$'\033[0m'; _C_DIM=$'\033[2m'; _C_BOLD=$'\033[1m'
  _C_RED=$'\033[31m'; _C_GREEN=$'\033[32m'; _C_YELLOW=$'\033[33m'; _C_BLUE=$'\033[34m'
else
  _C_RESET=""; _C_DIM=""; _C_BOLD=""; _C_RED=""; _C_GREEN=""; _C_YELLOW=""; _C_BLUE=""
fi

log::step()  { printf '%s==>%s %s%s%s\n' "$_C_BLUE" "$_C_RESET" "$_C_BOLD" "$*" "$_C_RESET"; }
log::info()  { printf '    %s\n' "$*"; }
log::dim()   { printf '    %s%s%s\n' "$_C_DIM" "$*" "$_C_RESET"; }
log::ok()    { printf '    %s✓%s %s\n' "$_C_GREEN" "$_C_RESET" "$*"; }
log::warn()  { printf '    %s!%s %s\n' "$_C_YELLOW" "$_C_RESET" "$*" >&2; }
log::fail()  { printf '    %s✗%s %s\n' "$_C_RED" "$_C_RESET" "$*" >&2; }
log::die()   { printf '\n%sERROR:%s %s\n\n' "$_C_RED$_C_BOLD" "$_C_RESET" "$*" >&2; exit 1; }

# A banner used by verify.sh scripts so pass/fail is impossible to misread.
log::verdict_pass() {
  printf '\n%s  PASS  %s %s\n\n' "$_C_GREEN$_C_BOLD" "$_C_RESET" "${1:-scenario resolved}"
}
log::verdict_fail() {
  printf '\n%s  FAIL  %s %s\n\n' "$_C_RED$_C_BOLD" "$_C_RESET" "${1:-scenario not resolved yet}"
}
