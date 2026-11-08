#!/usr/bin/perl
# Turns `pg_dump --schema-only --no-owner --no-privileges` of the Alembic
# database into a goose migration body. Reads the dump on stdin, writes the
# migration (without the Up/Down markers) on stdout.
#
# Removed: psql meta-commands (\restrict, \unrestrict), the SET lines and the
# search_path reset (pg_dump qualifies every name with `public.`, so nothing
# needs a search_path; leaving the reset in would leak into the pooled
# session), alembic_version (the Python image's own bookkeeping table; it
# stays on an existing database and is never created here), and the
# "Dumped from/by" comments. Everything else is kept as dumped, including the
# partial unique indexes, column defaults (pg_current_xact_id()), and
# constraint and index names. The one function gets goose's statement
# markers, because its body contains semicolons. One constraint is rewritten to
# its source form (see the comment at the substitution).
use strict;
use warnings;

local $/;
my $dump = <STDIN>;
$dump =~ s/^\x5c(?:un)?restrict [^\n]*\n//mg;

my @blocks = split /(?=^--\n-- Name: )/m, $dump;
shift @blocks;    # the preamble: header comments and SET lines
my @kept;
for my $b (@blocks) {
    $b =~ s/\n--\n-- PostgreSQL database dump complete\n--\n.*\z/\n/s;
    $b =~ s/^SET [^\n]*\n\n?//mg;
    my ($name) = $b =~ /^-- Name: ([^\n]*)/m;
    next if $name =~ /^alembic_version\b/;
    $b =~ s/^(CREATE FUNCTION .*?^\s*\$\$;)$/-- +goose StatementBegin\n$1\n-- +goose StatementEnd/msg;
    # The one constraint pg_dump does not round-trip: Alembic made ck_items_type
    # from `item_type IN ('SERVICE','USAGE','FIXED_FEE')` (0003_vendors_items.py)
    # and the dump spells the stored tree as an explicit array cast, which
    # Postgres parses into a different tree. Writing the source form back
    # builds the identical one.
    $b =~ s/CHECK \(\(\(item_type\)::text = ANY \(\(ARRAY\['SERVICE'::character varying, 'USAGE'::character varying, 'FIXED_FEE'::character varying\]\)::text\[\]\)\)\)/CHECK (item_type IN ('SERVICE', 'USAGE', 'FIXED_FEE'))/;
    $b =~ s/\n+\z/\n/;
    push @kept, $b;
}
print join("\n", @kept);
