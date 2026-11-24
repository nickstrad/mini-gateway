# Brief B1 - spec-named influences (spec section 6)

Read `_common.md` rules below first (they are appended to this brief).
Output file: /tmp/zgateway-phase0/out/b1-spec-named.md

Cover every reference in section 6 of the spec, from the SOURCE, not
from the spec's one-liner:

1. Meta Engineering, "ZGateway: Learnings from Putting a Proxy in Front
   of ZippyDB" (spec says Sep 2026). Find it (engineering.fb.com).
   Extract precisely: the fan-in/fan-out balls-into-bins model, the
   "19x connection reduction" claim and how it was measured, hot-key
   coalescing design, adaptive load balancing, per-use-case
   observability, anything on admission control / error semantics /
   what went wrong. Also look for earlier ZippyDB / Akkio / ServiceRouter
   / Meta "Shard Manager" posts that give context.
2. Cloudflare Hyperdrive: docs "How Hyperdrive works"
   (developers.cloudflare.com/hyperdrive/...), the connection-pooling
   and query-caching docs pages, and the engineering blog post the spec
   calls "Inside Hyperdrive: How we solved a rare MySQL caching bug"
   (check the real title/date on blog.cloudflare.com; also look at
   "How Hyperdrive speeds up database access" / pooling posts).
   Extract: edge connection setup split, transaction-mode pooling rules
   (what breaks: SET, prepared statements, advisory locks...), caching
   semantics (what is cached, TTL/stale-while-revalidate defaults), and
   the exact caching bug and its lesson.
3. Canva Engineering (spec says Jul & Sep 2026): "Session revocations at
   scale" and a "Worker Backpressure" post. Find them on
   canva.dev/blog/engineering. Extract the mechanisms.
4. "Fail at Scale" (Ben Maurer, ACM Queue 2015) - read the real article
   on queue.acm.org: controlled delay as Facebook implemented it (the
   exact M / N constants and the pseudo-code), adaptive LIFO, concurrency
   control / client-side throttling, the lessons list. And The Morning
   Paper write-ups of "Fail at Scale" and "Controlling Queue Delay".
5. Nichols & Jacobson, "Controlling Queue Delay" (ACM Queue 2012): the
   real algorithm - target (5 ms), interval (100 ms), sojourn time, the
   dropping state, the interval/sqrt(count) control law. Also RFC 8289.
   IMPORTANT: state clearly how network CoDel (the paper) DIFFERS from
   the Facebook/"Fail at Scale" request-queue variant (and from folly's
   Codel.h, wangle, and any Go implementations) - the project must pick
   one; say which mechanism fits a request queue in front of a DB.
6. Apollo GraphQL, "How we know a router performance result is real"
   (spec says Oct 2026). Find it on apollographql.com/blog. Extract the
   statistical method (run counts, variance control, A/A tests,
   confidence intervals, noise sources, what they refuse to claim).
7. Companion docs: "Master Systems Engineering Projects Roadmap",
   "Systems Engineering Deep-Dives - Master Reading List", "Project 2"
   and "Project 4" docs are Nick's Google Docs and are probably NOT
   reachable. Search this machine for local copies
   (`docs/project-source.md` in /root/Software/*/, /root/Software/research,
   /root/Software/portfolio, /root/briefs) and report what exists; do not
   analyse the Project 2 / Project 4 code (another agent has that).

Add any closely-related primary source you find while reading these
(e.g. Meta's "Metastable failures" papers, Cloudflare pooling posts) as
extra entries, labelled "found while verifying".
