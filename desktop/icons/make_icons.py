"""Draws the Gorget arcs logo as tray/app icons. Run: python make_icons.py (needs Pillow)."""
from PIL import Image, ImageDraw

S = 8  # supersampling

def arcs(size, colors, bg=None, template=False):
    n = size * S
    img = Image.new("RGBA", (n, n), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    if bg:
        d.rounded_rectangle([0, 0, n - 1, n - 1], radius=int(n * 0.22), fill=bg)
    w = int(n * 0.1)
    # three nested half-rings, as in the favicon (viewBox 32)
    for (r, cy, col) in [(10, 11, colors[0]), (7.5, 16.5, colors[1]), (4.5, 21.5, colors[2])]:
        cx = 16 * n / 32
        rr, cyy = r * n / 32, cy * n / 32
        d.arc([cx - rr, cyy - rr, cx + rr, cyy + rr], 0, 180, fill=col, width=w)
        for ex in (cx - rr, cx + rr):  # round caps
            d.ellipse([ex - w / 2, cyy - w / 2, ex + w / 2, cyy + w / 2], fill=col)
    return img.resize((size, size), Image.LANCZOS)

on = [(58, 85, 180, 255), (123, 79, 168, 255), (196, 154, 58, 255)]
off = [(140, 146, 156, 255)] * 3
tmpl = [(0, 0, 0, 255)] * 3
arcs(64, on).save("tray-on.png")
arcs(64, off).save("tray-off.png")
arcs(32, tmpl).save("tray-template.png")
app = arcs(512, on, bg=(21, 26, 33, 255))
app.save("app.png")
app.save("app.ico", sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)])
