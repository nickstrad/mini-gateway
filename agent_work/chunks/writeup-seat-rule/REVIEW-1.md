VERDICT: NITS

## Findings

1. **Minor — PLAN.md:1255:** “The C4 pages are generated from `docs/c4/gen.py`: their introductions are rewritten there” overstates the generator’s coverage. The introductions of the twelve numbered diagram pages live in `gen.py`, but the introduction and other body prose of `docs/c4/README.md` live in that Markdown file. The generator replaces only the README’s diagram-table marker block (`docs/c4/gen.py:1550`). The ruling explicitly includes the C4 README in the rewrite. Qualify the instruction to distinguish generated diagram-page introductions from the README’s directly maintained prose, while retaining Neon ownership and preservation of diagram definitions.

## What I ran

- Read `/root/briefs/zgateway-writeup-rule-review.md`, the binding `/root/briefs/zgateway-clarity-prose-pass-ruling.md` (including Part 2), and `/root/MUSE_HATCH_README.md`.
- Read STATE.md for context only; excluded it from findings as instructed.
- `git diff main..HEAD`, `git diff --check main..HEAD`, and `git log -1 --format='%H %s'`: reviewed all changed sentences at `8921c57b89fd49da987bd9b636ca619768780f16`; whitespace check exited 0.
- `git -C /root/Software/project-skeleton show 5755768 -- docs/build-flow.md`: compared the mirrored rule.
- Read PLAN.md sections 12.1, 12.2, 12.3, 15, 18 and 19; searched PLAN.md, `agent_work/orch/`, `docs/knowledge/` and README.md for prose, write-up, Neon and Clarity references. Inspected the prose and C4 templates, stage dispatch, `tools/lint-rules/prose-paths.txt`, `docs/knowledge/foundation-gates.md`, `docs/knowledge/c4-as-built.md`, and the C4 generator and README. No conflicting write-up seat assignment found. The three required seat/rewrite rules are stated.
- Verified the cited PLAN sections, Clarity SKILL.md and edit/medium/longform reference files, all seven named contract pages under docs/, the prose template, prose-path rules and C4 generator exist.
- `agent_work/orch/templates/mk.sh prose /tmp/zgw-writeup-review-prose.md CHUNK=c06-walking-skeleton WT=/root/Software/mini-gateway-work/writeup-rule BRANCH=work/writeup-seat-rule BRIEF=/root/briefs/zgateway-writeup-rule-review.md PACKAGES=internal/proxy CHAPTER_FILE=book/chapters/06-walking-skeleton.md CHAPTER_TITLE='Walking skeleton' OTHER_PROSE=README.md EXTRA='Review template expansion only.'`: exited 0 without unset-key warnings. Python assertions confirmed substitutions and no remaining `@KEY@` tokens.
- A preliminary `python3` import of `markdown` failed because it is not installed in system Python. The site script’s existing dependency fallback worked: `PATH=/usr/local/go/bin:$PATH scripts/site.sh /tmp/zgw-writeup-review-site` exited 0 and built 24 pages.
- `git status --short` before and after verification: empty; no repository files changed.
- Environment knowledge searches using `kb search`, `kb --global search` and `kb --root /root/Raw/knowledge search` failed because no configured kb location was found. No search ID was issued, so feedback could not be recorded; the knowledge corpus was not opened.

NITS CLOSED 2026-11-27 by the orchestrator: finding 1 fixed in PLAN.md section 19.4 item 4 (the numbered pages come from gen.py; the README prose outside the markers is edited in the file).
