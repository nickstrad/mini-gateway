import math
# HDR counts length (Java/Go geometry)
def hdr(lowest, highest, d):
    largest = 2*10**d
    unitMag = int(math.floor(math.log2(lowest)))
    sbcMag = int(math.ceil(math.log2(largest)))
    sbc = 1<<sbcMag
    smallestUntrackable = sbc<<unitMag
    buckets=1
    while smallestUntrackable <= highest:
        smallestUntrackable <<=1; buckets+=1
    n=(buckets+1)*(sbc//2)
    return sbc,buckets,n,n*8
for (lo,hi,unit) in [(1,60_000_000,'us units'),(1000,60_000_000_000,'ns units, lowest=1000')]:
    for d in (2,3):
        print('HDR',unit,'digits',d,'subBucketCount,buckets,countsLen,bytes(int64)=',hdr(lo,hi,d))
# distinct bins
def distinct(M,k): return M*(1-(1-1/M)**k)
for M,k in [(32,4),(100,10),(100,100),(1000,100)]:
    print('distinct servers hit by one client opening k=%d random conns to M=%d: %.2f'%(k,M,distinct(M,k)))
# zipf share of top 1%
def share(N,s,top):
    w=[1/(i**s) for i in range(1,N+1)]; t=sum(w); return sum(w[:top])/t, w[0]/t
for N in (10_000,100_000,1_000_000):
    for s in (0.99,1.01,1.1,1.2,1.5):
        a,b=share(N,s,max(1,N//100)); print('zipf N=%d s=%.2f top1%%keys share=%.3f top1key share=%.3f'%(N,s,a,b))
# max load
for n in (32,1000,10**6):
    print('n=%d ln n/ln ln n=%.2f  ln ln n/ln 2=%.2f'%(n,math.log(n)/math.log(math.log(n)),math.log(math.log(n))/math.log(2)))
