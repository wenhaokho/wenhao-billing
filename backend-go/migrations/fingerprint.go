package migrations

import (
	"context"
	"database/sql"
)

// schemaFingerprint excludes row data, but
// includes user-visible schema definitions. Search path is fixed by the caller
// so PostgreSQL renders object definitions consistently.
func schemaFingerprint(ctx context.Context, conn interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, error) {
	const query = `WITH namespaces AS (
 SELECT oid,nspname FROM pg_namespace WHERE nspname NOT IN ('pg_catalog','information_schema') AND nspname NOT LIKE 'pg_toast%' AND nspname NOT LIKE 'pg_temp_%'
), objects AS (
 SELECT jsonb_build_array('namespace',nspname)::text AS item FROM namespaces
 UNION ALL SELECT jsonb_build_array('named_object',ident.type,ident.schema,ident.identity)::text FROM pg_depend d JOIN namespaces n ON d.refobjid=n.oid AND d.refclassid='pg_namespace'::regclass CROSS JOIN LATERAL pg_identify_object(d.classid,d.objid,d.objsubid) ident WHERE d.deptype='n' AND d.objsubid=0
 UNION ALL SELECT jsonb_build_array('collation',n.nspname,c.collname,c.collprovider,c.collisdeterministic,c.collencoding,c.collcollate,c.collctype,c.colliculocale,c.collversion)::text FROM pg_collation c JOIN namespaces n ON n.oid=c.collnamespace
 UNION ALL SELECT jsonb_build_array('extension',e.extname,e.extversion,n.nspname)::text FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace
 UNION ALL SELECT jsonb_build_array('relation',n.nspname,c.relname,c.relkind,c.relpersistence,c.relrowsecurity,c.relforcerowsecurity,coalesce(pg_get_viewdef(c.oid),''))::text FROM pg_class c JOIN namespaces n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p','v','m','S','f')
 UNION ALL SELECT jsonb_build_array('policy',n.nspname,c.relname,p.polname,p.polcmd,p.polpermissive,coalesce((SELECT jsonb_agg(CASE WHEN r.roleid=0 THEN 'PUBLIC' ELSE pg_get_userbyid(r.roleid) END ORDER BY CASE WHEN r.roleid=0 THEN 'PUBLIC' ELSE pg_get_userbyid(r.roleid) END COLLATE "C") FROM unnest(p.polroles) r(roleid)),'[]'::jsonb),coalesce(pg_get_expr(p.polqual,p.polrelid),''),coalesce(pg_get_expr(p.polwithcheck,p.polrelid),''))::text FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN namespaces n ON n.oid=c.relnamespace
 UNION ALL SELECT jsonb_build_array('column',n.nspname,c.relname,a.attnum,a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attidentity,a.attgenerated,coalesce((SELECT jsonb_build_array(cn.nspname,co.collname) FROM pg_collation co JOIN pg_namespace cn ON cn.oid=co.collnamespace WHERE co.oid=a.attcollation),'null'::jsonb),coalesce(pg_get_expr(d.adbin,d.adrelid),''))::text FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN namespaces n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum WHERE c.relkind IN ('r','p','v','m','f') AND a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT jsonb_build_array('constraint',n.nspname,c.relname,con.conname,pg_get_constraintdef(con.oid),con.convalidated)::text FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid JOIN namespaces n ON n.oid=c.relnamespace
 UNION ALL SELECT jsonb_build_array('index',n.nspname,c.relname,i.relname,pg_get_indexdef(i.oid),x.indisvalid,x.indisready,x.indislive)::text FROM pg_index x JOIN pg_class c ON c.oid=x.indrelid JOIN pg_class i ON i.oid=x.indexrelid JOIN namespaces n ON n.oid=c.relnamespace
 UNION ALL SELECT jsonb_build_array('sequence',n.nspname,c.relname,format_type(s.seqtypid,NULL),s.seqstart,s.seqincrement,s.seqmin,s.seqmax,s.seqcache,s.seqcycle,coalesce((SELECT jsonb_build_array(tbl.relname,a.attname,d.deptype) FROM pg_depend d JOIN pg_class tbl ON tbl.oid=d.refobjid JOIN pg_attribute a ON a.attrelid=tbl.oid AND a.attnum=d.refobjsubid WHERE d.classid='pg_class'::regclass AND d.objid=c.oid AND d.refclassid='pg_class'::regclass AND d.deptype IN ('a','i')), 'null'::jsonb))::text FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid JOIN namespaces n ON n.oid=c.relnamespace
 UNION ALL SELECT jsonb_build_array('function',n.nspname,p.proname,pg_get_function_identity_arguments(p.oid),pg_get_functiondef(p.oid))::text FROM pg_proc p JOIN namespaces n ON n.oid=p.pronamespace WHERE p.prokind IN ('f','p')
 UNION ALL SELECT jsonb_build_array('type',n.nspname,t.typname,t.typtype,coalesce((SELECT jsonb_agg(e.enumlabel ORDER BY e.enumsortorder) FROM pg_enum e WHERE e.enumtypid=t.oid),'[]'::jsonb),coalesce(pg_get_expr(t.typdefaultbin,0),''))::text FROM pg_type t JOIN namespaces n ON n.oid=t.typnamespace WHERE t.typtype IN ('e','d') OR (t.typtype='c' AND EXISTS (SELECT 1 FROM pg_class c WHERE c.oid=t.typrelid AND c.relkind='c'))
 UNION ALL SELECT jsonb_build_array('trigger',n.nspname,c.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled)::text FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN namespaces n ON n.oid=c.relnamespace WHERE NOT t.tgisinternal
) SELECT md5(string_agg(item,E'\n' ORDER BY item COLLATE "C")) FROM objects`
	var hash string
	err := conn.QueryRowContext(ctx, query).Scan(&hash)
	return hash, err
}
