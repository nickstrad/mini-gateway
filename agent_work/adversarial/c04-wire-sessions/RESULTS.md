HELD: 28  BROKEN: 7

35 independent named attacks across all 11 claims, authored from the builder brief and exported APIs. Subtests are not counted separately. All use seed `4042026`; the campaign spans additional seeds as printed in its log. Broken tests remain failing.

| Claim | Attack | Result |
| --- | --- | --- |
| 1 | `TestAdv_1_BackendParameterReplacementPartitions` | held |
| 1 | `TestAdv_1_ClientFragmentedDuplicateParameters` | held |
| 1 | `TestAdv_1_CycleRandomPartitions` | held |
| 2 | `TestAdv_2_HeaderLengthExtremes` | held |
| 2 | `TestAdv_2_InvalidHandshakeEnums` | BROKEN |
| 2 | `TestAdv_2_ScramMessageBound` | BROKEN |
| 2 | `TestAdv_2_SeededHostileCampaign` | held |
| 2 | `TestAdv_2_WireSCRAMBound` | BROKEN |
| 3 | `TestAdv_3_BackendCoalescedTLSBytes` | held |
| 3 | `TestAdv_3_EarlyByteAtEveryRequestCut` | held |
| 3 | `TestAdv_3_HandshakeReplayCannotUpgradeTwice` | held |
| 4 | `TestAdv_4_AllowIndependentModes` | held |
| 4 | `TestAdv_4_OffCoalescedGSSSSLStartup` | held |
| 4 | `TestAdv_4_RequireCannotBeBypassedByGSSTrain` | held |
| 5 | `TestAdv_5_ChannelBindingSubstitution` | held |
| 5 | `TestAdv_5_ProofReplayAcrossNonces` | held |
| 5 | `TestAdv_5_UnknownUserIterationOracle` | BROKEN |
| 6 | `TestAdv_6_AuthOKBeforeServerProof` | held |
| 6 | `TestAdv_6_MechanismOrderAndDuplicates` | held |
| 6 | `TestAdv_6_SignatureBitFlips` | held |
| 7 | `TestAdv_7_BackendParameterFloodThenFaults` | held |
| 7 | `TestAdv_7_ClientFaultOrderConservation` | held |
| 7 | `TestAdv_7_HandshakeDeadlineClassification` | BROKEN |
| 8 | `TestAdv_8_AllUnknownTypesAndStatuses` | held |
| 8 | `TestAdv_8_LargePartialUnknownNoBuffer` | held |
| 8 | `TestAdv_8_UnsolicitedBetweenReadyAndNextQuery` | held |
| 9 | `TestAdv_9_ClosedSessionsDoNotRecycleKeys` | held |
| 9 | `TestAdv_9_ConstantRandomSingleIssuer` | held |
| 9 | `TestAdv_9_IndependentIssuerCollision` | BROKEN |
| 10 | `TestAdv_10_NoGoroutinesOrClockReads` | held |
| 10 | `TestAdv_10_NoIOImports` | held |
| 10 | `TestAdv_10_ProcessReplay` | held |
| 11 | `TestAdv_11_RepeatedSSLAfterRefusal` | BROKEN |
| 11 | `TestAdv_11_SSLRequestWithBody` | held |
| 11 | `TestAdv_11_StartupMissingTerminator` | held |

## Reproduction and evidence

Run commands from `/root/Software/mini-gateway-work/c04-adv`. Each focused command below prints the seed with `-v`.

- Default: `PATH=/usr/local/go/bin:$PATH go test -count=1 ./agent_work/adversarial/c04-wire-sessions/...` — expected exit 1, 28 held / 7 broken; final package runtime 0.245 seconds. Full machine-readable evidence: `default.jsonl`.
- Extended campaign: `PATH=/usr/local/go/bin:$PATH ADV_LONG=1 go test -count=1 -timeout 150s -v -run '^TestAdv_2_SeededHostileCampaign$' ./agent_work/adversarial/c04-wire-sessions/...` — PASS, 50,000 seeds (4042026–4092025), 600,000 hostile-input steps, 14.687 seconds. Evidence: `long.jsonl`. This is the same attack counted once in the table.
- Live fidelity: `agent_work/adversarial/c04-wire-sessions/tier-r.sh` — expected exit 1, one broken / two held against Postgres **17.11 (Debian 17.11-1.pgdg13+2)**. Evidence: `tier-r.jsonl` and three `oracle-*.json` records. The wrapper follows `scripts/tier-r.sh`'s image, SCRAM configuration and `MG_TIER_R` gate, using one throwaway database and just these attacks. It removes the container on exit. `ADV_RECORD=1` writes only the actual real-server observations; it never replaces them with fake output. Default tests replay those independent observations without sockets or Docker.

## Broken attacks

### Claim 2 — invalid handshake event enums

`PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_2_InvalidHandshakeEnums$' ./agent_work/adversarial/c04-wire-sessions/...`

Seed: **4042026**. Expected: no machine input panics; reject invalid event values or classify them safely. Observed: both machines panic for outcome 255 (index 255 into a five-element array) and completed/version 255 (index 255 into a three-element array). The test catches each panic and fails, so all four paths are exercised. **Product defect at the exported event boundary**; this is not a demonstrated network-only exploit. `HandshakeDone` directly indexes snapshot arrays before validating enums.

### Claim 2 — unbounded pure SCRAM first message

`PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_2_ScramMessageBound$' ./agent_work/adversarial/c04-wire-sessions/...`

Seed: **4042026**. Expected: the documented 2,048-byte SCRAM message/state bound. Observed: `ScramServer.First` accepts an 8,205-byte message, returns a valid challenge, and retains the large client-first string for the proof exchange. The same construction scales with the input username length. **Product defect in the pure SCRAM API's bound**, or the API needs an explicit caller-bound precondition absent from the claim. This attack does not assert that an arbitrarily large message passes the network framer.

### Claim 2 — wire SCRAM limit exceeded

`PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_2_WireSCRAMBound$' ./agent_work/adversarial/c04-wire-sessions/...`

Seed: **4042026**. Expected: SCRAM messages no longer than the stated 2,048 bytes. Observed: a 2,061-byte SASL message passes the client-facing session and receives AuthenticationSASLContinue instead of closing. **Product defect**: the framing allowance (`MaxScramMessage + 64`) covers envelope overhead but the inner SCRAM body is never checked against `MaxScramMessage`. This demonstrates the preceding boundary gap through a validly configured wire session; it is a distinct attack, not a second count of its pure-function subcase.

### Claim 5 — iteration count discloses user existence

`PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_5_UnknownUserIterationOracle$' ./agent_work/adversarial/c04-wire-sessions/...`

Seed: **4042026**. Expected from the claim: unknown user and wrong-password replies are indistinguishable. Observed before proof: known `alice` receives `i=32`, unknown equal-length `ghost` receives `i=4096`, with `MockIterations` left at its default and an accepted 32-iteration verifier. **Over-broad claim**: the builder notes explicitly require configuring mock iterations to match real verifiers. The guarantee therefore depends on configuration (and uniform verifier iterations), rather than holding for every accepted table. This is a challenge oracle, not a claimed difference in the final FATAL template. The test also checks that PLUS is not offered.

### Claim 7 — handshake deadline misclassified

`PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_7_HandshakeDeadlineClassification$' ./agent_work/adversarial/c04-wire-sessions/...`

Seed: **4042026**. Expected: a handshake-result event with `HandshakeDeadline` ends startup as `deadline`, as does the separate deadline event. Observed: the client reason and backend class are both `tls-failed`. **Product defect in event classification / API contract**: deadline is an exported handshake outcome, and the brief permits deadline as a handshake result. If adapters must only deliver deadlines via `Deadline`, that restriction needs explicit documentation and the claim must be narrowed. The separate deadline-event orderings hold.

### Claim 9 — independent issuers collide

`PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_9_IndependentIssuerCollision$' ./agent_work/adversarial/c04-wire-sessions/...`

Seed: **4042026**; injected random stream is the constant value **7**. Expected from literal claim: unique key data per session. Observed: two issuers each return `{ProcessID:8 Secret:[7 0 0 0]}`. **Over-broad claim**, not a cryptographic RNG failure: the notes correctly scope guaranteed PID uniqueness to one issuer for 2^31−1 sessions. Distinct issuers need an entropy/coordination assumption and do not provide unconditional uniqueness. Single-issuer wrap-near-boundary and shared-issuer session churn attacks hold. This does not claim that constant RNG is production-safe or that backend keys are copied.

### Claim 11 — repeated refused SSL request differs from Postgres

`PATH=/usr/local/go/bin:$PATH go test -count=1 -v -run '^TestAdv_11_RepeatedSSLAfterRefusal$' ./agent_work/adversarial/c04-wire-sessions/...`

Seed: **4042026**. Live reproduction: `agent_work/adversarial/c04-wire-sessions/tier-r.sh`. Expected: the fake matches Postgres's pre-startup conversation. Observed with SSL disabled: first SSLRequest gets `N` on both; second gets `N` from the fake but `E:FATAL:0A000` from real Postgres 17.11. **Product defect in fake fidelity**, additional to the already documented GSS difference. The oracle records real responses from this independently authored conversation, not the builder's recording or StartupScript.

## Coverage and limits

No claim was left unattacked: every claim has at least three independent named attacks. Held means these finite attacks did not break the claim, not a proof over every input. Claim 1 covers the two startup machines and reply tracker with independently constructed duplicate parameters, replacements and randomized unknown-message streams. Claim 10 combines source AST checks with separate-process byte/snapshot digests; the source checks are scoped to product files in `internal/pgwire` and are not a substitute for the repository-wide lint. Claim 11 adds three new raw conversations, rather than reusing the builder's fidelity script. The live test does not exercise actual TLS cryptography, which belongs to chunk 6.

No product fixes, builder test edits, module changes, root STATE.md edits, or index edits were made. The task's strict directory restriction overrides the global instruction to edit the project root state; the handoff is `STATE.md` beside this report. The initial prescribed `kb search` and `kb --global search` both failed before issuing a search ID because neither local nor global store configuration was found; no result existed to judge. No VM configuration was changed.
