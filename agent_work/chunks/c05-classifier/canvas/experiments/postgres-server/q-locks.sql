SELECT count(*) FROM pg_locks WHERE pid = pg_backend_pid() AND locktype = 'advisory';
