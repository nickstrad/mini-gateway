#!/usr/bin/env python3
"""Build the GitHub Pages site for mini-gateway.

Usage: python3 .github/scripts/build_site.py [output_dir]      (default: _site)
Needs: the `markdown` package (pip install markdown). Nothing else.

Pages (all links relative, so the site works under any path prefix):
  docs/index.md               -> index.html
  PLAN.md                     -> plan.html
  docs/influences-summary.md  -> influences.html
  INFLUENCES.md               -> influences-full.html
  docs/c4/README.md           -> c4/index.html
  docs/c4/NN-*.md             -> c4/NN-*.html
  docs/c4/model.json, docs/c4/diagrams/*.mmd are copied beside them.
A relative link from a published page to another published page is rewritten to the
built page; a relative link to anything else in the repository goes to GitHub.
Never published: agent_work/, STATE.md, docs/knowledge/.
Exit status: 0 on success; 1 with a message if a source is missing or a link is broken.
"""
import html
import posixpath
import re
import shutil
import sys
from pathlib import Path

import markdown

ROOT = Path(__file__).resolve().parents[2]
REPO_URL = "https://github.com/nickstrad/mini-gateway"
MERMAID_CDN = "https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs"

NAV = [("index.html", "Review"), ("plan.html", "Plan"), ("c4/index.html", "C4 diagrams"),
       ("influences.html", "Influences")]

CSS = """
:root { color-scheme: light dark; --fg: #1c1e21; --bg: #ffffff; --muted: #5c6370;
  --line: #d8dce2; --soft: #f5f6f8; --link: #0b5cad; }
@media (prefers-color-scheme: dark) {
  :root { --fg: #e3e5e8; --bg: #15171a; --muted: #9aa1ac; --line: #343941;
    --soft: #1e2126; --link: #7ab7f5; } }
* { box-sizing: border-box; }
body { margin: 0; color: var(--fg); background: var(--bg);
  font: 17px/1.6 system-ui, -apple-system, "Segoe UI", sans-serif; }
header.site { border-bottom: 1px solid var(--line); background: var(--soft); }
header.site nav, main, footer.site { max-width: 68rem; margin: 0 auto; padding: 0 1.25rem; }
header.site nav { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0.25rem 1.25rem;
  padding-top: 0.75rem; padding-bottom: 0.75rem; }
header.site .name { font-weight: 600; margin-right: auto; color: var(--fg); text-decoration: none; }
header.site a[aria-current] { font-weight: 600; text-decoration: none; color: var(--fg); }
main { padding-top: 1.5rem; padding-bottom: 3rem; }
footer.site { border-top: 1px solid var(--line); color: var(--muted); font-size: 0.875rem;
  padding-top: 1rem; padding-bottom: 2rem; }
a { color: var(--link); }
h1, h2, h3, h4 { line-height: 1.25; margin: 2rem 0 0.75rem; }
h1 { font-size: 1.85rem; margin-top: 0.5rem; }
h2 { font-size: 1.35rem; border-bottom: 1px solid var(--line); padding-bottom: 0.25rem; }
h3 { font-size: 1.1rem; }
code, pre { font-family: ui-monospace, "SF Mono", Menlo, Consolas, "DejaVu Sans Mono", monospace;
  font-size: 0.85em; }
code { background: var(--soft); padding: 0.1em 0.3em; border-radius: 3px; }
pre { background: var(--soft); border: 1px solid var(--line); border-radius: 6px;
  padding: 0.9rem 1rem; overflow-x: auto; line-height: 1.35; }
pre code { background: none; padding: 0; font-size: inherit; }
pre.mermaid { background: #ffffff; color: #1c1e21; text-align: center; line-height: 1.4; }
.tablewrap { overflow-x: auto; margin: 1rem 0; }
table { border-collapse: collapse; font-size: 0.9rem; min-width: 100%; }
th, td { border: 1px solid var(--line); padding: 0.4rem 0.6rem; text-align: left; vertical-align: top; }
th { background: var(--soft); }
details.toc { background: var(--soft); border: 1px solid var(--line); border-radius: 6px;
  padding: 0.5rem 1rem; margin: 1rem 0; }
details.toc ul { margin: 0.25rem 0; padding-left: 1.25rem; }
.banner { border: 1px solid var(--line); border-left: 4px solid var(--link); background: var(--soft);
  padding: 0.6rem 1rem; border-radius: 4px; margin: 1rem 0; }
"""

MERMAID_SCRIPT = """<script type="module">
import mermaid from "%s";
mermaid.initialize({ startOnLoad: false, theme: "neutral", securityLevel: "strict",
  flowchart: { useMaxWidth: true } });
await mermaid.run({ querySelector: "pre.mermaid" });
</script>
""" % MERMAID_CDN

PAGE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{title} — mini-gateway</title>
<style>{css}</style>
</head>
<body>
<header class="site"><nav><a class="name" href="{up}index.html">mini-gateway</a>{nav}</nav></header>
<main>
{banner}{toc}{body}
</main>
<footer class="site">Plan approved 2026-11-24; nothing built yet. Source: <a href="{repo}">{repo}</a></footer>
{script}</body>
</html>
"""


class BuildError(Exception):
    pass


def pages():
    """source path (repo-relative, posix) -> output path (site-relative, posix)."""
    out = {
        "docs/index.md": "index.html",
        "PLAN.md": "plan.html",
        "docs/influences-summary.md": "influences.html",
        "INFLUENCES.md": "influences-full.html",
        "docs/c4/README.md": "c4/index.html",
    }
    for p in sorted((ROOT / "docs/c4").glob("[0-9][0-9]-*.md")):
        out["docs/c4/" + p.name] = "c4/" + p.stem + ".html"
    return out


COPIES = {"docs/c4/model.json": "c4/model.json", "docs/c4/gen.py": None}


def rewrite_links(body, src, out, page_map):
    src_dir = posixpath.dirname(src)
    out_dir = posixpath.dirname(out)

    def fix(m):
        href = html.unescape(m.group(2))
        if re.match(r"^[a-z][a-z0-9+.-]*:", href) or href.startswith("#") or href.startswith("//"):
            return m.group(0)
        path, _, frag = href.partition("#")
        target = posixpath.normpath(posixpath.join(src_dir, path))
        if target.startswith(".."):
            raise BuildError("%s: link leaves the repository: %s" % (src, href))
        if not (ROOT / target).exists():
            raise BuildError("%s: broken link: %s" % (src, href))
        if target in page_map:
            new = posixpath.relpath(page_map[target], out_dir or ".")
        elif COPIES.get(target):
            new = posixpath.relpath(COPIES[target], out_dir or ".")
        else:
            kind = "tree" if (ROOT / target).is_dir() else "blob"
            new = "%s/%s/main/%s" % (REPO_URL, kind, target)
        if frag:
            new += "#" + frag
        return '%s"%s"' % (m.group(1), html.escape(new, quote=True))

    return re.sub(r'(<a\s[^>]*?href=)"([^"]*)"', fix, body)


def render(src, out, page_map):
    text = (ROOT / src).read_text(encoding="utf-8")
    md = markdown.Markdown(extensions=["tables", "fenced_code", "sane_lists", "toc"],
                           extension_configs={"toc": {"toc_depth": "2-3"}})
    body = md.convert(text)
    body = re.sub(r'<pre><code class="language-mermaid">(.*?)</code></pre>',
                  lambda m: '<pre class="mermaid">' + m.group(1) + "</pre>", body, flags=re.S)
    body = body.replace("<table>", '<div class="tablewrap"><table>').replace("</table>", "</table></div>")
    body = rewrite_links(body, src, out, page_map)
    m = re.search(r"^#\s+(.+)$", text, flags=re.M)
    title = re.sub(r"[`*_]", "", m.group(1)) if m else out
    up = "../" * out.count("/")
    nav = "".join('<a href="%s%s"%s>%s</a>' % (
        up, href, ' aria-current="page"' if href == out else "", label) for href, label in NAV)
    toc = ""
    if len(text) > 20000 and md.toc_tokens:
        toc = '<details class="toc"><summary>Contents</summary>%s</details>\n' % md.toc
    banner = ""
    if src == "INFLUENCES.md":
        banner = ('<div class="banner">Written in Phase 0, before the steering, under the old name and with Redis as '
                  'its working assumption. Where it disagrees with the plan, the plan follows the steering. '
                  'See the <a href="influences.html">summary</a>.</div>\n')
    page = PAGE.format(title=html.escape(title), css=CSS, up=up, nav=nav, banner=banner, toc=toc, body=body,
                       repo=REPO_URL, script=MERMAID_SCRIPT if 'class="mermaid"' in body else "")
    dest = OUT / out
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(page, encoding="utf-8")


def main():
    global OUT
    OUT = Path(sys.argv[1] if len(sys.argv) > 1 else "_site").resolve()
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir(parents=True)
    page_map = pages()
    for src in page_map:
        if not (ROOT / src).is_file():
            raise BuildError("missing source: %s" % src)
    for src, out in page_map.items():
        render(src, out, page_map)
    for src, out in COPIES.items():
        if out:
            (OUT / out).parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / src, OUT / out)
    (OUT / "c4/diagrams").mkdir(parents=True, exist_ok=True)
    for p in sorted((ROOT / "docs/c4/diagrams").glob("*.mmd")):
        shutil.copyfile(p, OUT / "c4/diagrams" / p.name)
    (OUT / ".nojekyll").write_text("")
    (OUT / "pages.txt").write_text("\n".join(sorted(page_map.values())) + "\n")
    print("built %d pages into %s" % (len(page_map), OUT))


if __name__ == "__main__":
    try:
        main()
    except BuildError as e:
        print("build_site: %s" % e, file=sys.stderr)
        sys.exit(1)
