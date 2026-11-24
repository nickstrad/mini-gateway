# Common rules for every Phase 0 research brief (mini-zgateway)

You are a fresh RESEARCH agent for the Mini-ZGateway project (a Go storage
proxy in front of ONE vanilla Redis or Postgres: bounded multiplexed
connection pool with creation-rate caps, in-flight read coalescing
(singleflight, reads only, cancellation isolation), micro-batching /
pipelining, CoDel + Adaptive LIFO admission queue, per-tenant fairness
(token bucket / GCRA / DRR), adaptive concurrency limits (AIMD / Vegas /
gradient), retry budgets, pre-forward vs post-forward error semantics,
bounded everything, per-tenant observability, deterministic-simulation
testing first). Read the spec first:
/root/Software/mini-zgateway/docs/project-source.md (read-only).

This is Phase 0: research ONLY. The orchestrator (Fable) will merge your
notes into INFLUENCES.md for Nick. You write NO product code, NO plan,
NO diagrams. You do NOT edit, commit, or push anything in any git repo.
Your only output is ONE markdown file at the path named in your brief.
Do not spawn subagents.

## Honesty rules (these matter more than coverage)

- Read the real source wherever you can reach it. Tools: built-in
  WebSearch / WebFetch if present; if they fail (403, summary only) use
  `/root/Software/web-tools/websearch search "<q>" [--max N]` and
  `/root/Software/web-tools/websearch fetch <url>` (JS-rendered local
  fetch; Bing search from this VM is weak for long queries - use short
  head-term queries, and prefer fetching known URLs directly). For
  papers, try the publisher page, the author's page, arXiv, and
  queue.acm.org. For open-source systems, prefer the official docs and
  the source code on GitHub (raw.githubusercontent.com fetches work).
- For every influence mark exactly one of:
  `read directly` (you fetched and read the primary source this run -
  give the URL you actually read),
  `secondary only` (you read only a summary / third-party account -
  name it), or
  `could not verify` (say what you tried).
  Knowledge you hold only from training memory, with no source read this
  run, is `could not verify (from memory)` - still include the content
  but label it so.
- Today is 2026-11-24. Several spec references are dated mid/late 2026.
  Check they exist as described; if a post does not exist, has a
  different title/date/content, or cannot be found, SAY SO PLAINLY and
  describe what you found instead. Never invent a quote, number, URL,
  or parameter value. Numbers (thresholds, defaults, constants) must
  come from a source you read; otherwise flag them "from memory".
- Prune as well as add. An influence you examined and recommend
  excluding is as useful as one you recommend adopting - say why.

## Entry format (use for EVERY influence, all fields)

### <Name>
- **Source:** link / paper citation / repo path on this machine
- **Verified?** read directly | secondary only | could not verify - with the URL(s)/path(s) actually read
- **Key idea (plain words, 2-4 sentences):**
- **What Mini-ZGateway would concretely borrow:** the mechanism, exact enough to implement (constants, state variables, the decision rule), and which spec part it touches: pool / coalescer / batching / admission / fairness / concurrency-limit / errors+retries / observability / benchmarking / testing
- **What NOT to copy / where it misleads at this scale** (single vanilla backend, learning/portfolio scope, zero external core deps, Go stdlib):
- **Strength:** CORE | SECONDARY | BACKGROUND
- **Recommendation:** ADOPT | ADOPT WITH CHANGE | WATCH ONLY | EXCLUDE - one-line reason

After the entries add three short sections:
- **Conflicts / tensions noticed** (where two influences pull opposite ways, with the cost of each side)
- **Variant implications** (which influences push toward Variant A Redis RESP, B Postgres wire, or only matter for C)
- **Steering questions this bucket creates for Nick** (question, options, your recommended option)
- **Verification log**: a table of every URL/path you tried and whether it worked.

Keep entries compact (roughly 120-220 words each). Plain words. Finish
within about 30 minutes of work; breadth with honest labels beats depth
on two items. When the file is written, print its path and a one-line
count of entries by recommendation as your final message.
