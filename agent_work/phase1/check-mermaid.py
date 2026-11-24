#!/root/Software/web-tools/.venv/bin/python
"""Open every page listed in <base>/pages.txt in headless Chromium and report, per page,
the HTTP status, how many Mermaid blocks it has and how many drew (became an <svg> with no
error marker). Usage: check-mermaid.py <base-url>   Exit 1 if any page or picture fails.
Uses the Playwright install of /root/Software/web-tools (vm-1)."""
import os
import sys
import urllib.request

os.environ.setdefault("PLAYWRIGHT_BROWSERS_PATH", "/root/Software/web-tools/.playwright-browsers")
from playwright.sync_api import sync_playwright

base = sys.argv[1].rstrip("/") + "/"
pages = urllib.request.urlopen(base + "pages.txt").read().decode().split()
bad = 0
with sync_playwright() as p:
    b = p.chromium.launch()
    pg = b.new_page()
    for path in pages:
        r = pg.goto(base + path, wait_until="networkidle", timeout=60000)
        blocks = pg.locator("pre.mermaid").count()
        if blocks:
            try:
                pg.wait_for_function(
                    "document.querySelectorAll('pre.mermaid[data-processed]').length === document.querySelectorAll('pre.mermaid').length",
                    timeout=30000)
            except Exception:
                pass
        drawn = pg.locator("pre.mermaid svg").count()
        errs = pg.locator("pre.mermaid svg[aria-roledescription='error']").count()
        ok = r.status == 200 and drawn == blocks and errs == 0
        bad += 0 if ok else 1
        print("%s  %d  mermaid %d/%d drawn%s  %s" % ("ok  " if ok else "FAIL", r.status, drawn, blocks,
                                                    ", %d error" % errs if errs else "", path))
    b.close()
sys.exit(1 if bad else 0)
