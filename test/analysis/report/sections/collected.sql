-- "How the data was collected": tagged rows for scripts/report.sh.
--   tier       <tier> <runs> <flushes>
--   schema     <schema versions seen in run.csv, metrics.csv and histograms.csv rows>
--   totals     <runs> <flushes> <min flushes> <max flushes> <min instances> <max instances> <metric rows> <bucket rows> <scenarios>
--   components <components seen in the final flushes>
SELECT * FROM (
  SELECT 'tier' AS tag, tier AS a, count(*)::VARCHAR AS b, sum(flushes)::VARCHAR AS c, '' AS d, '' AS e, '' AS f, '' AS g, '' AS h, '' AS i
  FROM runs GROUP BY tier
  UNION ALL
  SELECT 'schema', string_agg(DISTINCT v::VARCHAR, ',' ORDER BY v::VARCHAR), '', '', '', '', '', '', '', ''
  FROM (SELECT schema_version AS v FROM runs UNION SELECT DISTINCT schema_version FROM metrics UNION SELECT DISTINCT schema_version FROM hist)
  UNION ALL
  SELECT 'totals', count(*)::VARCHAR, sum(flushes)::VARCHAR, min(flushes)::VARCHAR, max(flushes)::VARCHAR,
         min(instances)::VARCHAR, max(instances)::VARCHAR,
         (SELECT count(*) FROM metrics)::VARCHAR, (SELECT count(*) FROM hist)::VARCHAR, count(DISTINCT scenario)::VARCHAR
  FROM runs
  UNION ALL
  SELECT 'components', string_agg(DISTINCT component, ',' ORDER BY component), '', '', '', '', '', '', '', ''
  FROM final
) ORDER BY tag, a;
