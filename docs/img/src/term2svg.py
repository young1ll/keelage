#!/usr/bin/env python3
"""Render a terminal capture (plain text) as an SVG 'screenshot' (dark, monospace)."""
import sys, html, textwrap, re
src, dst, title = sys.argv[1], sys.argv[2], sys.argv[3]
cols = int(sys.argv[4]) if len(sys.argv) > 4 else 110
lines = []
for raw in open(src, encoding='utf-8').read().rstrip('\n').split('\n'):
    raw = raw.replace('\t', '    ')
    if len(raw) <= cols:
        lines.append(raw)
    else:
        lines.extend(textwrap.wrap(raw, cols, subsequent_indent='    ', break_long_words=True, break_on_hyphens=False) or [''])
cw, lh, pad, top = 7.8, 19, 18, 40
w = int(pad*2 + cols*cw)
h = int(top + pad + len(lines)*lh + pad)
def color(line):
    if line.startswith('$ '): return '#8be9fd'
    if re.match(r'^[A-Z]{2,}(\s{2,}[A-Z ]+)+$', line): return '#bd93f9'
    if 'deny' in line or '거부' in line or 'stale' in line or 'review' in line: return '#ffb86c'
    return '#f8f8f2'
out = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" font-family="ui-monospace, SFMono-Regular, Menlo, Consolas, monospace" font-size="13">',
       f'<rect width="{w}" height="{h}" rx="10" fill="#1e1f29"/>',
       f'<rect width="{w}" height="{top-8}" rx="10" fill="#2b2c3b"/>',
       '<circle cx="18" cy="16" r="6" fill="#ff5f56"/><circle cx="38" cy="16" r="6" fill="#ffbd2e"/><circle cx="58" cy="16" r="6" fill="#27c93f"/>',
       f'<text x="{w/2:.0f}" y="21" text-anchor="middle" fill="#c8c8d0" font-size="12">{html.escape(title)}</text>']
y = top + pad
for line in lines:
    seg = html.escape(line)
    if line.startswith('$ ') and '   #' in line:
        cmd, _, cm = line.partition('   #')
        seg = f'<tspan fill="#8be9fd">{html.escape(cmd)}</tspan><tspan fill="#6272a4">   #{html.escape(cm)}</tspan>'
        out.append(f'<text x="{pad}" y="{y}" xml:space="preserve">{seg}</text>')
    else:
        out.append(f'<text x="{pad}" y="{y}" fill="{color(line)}" xml:space="preserve">{seg}</text>')
    y += lh
out.append('</svg>')
open(dst, 'w', encoding='utf-8').write('\n'.join(out))
print(dst, w, h, len(lines))
