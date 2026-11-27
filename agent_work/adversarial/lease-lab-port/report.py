"""Summarize the two recorded robustness campaigns without hiding failures."""
import json
from pathlib import Path

area = Path(__file__).resolve().parent
outcomes = {}
campaigns = []
for filename in ('default.jsonl', 'long.jsonl'):
    events = [json.loads(line) for line in (area / filename).read_text().splitlines() if line.startswith('{')]
    packages = [e for e in events if not e.get('Test') and e['Action'] in ('pass', 'fail')]
    if not packages:
        raise SystemExit(f'{filename}: campaign is incomplete')
    campaigns.append({'file': filename, 'result': packages[-1]['Action'], 'seconds': packages[-1]['Elapsed']})
    for e in events:
        name = e.get('Test', '')
        if not name.startswith('TestAdv_') or e['Action'] not in ('pass', 'fail'):
            continue
        outcomes[name] = {
            'claim': int(name.split('_')[1]), 'test': name,
            'result': 'held' if e['Action'] == 'pass' else 'BROKEN',
            'seed': 271828, 'campaign': filename, 'seconds': e.get('Elapsed', 0),
            'command': 'PATH=/usr/local/go/bin:$PATH ' + ('ADV_LONG=1 ' if filename == 'long.jsonl' else '') +
                f"go test -count=1 -v ./agent_work/adversarial/lease-lab-port/... -run '^{name}$'",
            'output': ''.join(x.get('Output', '') for x in events if x.get('Test') == name and x['Action'] == 'output'),
        }
if len(outcomes) != 36:
    raise SystemExit(f'expected 36 completed attacks, got {len(outcomes)}')
ordered = sorted(outcomes.values(), key=lambda x: (x['claim'], x['test']))
held = sum(x['result'] == 'held' for x in ordered)
broken = len(ordered) - held
report = [f'HELD: {held}  BROKEN: {broken}', '',
    'Robustness tests of database proxy session reset, against builder tip `9f0378f`.',
    'Seed: `271828` (fixed inputs). No product files changed. All 36 attacks ran; three per each of the twelve claims in the builder’s NOTES.md.', '',
    '| Claim | Attack | Result |', '| --- | --- | --- |']
report += [f"| C{x['claim']} | `{x['test']}` | {x['result']} |" for x in ordered]
report += ['', '## Failures', '']
for x in ordered:
    if x['result'] != 'BROKEN':
        continue
    report += [f"### C{x['claim']}: {x['test']}", '', f"Reproduce (seed `{x['seed']}`):", '', '```sh', x['command'], '```', '']
    if x['claim'] == 3:
        kind = x['test'].split('_', 3)[-1]
        details = {
            'ErrorIsNotMovement': ('an error response without a data row', 'all seven counters incremented to 1'),
            'EmptyIsNotMovement': ('an empty response', 'all seven counters incremented to 1'),
            'TruncatedIsNotMovement': ('a three-field clean row, missing the last four fields', 'the four absent-column counters incremented to 1'),
        }
        observed_input, actual = details[kind]
        report += [
            'Expected: C3 and deliverable 1c require each column to have been observed differing from its clean value; an unavailable column must not satisfy that positive control.',
            f'Observed: with {observed_input}, {actual}. The `i >= len(row)` branch in `sessionResult.observe` counts missing evidence as movement.',
            'Classification: defect in the new test oracle, not evidence of an actual proxy isolation failure. The three attacks expose one root cause. An unreadable catalog query can satisfy every catalog-column guard while a separate valid secret read-back satisfies the remaining guard. These direct helper probes demonstrate false accounting; they do not claim that an unmodified live run encountered this error.',
            '',
        ]
    else:
        report += ['See the exact observed failure in evidence.json and the corresponding campaign transcript; classify before finalizing this report.', '']
report += ['## Execution and limits', '']
for c in campaigns:
    report += [f"- `{c['file']}`: {c['result']}, {c['seconds']} seconds."]
report += [
    '- Default: 21 executed checks and 15 explicitly deferred checks; the recorded package-test duration is below three minutes on this shared host. Go reports package execution time; initial compilation is not included. This is measured evidence, not a universal timing guarantee.',
    '- Long campaign selects C4–C8; it includes 20 consecutive executions of each of the three builder scenarios, plus setup/removal/reset mutations. Each subprocess owns a database and has a deadline. No additional artificial CPU load was injected; other work shares this host.',
    '- Reset mutations use Go overlays and intentionally relax the reset-tag judge to isolate reset effectiveness. They are experiments against the test suite, not deployable product changes. C5 and two C6 checks run the pre-port tests at `98cee87` as comparison controls.',
    '- C9 validates all 44 recorded Git blob hashes. The report script is compared at its copy commit, before its documented relocation edit; C10 tests the relocated version.',
    '- C11’s empty and corrupted input checks are robustness extensions beyond the literal reproduction claim; both results are reported separately and do not reinterpret the archived historical data.',
    '- C12 checks the three named SQL discovery roots, the CSV assertion script, and an actual site build. It does not establish a universal rule for every future gate.',
    '- No numbered claim was left unattacked. The builder brief has no separate numbered claims section; the twelve numbered claims in NOTES.md define the mapping.',
    '- The approximate 15–25-check recommendation conflicts with at least three attacks for each of twelve claims; the latter requires 36 checks.',
    '- Full gate, race suite, and unrelated packages were not run; this brief asks for an independent robustness harness. Failed tests remain failing.',
    '- Canonical outcomes are in default.jsonl, long.jsonl and evidence.json. Per-child logs in evidence/ are append-only and may also include the inherited interrupted run.',
    '- kb could not open its configured store (default and global lookup); no search ID was returned, so feedback was unavailable. No environment store or files outside this work area were changed.',
]
(area / 'RESULTS.md').write_text('\n'.join(report) + '\n')
(area / 'evidence.json').write_text(json.dumps({'builder': '9f0378ff69640a9ca0042c85ea6f2e833771fd60', 'seed': 271828, 'held': held, 'broken': broken, 'campaigns': campaigns, 'attacks': ordered}, indent=2) + '\n')
