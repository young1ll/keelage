#!/usr/bin/env sh
# Regenerates docs/img from these sources.
#   docs/img/src/render.sh            # needs python3 and a Chromium/Chrome binary (CHROME=/path/to/chrome)
# - *.txt   : terminal captures (real CLI / hook-simulator output) → SVG via term2svg.py
# - ui-*.html: reproductions of the Claude Code and GitHub views built from the real hook/ledger strings → PNG (2x)
#   Replace the PNGs with real screenshots when you have them; keep the file names.
set -eu
cd "$(dirname "$0")"
OUT=..
python3 term2svg.py 01-setup-init.txt          $OUT/01-setup-init.svg          "keelage setup → init → constraint (실제 출력)" 104
python3 term2svg.py 02-hook-inject-deny.txt    $OUT/02-hook-inject-deny.svg    "편집 직전 주입과 거부 — Claude Code 훅이 받는 답 (실제 출력)" 118
python3 term2svg.py 03-change-inbox-history.txt $OUT/03-change-inbox-history.svg "커밋 → Change · inbox · history (실제 출력)" 118
CHROME="${CHROME:-$(command -v chromium || command -v chromium-browser || command -v google-chrome || command -v chrome || true)}"
[ -n "$CHROME" ] || { echo "render.sh: set CHROME=/path/to/chrome to render the PNGs" >&2; exit 0; }
shot() { "$CHROME" --headless=new --no-sandbox --disable-gpu --hide-scrollbars --force-device-scale-factor=2 --window-size="$2" --screenshot="$OUT/$1.png" "file://$PWD/$1.html" >/dev/null 2>&1; }
shot ui-claude-code-inject 940,650
shot ui-claude-code-deny   940,490
shot ui-github-pr          940,1030
echo "rendered into $OUT"
