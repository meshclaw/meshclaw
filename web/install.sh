#!/usr/bin/env bash
set -e

BOLD="\033[1m"
GREEN="\033[32m"
CYAN="\033[36m"
RESET="\033[0m"

echo ""
echo -e "${BOLD}  meshclaw${RESET} — AI workers, anywhere"
echo -e "  ${CYAN}https://meshclaw.run${RESET}"
echo ""

pip install meshclaw -q --break-system-packages 2>/dev/null || pip install meshclaw -q

echo -e "${GREEN}  ✓ installed$(RESET)"
echo ""
echo -e "  Start a worker:"
echo -e "  ${BOLD}meshclaw start assistant${RESET}"
echo ""
