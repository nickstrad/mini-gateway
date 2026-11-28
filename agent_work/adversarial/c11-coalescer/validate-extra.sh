#!/usr/bin/env bash
# Run from the repository root. Every artifact stays under this attack directory.
set -u
export PATH=/usr/local/go/bin:$PATH GOMAXPROCS=2
root=$PWD
out=$root/agent_work/adversarial/c11-coalescer
mkdir -p "$out/evidence"
go test -c -o "$out/evidence/coalesce.test" ./internal/coalesce > "$out/evidence/compile.log" 2>&1 || exit 1
(
 cd "$out/evidence" || exit 1
 ./coalesce.test -test.run='^$' -test.fuzz='^FuzzCoalesceOps$' -test.fuzztime=5s -test.parallel=1 -test.fuzzcachedir="$out/evidence/cache" > fuzz-clean.log 2>&1
 echo $? > fuzz-clean.exit
 MG_FUZZ_PLANT=coalesce-leaver-result ./coalesce.test -test.run='^$' -test.fuzz='^FuzzCoalesceOps$' -test.fuzztime=20s -test.parallel=1 -test.fuzzcachedir="$out/evidence/cache" > fuzz-plant.log 2>&1
 echo $? > fuzz-plant.exit
)
python3 - "$root" "$out" <<'PY'
import json,pathlib,sys
root,out=map(pathlib.Path,sys.argv[1:])
p=root/'internal/proxy/engine.go'
s=p.read_text()
needle='\tif err := e.coalCheck(); err != nil {\n\t\treturn err\n\t}\n'
assert s.count(needle)==1
mut=out/'evidence/engine-mutant.txt';mut.write_text(s.replace(needle,''))
(out/'evidence/overlay.json').write_text(json.dumps({'Replace':{str(p):str(mut)}}))
PY
go test -overlay "$out/evidence/overlay.json" -count=1 -run '^TestAdv_16_' ./agent_work/adversarial/c11-coalescer > "$out/evidence/invariant-mutant.log" 2>&1
echo $? > "$out/evidence/invariant-mutant.exit"
python3 - "$out" <<'PY'
import pathlib,sys
p=pathlib.Path(sys.argv[1])/'evidence'
assert (p/'fuzz-clean.exit').read_text().strip()=='0', 'unplanted fuzz failed'
assert (p/'fuzz-plant.exit').read_text().strip()!='0', 'plant survived'
assert 'FAIL: FuzzCoalesceOps' in (p/'fuzz-plant.log').read_text(), 'plant failed for unrelated reason'
assert (p/'invariant-mutant.exit').read_text().strip()!='0', 'invariant removal survived'
s=(p/'invariant-mutant.log').read_text()
assert s.count('--- FAIL: TestAdv_16_')==3, s
print('clean fuzz passed; cancellation plant caught; all three invariant attacks catch removal')
PY
