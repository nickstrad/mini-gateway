Goal: execute /root/briefs/zgateway-p2-lease-port-adversarial.md against build 9f0378f.
Status: harness implementation; all writes confined to this directory (temporary overlays use its .scratch directory).
Plan: 36 attacks, three per C1–C12; expensive mutation/load campaigns require ADV_LONG=1.
Next: execute default and long campaigns, preserve failures, write RESULTS.md and commit.
Environment: kb searches failed before producing a search ID (default, --global, and explicit /root/Raw/knowledge root all lack kb.toml); feedback cannot target an ID. No store initialized or changed.
Resume: branch work/lease-port-adv; no detached jobs. Harness subprocesses have deadlines and save output.
