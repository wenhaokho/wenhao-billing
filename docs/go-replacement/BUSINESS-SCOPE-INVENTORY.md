# Business scope SQL inventory

Snapshot of Go v7. All owned-table statements currently use the single database without a business predicate; the future scope source is the configured, validated `business.ID`, explicitly passed to the operation. A write target is also listed as read when its existing rows are tested by UPDATE, DELETE, or conflict handling. Rows identify source SQL; generated sqlc duplicates are omitted. This index is not a proof of semantic isolation.

## Named sqlc statements

| Location and name | Reads | Writes | Scope source or exact exception |
| --- | --- | --- | --- |
| `backend-go/queries/accounts.sql:1` `ListAccounts` | chart_of_accounts | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/accounts.sql:4` `GetAccount` | chart_of_accounts | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/accounts.sql:7` `CreateAccount` | — | chart_of_accounts | No current business scope; require configured deployment ID. |
| `backend-go/queries/accounts.sql:10` `LockAccount` | chart_of_accounts | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/accounts.sql:13` `UpdateAccount` | chart_of_accounts | chart_of_accounts | No current business scope; require configured deployment ID. |
| `backend-go/queries/bills.sql:1` `InsertBill` | — | bills | No current business scope; require configured deployment ID. |
| `backend-go/queries/bills.sql:5` `InsertBillLine` | — | bill_line_items | No current business scope; require configured deployment ID. |
| `backend-go/queries/bills.sql:9` `BillLines` | bill_line_items | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/bills.sql:12` `GetBill` | bills | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/bills.sql:15` `ListBills` | bills | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/bills.sql:18` `LockBill` | bills | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/bills.sql:21` `UpdateBill` | bills | bills | No current business scope; require configured deployment ID. |
| `backend-go/queries/bills.sql:24` `DeleteBillLines` | bill_line_items | bill_line_items | No current business scope; require configured deployment ID. |
| `backend-go/queries/business_profile.sql:1` `GetBusinessProfile` | business_profile | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/business_profile.sql:4` `GetBusinessProfileDefaultTime` | — | — | PostgreSQL time query; no owned table is accessed. |
| `backend-go/queries/business_profile.sql:7` `ListBusinessPaymentAccounts` | payment_account | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/business_profile.sql:10` `EnsureBusinessProfile` | business_profile | business_profile | No current business scope; require configured deployment ID. |
| `backend-go/queries/business_profile.sql:13` `ApplyBusinessProfileUpdate` | business_profile | business_profile | No current business scope; require configured deployment ID. |
| `backend-go/queries/business_profile.sql:28` `LockBusinessProfile` | business_profile | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/business_profile.sql:31` `DeleteBusinessPaymentAccounts` | payment_account | payment_account | No current business scope; require configured deployment ID. |
| `backend-go/queries/business_profile.sql:34` `InsertBusinessPaymentAccount` | — | payment_account | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:1` `ListCustomers` | customers | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:4` `GetCustomer` | customers | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:7` `DeactivateCustomer` | customers | customers | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:10` `CreateCustomer` | — | customers | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:13` `ApplyCustomerCreate` | customers | customers | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:17` `LockCustomer` | customers | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:20` `ApplyCustomerUpdate` | customers | customers | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:23` `CustomerBalances` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:31` `CustomerInvoices` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:34` `CustomerInvoiceLines` | invoice_line_items, invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:37` `CustomerRecentPayments` | payments | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/customers.sql:40` `CustomerOpenInvoices` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/fx.sql:1` `GetFXRate` | fx_rates | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/fx.sql:6` `ListFXRates` | fx_rates | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/fx.sql:12` `LatestFXRate` | fx_rates | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/fx.sql:18` `CreateFXRate` | — | fx_rates | No current business scope; require configured deployment ID. |
| `backend-go/queries/fx.sql:22` `DeleteFXRate` | fx_rates | fx_rates | No current business scope; require configured deployment ID. |
| `backend-go/queries/fx.sql:25` `LockFXSync` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/queries/fx.sql:28` `FindFXSyncRate` | fx_rates | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/fx.sql:31` `UpdateFXSyncRate` | fx_rates | fx_rates | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:1` `GetInvoice` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:4` `ListInvoices` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:7` `AwaitingFinalization` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:10` `InvoiceLinesByIDs` | invoice_line_items | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:13` `ListRecurringTemplates` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:16` `InsertDraftInvoice` | — | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:20` `MarkInvoiceTemplate` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:23` `InsertDraftInvoiceLine` | — | invoice_line_items | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:26` `LockInvoiceNumberAllocation` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/queries/invoices.sql:29` `InvoiceNumbersForPrefix` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:32` `CustomerWithholdingDefaults` | customers | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:35` `LockInvoice` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:38` `TransitionInvoice` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:41` `UpdateDraftInvoice` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:44` `DeleteInvoiceLines` | invoice_line_items | invoice_line_items | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:47` `LockInvoiceLines` | invoice_line_items | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:50` `InsertMilestoneInvoice` | — | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:54` `LockMilestoneCustomer` | customers | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:57` `LockMilestoneProject` | projects | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:60` `DeleteInvoice` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:63` `RecurringTemplateRows` | customers, invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:80` `LatestRecurringChild` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:86` `UpdateTemplateSchedule` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:89` `FindRecurringCycle` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:92` `SetRecurringChildFields` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:95` `ItemIsHosting` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:98` `ListRecurringCycleCandidates` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:101` `LockUsageInvoice` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:106` `ApplyInvoicePayment` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:109` `InsertInvoicePayment` | — | payments | No current business scope; require configured deployment ID. |
| `backend-go/queries/invoices.sql:113` `InsertInvoicePaymentLog` | — | reconciliation_log | No current business scope; require configured deployment ID. |
| `backend-go/queries/items.sql:1` `ListItems` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/items.sql:4` `GetItem` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/items.sql:7` `LockItem` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/items.sql:10` `InsertItem` | — | items | No current business scope; require configured deployment ID. |
| `backend-go/queries/items.sql:14` `UpdateItem` | items | items | No current business scope; require configured deployment ID. |
| `backend-go/queries/items.sql:17` `GetItemTarget` | cloudflare_targets | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/items.sql:20` `UpsertItemTarget` | cloudflare_targets | cloudflare_targets | No current business scope; require configured deployment ID. |
| `backend-go/queries/items.sql:24` `DeactivateItem` | items | items | No current business scope; require configured deployment ID. |
| `backend-go/queries/ledger.sql:1` `LedgerAccount` | chart_of_accounts | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/ledger.sql:4` `LedgerCreateEntry` | — | journal_entries | No current business scope; require configured deployment ID. |
| `backend-go/queries/ledger.sql:7` `LedgerCreateLine` | — | journal_lines | No current business scope; require configured deployment ID. |
| `backend-go/queries/ledger.sql:10` `LedgerLockEntry` | journal_entries | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/ledger.sql:13` `LedgerLines` | journal_lines | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/ledger.sql:16` `LedgerLinkReversal` | journal_entries | journal_entries | No current business scope; require configured deployment ID. |
| `backend-go/queries/payment_reads.sql:1` `ListPaymentsAwaitingReview` | payments | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/payment_reads.sql:4` `GetPayment` | payments | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/payment_reads.sql:7` `PaymentLogs` | reconciliation_log | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/payment_reads.sql:10` `PaymentAudit` | payments, reconciliation_log, users | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/payment_reads.sql:21` `ListInvoicePayments` | payments | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:1` `ListProjects` | projects | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:4` `GetProject` | projects | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:7` `ProjectInvoices` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:10` `ProjectInvoiceLines` | invoice_line_items, invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:13` `ProjectInvoiceStats` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:16` `ProjectBillStats` | bills | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:19` `CreateProject` | — | projects | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:23` `LockProject` | projects | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:26` `UpdateProject` | projects | projects | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:29` `CountProjectReferences` | bills, invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/projects.sql:32` `DeleteProject` | projects | projects | No current business scope; require configured deployment ID. |
| `backend-go/queries/quotations.sql:1` `ListQuotations` | quotations | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/quotations.sql:3` `GetQuotation` | quotations | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/quotations.sql:5` `QuotationLines` | quotation_line_items | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/quotations.sql:7` `LockQuotationNumberAllocation` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/queries/quotations.sql:9` `QuotationNumbersForPrefix` | quotations | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/quotations.sql:11` `InsertQuotation` | — | quotations | No current business scope; require configured deployment ID. |
| `backend-go/queries/quotations.sql:14` `InsertQuotationLine` | — | quotation_line_items | No current business scope; require configured deployment ID. |
| `backend-go/queries/quotations.sql:17` `LockQuotation` | quotations | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/quotations.sql:19` `DeleteQuotationLines` | quotation_line_items | quotation_line_items | No current business scope; require configured deployment ID. |
| `backend-go/queries/quotations.sql:21` `UpdateQuotation` | quotations | quotations | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:1` `FindUserByEmail` | users | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:4` `SetUserPassword` | users | users | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:7` `GetSessionUserByEmail` | users | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:10` `GetSessionUserByID` | users | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:13` `LockSessionUserByID` | users | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:16` `ListUsers` | users | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:19` `UpdateUserProfile` | users | users | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:22` `CreateUser` | — | users | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:25` `LockUserByResetToken` | users | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:28` `SetResetToken` | users | users | No current business scope; require configured deployment ID. |
| `backend-go/queries/users.sql:31` `ClearResetToken` | users | users | No current business scope; require configured deployment ID. |
| `backend-go/queries/vendors.sql:1` `ListVendors` | vendors | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/vendors.sql:4` `GetVendor` | vendors | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/vendors.sql:7` `CreateVendor` | — | vendors | No current business scope; require configured deployment ID. |
| `backend-go/queries/vendors.sql:11` `DeactivateVendor` | vendors | vendors | No current business scope; require configured deployment ID. |
| `backend-go/queries/vendors.sql:14` `LockVendor` | vendors | — | No current business scope; require configured deployment ID. |
| `backend-go/queries/vendors.sql:17` `UpdateVendor` | vendors | vendors | No current business scope; require configured deployment ID. |

## Inline, embedded, and command SQL

Each row identifies one Go SQL literal. Commands use generated queries or the inline statements listed here; migration SQL files are listed separately. Call sites passing an identifier instead of a literal and any dynamic SQL require review below.

| Location | Reads | Writes | Scope source or exact exception |
| --- | --- | --- | --- |
| `backend-go/internal/platform/fx_sync.go:201` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/internal/platform/fx_sync.go:211` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/internal/platform/hosting_enforcement.go:44` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/internal/platform/hosting_enforcement.go:54` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/internal/platform/hosting_enforcement.go:58` | hosting_operation_leases, items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:127` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:132` | hosting_operation_leases | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:143` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:155` | invoice_line_items, invoices, items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:160` | hosting_operation_leases | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:165` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:183` | cloudflare_targets | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:199` | items | items | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:205` | cloudflare_targets, hosting_operation_leases, items | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:218` | cloudflare_targets, hosting_operation_leases, items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:230` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:235` | cloudflare_targets | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:240` | cloudflare_targets, hosting_operation_leases | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:245` | invoice_line_items, invoices, items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:253` | cloudflare_targets | cloudflare_targets | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:259` | items | items | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:261` | cloudflare_targets | cloudflare_targets | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:270` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:276` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:278` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:281` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_enforcement.go:283` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:68` | hosting_restore_intents | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:76` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:85` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:88` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:94` | hosting_operation_leases, hosting_restore_intents | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:97` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:101` | hosting_operation_leases | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:105` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:112` | hosting_restore_intents, invoice_line_items, invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:120` | hosting_restore_intents | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:129` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:131` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:138` | cloudflare_targets | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:140` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:147` | hosting_operation_leases | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:151` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:165` | cloudflare_targets, hosting_operation_leases, items | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:167` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:177` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:195` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:199` | hosting_operation_leases, hosting_restore_intents | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:209` | cloudflare_targets | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:214` | cloudflare_targets, hosting_operation_leases, items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:219` | invoice_line_items, invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:225` | items | items | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:228` | cloudflare_targets | cloudflare_targets | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:232` | cloudflare_targets | cloudflare_targets | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:238` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:240` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:246` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:250` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:253` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/hosting_restore.go:255` | hosting_operation_leases | hosting_operation_leases | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/invoice_create.go:416` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/invoice_record_payment.go:225` | invoice_line_items, items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/invoice_record_payment.go:247` | cloudflare_targets | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/invoice_record_payment.go:251` | items | items | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/invoice_record_payment.go:255` | hosting_restore_intents | hosting_restore_intents | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/invoice_send.go:249` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/invoice_transitions.go:181` | invoice_line_items, items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/invoice_update.go:350` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_convert.go:182` | quotations | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_convert.go:216` | projects | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_convert.go:228` | items | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_convert.go:316` | invoices | invoices | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_convert.go:332` | quotations | quotations | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_delete.go:62` | quotations | quotations | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_send.go:252` | quotations | quotations | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_status.go:176` | quotations | quotations | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_status.go:178` | quotations | quotations | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_status.go:180` | quotations | quotations | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/quotation_status.go:182` | quotations | quotations | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/receipt_send.go:162` | payments | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_outbox.go:84` | recurring_draft_deliveries | recurring_draft_deliveries | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_outbox.go:86` | recurring_draft_deliveries | recurring_draft_deliveries | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_outbox.go:116` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/internal/platform/recurring_outbox.go:123` | recurring_draft_deliveries, recurring_draft_events | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_outbox.go:130` | users | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_outbox.go:132` | recurring_draft_deliveries, recurring_draft_events | recurring_draft_deliveries | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_outbox.go:146` | recurring_draft_deliveries, recurring_draft_events | recurring_draft_deliveries | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_scan.go:25` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/internal/platform/recurring_scan.go:31` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/internal/platform/recurring_scan.go:35` | users | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_scan.go:54` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_trigger.go:416` | customers, invoices | recurring_draft_events | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_trigger.go:421` | users | recurring_draft_deliveries | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/recurring_trigger.go:423` | — | recurring_draft_deliveries | No current business scope; require configured deployment ID. |
| `backend-go/internal/platform/usage_scan.go:65` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/internal/platform/usage_scan.go:71` | — | — | Application advisory lock is currently global; namespace it by configured business and purpose (no global exception). |
| `backend-go/internal/platform/usage_scan.go:72` | invoices | — | No current business scope; require configured deployment ID. |
| `backend-go/migrations/admission.go:31` | — | — | Global catalog inspection: verifies database version, schema contents, or exact fingerprint. |
| `backend-go/migrations/admission.go:35` | — | — | Global catalog inspection: verifies database version, schema contents, or exact fingerprint. |
| `backend-go/migrations/admission.go:39` | pg_class, pg_namespace, catalog lookup via to_regclass | — | Global migration metadata: checks schema history or excludes Alembic sources. |
| `backend-go/migrations/admission.go:44` | goose_db_version | — | Global migration metadata: checks schema history or excludes Alembic sources. |
| `backend-go/migrations/adopt.go:23` | — | — | Global catalog inspection: verifies database version, schema contents, or exact fingerprint. |
| `backend-go/migrations/adopt.go:30` | alembic_version | — | Global migration metadata: checks schema history or excludes Alembic sources. |
| `backend-go/migrations/adopt.go:34` | catalog lookup via to_regclass | — | Global migration metadata: checks schema history or excludes Alembic sources. |
| `backend-go/migrations/adopt.go:42` | goose_db_version | — | Global migration metadata: checks schema history or excludes Alembic sources. |
| `backend-go/migrations/fingerprint.go:14` | pg_attrdef, pg_attribute, pg_class, pg_collation, pg_constraint, pg_depend, pg_enum, pg_extension, pg_index, pg_namespace, pg_policy, pg_proc, pg_sequence, pg_trigger, pg_type | — | Global catalog inspection: verifies database version, schema contents, or exact fingerprint. |
| `backend-go/migrations/runtime.go:49` | — | — | Global migration lock: serializes schema migration and admission. |
| `backend-go/migrations/runtime.go:52` | — | — | Global migration lock: serializes schema migration and admission. |
| `backend-go/migrations/runtime.go:53` | — | — | Global catalog inspection: verifies database version, schema contents, or exact fingerprint. |
| `backend-go/migrations/runtime.go:57` | — | — | Global catalog inspection: verifies database version, schema contents, or exact fingerprint. |
| `backend-go/migrations/runtime.go:64` | pg_class, pg_namespace, catalog lookup via to_regclass | — | Global migration metadata: checks schema history or excludes Alembic sources. |
| `backend-go/migrations/runtime.go:73` | goose_db_version | — | Global migration metadata: checks schema history or excludes Alembic sources. |
| `backend-go/migrations/runtime.go:88` | pg_class, pg_depend, pg_namespace, pg_proc, pg_type | — | Global catalog inspection: verifies database version, schema contents, or exact fingerprint. |

## V8-only intake and ledger statements

These statements extend the v7 snapshot above. Held intake passes the configured router scope to both writes. Ledger posting and reversal pass their explicit business scope to all four reads.

| Location and name | Reads | Writes | Scope and purpose |
| --- | --- | --- | --- |
| `backend-go/queries_v8/payment_intake.sql:1` `CreateHeldIntakePayment` | payments | payments | Sets `business_id` on the held payment and includes it in the conflict key for duplicate intake. |
| `backend-go/queries_v8/payment_intake.sql:9` `LogHeldIntakePayment` | — | reconciliation_log | Sets `business_id` on the audit row for the payment returned by the insert. |
| `backend-go/queries_v8/ledger.sql:19` `LedgerLockPayment` | payments | — | Locks the payment matching the business and invoice before posting. |
| `backend-go/queries_v8/ledger.sql:26` `LedgerPaymentPosted` | journal_entries | — | Detects an existing posting for the payment in the same business. |
| `backend-go/queries_v8/ledger.sql:32` `LedgerReversalMatches` | journal_entries, journal_lines | — | Checks that the existing reversal belongs to the same business and reverses the original entry's lines. |
| `backend-go/queries_v8/ledger.sql:53` `LedgerHasInvalidLineAccount` | journal_lines, chart_of_accounts | — | Detects journal lines whose account is missing from the same business. |

## Migration SQL and generated queries

`migrations.Files` embeds migrations 00001–00007. These run only for fresh schema setup, not API startup. They are migration infrastructure, not application scope exemptions: baseline DDL creates the owned schema and seeds chart-of-accounts and profile records, and worker migrations create worker tables and indexes. Migration 8 must introduce and backfill ownership transactionally; historical migrations remain unchanged. `internal/dbquery` is sqlc output for the named statements above.

## Dynamic execution and calls without literal SQL

- `backend-go/internal/platform/server.go:65`: `pool.Ping` performs a global PostgreSQL connection health check, accesses no owned table, and is permitted under the health-query exception.

- `migrations/fingerprint.go` passes the catalog query stored in its local `query` constant to `QueryRowContext`; its literal appears above. It inspects schema catalogs globally to verify the migration fingerprint.
- PostgreSQL job advisory locks in `internal/platform` are currently global and must become business-namespaced; they are not migration-level global exceptions.
- Source search found no production `CopyFrom`, `SendBatch`, or batch `Queue` calls. `go:embed` in migrations embeds SQL; platform embeds PDF assets, not SQL. HTTP URL `Query()` calls are not database operations.
- The v7 migration files contain historical DDL and seed statements; they are not rewritten. The sqlc source and inline literals above are the application conversion inventory.

Classification is based on SQL relation references; transaction control, CTE names, built-in functions and parameter-supplied values are not tables. Subsequent SQL coverage work must detect new or dynamically assembled statements and verify joins, locks, conflict targets, and scope semantically.
