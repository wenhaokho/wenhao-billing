import { createRouter, createWebHistory } from "vue-router";
import { useAuthStore } from "@/stores/auth";

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", redirect: "/dashboard" },
    {
      path: "/login",
      name: "login",
      component: () => import("@/views/LoginView.vue"),
      meta: { public: true },
    },
    {
      path: "/forgot-password",
      name: "forgot-password",
      component: () => import("@/views/ForgotPasswordView.vue"),
      meta: { public: true },
    },
    {
      path: "/reset-password",
      name: "reset-password",
      component: () => import("@/views/ResetPasswordView.vue"),
      meta: { public: true },
    },
    {
      path: "/account",
      name: "account",
      component: () => import("@/views/AccountView.vue"),
    },
    {
      path: "/dashboard",
      name: "dashboard",
      component: () => import("@/views/PaymentIntakeWidget.vue"),
    },
    {
      path: "/customers",
      name: "customers",
      component: () => import("@/views/CustomersView.vue"),
    },
    {
      path: "/customers/new",
      name: "customer-new",
      component: () => import("@/views/CustomerNewView.vue"),
    },
    {
      path: "/customers/:customerId",
      name: "customer-detail",
      component: () => import("@/views/CustomerDetailView.vue"),
      props: true,
    },
    {
      path: "/invoices",
      name: "invoices",
      component: () => import("@/views/InvoicesView.vue"),
    },
    {
      path: "/invoices/new",
      name: "invoice-new",
      component: () => import("@/views/InvoiceFormView.vue"),
    },
    {
      path: "/invoices/recurring",
      name: "invoices-recurring",
      component: () => import("@/views/RecurringInvoicesView.vue"),
    },
    {
      path: "/invoices/recurring/new",
      name: "invoice-recurring-new",
      component: () => import("@/views/InvoiceFormView.vue"),
    },
    {
      path: "/invoices/recurring/:id/edit",
      name: "invoice-recurring-edit",
      component: () => import("@/views/InvoiceFormView.vue"),
      props: true,
    },
    {
      path: "/invoices/:id/edit",
      name: "invoice-edit",
      component: () => import("@/views/InvoiceFormView.vue"),
      props: true,
    },
    {
      path: "/quotations",
      name: "quotations",
      component: () => import("@/views/QuotationsView.vue"),
    },
    {
      path: "/quotations/new",
      name: "quotation-new",
      component: () => import("@/views/QuotationFormView.vue"),
    },
    {
      path: "/quotations/:quotationId",
      name: "quotation-detail",
      component: () => import("@/views/QuotationDetailView.vue"),
      props: true,
    },
    {
      path: "/quotations/:quotationId/edit",
      name: "quotation-edit",
      component: () => import("@/views/QuotationFormView.vue"),
      props: true,
    },
    {
      path: "/bills",
      name: "bills",
      component: () => import("@/views/BillsView.vue"),
    },
    {
      path: "/bills/new",
      name: "bill-new",
      component: () => import("@/views/BillFormView.vue"),
    },
    {
      path: "/bills/:billId/edit",
      name: "bill-edit",
      component: () => import("@/views/BillFormView.vue"),
      props: true,
    },
    {
      path: "/queue",
      name: "queue",
      component: () => import("@/views/AwaitingFinalizationView.vue"),
    },
    {
      path: "/review",
      name: "review",
      component: () => import("@/views/ManualReviewView.vue"),
    },
    {
      path: "/review/:paymentId",
      name: "review-one",
      component: () => import("@/views/ManualReviewView.vue"),
      props: true,
    },
    {
      path: "/audit-log",
      name: "audit-log",
      component: () => import("@/views/AuditLogView.vue"),
    },
    {
      path: "/vendors",
      name: "vendors",
      component: () => import("@/views/VendorsView.vue"),
    },
    {
      path: "/vendors/new",
      name: "vendor-new",
      component: () => import("@/views/VendorFormView.vue"),
    },
    {
      path: "/vendors/:vendorId",
      name: "vendor-detail",
      component: () => import("@/views/VendorDetailView.vue"),
      props: true,
    },
    {
      path: "/vendors/:vendorId/edit",
      name: "vendor-edit",
      component: () => import("@/views/VendorFormView.vue"),
      props: true,
    },
    {
      path: "/items",
      name: "items",
      component: () => import("@/views/ItemsView.vue"),
    },
    {
      path: "/items/new",
      name: "item-new",
      component: () => import("@/views/ItemFormView.vue"),
    },
    {
      path: "/items/:itemId",
      name: "item-detail",
      component: () => import("@/views/ItemDetailView.vue"),
      props: true,
    },
    {
      path: "/items/:itemId/edit",
      name: "item-edit",
      component: () => import("@/views/ItemFormView.vue"),
      props: true,
    },
    {
      path: "/projects",
      name: "projects",
      component: () => import("@/views/ProjectsView.vue"),
    },
    {
      path: "/projects/new",
      name: "project-new",
      component: () => import("@/views/ProjectFormView.vue"),
    },
    {
      path: "/projects/:projectId",
      name: "project-detail",
      component: () => import("@/views/ProjectDetailView.vue"),
      props: true,
    },
    {
      path: "/projects/:projectId/edit",
      name: "project-edit",
      component: () => import("@/views/ProjectFormView.vue"),
      props: true,
    },
    {
      path: "/chart-of-accounts",
      name: "chart-of-accounts",
      component: () => import("@/views/ChartOfAccountsView.vue"),
    },
    {
      path: "/chart-of-accounts/new",
      name: "account-new",
      component: () => import("@/views/AccountFormView.vue"),
    },
    {
      path: "/chart-of-accounts/:accountId",
      name: "account-edit",
      component: () => import("@/views/AccountFormView.vue"),
      props: true,
    },
    {
      path: "/reports",
      name: "reports",
      component: () => import("@/views/ReportsView.vue"),
    },
    {
      path: "/fx-rates",
      name: "fx-rates",
      component: () => import("@/views/FxRatesView.vue"),
    },
    {
      path: "/fx-rates/new",
      name: "fx-rate-new",
      component: () => import("@/views/FxRateNewView.vue"),
    },
    {
      path: "/users",
      name: "users",
      component: () => import("@/views/UsersView.vue"),
    },
    {
      path: "/users/new",
      name: "user-new",
      component: () => import("@/views/UserInviteView.vue"),
    },
    {
      path: "/users/:userId/reset-password",
      name: "user-reset-password",
      component: () => import("@/views/UserResetPasswordView.vue"),
      props: true,
    },
  ],
});

router.beforeEach(async (to) => {
  if (to.meta.public) return true;
  const auth = useAuthStore();
  if (!auth.user) await auth.fetchMe();
  if (!auth.user) return { name: "login", query: { next: to.fullPath } };
  return true;
});

export default router;
