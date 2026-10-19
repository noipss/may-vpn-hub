# Usage: python3 scripts/sheet.py <dir> <out.png> [cols] [glob] — contact sheet of captures
import sys, glob, os
from PIL import Image, ImageDraw
d, out = sys.argv[1], sys.argv[2]
cols = int(sys.argv[3]) if len(sys.argv) > 3 else 5
pat = sys.argv[4] if len(sys.argv) > 4 else '*.png'
fs = sorted(glob.glob(os.path.join(d, pat)))
tw, th = 300, 500
rows = (len(fs) + cols - 1) // cols
sheet = Image.new('RGB', (cols * tw, rows * th), 'white')
for i, f in enumerate(fs):
    im = Image.open(f).convert('RGB').resize((tw, th))
    ImageDraw.Draw(im).text((6, 486), os.path.basename(f)[:-4], fill=(220, 0, 0))
    sheet.paste(im, ((i % cols) * tw, (i // cols) * th))
sheet.save(out)
print(out)
