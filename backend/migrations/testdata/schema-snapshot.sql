-- A text description of the public schema, one object per line, sorted. It
-- reads the catalogs, so two databases that hold the same tables, columns,
-- defaults, constraints, indexes (partial ones with their WHERE), sequences,
-- functions and triggers give the same lines whatever order they were built
-- in. Run by TestSchemaMatchesAlembic against the goose-migrated database and
-- by port/make-baseline.sh against the Alembic one (psql -At -f), so both
-- sides use the same query. Left out: alembic_version and goose_db_version,
-- the two bookkeeping tables, which differ by design.
-- Not compared (Adopt's schema check is blind to these): domain constraints
-- (domains are listed, their CHECKs are not), index validity (indisvalid),
-- row-level security policies, grants and ownership, column collations, and
-- any object outside the public schema.
WITH t AS (
    SELECT c.oid, c.relname, c.relkind
    FROM pg_class c
    WHERE c.relnamespace = 'public'::regnamespace
      AND c.relname NOT IN ('alembic_version', 'goose_db_version', 'goose_db_version_id_seq')
)
SELECT line FROM (
    SELECT format('table %s kind=%s', relname, relkind) AS line
    FROM t WHERE relkind IN ('r', 'p', 'v', 'm')
  UNION ALL
    SELECT format('column %s.%s %s notnull=%s default=%s identity=%s generated=%s',
                  t.relname, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
                  coalesce(pg_get_expr(d.adbin, d.adrelid), ''), a.attidentity, a.attgenerated)
    FROM t
    JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum > 0 AND NOT a.attisdropped
    LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
    WHERE t.relkind IN ('r', 'p', 'v', 'm')
  UNION ALL
    SELECT format('constraint %s %s %s deferrable=%s deferred=%s',
                  t.relname, k.conname, pg_get_constraintdef(k.oid), k.condeferrable, k.condeferred)
    FROM pg_constraint k JOIN t ON t.oid = k.conrelid
  UNION ALL
    SELECT pg_get_indexdef(i.indexrelid)
    FROM pg_index i JOIN t ON t.oid = i.indrelid
  UNION ALL
    SELECT format('sequence %s type=%s start=%s increment=%s min=%s max=%s cache=%s cycle=%s owned_by=%s',
                  s.sequencename, s.data_type, s.start_value, s.increment_by, s.min_value, s.max_value,
                  s.cache_size, s.cycle,
                  coalesce((SELECT o.relname || '.' || a.attname
                            FROM pg_depend dep
                            JOIN pg_class o ON o.oid = dep.refobjid
                            JOIN pg_attribute a ON a.attrelid = dep.refobjid AND a.attnum = dep.refobjsubid
                            WHERE dep.objid = (quote_ident(s.schemaname) || '.' || quote_ident(s.sequencename))::regclass
                              AND dep.deptype = 'a' LIMIT 1), ''))
    FROM pg_sequences s
    WHERE s.schemaname = 'public' AND s.sequencename NOT IN ('goose_db_version_id_seq')
  UNION ALL
    SELECT format('trigger %s %s', t.relname, pg_get_triggerdef(g.oid))
    FROM pg_trigger g JOIN t ON t.oid = g.tgrelid
    WHERE NOT g.tgisinternal
  UNION ALL
    SELECT 'function ' || p.proname || E'\n' || pg_get_functiondef(p.oid)
    FROM pg_proc p
    WHERE p.pronamespace = 'public'::regnamespace
      AND NOT EXISTS (SELECT 1 FROM pg_depend dep WHERE dep.objid = p.oid AND dep.deptype = 'e')
  UNION ALL
    SELECT format('type %s kind=%s labels=%s', ty.typname, ty.typtype,
                  coalesce((SELECT string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder)
                            FROM pg_enum e WHERE e.enumtypid = ty.oid), ''))
    FROM pg_type ty
    WHERE ty.typnamespace = 'public'::regnamespace AND ty.typtype IN ('e', 'd')
  UNION ALL
    SELECT format('view %s %s', relname, pg_get_viewdef(oid))
    FROM t WHERE relkind IN ('v', 'm')
  UNION ALL
    SELECT 'extension ' || extname FROM pg_extension WHERE extname <> 'plpgsql'
) s
ORDER BY line COLLATE "C";
