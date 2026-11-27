# mini-gateway: the book

`mini-gateway` is a Go proxy that sits in front of one ordinary Postgres server. It speaks the Postgres wire protocol—the message format clients already use—so applications connect to it as if it were Postgres.

Its job is to bound both the connections and the work reaching that database. Instead of passing every client connection straight to Postgres, the proxy holds a bounded pool of database connections and lends one for a single transaction (transaction pooling). When Postgres reports that the transaction has ended, the connection returns to the pool. Under overload, the proxy enforces per-tenant admission control: each tenant, identified by its startup user name, has a bounded queue. The proxy decides which requests wait, which run, and which are refused quickly with an error stating whether a retry is safe. It also terminates client TLS (encrypted connections) and lets identical in-flight read-only statements share one database round trip.

This book follows the build, one chapter per step. Each chapter is published only after that step's code has been built, reviewed, attacked in an adversarial pass, and passed its landing gate of automated checks. The book grows with the code.

Each chapter states the problem, explains the design and the alternatives turned down, and shows real code and real output. It also covers how the step was tested, what the attacks found, and where the limits are.

The simulator comes first. Before any proxy code exists, the first two chapters build a deterministic simulator. It is a single-threaded test harness with a virtual clock, seeded random streams, simulated byte-stream connections, a fake Postgres, and simulated clients. Every later part of the proxy is proved in that repeatable world before it is tested against a real Postgres server.

Chapters 1 through 6—the simulator kernel, the simulated world, the observability spine, wire sessions, the statement classifier and contracts, and the walking skeleton—are live below. The remaining planned chapters are listed after them and will become links as each chunk lands.
