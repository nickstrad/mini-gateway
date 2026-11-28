# Lab branch port — adversarial record (Astra, gpt-6-astra)

Astra wrote a harness of 36 attacks, three for each of the twelve claims in the builder's
notes, against the build tip `9f0378f`: the three new kinds of leftover session state
under commit, rollback and savepoint; each release policy; resets reordered and repeated;
each new kind removed from the deck; repeat runs; the blob hashes of the copied records;
the kept report script run from its new folder; and the scope of the SQL and CSV discovery
(nothing under `agent_work/` is picked up). **HELD 33, BROKEN 3.** Astra's first run was cut
by its provider's content filter and completed on the one re-dispatch.

The three broken attacks (`TestAdv_3_EmptyIsNotMovement`, `TestAdv_3_ErrorIsNotMovement`,
`TestAdv_3_TruncatedIsNotMovement`) have one root cause, which review 1 found
independently (finding 2): the new oracle credited a catalog column with "showed dirt"
when the check's reply was empty, an error or cut short. It was a defect in the test's own
check, not a session-state crossing in the proxy.

After the repair round (`d3e8d4b`): a column is credited only when a complete row was read
and differs from the clean value, and an unreadable check fails the test. The builder
re-ran the harness inside the gate's unit stage: 36 pass. No claim was narrowed, none
settled, none disputed. Review 2 re-checks it.
