SELECT
  (SELECT count(*) FROM pg_prepared_statements)                       AS prepared,
  (SELECT count(*) FROM pg_cursors)                                   AS cursors,
  (SELECT count(*) FROM pg_listening_channels())                      AS listens,
  (SELECT count(*) FROM pg_locks WHERE pid = pg_backend_pid()
                                   AND locktype = 'advisory')         AS adv_locks,
  (SELECT count(*) FROM pg_settings WHERE source = 'session')         AS session_gucs,
  (SELECT count(*) FROM pg_class WHERE relnamespace = pg_my_temp_schema()) AS temp_rels,
  current_user <> session_user                                        AS role_changed;
