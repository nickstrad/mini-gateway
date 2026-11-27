-- Schema of the dev stack's database, run once by the Postgres image at first start.
-- The table is the one the load tool's operations and the fake Postgres both use:
-- kv (k text primary key, v text), rows k1..k1000. The gateway logs in as role gateway.
CREATE ROLE gateway LOGIN PASSWORD 'mg-dev-gateway';
CREATE TABLE kv (k text PRIMARY KEY, v text NOT NULL);
INSERT INTO kv SELECT 'k' || i, 'v' || i FROM generate_series(1, 1000) AS i;
GRANT SELECT, INSERT, UPDATE, DELETE ON kv TO gateway;
