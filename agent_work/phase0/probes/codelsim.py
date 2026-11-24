import heapq, random, math, sys
from collections import deque
MS=1_000_000
TARGET=5*MS; INTERVAL=100*MS
def run(variant, lam, c=20, svc=50*MS, T=30_000*MS, seed=1, lifo=False):
    rng=random.Random(seed)
    q=deque(); free=c; ev=[]  # events: (t, kind) kind 0 arrival,1 completion
    t=0; heapq.heappush(ev,(int(rng.expovariate(lam)*1e9),0))
    waits=[]; drops=0; served=0; arrivals=0; maxq=0
    st=dict(first_above=0,dropping=False,drop_next=0,count=0,lastcount=0,
            minDelay=0,intervalEnd=0,reset=True,over=False,lastEmpty=0)
    def ok_to_drop(soj,now,qempty_after):
        if soj<TARGET or qempty_after: st['first_above']=0; return False
        if st['first_above']==0: st['first_above']=now+INTERVAL; return False
        return now>=st['first_above']
    def ctl(t0,count): return t0+int(INTERVAL/math.sqrt(count))
    def pop():
        return q.pop() if (lifo and st.get('lifo_on')) else q.popleft()
    def folly(delay,now):
        if now>st['intervalEnd'] and not st['reset']:
            st['reset']=True; st['intervalEnd']=now+INTERVAL; st['over']= st['minDelay']>TARGET
        if st['reset']:
            st['reset']=False; st['minDelay']=delay; return False
        if delay<st['minDelay']: st['minDelay']=delay
        return st['over'] and delay>2*TARGET
    def dequeue(now):
        nonlocal drops
        while q:
            if variant=='none':
                return pop()
            if variant=='folly':
                st['lifo_on']=st['over']
                a=pop()
                if folly(now-a[0],now): drops+=1; continue
                return a
            if variant=='fas':
                st['lifo_on']= st['lastEmpty'] < now-INTERVAL
                a=pop()
                if now-a[0] > a[1]: drops+=1; continue
                return a
            if variant=='rfc':
                a=q.popleft(); ok=ok_to_drop(now-a[0],now,len(q)==0)
                if st['dropping']:
                    if not ok: st['dropping']=False
                    while now>=st['drop_next'] and st['dropping']:
                        drops+=1; st['count']+=1
                        if not q: st['first_above']=0; st['dropping']=False; a=None; break
                        a=q.popleft(); ok=ok_to_drop(now-a[0],now,len(q)==0)
                        if not ok: st['dropping']=False
                        else: st['drop_next']=ctl(st['drop_next'],st['count'])
                elif ok:
                    drops+=1
                    st['dropping']=True
                    delta=st['count']-st['lastcount']; st['count']=1
                    if delta>1 and now-st['drop_next']<16*INTERVAL: st['count']=delta
                    st['drop_next']=ctl(now,st['count']); st['lastcount']=st['count']
                    if not q: st['first_above']=0; return None
                    a=q.popleft(); ok_to_drop(now-a[0],now,len(q)==0)
                if a is None: continue
                return a
        st['lastEmpty']=now
        return None
    while ev:
        t,k=heapq.heappop(ev)
        if t>T: break
        if k==0:
            arrivals+=1
            heapq.heappush(ev,(t+int(rng.expovariate(lam)*1e9),0))
            if not q: st['lastEmpty']=t
            timeout = 5*MS if st['lastEmpty'] < t-INTERVAL else INTERVAL
            q.append((t,timeout)); maxq=max(maxq,len(q))
        else: free+=1
        while free>0 and q:
            a=dequeue(t)
            if a is None: break
            free-=1; served+=1
            if t>10_000*MS: waits.append((t-a[0])/MS)
            heapq.heappush(ev,(t+svc,1))
    waits.sort()
    p=lambda x: waits[min(len(waits)-1,int(x*len(waits)))] if waits else float('nan')
    return dict(v=variant+('+lifo' if lifo else ''),lam=lam,arr=arrivals,served=served,drops=drops,left=len(q),maxq=maxq,p50=round(p(.5),1),p99=round(p(.99),1),max=round(waits[-1],1) if waits else None)
for lam in (360,480,800,4000):
    for v,l in (('none',False),('rfc',False),('folly',False),('folly',True),('fas',False),('fas',True)):
        print(run(v,lam,lifo=l))
