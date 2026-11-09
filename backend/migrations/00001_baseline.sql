-- +goose Up
-- The Python backend's schema at Alembic head 0028_business_npwp (backend-python/FROZEN.md),
-- produced by port/make-baseline.sh. An existing database whose alembic_version
-- reads that head is stamped as applied instead of running this.

--
-- Name: bill_line_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.bill_line_items (
    line_item_id uuid NOT NULL,
    bill_id uuid NOT NULL,
    item_id uuid,
    expense_account_id integer,
    "position" integer NOT NULL,
    description text NOT NULL,
    quantity numeric(19,4) NOT NULL,
    unit_price numeric(19,4) NOT NULL,
    amount numeric(19,4) NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

--
-- Name: bills; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.bills (
    bill_id uuid NOT NULL,
    vendor_id uuid NOT NULL,
    bill_number character varying(100),
    po_number character varying(100),
    currency character varying(3) NOT NULL,
    subtotal numeric(19,4),
    discount_type character varying(10),
    discount_value numeric(19,4),
    amount numeric(19,4) NOT NULL,
    balance_due numeric(19,4) NOT NULL,
    status character varying(20) DEFAULT 'DRAFT'::character varying NOT NULL,
    issue_date date,
    due_date date,
    payment_terms character varying(30),
    notes text,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    project_id uuid
);

--
-- Name: business_profile; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.business_profile (
    id integer NOT NULL,
    name character varying(255),
    address text,
    contact_email character varying(255),
    contact_phone character varying(50),
    invoice_title character varying(255),
    invoice_summary text,
    logo_url text,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    default_notes text,
    npwp character varying(32),
    npwp_name character varying(255),
    CONSTRAINT ck_business_profile_singleton CHECK ((id = 1))
);

--
-- Name: business_profile_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.business_profile_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: business_profile_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.business_profile_id_seq OWNED BY public.business_profile.id;

--
-- Name: chart_of_accounts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.chart_of_accounts (
    account_id integer NOT NULL,
    code character varying(10) NOT NULL,
    name character varying(100) NOT NULL,
    type character varying(50) NOT NULL,
    active boolean DEFAULT true NOT NULL
);

--
-- Name: chart_of_accounts_account_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.chart_of_accounts_account_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: chart_of_accounts_account_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.chart_of_accounts_account_id_seq OWNED BY public.chart_of_accounts.account_id;

--
-- Name: cloudflare_targets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cloudflare_targets (
    target_id uuid NOT NULL,
    item_id uuid NOT NULL,
    zone_id character varying(100) NOT NULL,
    record_id character varying(100) NOT NULL,
    record_name character varying(255) NOT NULL,
    record_type character varying(20) NOT NULL,
    live_content text NOT NULL,
    maintenance_content text NOT NULL,
    proxied boolean DEFAULT true NOT NULL,
    provider_status character varying(20) DEFAULT 'ACTIVE'::character varying NOT NULL,
    last_action_at timestamp without time zone,
    last_error text,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

--
-- Name: customers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.customers (
    customer_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    matching_aliases jsonb DEFAULT '[]'::jsonb NOT NULL,
    active boolean DEFAULT true NOT NULL,
    contact_email character varying(255),
    contact_phone character varying(64),
    billing_address text,
    contact_first_name character varying(120),
    contact_last_name character varying(120),
    contact_phone_2 character varying(64),
    account_number character varying(255),
    website character varying(255),
    notes text,
    default_currency character varying(3) DEFAULT 'IDR'::character varying NOT NULL,
    billing_address1 character varying(255),
    billing_address2 character varying(255),
    billing_country character varying(100),
    billing_state character varying(255),
    billing_city character varying(255),
    billing_postal_code character varying(32),
    ship_to_name character varying(255),
    shipping_address1 character varying(255),
    shipping_address2 character varying(255),
    shipping_country character varying(100),
    shipping_state character varying(255),
    shipping_city character varying(255),
    shipping_postal_code character varying(32),
    shipping_phone character varying(64),
    shipping_delivery_instructions text,
    quotation_cc_emails jsonb DEFAULT '[]'::jsonb NOT NULL,
    billing_cc_emails jsonb DEFAULT '[]'::jsonb NOT NULL,
    default_wht_type character varying(10) DEFAULT 'NONE'::character varying NOT NULL,
    default_wht_rate numeric(7,4),
    default_wht_gross_up boolean DEFAULT false NOT NULL
);

--
-- Name: fx_rates; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.fx_rates (
    rate_id bigint NOT NULL,
    from_currency character varying(3) NOT NULL,
    to_currency character varying(3) NOT NULL,
    rate numeric(19,8) NOT NULL,
    as_of_date date NOT NULL,
    source character varying(50) DEFAULT 'MANUAL'::character varying NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

--
-- Name: fx_rates_rate_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.fx_rates_rate_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: fx_rates_rate_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.fx_rates_rate_id_seq OWNED BY public.fx_rates.rate_id;

--
-- Name: invoice_line_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.invoice_line_items (
    line_item_id uuid NOT NULL,
    invoice_id uuid NOT NULL,
    item_id uuid,
    "position" integer NOT NULL,
    description text NOT NULL,
    quantity numeric(19,4) NOT NULL,
    unit_price numeric(19,4) NOT NULL,
    amount numeric(19,4) NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

--
-- Name: invoices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.invoices (
    invoice_id uuid NOT NULL,
    customer_id uuid,
    invoice_type character varying(20) NOT NULL,
    currency character varying(3) NOT NULL,
    amount numeric(19,4) NOT NULL,
    balance_due numeric(19,4) NOT NULL,
    status character varying(20) NOT NULL,
    billing_cycle_ref jsonb,
    issue_date date,
    due_date date,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    invoice_number character varying(50),
    po_so_number character varying(100),
    subtotal numeric(19,4),
    discount_type character varying(10),
    discount_value numeric(19,4),
    notes text,
    footer text,
    payment_terms character varying(30),
    is_template boolean DEFAULT false NOT NULL,
    project_id uuid,
    source_quote_id uuid,
    coverage_start date,
    coverage_end date,
    internal_note text,
    wht_type character varying(10) DEFAULT 'NONE'::character varying NOT NULL,
    wht_rate numeric(7,4),
    wht_gross_up boolean DEFAULT false NOT NULL,
    wht_base numeric(19,4),
    wht_amount numeric(19,4) DEFAULT '0'::numeric NOT NULL,
    wht_booked numeric(19,4) DEFAULT '0'::numeric NOT NULL
);

--
-- Name: items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.items (
    item_id uuid NOT NULL,
    sku character varying(50),
    name character varying(255) NOT NULL,
    item_type character varying(20) NOT NULL,
    description text,
    default_currency character varying(3) DEFAULT 'IDR'::character varying NOT NULL,
    default_unit_price numeric(19,4),
    revenue_account_id integer,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    is_sold boolean DEFAULT true NOT NULL,
    is_purchased boolean DEFAULT false NOT NULL,
    expense_account_id integer,
    default_purchase_price numeric(19,4),
    is_hosting boolean DEFAULT false NOT NULL,
    hosting_domain character varying(255),
    hosting_grace_days integer,
    hosting_suspension_enabled boolean,
    hosting_status character varying(20),
    hosting_last_paid_at timestamp without time zone,
    hosting_last_action_at timestamp without time zone,
    hosting_last_error text,
    CONSTRAINT ck_items_type CHECK (item_type IN ('SERVICE', 'USAGE', 'FIXED_FEE'))
);

--
-- Name: journal_entries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.journal_entries (
    entry_id uuid NOT NULL,
    posted_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    source_type character varying(20) NOT NULL,
    source_id uuid NOT NULL,
    memo character varying(500),
    reversed_by_entry_id uuid
);

--
-- Name: journal_lines; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.journal_lines (
    line_id uuid NOT NULL,
    entry_id uuid NOT NULL,
    account_id integer NOT NULL,
    debit numeric(19,4) DEFAULT '0'::numeric NOT NULL,
    credit numeric(19,4) DEFAULT '0'::numeric NOT NULL,
    currency character varying(3) NOT NULL,
    base_amount_debit numeric(19,4) DEFAULT '0'::numeric NOT NULL,
    base_amount_credit numeric(19,4) DEFAULT '0'::numeric NOT NULL,
    fx_rate numeric(19,8) DEFAULT '1'::numeric NOT NULL,
    CONSTRAINT ck_journal_lines_one_sided CHECK ((((debit = (0)::numeric) AND (credit > (0)::numeric)) OR ((credit = (0)::numeric) AND (debit > (0)::numeric))))
);

--
-- Name: oauth_clients; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_clients (
    client_id character varying NOT NULL,
    client_name character varying,
    redirect_uris character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: oauth_refresh_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.oauth_refresh_tokens (
    jti character varying NOT NULL,
    user_id character varying NOT NULL,
    revoked boolean NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

--
-- Name: payment_account; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.payment_account (
    id integer NOT NULL,
    business_profile_id integer NOT NULL,
    currency character varying(3) NOT NULL,
    instructions text NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

--
-- Name: payment_account_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.payment_account_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: payment_account_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.payment_account_id_seq OWNED BY public.payment_account.id;

--
-- Name: payments; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.payments (
    payment_id uuid NOT NULL,
    invoice_id uuid,
    customer_id uuid,
    amount numeric(19,4) NOT NULL,
    currency character varying(3) NOT NULL,
    payer_name character varying(255) NOT NULL,
    payer_reference character varying(255),
    payment_date date NOT NULL,
    intake_source character varying(50) NOT NULL,
    external_ref character varying(255),
    status character varying(30) NOT NULL,
    adjustment_type character varying(30) DEFAULT 'NONE'::character varying NOT NULL,
    confidence_score numeric(5,2),
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    wht_amount numeric(19,4) DEFAULT '0'::numeric NOT NULL
);

--
-- Name: projects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.projects (
    project_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    code character varying(40) NOT NULL,
    name character varying(255) NOT NULL,
    currency character varying(3) NOT NULL,
    contract_value numeric(19,4),
    status character varying(20) DEFAULT 'ACTIVE'::character varying NOT NULL,
    start_date date,
    end_date date,
    notes text,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

--
-- Name: quotation_line_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.quotation_line_items (
    line_item_id uuid NOT NULL,
    quotation_id uuid NOT NULL,
    item_id uuid,
    "position" integer NOT NULL,
    description text NOT NULL,
    quantity numeric(19,4) NOT NULL,
    unit_price numeric(19,4) NOT NULL,
    amount numeric(19,4) NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

--
-- Name: quotations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.quotations (
    quotation_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    project_id uuid,
    quotation_number character varying(50),
    po_so_number character varying(100),
    currency character varying(3) NOT NULL,
    subtotal numeric(19,4),
    discount_type character varying(10),
    discount_value numeric(19,4),
    amount numeric(19,4) NOT NULL,
    status character varying(20) DEFAULT 'DRAFT'::character varying NOT NULL,
    issue_date date,
    valid_until date,
    payment_terms character varying(30),
    notes text,
    footer text,
    last_sent_at timestamp without time zone,
    accepted_at timestamp without time zone,
    accepted_by character varying(255),
    converted_invoice_id uuid,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

--
-- Name: reconciliation_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.reconciliation_log (
    log_id uuid NOT NULL,
    payment_id uuid NOT NULL,
    action character varying(30) NOT NULL,
    reasons text[] DEFAULT ARRAY[]::text[] NOT NULL,
    actor_user_id uuid,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);

--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    user_id uuid NOT NULL,
    email character varying(255) NOT NULL,
    password_hash character varying(255) NOT NULL,
    role character varying(20) DEFAULT 'admin'::character varying NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    display_name character varying(255),
    password_reset_token character varying(64),
    password_reset_expires timestamp without time zone
);

--
-- Name: vendors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.vendors (
    vendor_id uuid NOT NULL,
    name character varying(255) NOT NULL,
    contact_email character varying(255),
    contact_name character varying(255),
    default_currency character varying(3) DEFAULT 'IDR'::character varying NOT NULL,
    payment_terms_days integer DEFAULT 30 NOT NULL,
    tax_id character varying(100),
    notes text,
    active boolean DEFAULT true NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

--
-- Name: business_profile id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.business_profile ALTER COLUMN id SET DEFAULT nextval('public.business_profile_id_seq'::regclass);

--
-- Name: chart_of_accounts account_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_of_accounts ALTER COLUMN account_id SET DEFAULT nextval('public.chart_of_accounts_account_id_seq'::regclass);

--
-- Name: fx_rates rate_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.fx_rates ALTER COLUMN rate_id SET DEFAULT nextval('public.fx_rates_rate_id_seq'::regclass);

--
-- Name: payment_account id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payment_account ALTER COLUMN id SET DEFAULT nextval('public.payment_account_id_seq'::regclass);

--
-- Name: bill_line_items bill_line_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bill_line_items
    ADD CONSTRAINT bill_line_items_pkey PRIMARY KEY (line_item_id);

--
-- Name: bills bills_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bills
    ADD CONSTRAINT bills_pkey PRIMARY KEY (bill_id);

--
-- Name: business_profile business_profile_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.business_profile
    ADD CONSTRAINT business_profile_pkey PRIMARY KEY (id);

--
-- Name: chart_of_accounts chart_of_accounts_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_of_accounts
    ADD CONSTRAINT chart_of_accounts_code_key UNIQUE (code);

--
-- Name: chart_of_accounts chart_of_accounts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.chart_of_accounts
    ADD CONSTRAINT chart_of_accounts_pkey PRIMARY KEY (account_id);

--
-- Name: cloudflare_targets cloudflare_targets_item_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloudflare_targets
    ADD CONSTRAINT cloudflare_targets_item_id_key UNIQUE (item_id);

--
-- Name: cloudflare_targets cloudflare_targets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloudflare_targets
    ADD CONSTRAINT cloudflare_targets_pkey PRIMARY KEY (target_id);

--
-- Name: customers customers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.customers
    ADD CONSTRAINT customers_pkey PRIMARY KEY (customer_id);

--
-- Name: fx_rates fx_rates_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.fx_rates
    ADD CONSTRAINT fx_rates_pkey PRIMARY KEY (rate_id);

--
-- Name: invoice_line_items invoice_line_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoice_line_items
    ADD CONSTRAINT invoice_line_items_pkey PRIMARY KEY (line_item_id);

--
-- Name: invoices invoices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoices
    ADD CONSTRAINT invoices_pkey PRIMARY KEY (invoice_id);

--
-- Name: items items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.items
    ADD CONSTRAINT items_pkey PRIMARY KEY (item_id);

--
-- Name: items items_sku_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.items
    ADD CONSTRAINT items_sku_key UNIQUE (sku);

--
-- Name: journal_entries journal_entries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.journal_entries
    ADD CONSTRAINT journal_entries_pkey PRIMARY KEY (entry_id);

--
-- Name: journal_lines journal_lines_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.journal_lines
    ADD CONSTRAINT journal_lines_pkey PRIMARY KEY (line_id);

--
-- Name: oauth_clients oauth_clients_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_clients
    ADD CONSTRAINT oauth_clients_pkey PRIMARY KEY (client_id);

--
-- Name: oauth_refresh_tokens oauth_refresh_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.oauth_refresh_tokens
    ADD CONSTRAINT oauth_refresh_tokens_pkey PRIMARY KEY (jti);

--
-- Name: payment_account payment_account_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payment_account
    ADD CONSTRAINT payment_account_pkey PRIMARY KEY (id);

--
-- Name: payments payments_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payments
    ADD CONSTRAINT payments_pkey PRIMARY KEY (payment_id);

--
-- Name: projects projects_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_code_key UNIQUE (code);

--
-- Name: projects projects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (project_id);

--
-- Name: quotation_line_items quotation_line_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quotation_line_items
    ADD CONSTRAINT quotation_line_items_pkey PRIMARY KEY (line_item_id);

--
-- Name: quotations quotations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quotations
    ADD CONSTRAINT quotations_pkey PRIMARY KEY (quotation_id);

--
-- Name: quotations quotations_quotation_number_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quotations
    ADD CONSTRAINT quotations_quotation_number_key UNIQUE (quotation_number);

--
-- Name: reconciliation_log reconciliation_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconciliation_log
    ADD CONSTRAINT reconciliation_log_pkey PRIMARY KEY (log_id);

--
-- Name: fx_rates uq_fx_rates_triple; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.fx_rates
    ADD CONSTRAINT uq_fx_rates_triple UNIQUE (from_currency, to_currency, as_of_date);

--
-- Name: invoices uq_invoices_invoice_number; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoices
    ADD CONSTRAINT uq_invoices_invoice_number UNIQUE (invoice_number);

--
-- Name: payment_account uq_payment_account_currency; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payment_account
    ADD CONSTRAINT uq_payment_account_currency UNIQUE (business_profile_id, currency);

--
-- Name: payments uq_payments_source_ref; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payments
    ADD CONSTRAINT uq_payments_source_ref UNIQUE (intake_source, external_ref);

--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);

--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (user_id);

--
-- Name: vendors vendors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vendors
    ADD CONSTRAINT vendors_pkey PRIMARY KEY (vendor_id);

--
-- Name: ix_bill_line_items_bill_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_bill_line_items_bill_id ON public.bill_line_items USING btree (bill_id);

--
-- Name: ix_bills_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_bills_project_id ON public.bills USING btree (project_id);

--
-- Name: ix_bills_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_bills_status ON public.bills USING btree (status);

--
-- Name: ix_bills_vendor_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_bills_vendor_id ON public.bills USING btree (vendor_id);

--
-- Name: ix_fx_rates_from_to_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_fx_rates_from_to_date ON public.fx_rates USING btree (from_currency, to_currency, as_of_date);

--
-- Name: ix_invoice_line_items_invoice_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_invoice_line_items_invoice_id ON public.invoice_line_items USING btree (invoice_id);

--
-- Name: ix_invoices_customer_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_invoices_customer_id ON public.invoices USING btree (customer_id);

--
-- Name: ix_invoices_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_invoices_project_id ON public.invoices USING btree (project_id);

--
-- Name: ix_invoices_source_quote_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_invoices_source_quote_id ON public.invoices USING btree (source_quote_id);

--
-- Name: ix_invoices_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_invoices_status ON public.invoices USING btree (status);

--
-- Name: ix_items_is_hosting; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_items_is_hosting ON public.items USING btree (is_hosting) WHERE (is_hosting = true);

--
-- Name: ix_items_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_items_name ON public.items USING btree (name);

--
-- Name: ix_journal_entries_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_journal_entries_source ON public.journal_entries USING btree (source_type, source_id);

--
-- Name: ix_journal_lines_entry_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_journal_lines_entry_id ON public.journal_lines USING btree (entry_id);

--
-- Name: ix_payments_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_payments_status ON public.payments USING btree (status);

--
-- Name: ix_projects_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_projects_code ON public.projects USING btree (code);

--
-- Name: ix_projects_customer_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_projects_customer_id ON public.projects USING btree (customer_id);

--
-- Name: ix_quotation_line_items_quotation_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_quotation_line_items_quotation_id ON public.quotation_line_items USING btree (quotation_id);

--
-- Name: ix_quotations_converted_invoice_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_quotations_converted_invoice_id ON public.quotations USING btree (converted_invoice_id);

--
-- Name: ix_quotations_customer_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_quotations_customer_id ON public.quotations USING btree (customer_id);

--
-- Name: ix_quotations_project_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_quotations_project_id ON public.quotations USING btree (project_id);

--
-- Name: ix_reconciliation_log_payment_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_reconciliation_log_payment_id ON public.reconciliation_log USING btree (payment_id);

--
-- Name: ix_users_password_reset_token; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ix_users_password_reset_token ON public.users USING btree (password_reset_token);

--
-- Name: ix_vendors_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX ix_vendors_name ON public.vendors USING btree (name);

--
-- Name: uq_payment_account_one_default; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_payment_account_one_default ON public.payment_account USING btree (business_profile_id) WHERE is_default;

--
-- Name: bill_line_items bill_line_items_bill_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bill_line_items
    ADD CONSTRAINT bill_line_items_bill_id_fkey FOREIGN KEY (bill_id) REFERENCES public.bills(bill_id) ON DELETE CASCADE;

--
-- Name: bill_line_items bill_line_items_expense_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bill_line_items
    ADD CONSTRAINT bill_line_items_expense_account_id_fkey FOREIGN KEY (expense_account_id) REFERENCES public.chart_of_accounts(account_id);

--
-- Name: bill_line_items bill_line_items_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bill_line_items
    ADD CONSTRAINT bill_line_items_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.items(item_id);

--
-- Name: bills bills_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bills
    ADD CONSTRAINT bills_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(project_id);

--
-- Name: bills bills_vendor_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.bills
    ADD CONSTRAINT bills_vendor_id_fkey FOREIGN KEY (vendor_id) REFERENCES public.vendors(vendor_id);

--
-- Name: cloudflare_targets cloudflare_targets_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cloudflare_targets
    ADD CONSTRAINT cloudflare_targets_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.items(item_id) ON DELETE CASCADE;

--
-- Name: items fk_items_expense_account; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.items
    ADD CONSTRAINT fk_items_expense_account FOREIGN KEY (expense_account_id) REFERENCES public.chart_of_accounts(account_id);

--
-- Name: invoice_line_items invoice_line_items_invoice_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoice_line_items
    ADD CONSTRAINT invoice_line_items_invoice_id_fkey FOREIGN KEY (invoice_id) REFERENCES public.invoices(invoice_id) ON DELETE CASCADE;

--
-- Name: invoice_line_items invoice_line_items_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoice_line_items
    ADD CONSTRAINT invoice_line_items_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.items(item_id);

--
-- Name: invoices invoices_customer_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoices
    ADD CONSTRAINT invoices_customer_id_fkey FOREIGN KEY (customer_id) REFERENCES public.customers(customer_id);

--
-- Name: invoices invoices_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoices
    ADD CONSTRAINT invoices_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(project_id);

--
-- Name: invoices invoices_source_quote_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.invoices
    ADD CONSTRAINT invoices_source_quote_id_fkey FOREIGN KEY (source_quote_id) REFERENCES public.quotations(quotation_id);

--
-- Name: items items_revenue_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.items
    ADD CONSTRAINT items_revenue_account_id_fkey FOREIGN KEY (revenue_account_id) REFERENCES public.chart_of_accounts(account_id);

--
-- Name: journal_entries journal_entries_reversed_by_entry_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.journal_entries
    ADD CONSTRAINT journal_entries_reversed_by_entry_id_fkey FOREIGN KEY (reversed_by_entry_id) REFERENCES public.journal_entries(entry_id);

--
-- Name: journal_lines journal_lines_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.journal_lines
    ADD CONSTRAINT journal_lines_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.chart_of_accounts(account_id);

--
-- Name: journal_lines journal_lines_entry_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.journal_lines
    ADD CONSTRAINT journal_lines_entry_id_fkey FOREIGN KEY (entry_id) REFERENCES public.journal_entries(entry_id);

--
-- Name: payment_account payment_account_business_profile_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payment_account
    ADD CONSTRAINT payment_account_business_profile_id_fkey FOREIGN KEY (business_profile_id) REFERENCES public.business_profile(id) ON DELETE CASCADE;

--
-- Name: payments payments_customer_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payments
    ADD CONSTRAINT payments_customer_id_fkey FOREIGN KEY (customer_id) REFERENCES public.customers(customer_id);

--
-- Name: payments payments_invoice_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.payments
    ADD CONSTRAINT payments_invoice_id_fkey FOREIGN KEY (invoice_id) REFERENCES public.invoices(invoice_id);

--
-- Name: projects projects_customer_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_customer_id_fkey FOREIGN KEY (customer_id) REFERENCES public.customers(customer_id);

--
-- Name: quotation_line_items quotation_line_items_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quotation_line_items
    ADD CONSTRAINT quotation_line_items_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.items(item_id);

--
-- Name: quotation_line_items quotation_line_items_quotation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quotation_line_items
    ADD CONSTRAINT quotation_line_items_quotation_id_fkey FOREIGN KEY (quotation_id) REFERENCES public.quotations(quotation_id) ON DELETE CASCADE;

--
-- Name: quotations quotations_converted_invoice_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quotations
    ADD CONSTRAINT quotations_converted_invoice_id_fkey FOREIGN KEY (converted_invoice_id) REFERENCES public.invoices(invoice_id);

--
-- Name: quotations quotations_customer_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quotations
    ADD CONSTRAINT quotations_customer_id_fkey FOREIGN KEY (customer_id) REFERENCES public.customers(customer_id);

--
-- Name: quotations quotations_project_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.quotations
    ADD CONSTRAINT quotations_project_id_fkey FOREIGN KEY (project_id) REFERENCES public.projects(project_id);

--
-- Name: reconciliation_log reconciliation_log_actor_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconciliation_log
    ADD CONSTRAINT reconciliation_log_actor_user_id_fkey FOREIGN KEY (actor_user_id) REFERENCES public.users(user_id);

--
-- Name: reconciliation_log reconciliation_log_payment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reconciliation_log
    ADD CONSTRAINT reconciliation_log_payment_id_fkey FOREIGN KEY (payment_id) REFERENCES public.payments(payment_id);

-- Rows the Alembic migrations insert (business_profile chart_of_accounts fx_rates).
INSERT INTO public.business_profile (id, name, address, contact_email, contact_phone, invoice_title, invoice_summary, logo_url, updated_at, default_notes, npwp, npwp_name) VALUES (1, NULL, NULL, NULL, NULL, NULL, NULL, NULL, '2026-11-08 20:27:22.570386', NULL, NULL, NULL);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (1, '1000', 'Operating Bank', 'ASSET', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (10, '5100', 'Cloud Infrastructure', 'COGS', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (11, '5200', 'AI Inference Costs', 'COGS', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (12, '4900', 'FX Gain (Realised)', 'INCOME', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (13, '5900', 'FX Loss (Realised)', 'EXPENSE', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (14, '1300', 'Prepaid Income Tax (PPh dibayar di muka)', 'ASSET', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (2, '1100', 'Accounts Receivable', 'ASSET', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (3, '2000', 'Accounts Payable', 'LIABILITY', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (4, '2100', 'Sales Tax Payable', 'LIABILITY', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (5, '2200', 'Unbilled AR (Accrued Revenue)', 'LIABILITY', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (6, '4000', 'Fixed-Fee Revenue', 'INCOME', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (7, '4100', 'Support/Maintenance Revenue', 'INCOME', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (8, '4200', 'Usage-Based Revenue (AI Tokens)', 'INCOME', true);
INSERT INTO public.chart_of_accounts (account_id, code, name, type, active) VALUES (9, '5000', 'External Contractor Fees', 'COGS', true);
INSERT INTO public.fx_rates (rate_id, from_currency, to_currency, rate, as_of_date, source, created_at) VALUES (1, 'IDR', 'IDR', 1.00000000, '2026-11-08', 'SEED', '2026-11-08 20:27:22.570386');
SELECT pg_catalog.setval('public.business_profile_id_seq', 1, false);
SELECT pg_catalog.setval('public.chart_of_accounts_account_id_seq', 14, true);
SELECT pg_catalog.setval('public.fx_rates_rate_id_seq', 1, true);

-- +goose Down
-- The baseline is not reversible: it is the schema of a database that holds data.
SELECT 1/0;
