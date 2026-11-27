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
  book/index.md               -> book/index.html, followed by a table of contents
                                 generated from book/manifest.json
  book/chapters/NN-<slug>.md  -> book/NN-<slug>.html, for every chapter the manifest
                                 marks "live" (NN<suffix>-<slug> for a chapter with a suffix)
The book manifest lists every chapter with its number, chunk, title, file and status
("planned" or "live"). A chapter may also carry a "suffix", one lowercase letter: chapter 6
with suffix "a" is chapter "6a", its file is book/chapters/06a-<slug>.md, and it is listed
after chapter 6 and before chapter 7. Two chapters may share a number but not a number and
suffix. A live chapter is published and linked from the contents; a planned
chapter is listed as plain text. The build fails if a live chapter's file is missing, if a
planned chapter's file exists, or if book/chapters/ holds a file the manifest does not list.
A relative link must name a file or folder git tracks (one that exists only in the working
tree, such as an ignored gate log, fails the build). A relative link from a published page to another published page is rewritten to the
built page; a relative link to anything else in the repository goes to GitHub.
Never published: agent_work/, STATE.md, docs/knowledge/.
Exit status: 0 on success; 1 with a message if a source is missing, a link is broken or the
book manifest is wrong.
"""
import html
import json
import posixpath
import re
import shutil
import subprocess
import sys
from pathlib import Path

import markdown

ROOT = Path(__file__).resolve().parents[2]
REPO_URL = "https://github.com/nickstrad/mini-gateway"
MERMAID_CDN = "https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs"

NAV = [("index.html", "Review"), ("book/index.html", "Book"), ("plan.html", "Plan"),
       ("c4/index.html", "C4 diagrams"), ("influences.html", "Influences")]
BOOK_INDEX = "book/index.md"
BOOK_MANIFEST = "book/manifest.json"

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
ul.book-toc { list-style: none; padding-left: 0; }
ul.book-toc li { padding: 0.2rem 0; }
ul.book-toc .planned { color: var(--muted); }
nav.chapter { display: flex; flex-wrap: wrap; gap: 0.25rem 1.5rem; border-top: 1px solid var(--line);
  margin-top: 2.5rem; padding-top: 0.75rem; font-size: 0.95rem; }
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
{banner}{toc}{body}{after}
</main>
<footer class="site">Build in progress. Source: <a href="{repo}">{repo}</a></footer>
{script}</body>
</html>
"""


class BuildError(Exception):
    pass


def load_book():
    """Read and check book/manifest.json. Returns (title, chapters); each chapter is a dict
    with number, suffix ("" when the entry has none), chunk, title, file, status and out
    (the page it is published to), ordered by number and then suffix, no suffix first."""
    path = ROOT / BOOK_MANIFEST
    if not path.is_file():
        raise BuildError("missing source: %s" % BOOK_MANIFEST)
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
    except ValueError as e:
        raise BuildError("%s: not valid JSON: %s" % (BOOK_MANIFEST, e))
    title = manifest.get("title") if isinstance(manifest, dict) else None
    chapters = manifest.get("chapters") if isinstance(manifest, dict) else None
    if not isinstance(title, str) or not title.strip() or not isinstance(chapters, list):
        raise BuildError("%s: needs a \"title\" and a \"chapters\" list" % BOOK_MANIFEST)
    seen_numbers, seen_files, out = set(), set(), []
    for i, ch in enumerate(chapters):
        where = "%s: chapter entry %d" % (BOOK_MANIFEST, i + 1)
        if not isinstance(ch, dict):
            raise BuildError("%s: not an object" % where)
        number, chunk, ctitle, file, status = (ch.get(k) for k in ("number", "chunk", "title", "file", "status"))
        if type(number) is not int or number < 0 or type(chunk) is not int or chunk < 0:
            raise BuildError("%s: \"number\" and \"chunk\" must be whole numbers" % where)
        if not isinstance(ctitle, str) or not ctitle.strip():
            raise BuildError("%s: needs a \"title\"" % where)
        if status not in ("planned", "live"):
            raise BuildError("%s: \"status\" must be \"planned\" or \"live\", got %r" % (where, status))
        suffix = ""
        if "suffix" in ch:
            suffix = ch["suffix"]
            if not isinstance(suffix, str) or not re.fullmatch(r"[a-z]", suffix):
                raise BuildError("%s: \"suffix\" must be one lowercase letter (a to z), got %r" % (where, suffix))
        m = re.fullmatch(r"book/chapters/((\d\d)([a-z]?)-[a-z0-9]+(?:-[a-z0-9]+)*)\.md",
                         file if isinstance(file, str) else "")
        if not suffix and (not m or m.group(3) or int(m.group(2)) != number):
            raise BuildError("%s: \"file\" must be book/chapters/NN-<slug>.md with NN equal to the number, got %r"
                             % (where, file))
        if suffix and (not m or m.group(3) != suffix or int(m.group(2)) != number):
            raise BuildError("%s: \"file\" must be book/chapters/NN%s-<slug>.md with NN equal to the number "
                             "and followed by the suffix, got %r" % (where, suffix, file))
        if (number, suffix) in seen_numbers or file in seen_files:
            raise BuildError("%s: chapter number or file listed twice" % where)
        seen_numbers.add((number, suffix))
        seen_files.add(file)
        exists = (ROOT / file).is_file()
        if status == "live" and not exists:
            raise BuildError("%s: chapter %d%s is live but %s is missing" % (BOOK_MANIFEST, number, suffix, file))
        if status == "planned" and exists:
            raise BuildError("%s: chapter %d%s is planned but %s exists (set its status to live, or remove the file)"
                             % (BOOK_MANIFEST, number, suffix, file))
        out.append(dict(ch, suffix=suffix, out="book/%s.html" % m.group(1)))
    chapter_dir = ROOT / "book/chapters"
    if chapter_dir.is_dir():
        for p in sorted(chapter_dir.iterdir()):
            if "book/chapters/" + p.name not in seen_files:
                raise BuildError("book/chapters/%s is not listed in %s" % (p.name, BOOK_MANIFEST))
    out.sort(key=lambda ch: (ch["number"], ch["suffix"]))
    return title, out


def chapter_label(ch):
    """The chapter as the contents and the previous and next links show it: "6. Title", or
    "6a. Title" with a suffix. Chapter 0 without a suffix, the preface, shows its title alone."""
    text = html.escape(ch["title"])
    if ch["number"] == 0 and not ch["suffix"]:
        return text
    return "%d%s. %s" % (ch["number"], ch["suffix"], text)


def book_contents(chapters):
    """The table of contents placed under the front page: links for live chapters, plain
    text marked "planned" for the rest."""
    items = []
    for ch in chapters:
        if ch["status"] == "live":
            items.append('<li><a href="%s">%s</a></li>' % (posixpath.basename(ch["out"]), chapter_label(ch)))
        else:
            items.append('<li class="planned">%s (planned)</li>' % chapter_label(ch))
    return '\n<h2 id="contents">Contents</h2>\n<ul class="book-toc">\n%s\n</ul>\n' % "\n".join(items)


def chapter_footer(chapters, ch):
    """Previous, contents and next links under a chapter (live chapters only)."""
    live = [c for c in chapters if c["status"] == "live"]
    i = live.index(ch)
    parts = []
    if i > 0:
        parts.append('<a href="%s">Previous: %s</a>' % (posixpath.basename(live[i - 1]["out"]), chapter_label(live[i - 1])))
    parts.append('<a href="index.html">Contents</a>')
    if i + 1 < len(live):
        parts.append('<a href="%s">Next: %s</a>' % (posixpath.basename(live[i + 1]["out"]), chapter_label(live[i + 1])))
    return '\n<nav class="chapter">%s</nav>\n' % "".join(parts)


def pages(chapters):
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
    out[BOOK_INDEX] = "book/index.html"
    for ch in chapters:
        if ch["status"] == "live":
            out[ch["file"]] = ch["out"]
    return out


COPIES = {"docs/c4/model.json": "c4/model.json", "docs/c4/gen.py": None}


_TRACKED = {}


def tracked(target):
    """Whether git tracks the file, or a file below the folder, at target: a link to a file
    git ignores exists in a working tree but not in the published repository."""
    if ROOT not in _TRACKED:
        try:
            out = subprocess.run(["git", "-C", str(ROOT), "ls-files", "-z"], check=True, capture_output=True).stdout
        except (OSError, subprocess.CalledProcessError) as e:
            raise BuildError("cannot list the tracked files with git ls-files: %s" % e)
        _TRACKED[ROOT] = set(out.decode("utf-8").split("\0")) - {""}
    files = _TRACKED[ROOT]
    return target in files or target == "." or any(f.startswith(target + "/") for f in files)


TAG = re.compile(r"<[A-Za-z][^<>]*>")
LINK_ATTR = re.compile(r"""(\s)(href|src)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'<>]+))""", re.I)


def rewrite_links(body, src, out, page_map):
    """Check and rewrite every href and src attribute of every tag in the page body:
    Markdown links and raw HTML alike, in double quotes, single quotes or none."""
    src_dir = posixpath.dirname(src)
    out_dir = posixpath.dirname(out)

    def fix(m):
        attr = m.group(2).lower()
        raw = next(g for g in m.group(3, 4, 5) if g is not None)
        href = html.unescape(raw).strip()
        if re.match(r"^[a-z][a-z0-9+.-]*:", href, re.I) or href.startswith("#") or href.startswith("//"):
            return m.group(0)
        path, _, frag = href.partition("#")
        path = path.partition("?")[0]
        target = posixpath.normpath(posixpath.join(src_dir, path))
        if target.startswith("..") or path.startswith("/"):
            raise BuildError("%s: link leaves the repository: %s" % (src, href))
        if not (ROOT / target).exists():
            raise BuildError("%s: broken link: %s" % (src, href))
        if not tracked(target):
            raise BuildError("%s: link to a file git does not track: %s" % (src, href))
        if target in page_map:
            new = posixpath.relpath(page_map[target], out_dir or ".")
        elif COPIES.get(target):
            new = posixpath.relpath(COPIES[target], out_dir or ".")
        elif attr == "src":
            new = "%s/raw/main/%s" % (REPO_URL, target)
        else:
            kind = "tree" if (ROOT / target).is_dir() else "blob"
            new = "%s/%s/main/%s" % (REPO_URL, kind, target)
        if frag:
            new += "#" + frag
        return '%s%s="%s"' % (m.group(1), attr, html.escape(new, quote=True))

    return TAG.sub(lambda t: LINK_ATTR.sub(fix, t.group(0)), body)


def render(src, out, page_map, after=""):
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
        up, href, ' aria-current="page"' if href == out or (href == "book/index.html" and out.startswith("book/"))
        else "", label) for href, label in NAV)
    toc = ""
    if len(text) > 20000 and md.toc_tokens:
        toc = '<details class="toc"><summary>Contents</summary>%s</details>\n' % md.toc
    banner = ""
    if src == "INFLUENCES.md":
        banner = ('<div class="banner">Written in Phase 0, before the steering, under the old name and with Redis as '
                  'its working assumption. Where it disagrees with the plan, the plan follows the steering. '
                  'See the <a href="influences.html">summary</a>.</div>\n')
    page = PAGE.format(title=html.escape(title), css=CSS, up=up, nav=nav, banner=banner, toc=toc, body=body, after=after,
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
    _, chapters = load_book()
    page_map = pages(chapters)
    for src in page_map:
        if not (ROOT / src).is_file():
            raise BuildError("missing source: %s" % src)
    after = {BOOK_INDEX: book_contents(chapters)}
    for ch in chapters:
        if ch["status"] == "live":
            after[ch["file"]] = chapter_footer(chapters, ch)
    for src, out in page_map.items():
        render(src, out, page_map, after.get(src, ""))
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
