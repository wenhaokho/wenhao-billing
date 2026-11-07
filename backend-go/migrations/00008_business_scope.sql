-- +goose Up
CREATE TABLE businesses (
    business_id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO businesses (business_id) VALUES (gen_random_uuid());

-- All operations, including the identity creation, run in Goose's transaction.
-- +goose StatementBegin
DO $$
DECLARE
    tbl text;
    pk_columns text;
    c record;
    idx record;
    definition text;
    owned text[] := ARRAY['users','business_profile','payment_account','oauth_clients','oauth_refresh_tokens',
      'customers','vendors','projects','items','chart_of_accounts','fx_rates','cloudflare_targets',
      'invoices','invoice_line_items','quotations','quotation_line_items','bills','bill_line_items',
      'payments','journal_entries','journal_lines','reconciliation_log','recurring_draft_events',
      'recurring_draft_deliveries','hosting_restore_intents','hosting_operation_leases'];
BEGIN
    FOREACH tbl IN ARRAY owned LOOP
        EXECUTE format('ALTER TABLE public.%I ADD COLUMN business_id uuid', tbl);
        EXECUTE format('UPDATE public.%I SET business_id = (SELECT business_id FROM public.businesses)', tbl);
        EXECUTE format('ALTER TABLE public.%I ADD CONSTRAINT %I FOREIGN KEY (business_id) REFERENCES public.businesses(business_id) NOT VALID', tbl, tbl || '_business_id_fkey');
        SELECT string_agg(quote_ident(a.attname), ', ' ORDER BY k.ord) INTO pk_columns
          FROM pg_constraint p CROSS JOIN LATERAL unnest(p.conkey) WITH ORDINALITY k(attnum,ord)
          JOIN pg_attribute a ON a.attrelid=p.conrelid AND a.attnum=k.attnum
          WHERE p.conrelid=format('public.%I',tbl)::regclass AND p.contype='p';
        EXECUTE format('ALTER TABLE public.%I ADD CONSTRAINT %I UNIQUE (business_id, %s)', tbl, tbl || '_business_pk_key', pk_columns);
    END LOOP;

    ALTER TABLE business_profile DROP CONSTRAINT ck_business_profile_singleton;
    ALTER TABLE business_profile ADD CONSTRAINT business_profile_business_key UNIQUE (business_id);

    -- Replace only existing entity foreign keys; snapshots without a v7 FK stay snapshots.
    FOR c IN SELECT co.conname, rel.relname AS tbl, pg_get_constraintdef(co.oid) AS def
      FROM pg_constraint co JOIN pg_class rel ON rel.oid=co.conrelid
      JOIN pg_namespace n ON n.oid=rel.relnamespace
      WHERE n.nspname='public' AND rel.relname=ANY(owned) AND co.contype='f'
        AND co.confrelid <> 'public.businesses'::regclass
    LOOP
        definition := regexp_replace(c.def, '^FOREIGN KEY \(([^)]+)\) REFERENCES ([^(]+)\(([^)]+)\)',
          'FOREIGN KEY (business_id, \1) REFERENCES \2(business_id, \3)');
        IF definition = c.def THEN RAISE EXCEPTION 'unexpected foreign key definition: %', c.def; END IF;
        EXECUTE format('ALTER TABLE public.%I DROP CONSTRAINT %I', c.tbl, c.conname);
        EXECUTE format('ALTER TABLE public.%I ADD CONSTRAINT %I %s NOT VALID', c.tbl, c.conname, definition);
    END LOOP;

    -- Prefix existing natural-key constraints. The newly added composite PK
    -- constraints are excluded, as are global entity primary keys.
    FOR c IN SELECT co.conname, rel.relname AS tbl, pg_get_constraintdef(co.oid) AS def
      FROM pg_constraint co JOIN pg_class rel ON rel.oid=co.conrelid
      JOIN pg_namespace n ON n.oid=rel.relnamespace
      WHERE n.nspname='public' AND rel.relname=ANY(owned) AND co.contype='u'
        AND co.conname NOT LIKE '%\_business\_pk\_key' ESCAPE '\'
        AND co.conname <> 'business_profile_business_key'
    LOOP
        definition := regexp_replace(c.def, '^UNIQUE \(', 'UNIQUE (business_id, ');
        EXECUTE format('ALTER TABLE public.%I DROP CONSTRAINT %I', c.tbl, c.conname);
        EXECUTE format('ALTER TABLE public.%I ADD CONSTRAINT %I %s', c.tbl, c.conname, definition);
    END LOOP;

    -- Prefix existing standalone indexes, including worker partial indexes.
    FOR idx IN SELECT i.indexname, i.indexdef FROM pg_indexes i
      JOIN pg_class t ON t.relname=i.tablename JOIN pg_namespace n ON n.oid=t.relnamespace AND n.nspname=i.schemaname
      JOIN pg_class x ON x.relname=i.indexname AND x.relnamespace=n.oid
      WHERE i.schemaname='public' AND i.tablename=ANY(owned)
        AND NOT EXISTS (SELECT 1 FROM pg_constraint co WHERE co.conindid=x.oid)
    LOOP
        definition := regexp_replace(idx.indexdef, ' USING btree \(', ' USING btree (business_id, ');
        IF definition = idx.indexdef THEN RAISE EXCEPTION 'unexpected index definition: %', idx.indexdef; END IF;
        EXECUTE format('DROP INDEX public.%I', idx.indexname);
        EXECUTE definition;
    END LOOP;
    FOREACH tbl IN ARRAY owned LOOP
        FOR c IN SELECT conname FROM pg_constraint WHERE conrelid=format('public.%I',tbl)::regclass AND contype='f' LOOP
            EXECUTE format('ALTER TABLE public.%I VALIDATE CONSTRAINT %I', tbl, c.conname);
        END LOOP;
        EXECUTE format('ALTER TABLE public.%I ALTER COLUMN business_id SET NOT NULL', tbl);
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Irreversible: restore a backup or export into a fresh legacy-schema target.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'migration 8 cannot be reversed; restore or export to a legacy target'; END $$;
-- +goose StatementEnd
