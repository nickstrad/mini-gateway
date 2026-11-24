#!/bin/bash
# Usage: ./agent_work/adversarial/c00-foundation/attack.sh [attack-id ...]
# Runs isolated attacks; writes RESULTS.md and evidence.json beside this script.
# Exit 0 means all selected attacks HELD; 1 means BROKEN; 2 means harness failure.
set -eu
export PATH="/usr/local/go/bin:$PATH"
exec python3 - "$@" <<'PY'
import os, sys, subprocess as sp, pathlib as pl, tempfile, shutil, json, re, time, tarfile, io
ROOT=pl.Path.cwd()
OUT=ROOT/'agent_work/adversarial/c00-foundation'
if not (ROOT/'scripts/gate.sh').is_file(): raise SystemExit('Run from the repository root')
WORK=pl.Path('/tmp/zgw-c00-adv')
WORK.mkdir(exist_ok=True)
run=pl.Path(tempfile.mkdtemp(prefix='run-',dir=WORK))
ENV=os.environ.copy()
for key in list(ENV):
    if key.startswith(('GATE_','CANARY_','LAND_')) or key in ('GOFLAGS','GOWORK','SWEEP_SEEDS','FUZZ_SECONDS','SITE_PYLIB'):
        ENV.pop(key,None)
ENV.update(TMPDIR=str(run), GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL='/dev/null',
           GIT_AUTHOR_NAME='Adversarial', GIT_AUTHOR_EMAIL='adversarial@example.invalid',
           GIT_COMMITTER_NAME='Adversarial', GIT_COMMITTER_EMAIL='adversarial@example.invalid')
CLAIMS={
  1:'Checks fail closed, and only a full green run on a clean tree counts for landing.',
  2:'An unbuilt stage needs both marker and not-built message; a previously built stage cannot silently become skippable again.',
  3:'G0 rejects formatting, compilation, vet and tidy failures in both Go modules.',
  4:'G1 enforces determinism, every import/requirement restriction, and bounded shell channels and growth.',
  5:'Build stage rejects added product Go comments; final stage permits no behavior change; later chunks cannot bypass G2 with foundation stage.',
  6:'G3 rejects failing/panicking/nonbuilding tests, failing TestMain and listed data races.',
  7:'G9 rejects broken relative links, missing live chapters and unlisted chapter files.',
  8:'G10 rejects failing or non-executable chunk extras and fails closed if an extra cannot run.',
  9:'Landing rejects red/partial/mismatched evidence, post-gate code changes, missing/failed review, open NITS, missing adversarial evidence, an unrebased branch or dirty main.',
  10:'A canary cannot be caught for the wrong diagnostic or check.',
  11:'Builder environment settings cannot leave result.json claiming more than the invocation established.',
}
rows=[]; selected=set(sys.argv[1:]); started=time.monotonic()
def cmd(p,args,env=None,ok=False,timeout=150):
    e=ENV.copy(); e.update(env or {})
    r=sp.run(args,cwd=p,env=e,text=True,stdout=sp.PIPE,stderr=sp.STDOUT,timeout=timeout)
    if ok and r.returncode: raise RuntimeError(f'{args}: {r.stdout}')
    return r.returncode,r.stdout

def put(p,path,data,mode=None):
    f=p/path; f.parent.mkdir(parents=True,exist_ok=True); f.write_text(data)
    if mode is not None: f.chmod(mode)
def append(p,path,data): put(p,path,(p/path).read_text()+data)
def commit(p):
    cmd(p,['git','add','-A'],ok=True); cmd(p,['git','commit','-qm','fixture','--allow-empty'],ok=True)
    return cmd(p,['git','rev-parse','HEAD'],ok=True)[1].strip()
def fmt(p):
    files=[str(x.relative_to(p)) for x in p.rglob('*.go') if '.git' not in x.parts and 'tools' not in x.parts]
    if files: cmd(p,['gofmt','-w',*files],ok=True)
def gate(p,only,stage='build',extra=None,chunk='attack'):
    e={'GATE_STAGE':stage}
    if only: e['GATE_ONLY']=only
    e.update(extra or {})
    rc,log=cmd(p,['bash','scripts/gate.sh',chunk],e)
    out=p/e.get('GATE_OUT',f'agent_work/chunks/{chunk}/gate')
    try: result=json.loads((out/'result.json').read_text())
    except (FileNotFoundError,json.JSONDecodeError): result=None
    logs={f.name:f.read_text() for f in out.glob('G*.log')}
    return dict(rc=rc,stdout=log,result=result,logs=logs)
def red(o,g,c,pattern):
    r=o['result'] or {}
    return (o['rc']==1 and r.get('overall')=='fail' and r.get('counts_for_landing') is False
        and any(x['gate']==g and x['check']==c and x['status']=='fail' for x in r.get('checks',[]))
        and re.search(pattern,o['logs'].get(f'{g}-{c}.log','')) is not None)
def test(claim,id,description,action,expect,fix):
    if selected and id not in selected: return
    p=run/id
    cmd(run,['git','clone','-q','--no-hardlinks',str(template),str(p)],ok=True)
    cmd(p,['git','branch','main',base],ok=True)
    try:
        o=action(p); held=expect(o)
    except Exception as exc:
        raise RuntimeError(f'Harness failure in {id}: {exc}') from exc
    row=dict(claim=claim,id=id,attack=description,result='HELD' if held else 'BROKEN',fix=fix,evidence=o)
    rows.append(row); print(f'{row["result"]} {id}: {description}',flush=True)
    shutil.rmtree(p)
def plant(claim,id,desc,files,g,c,pat,stage='build',extra=None,fix='Make the named check reject this fixture.',format=True,mutate=None,full=False,chunk='attack'):
    def action(p):
        for path,data in files.items(): put(p,path,data)
        if mutate: mutate(p)
        if format: fmt(p)
        commit(p)
        return gate(p,None if full else g,stage,extra,chunk)
    test(claim,id,desc,action,lambda o:red(o,g,c,pat),fix)
MODULE='github.com/nickstrad/mini-gateway'
try:
    template=run/'template'; template.mkdir()
    archive=sp.check_output(['git','archive','HEAD'],cwd=ROOT)
    with tarfile.open(fileobj=io.BytesIO(archive)) as t: t.extractall(template,filter='data')
    cmd(template,['git','init','-q','-b','main'],ok=True)
    base=commit(template)
    cmd(template,['git','checkout','-qb','fixture'],ok=True)
    put(template,'internal/probe/probe.go','package probe\n\nfunc Value() string { return "before" }\n\nconst First = 1\nconst Second = 2\n')
    put(template,'internal/probe/testdata/input.txt','before\n')
    fmt(template); clean=commit(template)
    rc,log=cmd(template,['scripts/site.sh',str(run/'site')])
    if rc: raise RuntimeError('Site prerequisite failed: '+log)
    if (template/'.cache/pylib').exists(): ENV['SITE_PYLIB']=str(template/'.cache/pylib')
    baseline=gate(template,None)
    if baseline['rc'] or not baseline['result']['counts_for_landing']:
        raise RuntimeError('Clean full gate is not green: '+baseline['stdout'])

    # 1: failed dependencies, absent scripts, and dirty evidence.
    plant(1,'missing-site','Missing site executable',{},'G9','site','No such file',mutate=lambda p:(p/'scripts/site.sh').unlink())
    plant(1,'missing-rules','Missing imports rule file',{},'G1','importlint','no such file',mutate=lambda p:(p/'tools/lint-rules/imports.txt').unlink())
    plant(1,'broken-tools','Lint tools cannot compile',{'tools/detlint/broken.go':'package main\nvar Bad = absent\n'},'G1','detlint','was not built')
    def dirty(p):
        put(p,'untracked.txt','dirty'); return gate(p,None)
    test(1,'dirty-tree','Full green dirty tree cannot count',dirty,
         lambda o:o['rc']==0 and o['result']['dirty_tree'] and not o['result']['counts_for_landing'],'Include all input changes in dirty-tree detection.')
    def dirty_extra(p):
        put(p,'agent_work/chunks/attack/gate/extra.sh','#!/bin/bash\nexit 0\n',0o755)
        return gate(p,None)
    test(1,'dirty-extra','Uncommitted extra.sh must not produce clean landing evidence',dirty_extra,
         lambda o:o['rc']==0 and o['result']['dirty_tree'] and not o['result']['counts_for_landing'],
         'Exclude only generated result/log files from git status, not the executable extra.sh.')

    # 2: all marker combinations, including a regression to a skeleton after a built commit.
    plant(2,'marker-gone','Skeleton without its marker',{},'G4','sweep','a stage that exists cannot be skipped',mutate=lambda p:(p/'scripts/.not-built/sweep').unlink())
    plant(2,'marker-still-there','Built script with marker retained',{'scripts/fuzz.sh':'#!/bin/bash\nexit 0\n'},'G5','fuzz','delete the marker')
    plant(2,'built-fails','Built stage exits nonzero',{'scripts/tier-r.sh':'#!/bin/bash\necho ATTACK-STAGE\nexit 9\n'},'G6','tier-r','ATTACK-STAGE',mutate=lambda p:(p/'scripts/.not-built/tier-r').unlink())
    plant(2,'silent-skip','Exit 3 with marker but without required text',{'scripts/sweep.sh':'#!/bin/bash\nexit 3\n'},'G4','sweep','delete the marker')
    def resurrect(p):
        (p/'scripts/.not-built/sweep').unlink(); put(p,'scripts/sweep.sh','#!/bin/bash\nexit 0\n'); commit(p)
        put(p,'scripts/.not-built/sweep','resurrected\n'); put(p,'scripts/sweep.sh','#!/bin/bash\necho "not built yet"\nexit 3\n'); commit(p)
        return gate(p,'G4')
    test(2,'resurrect-marker','Restore a marker after the stage was built',resurrect,lambda o:red(o,'G4','sweep','.'),
         'Reject marker reintroduction relative to the accepted baseline/history; the current pair alone cannot prove never-built.')

    # 3: root and nested-module format/build/vet/tidy.
    for prefix,label in [('internal/probe/','root'),('tools/detlint/internal/report/','nested')]:
        package='probe' if label=='root' else 'report'
        plant(3,f'{label}-format','Unformatted Go in '+label, {prefix+'bad.go':f'package {package}\nfunc  Bad( ) int {{return 1}}\n'},'G0','gofmt','bad.go',format=False)
        plant(3,f'{label}-compile','Compile error in '+label,{prefix+'bad.go':f'package {package}\nvar Bad = missingName\n'},'G0','build' if label=='root' else 'tools','undefined: missingName')
        plant(3,f'{label}-vet','Type-correct printf vet error in '+label,{prefix+'bad.go':f'package {package}\nimport "fmt"\nfunc Bad() {{ fmt.Printf("%d", "string") }}\n'},'G0','vet' if label=='root' else 'tools-vet','format %d')
        mod='go.mod' if label=='root' else 'tools/detlint/go.mod'
        plant(3,f'{label}-tidy','Unused requirement in '+label,{},'G0','tidy','not tidy',mutate=lambda p,m=mod:append(p,m,'\nrequire github.com/jackc/pgx/v5 v5.13.0\n'))

    # 4: deterministic operations and each import policy surface.
    for id,code,pat in [
        ('clock','import "time"\nfunc Bad() int64 { return time.Now().UnixNano() }','wallclock'),
        ('goroutine','func Bad() { go func(){}() }','concurrency'),
        ('rand','import "math/rand/v2"\nfunc Bad() int { return rand.Int() }','globalrand'),
        ('map-order','func Bad(m map[int]int) []int { var a []int; for k := range m { a=append(a,k) }; return a }','maprange'),
        ('float','func Bad() float64 { return 1.5 }','float')]:
        plant(4,'det-'+id,'Determinism: '+id,{'internal/det/bad.go':'package det\n'+code+'\n','tools/lint-rules/det-packages.txt':'internal/det\n'},'G1','detlint',pat)
    for pkg in ['pool','admission','coalesce','retry','work','proxy']:
        plant(4,'import-'+pkg,'Adapter import in '+pkg,{f'internal/{pkg}/bad.go':f'package {pkg}\nimport _ "{MODULE}/internal/obs/csv"\n'},'G1','importlint','deny')
    for id,path,code,pat in [
        ('dot','internal/pool/bad.go',f'package pool\nimport . "{MODULE}/internal/pgwire"\n','deny'),
        ('blank','internal/probe/bad.go','package probe\nimport _ "example.com/evil"\n','third-party'),
        ('tagged','internal/probe/bad.go','//go:build never\n\npackage probe\nimport _ "example.com/evil"\n','third-party'),
        ('test','internal/probe/bad_test.go','package probe\nimport _ "example.com/evil"\n','third-party'),
        ('cgo','internal/probe/bad.go','package probe\nimport "C"\n','third-party'),
        ('pgproto','internal/probe/bad.go','package probe\nimport _ "github.com/jackc/pgx/v5/pgproto3"\n','only'),
        ('vendor','vendor/evil/bad.go','package evil\n','gomod'),
        ('nested','internal/nested/go.mod','module example.com/evil\n','nested-module'),
        ('skip-tools','internal/tools/bad.go','package tools\nimport _ "example.com/evil"\n','layout'),
        ('skip-agent','internal/agent_work/bad.go','package agent_work\nimport _ "example.com/evil"\n','layout'),
        ('skip-site','internal/_site/bad.go','package site\nimport _ "example.com/evil"\n','layout'),
    ]:
        plant(4,'import-'+id,'Import escape: '+id,{path:code},'G1','importlint',pat,fix='Do not silently skip importable directories; check every reachable product/test source or reject its layout.')
    plant(4,'import-reachable-testdata','Explicitly imported core testdata package imports pgwire',{
        'internal/pgwire/wire.go':'package pgwire\nconst V=1\n',
        'internal/pool/testdata/hidden/hidden.go':f'package hidden\nimport "{MODULE}/internal/pgwire"\nconst V=pgwire.V\n',
        'internal/probe/reachable.go':f'package probe\nimport "{MODULE}/internal/pool/testdata/hidden"\nconst Hidden=hidden.V\n',
    },'G1','importlint','deny',full=True,
      fix='Do not skip explicitly imported testdata packages; walk the reachable import graph or reject product imports of skipped directories.')
    for id,addition in [('replace','replace github.com/jackc/pgx/v5 => ./evil'),('tool','tool (\nexample.com/evil/cmd\n)'),('require','require (\nexample.com/evil v1.0.0\n)')]:
        plant(4,'gomod-'+id,'go.mod '+id+' directive',{},'G1','importlint','gomod',mutate=lambda p,a=addition:append(p,'go.mod','\n'+a+'\n'))
    for id,code,pat in [
        ('zero','func Bad() chan int { return make(chan int,0) }','unbuffered'),
        ('variable','func Bad(n int) chan int { return make(chan int,n) }','chan-cap'),
        ('growth','type Box struct { q []int }; func (b *Box) Bad(){ for { b.q=append(b.q,1) } }','unbounded-growth'),
        ('alias','type Box struct { q []int }; func (b *Box) Bad(){ q:=&b.q; for { *q=append(*q,1) } }','unbounded-growth'),
        ('helper','type Box struct { q []int }; func (b *Box) add(){ b.q=append(b.q,1) }; func (b *Box) Bad(){ for { b.add() } }','unbounded-growth'),
    ]:
        plant(4,'bound-'+id,'Shell channel/growth: '+id,{'internal/shell/bad.go':'package shell\n'+code+'\n'},'G1','boundlint',pat,
              fix='Track field aliases/called growth helpers, or explicitly prohibit these forms in shell loops; this is a documented lint limitation, not a type-check failure.')

    # 5: comment forms and code hidden in a final prose pass.
    for id,path,code in [
        ('block','internal/probe/comment.go','package probe\n/* prose */\nconst C=1\n'),
        ('fake-directive','internal/probe/comment.go','package probe\n//go:embedx prose\nconst C=1\n'),
        ('generated','internal/probe/comment.go','// Code generated by attack. DO NOT EDIT.\npackage probe\n'),
        ('test','internal/probe/comment_test.go','package probe\n// prose\n'),
        ('tagged','internal/probe/comment.go','//go:build never\n\npackage probe\n// prose\n'),
        ('outside','unusual/comment.go','package unusual\n// prose\n'),
        ('testdata','internal/probe/testdata/comment.go','package fixture\n// prose\n'),
    ]:
        plant(5,'comment-'+id,'Build comment: '+id,{path:code},'G2','comments','comment',fix='Narrow exclusions to inert fixtures, or reject Go fixtures outside the stated product scope instead of silently skipping them.')
    for id,mutation in [
        ('string',lambda p:put(p,'internal/probe/probe.go',(p/'internal/probe/probe.go').read_text().replace('"before"','"after"'))),
        ('reorder',lambda p:put(p,'internal/probe/probe.go',(p/'internal/probe/probe.go').read_text().replace('const First = 1\nconst Second = 2','const Second = 2\nconst First = 1'))),
        ('testdata',lambda p:put(p,'internal/probe/testdata/input.txt','after\n')),
        ('gomod',lambda p:append(p,'go.mod','\n// changed\n')),
        ('rename',lambda p:(p/'internal/probe/probe.go').rename(p/'internal/probe/renamed.go')),
        ('script',lambda p:put(p,'scripts/bench.sh','#!/bin/bash\nexit 0\n')),
    ]:
        plant(5,'prose-'+id,'Final prose pass changes '+id,{},'G2','astdiff','astdiff',stage='final',extra={'GATE_PREPROSE':clean},mutate=mutation,
              fix='At final stage compare all behavior-bearing non-Go files byte-for-byte (including testdata and scripts); allow only an explicit prose-file set.')
    def foundation_other(p): return gate(p,'G2','foundation')
    test(5,'foundation-other','Later chunk requests foundation stage',foundation_other,lambda o:o['rc']==2 and 'c00-foundation only' in o['stdout'],'Restrict foundation stage to chunk zero.')
    plant(5,'foundation-reuse','Product code passes when a later builder reuses c00-foundation identity',{'internal/probe/comment.go':'package probe\n// later product prose\nconst Later=1\n'},'G2','comments','comment',stage='foundation',chunk='c00-foundation',full=True,
          fix='Enforce that foundation has no product Go and bind stage/chunk identity at landing; the caller-controlled directory name is insufficient.')

    # 6: Go test exit propagation, including TestMain and race detector.
    for id,code,pat in [
        ('fail','import "testing"\nfunc TestBad(t *testing.T){ t.Fatal("ATTACK-FAIL") }','ATTACK-FAIL'),
        ('panic','import "testing"\nfunc TestBad(t *testing.T){ panic("ATTACK-PANIC") }','ATTACK-PANIC'),
        ('compile','import "testing"\nfunc TestBad(t *testing.T){ absent() }','undefined: absent'),
        ('testmain','import("testing";"os")\nfunc TestMain(m *testing.M){ os.Exit(7) }','FAIL'),
    ]:
        plant(6,'test-'+id,'Test failure: '+id,{'internal/probe/bad_test.go':'package probe\n'+code+'\n'},'G3','unit',pat)
    plant(6,'test-race','Listed race package writes concurrently',{'internal/probe/race_test.go':'package probe\nimport("testing";"sync")\nfunc TestRace(t *testing.T){ var x int; var w sync.WaitGroup; for i:=0;i<8;i++ { w.Add(1); go func(){ defer w.Done(); for j:=0;j<10000;j++{ x++ } }() }; w.Wait() }\n','tools/lint-rules/race-packages.txt':'./internal/probe/...\n'},'G3','race','WARNING: DATA RACE')

    # 7: site structural integrity.
    plant(7,'site-link','Broken relative link',{},'G9','site','broken link',mutate=lambda p:append(p,'docs/index.md','\n[bad](missing-attack.md)\n'))
    plant(7,'site-html-link','Broken relative link in single-quoted raw HTML',{},'G9','site','broken link',
          mutate=lambda p:append(p,'docs/index.md',"\n<a href='missing-attack.md'>broken</a>\n"),
          fix='Parse rendered HTML anchors instead of matching only double-quoted href attributes.')
    def live(p):
        m=json.loads((p/'book/manifest.json').read_text()); m['chapters'][0]['status']='live'; put(p,'book/manifest.json',json.dumps(m))
    plant(7,'site-live','Live chapter file missing',{},'G9','site','is live but',mutate=live)
    plant(7,'site-unlisted','Chapter file outside manifest',{'book/chapters/99-attack.md':'# Attack\n'},'G9','site','not listed')

    # 8: extra command failures and dangling symlink.
    for id,body,mode,pat in [('fail','echo ATTACK-EXTRA\nexit 4',0o755,'ATTACK-EXTRA'),('noexec','exit 0',0o644,'not executable'),('interpreter','exit 0',0o755,'cannot execute|required file not found')]:
        header='#!/missing/attack-interpreter' if id=='interpreter' else '#!/bin/bash'
        plant(8,'extra-'+id,'extra.sh '+id,{},'G10','extra',pat,mutate=lambda p,h=header,b=body,m=mode:put(p,'agent_work/chunks/attack/gate/extra.sh',h+'\n'+b+'\n',m))
    def dangling(p):
        f=p/'agent_work/chunks/attack/gate/extra.sh'; f.parent.mkdir(parents=True,exist_ok=True); f.symlink_to('missing.sh'); commit(p); return gate(p,'G10')
    test(8,'extra-dangling','Broken extra.sh symlink must fail closed',dangling,lambda o:red(o,'G10','extra','.'),'Test -L as well as -e; reject dangling symlinks instead of recording skip-none.')

    # 9: use a local clone and a Git wrapper that blocks merge/push/fetch BEFORE side effects.
    def land(p,variant):
        branch='candidate'; chunk='attack'; directory='agent_work/chunks/attack'
        cmd(p,['git','branch','-f','main',clean],ok=True); cmd(p,['git','checkout','-qb',branch],ok=True)
        gated=clean
        if variant=='off-branch':
            cmd(p,['git','checkout','-qb','unrelated'],ok=True); put(p,'unrelated.txt','x'); gated=commit(p); cmd(p,['git','checkout','-q',branch],ok=True)
        res={'overall':'pass','counts_for_landing':True,'sha':gated,'chunk':'attack','partial':False,'stage':'build','dirty_tree':False}
        if variant=='red': res.update(overall='fail',counts_for_landing=False)
        if variant=='partial': res.update(partial=True,counts_for_landing=False)
        if variant=='inconsistent-partial': res['partial']=True
        if variant=='wrong-chunk': res.update(chunk='c00-foundation',stage='foundation')
        put(p,directory+'/gate/result.json',json.dumps(res))
        if variant not in ('missing-review','bypass'):
            verdict='FAIL' if variant=='fail-review' else 'NITS' if variant=='nits' else 'PASS'
            put(p,directory+'/REVIEW-1.md','VERDICT: '+verdict+'\n')
        if variant not in ('missing-adversarial','bypass'): put(p,directory+'/ADVERSARIAL.md','HELD: 1  BROKEN: 0\n')
        if variant=='code-after': append(p,'internal/probe/probe.go','\nconst After=3\n')
        if variant=='extra-after': put(p,directory+'/gate/extra.sh','#!/bin/bash\nexit 1\n',0o755)
        commit(p)
        cmd(p,['git','checkout','-q','main'],ok=True)
        if variant=='unrebased': put(p,'main-only.txt','x'); commit(p)
        if variant=='dirty-main': put(p,'dirty-main.txt','x')
        guard=p/'.git/guard'; guard.mkdir()
        realgit=shutil.which('git',path=ENV['PATH'])
        put(guard,'git',f'#!/bin/bash\ncase "$1" in merge|push|fetch) echo "ATTACK-BLOCKED-$1"; exit 91;; esac\nexec {realgit} "$@"\n',0o755)
        msg=p/'.git/message'; msg.write_text('adversarial: local only\n')
        e={'PATH':str(guard)+':'+ENV['PATH']}
        if variant=='bypass': e['LAND_NO_REVIEW']='1'
        rc,log=cmd(p,['bash','agent_work/orch/land.sh',chunk,branch,str(msg)],e)
        return dict(rc=rc,stdout=log)
    for variant,pattern in [('red','not a full pass'),('partial','not a full pass'),('off-branch','not on candidate'),('code-after','files changed after'),('missing-review','no review file'),('fail-review',"says 'VERDICT: FAIL'"),('nits','NITS without'),('missing-adversarial','no ADVERSARIAL'),('unrebased','not rebased'),('dirty-main','checkout is dirty'),('bypass','no review|chunk'),('inconsistent-partial','not a full pass'),('wrong-chunk','chunk|stage'),('extra-after','files changed after')]:
        test(9,'land-'+variant,'Landing rejects '+variant,lambda p,v=variant:land(p,v),
             lambda o,pat=pattern:o['rc'] in (1,2) and re.search(pat,o['stdout']) is not None,
             {'bypass':'Reject LAND_NO_REVIEW=1 unless chunk is c00-foundation.', 'inconsistent-partial':'Require partial=false and dirty_tree=false independently of counts_for_landing.', 'wrong-chunk':'Require result.chunk to equal the requested chunk and foundation stage only for chunk zero.', 'extra-after':'Remove executable inputs such as gate/extra.sh from the post-gate agent_work exemption.'}.get(variant, 'Reject this invalid landing evidence before merge.'))

    # 10: sabotage the selected canary's plant so the check fails for the wrong reason.
    def canary_wrong(p,kind):
        if ENV.get('SITE_PYLIB'):
            shutil.copytree(ENV['SITE_PYLIB'],p/'.cache/pylib',dirs_exist_ok=True)
        f=p/'scripts/gate-canaries.sh'; s=f.read_text()
        # Confine the canary harness's own throwaway trees to this run directory.
        s=s.replace('/tmp/',str(run)+'/')
        if kind=='lint':
            s=s.replace('return time.Now().UnixNano()', 'return time.NoSuchClock().UnixNano()')
            id='g1-wallclock'; needle='wallclock'
        elif kind=='test':
            s=s.replace('t.Fatal("PLANTED-TEST-FAILURE")','missingFunction("PLANTED-TEST-FAILURE")')
            id='g3-failing-test'; needle='PLANTED-TEST-FAILURE'
        else:
            s=s.replace('return "not a number"','return missingVariable')
            id='g0-compile-error'; needle='not a number'
        f.write_text(s); commit(p)
        rc,log=cmd(p,['bash','scripts/gate-canaries.sh'],{'CANARY_ONLY':id},timeout=200)
        if 'clean copy is green' not in log:
            raise RuntimeError('Canary prerequisite failed: '+log)
        return dict(rc=rc,stdout=log,canary=id,expected_original_reason=needle)
    for kind in ('lint','test','compile'):
        test(10,'canary-wrong-'+kind,'Canary rejects substitute '+kind+' failure',lambda p,k=kind:canary_wrong(p,k),
             lambda o:o['rc']==1 and 'MISSED' in o['stdout'] and 'clean copy is green' in o['stdout'],
             'Require the intended semantic diagnostic, not just source text echoed in a compiler error.')

    # 11: accidental environment settings must not overstate evidence.
    for id,e in [('partial',{'GATE_ONLY':'G3'}),('out',{'GATE_OUT':'agent_work/custom-result'}),('short',{'GATE_SHORT':'1'}),('small-budget',{'SWEEP_SEEDS':'1'})]:
        test(11,'env-'+id,'Environment '+id+' cannot count for landing',lambda p,e=e:gate(p,None,extra=e),
             lambda o:o['rc']==0 and o['result']['overall']=='pass' and not o['result']['counts_for_landing'],
             'Ensure this environment override marks the run non-landing.')
    for id,e,pattern in [('stage',{'GATE_STAGE':'unknown'},'GATE_STAGE must'),('only',{'GATE_ONLY':'G99'},'GATE_ONLY must'),('base',{'GATE_BASE':clean},'own comment baseline'),('preprose',{'GATE_STAGE':'final','GATE_PREPROSE':clean},'HEAD itself')]:
        test(11,'env-'+id,'Invalid/self-referential '+id+' refused',lambda p,e=e:gate(p,None,extra=e),
             lambda o,pat=pattern:o['rc']==2 and pat in o['stdout'],'Reject self-reference/invalid settings before producing evidence.')
    def stale(p):
        a=gate(p,None); b=gate(p,None,extra={'GATE_STAGE':'invalid'})
        return dict(first=a,second=b)
    test(11,'env-stale','Refused run must not leave an earlier landing result looking current',stale,
         lambda o:o['second']['rc']==2 and o['second']['result'] is None,
         'Invalidate the default result before setup validation, or write a refusal result with an invocation identifier and require it at landing.')

    if selected-set(r['id'] for r in rows): raise RuntimeError('Unknown attack IDs: '+str(selected-set(r['id'] for r in rows)))
    held=sum(r['result']=='HELD' for r in rows); broken=len(rows)-held
    first=f'HELD: {held}  BROKEN: {broken}'
    evidence=dict(source_sha=cmd(ROOT,['git','rev-parse','HEAD'],ok=True)[1].strip(),seconds=round(time.monotonic()-started,2),baseline=baseline,attacks=rows)
    OUT.mkdir(parents=True,exist_ok=True)
    (OUT/'evidence.json').write_text(json.dumps(evidence,indent=2)+'\n')
    lines=[first,'', '# Foundation gate adversarial results','',f'Tested source `{evidence["source_sha"]}`. Runtime: {evidence["seconds"]} seconds. '+('Selected attacks only.' if selected else 'Full suite.'),'',
      'Run from the repository root: `./agent_work/adversarial/c00-foundation/attack.sh`. Each attack starts in a fresh disposable local Git clone under `/tmp/zgw-c00-adv/`. The seed has a passing full gate. Fixtures are committed before checks except intentional dirty-tree attacks. Evidence includes stdout, per-check logs and result JSON; see `evidence.json`. A BROKEN label means the stated claim did not hold, including documented limitations; it does not assert every partial-check bypass survives the full gate.', '',
      'Landing experiments run only in disposable clones. A Git wrapper refuses merge, push and fetch before any side effect. `ATTACK-BLOCKED-merge` proves the real landing checks accepted the evidence; it is a failure of the claim, not an actual merge. No real repository landing command or remote push runs.','',
      '| Claim | Attack | Result |','| --- | --- | --- |']
    lines += [f'| {r["claim"]} | `{r["id"]}` — {r["attack"]} | {r["result"]} |' for r in rows]
    for r in rows:
        if r['result']!='BROKEN': continue
        o=r['evidence']; summary={k:v for k,v in o.items() if k!='logs'}
        if 'result' in summary and summary['result']:
            summary['result']={k:v for k,v in summary['result'].items() if k in ('overall','stage','dirty_tree','partial','counts_for_landing','checks')}
        lines += ['',f'## BROKEN: {r["id"]}','',f'Claim {r["claim"]}: {CLAIMS[r["claim"]]}', '', f'Attack: {r["attack"]}.','',
          f'Exact reproduction: `./agent_work/adversarial/c00-foundation/attack.sh {r["id"]}`. The fixture and mutations are defined by this ID in `attack.sh`; this command recreates the seed and isolated clone, applies those mutations, and runs the real check. A selected run overwrites the report, so rerun the full suite to restore it.','',
          'Observed (full logs in `evidence.json`):','```json',json.dumps(summary,indent=2),'```','',f'Smallest suggested fix: {r["fix"]}']
    lines += ['', '## Coverage and limits','',
      '- At least three independent attacks per numbered claim; the table and executable fixtures are the coverage index. Root and nested-module format/build/vet/tidy are separate cases. All six core/engine adapter restrictions are exercised. The Go race attack relies on the runtime detector; its actual diagnostic is required.',
      '- The suite does not prove absence of other bypasses. It does not exhaust arbitrary Go aliasing, indirect calls, every directive spelling, import graph, platform/build-tag combination, or filesystem race. The boundlint alias/helper escapes are documented limits but violate the broad growth claim.',
      '- No live PostgreSQL, fuzz campaign, simulator or CSV pipeline exists at foundation, so their future algorithms cannot be attacked; marker/failure propagation is exercised instead. GitHub CI was read, not remotely dispatched. Real merge/push effects are deliberately blocked.',
      '- The foundation identity-reuse attack demonstrates gate acceptance using c00-foundation with product code; the separate landing wrong-chunk attack demonstrates that landing trusts the caller/result identity. It does not forge a cryptographic certificate; the gate explicitly assumes honest operators.',
      '- testdata is deliberately skipped by the implementation. The report records this mismatch with the adversarial brief rather than treating a documented exclusion as successful coverage.',
      '- The two initial required `kb search` attempts failed before assigning search IDs (no configured kb location for the installed CLI). No corpus files were read and no feedback ID exists. Project findings remain here, within the brief’s permitted edit scope.',
      '- The brief forbids edits outside this folder, so root STATE.md, docs/knowledge and work-scripts were not changed. This folder’s STATE.md is the handoff checkpoint; the attack script is the reusable artifact. No gate fixes were made.','']
    (OUT/'RESULTS.md').write_text('\n'.join(lines))
    (OUT/'STATE.md').write_text('# Adversarial chunk 0 checkpoint\n\nGoal: complete `/root/briefs/zgateway-p2-c00-adversarial.md`.\n\nStatus: full suite finished; results and raw evidence written.\n\nArtifacts: `attack.sh`, `RESULTS.md`, `evidence.json`. The script is the reusable reproduction index.\n\nResume: branch `work/c00-adv`, worktree `/root/Software/mini-gateway-work/c00-adv`. Run `./agent_work/adversarial/c00-foundation/attack.sh` to regenerate all evidence. No background child remains after normal completion; temporary clones are removed in a finally block.\n\nNext: orchestrator evaluates BROKEN findings before landing; no fixes are authorized in this worktree. Root state and durable knowledge directories are outside the brief’s allowed write scope. Commit ID is available via git log for this directory.\n')
    print(first,flush=True)
finally:
    shutil.rmtree(run)
    try: WORK.rmdir()
    except OSError: pass
sys.exit(1 if any(r['result']=='BROKEN' for r in rows) else 0)
PY
