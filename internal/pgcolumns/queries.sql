-- name: ListColumns :many
-- Every column of every table in the given schemas, as Postgres has it: its
-- type as format_type names it, whether it may be NULL, and whether it has a
-- default, is an identity column or is generated, so Postgres fills it in.
SELECT (n.nspname::text || '.' || c.relname::text)::text AS table_name,
       a.attname::text AS column_name,
       format_type(a.atttypid, a.atttypmod)::text AS type,
       (NOT a.attnotnull)::boolean AS nullable,
       (a.atthasdef OR a.attidentity <> '' OR a.attgenerated <> '')::boolean AS has_default
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = ANY(@schemas::text[])
  AND c.relkind IN ('r', 'p')
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY n.nspname, c.relname, a.attnum;
