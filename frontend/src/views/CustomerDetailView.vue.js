import { computed, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Chip from "primevue/chip";
import Tag from "primevue/tag";
import TabView from "primevue/tabview";
import TabPanel from "primevue/tabpanel";
import Dialog from "primevue/dialog";
import InputText from "primevue/inputtext";
import Textarea from "primevue/textarea";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount, formatMoney } from "@/utils/money";
const props = defineProps();
const router = useRouter();
const customerQuery = useQuery({
    queryKey: ["customer", () => props.customerId],
    queryFn: async () => (await api.get(`/customers/${props.customerId}`)).data,
});
const billingQuery = useQuery({
    queryKey: ["billing-summary", () => props.customerId],
    queryFn: async () => (await api.get(`/customers/${props.customerId}/billing-summary`)).data,
});
const invoicesQuery = useQuery({
    queryKey: ["customer-invoices", () => props.customerId],
    queryFn: async () => (await api.get(`/customers/${props.customerId}/invoices`)).data,
});
const today = new Date();
function daysUntilDue(dueDate) {
    const d = new Date(dueDate + "T00:00:00");
    const diffMs = d.getTime() - today.getTime();
    return Math.round(diffMs / (1000 * 60 * 60 * 24));
}
function isOverdue(row) {
    if (row.status !== "SENT" && row.status !== "PARTIAL")
        return false;
    return daysUntilDue(row.due_date) < 0;
}
function dueLabel(row) {
    const days = daysUntilDue(row.due_date);
    if (days < 0)
        return `Due ${Math.abs(days)} days ago`;
    if (days === 0)
        return "Due today";
    return `Due in ${days} days`;
}
function statusSeverity(status) {
    switch (status) {
        case "PAID": return "success";
        case "SENT": return "info";
        case "PARTIAL": return "warning";
        case "DRAFT": return "secondary";
        case "VOID": return "danger";
        default: return "secondary";
    }
}
function invoiceTypeLabel(type) {
    return type.charAt(0) + type.slice(1).toLowerCase();
}
function formatSumList(list) {
    if (!list || list.length === 0)
        return "—";
    return list.map((c) => formatMoney(c.amount, c.currency)).join(" · ");
}
const lastItemLabel = computed(() => {
    const s = billingQuery.data.value;
    if (!s || !s.last_invoice_issue_date)
        return null;
    const d = new Date(s.last_invoice_issue_date + "T00:00:00");
    return `Invoice on ${d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" })}`;
});
function goBack() {
    router.push({ name: "customers" });
}
// --- Edit dialog (in-place) ---
const queryClient = useQueryClient();
function emptyForm() {
    return {
        name: "",
        contact_name: "",
        contact_email: "",
        contact_phone: "",
        billing_address: "",
        aliases: [],
        active: true,
    };
}
const showEdit = ref(false);
const form = ref(emptyForm());
const aliasDraft = ref("");
function addAlias() {
    const v = aliasDraft.value.trim();
    if (!v)
        return;
    form.value.aliases = [...form.value.aliases, v];
    aliasDraft.value = "";
}
function removeAlias(i) {
    form.value.aliases = form.value.aliases.filter((_, idx) => idx !== i);
}
function openEdit() {
    const c = customerQuery.data.value;
    if (!c)
        return;
    form.value = {
        name: c.name,
        contact_name: c.contact_name ?? "",
        contact_email: c.contact_email ?? "",
        contact_phone: c.contact_phone ?? "",
        billing_address: c.billing_address ?? "",
        aliases: [...c.matching_aliases],
        active: c.active,
    };
    aliasDraft.value = "";
    saveCustomer.reset();
    showEdit.value = true;
}
const saveCustomer = useMutation({
    mutationFn: async () => api.patch(`/customers/${props.customerId}`, {
        name: form.value.name,
        matching_aliases: form.value.aliases,
        active: form.value.active,
        contact_name: form.value.contact_name.trim() || null,
        contact_email: form.value.contact_email.trim() || null,
        contact_phone: form.value.contact_phone.trim() || null,
        billing_address: form.value.billing_address.trim() || null,
    }),
    onSuccess: () => {
        showEdit.value = false;
        queryClient.invalidateQueries({ queryKey: ["customer", () => props.customerId] });
        queryClient.invalidateQueries({ queryKey: ["customers"] });
    },
});
// Auto-open the edit dialog if we were navigated here with ?edit=1
watch([() => router.currentRoute.value.query.edit, () => customerQuery.data.value], ([flag, cust]) => {
    if (flag && cust) {
        openEdit();
        router.replace({ query: {} });
    }
}, { immediate: true });
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['cd-layout']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-line']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-line']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-line']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi-row']} */ ;
/** @type {__VLS_StyleScopedClasses['section-head']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
// CSS variable injection 
// CSS variable injection end 
__VLS_asFunctionalElement(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "breadcrumbs" },
});
const __VLS_0 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    ...{ 'onClick': {} },
    label: "Customers",
    icon: "pi pi-arrow-left",
    text: true,
    size: "small",
}));
const __VLS_2 = __VLS_1({
    ...{ 'onClick': {} },
    label: "Customers",
    icon: "pi pi-arrow-left",
    text: true,
    size: "small",
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
let __VLS_4;
let __VLS_5;
let __VLS_6;
const __VLS_7 = {
    onClick: (__VLS_ctx.goBack)
};
var __VLS_3;
if (__VLS_ctx.customerQuery.error.value) {
    const __VLS_8 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
        severity: "error",
        closable: (false),
    }));
    const __VLS_10 = __VLS_9({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_9));
    __VLS_11.slots.default;
    var __VLS_11;
}
if (__VLS_ctx.customerQuery.data.value) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-header" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({
        ...{ class: "cd-title" },
    });
    (__VLS_ctx.customerQuery.data.value.name);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-actions" },
    });
    const __VLS_12 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
        ...{ 'onClick': {} },
        label: "Edit customer",
        outlined: true,
    }));
    const __VLS_14 = __VLS_13({
        ...{ 'onClick': {} },
        label: "Edit customer",
        outlined: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_13));
    let __VLS_16;
    let __VLS_17;
    let __VLS_18;
    const __VLS_19 = {
        onClick: (__VLS_ctx.openEdit)
    };
    var __VLS_15;
}
if (__VLS_ctx.customerQuery.data.value) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-layout" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.aside, __VLS_intrinsicElements.aside)({
        ...{ class: "cd-sidebar" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-card" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-avatar" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-user" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-group" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-label" },
    });
    if (__VLS_ctx.customerQuery.data.value.contact_name) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "cd-strong" },
        });
        (__VLS_ctx.customerQuery.data.value.contact_name);
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "muted" },
        });
    }
    if (__VLS_ctx.customerQuery.data.value.contact_email) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "cd-line" },
        });
        __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
            ...{ class: "pi pi-envelope" },
        });
        __VLS_asFunctionalElement(__VLS_intrinsicElements.a, __VLS_intrinsicElements.a)({
            href: (`mailto:${__VLS_ctx.customerQuery.data.value.contact_email}`),
        });
        (__VLS_ctx.customerQuery.data.value.contact_email);
    }
    if (__VLS_ctx.customerQuery.data.value.contact_phone) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "cd-line" },
        });
        __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
            ...{ class: "pi pi-phone" },
        });
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        (__VLS_ctx.customerQuery.data.value.contact_phone);
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div)({
        ...{ class: "cd-divider" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-group" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-label" },
    });
    if (__VLS_ctx.customerQuery.data.value.billing_address) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "cd-address" },
        });
        (__VLS_ctx.customerQuery.data.value.billing_address);
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "muted" },
        });
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div)({
        ...{ class: "cd-divider" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-group" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-label" },
    });
    const __VLS_20 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
        value: (__VLS_ctx.customerQuery.data.value.active ? 'Active' : 'Inactive'),
        severity: (__VLS_ctx.customerQuery.data.value.active ? 'success' : 'secondary'),
    }));
    const __VLS_22 = __VLS_21({
        value: (__VLS_ctx.customerQuery.data.value.active ? 'Active' : 'Inactive'),
        severity: (__VLS_ctx.customerQuery.data.value.active ? 'success' : 'secondary'),
    }, ...__VLS_functionalComponentArgsRest(__VLS_21));
    if (__VLS_ctx.customerQuery.data.value.matching_aliases.length) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "cd-group" },
        });
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "cd-label" },
        });
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "chips" },
        });
        for (const [a] of __VLS_getVForSourceType((__VLS_ctx.customerQuery.data.value.matching_aliases))) {
            const __VLS_24 = {}.Chip;
            /** @type {[typeof __VLS_components.Chip, ]} */ ;
            // @ts-ignore
            const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
                key: (a),
                label: (a),
            }));
            const __VLS_26 = __VLS_25({
                key: (a),
                label: (a),
            }, ...__VLS_functionalComponentArgsRest(__VLS_25));
        }
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-group" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-label" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({
        ...{ class: "small" },
    });
    (__VLS_ctx.customerQuery.data.value.customer_id);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "cd-main" },
    });
    const __VLS_28 = {}.TabView;
    /** @type {[typeof __VLS_components.TabView, typeof __VLS_components.TabView, ]} */ ;
    // @ts-ignore
    const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({}));
    const __VLS_30 = __VLS_29({}, ...__VLS_functionalComponentArgsRest(__VLS_29));
    __VLS_31.slots.default;
    const __VLS_32 = {}.TabPanel;
    /** @type {[typeof __VLS_components.TabPanel, typeof __VLS_components.TabPanel, ]} */ ;
    // @ts-ignore
    const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
        header: "Overview",
    }));
    const __VLS_34 = __VLS_33({
        header: "Overview",
    }, ...__VLS_functionalComponentArgsRest(__VLS_33));
    __VLS_35.slots.default;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi-row" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi-label" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi-value" },
    });
    (__VLS_ctx.formatSumList(__VLS_ctx.billingQuery.data.value?.paid_last_12_months));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi-label" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi-value" },
    });
    (__VLS_ctx.formatSumList(__VLS_ctx.billingQuery.data.value?.total_unpaid));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi-label" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "kpi-value kpi-link" },
    });
    (__VLS_ctx.lastItemLabel ?? "—");
    if (__VLS_ctx.billingQuery.error.value) {
        const __VLS_36 = {}.Message;
        /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
        // @ts-ignore
        const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
            severity: "error",
            closable: (false),
        }));
        const __VLS_38 = __VLS_37({
            severity: "error",
            closable: (false),
        }, ...__VLS_functionalComponentArgsRest(__VLS_37));
        __VLS_39.slots.default;
        var __VLS_39;
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "card-section" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "section-head" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.h3, __VLS_intrinsicElements.h3)({});
    const __VLS_40 = {}.DataTable;
    /** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
    // @ts-ignore
    const __VLS_41 = __VLS_asFunctionalComponent(__VLS_40, new __VLS_40({
        value: (__VLS_ctx.billingQuery.data.value?.unpaid_invoices ?? []),
        loading: (__VLS_ctx.billingQuery.isLoading.value),
        size: "small",
        stripedRows: true,
        dataKey: "invoice_id",
    }));
    const __VLS_42 = __VLS_41({
        value: (__VLS_ctx.billingQuery.data.value?.unpaid_invoices ?? []),
        loading: (__VLS_ctx.billingQuery.isLoading.value),
        size: "small",
        stripedRows: true,
        dataKey: "invoice_id",
    }, ...__VLS_functionalComponentArgsRest(__VLS_41));
    __VLS_43.slots.default;
    {
        const { empty: __VLS_thisSlot } = __VLS_43.slots;
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "empty-mini" },
        });
    }
    const __VLS_44 = {}.Column;
    /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
        header: "Status",
        ...{ style: ({ width: '120px' }) },
    }));
    const __VLS_46 = __VLS_45({
        header: "Status",
        ...{ style: ({ width: '120px' }) },
    }, ...__VLS_functionalComponentArgsRest(__VLS_45));
    __VLS_47.slots.default;
    {
        const { body: __VLS_thisSlot } = __VLS_47.slots;
        const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
        if (__VLS_ctx.isOverdue(row)) {
            const __VLS_48 = {}.Tag;
            /** @type {[typeof __VLS_components.Tag, ]} */ ;
            // @ts-ignore
            const __VLS_49 = __VLS_asFunctionalComponent(__VLS_48, new __VLS_48({
                value: "Overdue",
                severity: "danger",
            }));
            const __VLS_50 = __VLS_49({
                value: "Overdue",
                severity: "danger",
            }, ...__VLS_functionalComponentArgsRest(__VLS_49));
        }
        else {
            const __VLS_52 = {}.Tag;
            /** @type {[typeof __VLS_components.Tag, ]} */ ;
            // @ts-ignore
            const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({
                value: (row.status),
                severity: (__VLS_ctx.statusSeverity(row.status)),
            }));
            const __VLS_54 = __VLS_53({
                value: (row.status),
                severity: (__VLS_ctx.statusSeverity(row.status)),
            }, ...__VLS_functionalComponentArgsRest(__VLS_53));
        }
    }
    var __VLS_47;
    const __VLS_56 = {}.Column;
    /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_57 = __VLS_asFunctionalComponent(__VLS_56, new __VLS_56({
        header: "Type",
        ...{ style: ({ width: '110px' }) },
    }));
    const __VLS_58 = __VLS_57({
        header: "Type",
        ...{ style: ({ width: '110px' }) },
    }, ...__VLS_functionalComponentArgsRest(__VLS_57));
    __VLS_59.slots.default;
    {
        const { body: __VLS_thisSlot } = __VLS_59.slots;
        const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
        (__VLS_ctx.invoiceTypeLabel(row.invoice_type));
    }
    var __VLS_59;
    const __VLS_60 = {}.Column;
    /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({
        header: "Invoice",
    }));
    const __VLS_62 = __VLS_61({
        header: "Invoice",
    }, ...__VLS_functionalComponentArgsRest(__VLS_61));
    __VLS_63.slots.default;
    {
        const { body: __VLS_thisSlot } = __VLS_63.slots;
        const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
        __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({
            ...{ class: "small" },
        });
        (row.invoice_id.slice(0, 8));
    }
    var __VLS_63;
    const __VLS_64 = {}.Column;
    /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_65 = __VLS_asFunctionalComponent(__VLS_64, new __VLS_64({
        header: "Due",
    }));
    const __VLS_66 = __VLS_65({
        header: "Due",
    }, ...__VLS_functionalComponentArgsRest(__VLS_65));
    __VLS_67.slots.default;
    {
        const { body: __VLS_thisSlot } = __VLS_67.slots;
        const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: ({ 'text-danger': __VLS_ctx.isOverdue(row) }) },
        });
        (__VLS_ctx.dueLabel(row));
    }
    var __VLS_67;
    const __VLS_68 = {}.Column;
    /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_69 = __VLS_asFunctionalComponent(__VLS_68, new __VLS_68({
        header: "Amount",
        bodyStyle: ({ textAlign: 'right' }),
        headerStyle: ({ textAlign: 'right' }),
    }));
    const __VLS_70 = __VLS_69({
        header: "Amount",
        bodyStyle: ({ textAlign: 'right' }),
        headerStyle: ({ textAlign: 'right' }),
    }, ...__VLS_functionalComponentArgsRest(__VLS_69));
    __VLS_71.slots.default;
    {
        const { body: __VLS_thisSlot } = __VLS_71.slots;
        const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "num" },
        });
        (__VLS_ctx.formatAmount(row.balance_due, row.currency));
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "ccy" },
        });
        (row.currency);
    }
    var __VLS_71;
    var __VLS_43;
    var __VLS_35;
    const __VLS_72 = {}.TabPanel;
    /** @type {[typeof __VLS_components.TabPanel, typeof __VLS_components.TabPanel, ]} */ ;
    // @ts-ignore
    const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({
        header: "Invoices",
    }));
    const __VLS_74 = __VLS_73({
        header: "Invoices",
    }, ...__VLS_functionalComponentArgsRest(__VLS_73));
    __VLS_75.slots.default;
    const __VLS_76 = {}.DataTable;
    /** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
    // @ts-ignore
    const __VLS_77 = __VLS_asFunctionalComponent(__VLS_76, new __VLS_76({
        value: (__VLS_ctx.invoicesQuery.data.value ?? []),
        loading: (__VLS_ctx.invoicesQuery.isLoading.value),
        size: "small",
        stripedRows: true,
        dataKey: "invoice_id",
        rows: (20),
        paginator: true,
    }));
    const __VLS_78 = __VLS_77({
        value: (__VLS_ctx.invoicesQuery.data.value ?? []),
        loading: (__VLS_ctx.invoicesQuery.isLoading.value),
        size: "small",
        stripedRows: true,
        dataKey: "invoice_id",
        rows: (20),
        paginator: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_77));
    __VLS_79.slots.default;
    {
        const { empty: __VLS_thisSlot } = __VLS_79.slots;
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "empty-mini" },
        });
    }
    const __VLS_80 = {}.Column;
    /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_81 = __VLS_asFunctionalComponent(__VLS_80, new __VLS_80({
        header: "Status",
        ...{ style: ({ width: '120px' }) },
    }));
    const __VLS_82 = __VLS_81({
        header: "Status",
        ...{ style: ({ width: '120px' }) },
    }, ...__VLS_functionalComponentArgsRest(__VLS_81));
    __VLS_83.slots.default;
    {
        const { body: __VLS_thisSlot } = __VLS_83.slots;
        const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
        if (__VLS_ctx.isOverdue(row)) {
            const __VLS_84 = {}.Tag;
            /** @type {[typeof __VLS_components.Tag, ]} */ ;
            // @ts-ignore
            const __VLS_85 = __VLS_asFunctionalComponent(__VLS_84, new __VLS_84({
                value: "Overdue",
                severity: "danger",
            }));
            const __VLS_86 = __VLS_85({
                value: "Overdue",
                severity: "danger",
            }, ...__VLS_functionalComponentArgsRest(__VLS_85));
        }
        else {
            const __VLS_88 = {}.Tag;
            /** @type {[typeof __VLS_components.Tag, ]} */ ;
            // @ts-ignore
            const __VLS_89 = __VLS_asFunctionalComponent(__VLS_88, new __VLS_88({
                value: (row.status),
                severity: (__VLS_ctx.statusSeverity(row.status)),
            }));
            const __VLS_90 = __VLS_89({
                value: (row.status),
                severity: (__VLS_ctx.statusSeverity(row.status)),
            }, ...__VLS_functionalComponentArgsRest(__VLS_89));
        }
    }
    var __VLS_83;
    const __VLS_92 = {}.Column;
    /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_93 = __VLS_asFunctionalComponent(__VLS_92, new __VLS_92({
        header: "Type",
    }));
    const __VLS_94 = __VLS_93({
        header: "Type",
    }, ...__VLS_functionalComponentArgsRest(__VLS_93));
    __VLS_95.slots.default;
    {
        const { body: __VLS_thisSlot } = __VLS_95.slots;
        const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
        (__VLS_ctx.invoiceTypeLabel(row.invoice_type));
    }
    var __VLS_95;
    const __VLS_96 = {}.Column;
    /** @type {[typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_97 = __VLS_asFunctionalComponent(__VLS_96, new __VLS_96({
        field: "issue_date",
        header: "Issued",
    }));
    const __VLS_98 = __VLS_97({
        field: "issue_date",
        header: "Issued",
    }, ...__VLS_functionalComponentArgsRest(__VLS_97));
    const __VLS_100 = {}.Column;
    /** @type {[typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_101 = __VLS_asFunctionalComponent(__VLS_100, new __VLS_100({
        field: "due_date",
        header: "Due",
    }));
    const __VLS_102 = __VLS_101({
        field: "due_date",
        header: "Due",
    }, ...__VLS_functionalComponentArgsRest(__VLS_101));
    const __VLS_104 = {}.Column;
    /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_105 = __VLS_asFunctionalComponent(__VLS_104, new __VLS_104({
        header: "Amount",
        bodyStyle: ({ textAlign: 'right' }),
        headerStyle: ({ textAlign: 'right' }),
    }));
    const __VLS_106 = __VLS_105({
        header: "Amount",
        bodyStyle: ({ textAlign: 'right' }),
        headerStyle: ({ textAlign: 'right' }),
    }, ...__VLS_functionalComponentArgsRest(__VLS_105));
    __VLS_107.slots.default;
    {
        const { body: __VLS_thisSlot } = __VLS_107.slots;
        const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "num" },
        });
        (__VLS_ctx.formatAmount(row.amount, row.currency));
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "ccy" },
        });
        (row.currency);
    }
    var __VLS_107;
    const __VLS_108 = {}.Column;
    /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
    // @ts-ignore
    const __VLS_109 = __VLS_asFunctionalComponent(__VLS_108, new __VLS_108({
        header: "Balance",
        bodyStyle: ({ textAlign: 'right' }),
        headerStyle: ({ textAlign: 'right' }),
    }));
    const __VLS_110 = __VLS_109({
        header: "Balance",
        bodyStyle: ({ textAlign: 'right' }),
        headerStyle: ({ textAlign: 'right' }),
    }, ...__VLS_functionalComponentArgsRest(__VLS_109));
    __VLS_111.slots.default;
    {
        const { body: __VLS_thisSlot } = __VLS_111.slots;
        const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "num" },
        });
        (__VLS_ctx.formatAmount(row.balance_due, row.currency));
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "ccy" },
        });
        (row.currency);
    }
    var __VLS_111;
    var __VLS_79;
    var __VLS_75;
    const __VLS_112 = {}.TabPanel;
    /** @type {[typeof __VLS_components.TabPanel, typeof __VLS_components.TabPanel, ]} */ ;
    // @ts-ignore
    const __VLS_113 = __VLS_asFunctionalComponent(__VLS_112, new __VLS_112({
        header: "Activity",
        disabled: true,
    }));
    const __VLS_114 = __VLS_113({
        header: "Activity",
        disabled: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_113));
    __VLS_115.slots.default;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-mini" },
    });
    var __VLS_115;
    var __VLS_31;
}
const __VLS_116 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_117 = __VLS_asFunctionalComponent(__VLS_116, new __VLS_116({
    visible: (__VLS_ctx.showEdit),
    header: "Edit customer",
    modal: true,
    ...{ style: ({ width: '560px' }) },
}));
const __VLS_118 = __VLS_117({
    visible: (__VLS_ctx.showEdit),
    header: "Edit customer",
    modal: true,
    ...{ style: ({ width: '560px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_117));
__VLS_119.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (...[$event]) => {
            __VLS_ctx.saveCustomer.mutate();
        } },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_120 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_121 = __VLS_asFunctionalComponent(__VLS_120, new __VLS_120({
    modelValue: (__VLS_ctx.form.name),
    required: true,
    autofocus: true,
}));
const __VLS_122 = __VLS_121({
    modelValue: (__VLS_ctx.form.name),
    required: true,
    autofocus: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_121));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_124 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_125 = __VLS_asFunctionalComponent(__VLS_124, new __VLS_124({
    modelValue: (__VLS_ctx.form.contact_name),
}));
const __VLS_126 = __VLS_125({
    modelValue: (__VLS_ctx.form.contact_name),
}, ...__VLS_functionalComponentArgsRest(__VLS_125));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_128 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_129 = __VLS_asFunctionalComponent(__VLS_128, new __VLS_128({
    modelValue: (__VLS_ctx.form.contact_email),
    type: "email",
}));
const __VLS_130 = __VLS_129({
    modelValue: (__VLS_ctx.form.contact_email),
    type: "email",
}, ...__VLS_functionalComponentArgsRest(__VLS_129));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_132 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_133 = __VLS_asFunctionalComponent(__VLS_132, new __VLS_132({
    modelValue: (__VLS_ctx.form.contact_phone),
}));
const __VLS_134 = __VLS_133({
    modelValue: (__VLS_ctx.form.contact_phone),
}, ...__VLS_functionalComponentArgsRest(__VLS_133));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_136 = {}.Textarea;
/** @type {[typeof __VLS_components.Textarea, ]} */ ;
// @ts-ignore
const __VLS_137 = __VLS_asFunctionalComponent(__VLS_136, new __VLS_136({
    modelValue: (__VLS_ctx.form.billing_address),
    rows: "3",
}));
const __VLS_138 = __VLS_137({
    modelValue: (__VLS_ctx.form.billing_address),
    rows: "3",
}, ...__VLS_functionalComponentArgsRest(__VLS_137));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_140 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_141 = __VLS_asFunctionalComponent(__VLS_140, new __VLS_140({
    ...{ 'onKeydown': {} },
    modelValue: (__VLS_ctx.aliasDraft),
    placeholder: "e.g. Acme Corp, ACME",
}));
const __VLS_142 = __VLS_141({
    ...{ 'onKeydown': {} },
    modelValue: (__VLS_ctx.aliasDraft),
    placeholder: "e.g. Acme Corp, ACME",
}, ...__VLS_functionalComponentArgsRest(__VLS_141));
let __VLS_144;
let __VLS_145;
let __VLS_146;
const __VLS_147 = {
    onKeydown: (__VLS_ctx.addAlias)
};
var __VLS_143;
if (__VLS_ctx.form.aliases.length) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "chips" },
    });
    for (const [a, i] of __VLS_getVForSourceType((__VLS_ctx.form.aliases))) {
        const __VLS_148 = {}.Chip;
        /** @type {[typeof __VLS_components.Chip, ]} */ ;
        // @ts-ignore
        const __VLS_149 = __VLS_asFunctionalComponent(__VLS_148, new __VLS_148({
            ...{ 'onRemove': {} },
            key: (a),
            label: (a),
            removable: true,
        }));
        const __VLS_150 = __VLS_149({
            ...{ 'onRemove': {} },
            key: (a),
            label: (a),
            removable: true,
        }, ...__VLS_functionalComponentArgsRest(__VLS_149));
        let __VLS_152;
        let __VLS_153;
        let __VLS_154;
        const __VLS_155 = {
            onRemove: (...[$event]) => {
                if (!(__VLS_ctx.form.aliases.length))
                    return;
                __VLS_ctx.removeAlias(i);
            }
        };
        var __VLS_151;
    }
}
if (__VLS_ctx.saveCustomer.error.value) {
    const __VLS_156 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_157 = __VLS_asFunctionalComponent(__VLS_156, new __VLS_156({
        severity: "error",
        closable: (false),
    }));
    const __VLS_158 = __VLS_157({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_157));
    __VLS_159.slots.default;
    (__VLS_ctx.saveCustomer.error.value?.response?.data?.detail ?? 'Save failed');
    var __VLS_159;
}
{
    const { footer: __VLS_thisSlot } = __VLS_119.slots;
    const __VLS_160 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_161 = __VLS_asFunctionalComponent(__VLS_160, new __VLS_160({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_162 = __VLS_161({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_161));
    let __VLS_164;
    let __VLS_165;
    let __VLS_166;
    const __VLS_167 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showEdit = false;
        }
    };
    var __VLS_163;
    const __VLS_168 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_169 = __VLS_asFunctionalComponent(__VLS_168, new __VLS_168({
        ...{ 'onClick': {} },
        label: "Save",
        disabled: (!__VLS_ctx.form.name),
        loading: (__VLS_ctx.saveCustomer.isPending.value),
    }));
    const __VLS_170 = __VLS_169({
        ...{ 'onClick': {} },
        label: "Save",
        disabled: (!__VLS_ctx.form.name),
        loading: (__VLS_ctx.saveCustomer.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_169));
    let __VLS_172;
    let __VLS_173;
    let __VLS_174;
    const __VLS_175 = {
        onClick: (...[$event]) => {
            __VLS_ctx.saveCustomer.mutate();
        }
    };
    var __VLS_171;
}
var __VLS_119;
/** @type {__VLS_StyleScopedClasses['breadcrumbs']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-header']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-title']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-layout']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-sidebar']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-card']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-avatar']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-user']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-group']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-label']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-strong']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-line']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-envelope']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-line']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-phone']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-divider']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-group']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-label']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-address']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-divider']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-group']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-label']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-group']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-label']} */ ;
/** @type {__VLS_StyleScopedClasses['chips']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-group']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-label']} */ ;
/** @type {__VLS_StyleScopedClasses['small']} */ ;
/** @type {__VLS_StyleScopedClasses['cd-main']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi-row']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi-label']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi-value']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi-label']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi-value']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi-label']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi-value']} */ ;
/** @type {__VLS_StyleScopedClasses['kpi-link']} */ ;
/** @type {__VLS_StyleScopedClasses['card-section']} */ ;
/** @type {__VLS_StyleScopedClasses['section-head']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-mini']} */ ;
/** @type {__VLS_StyleScopedClasses['small']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['ccy']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-mini']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['ccy']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['ccy']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-mini']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
/** @type {__VLS_StyleScopedClasses['chips']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            DataTable: DataTable,
            Column: Column,
            Button: Button,
            Chip: Chip,
            Tag: Tag,
            TabView: TabView,
            TabPanel: TabPanel,
            Dialog: Dialog,
            InputText: InputText,
            Textarea: Textarea,
            Message: Message,
            formatAmount: formatAmount,
            customerQuery: customerQuery,
            billingQuery: billingQuery,
            invoicesQuery: invoicesQuery,
            isOverdue: isOverdue,
            dueLabel: dueLabel,
            statusSeverity: statusSeverity,
            invoiceTypeLabel: invoiceTypeLabel,
            formatSumList: formatSumList,
            lastItemLabel: lastItemLabel,
            goBack: goBack,
            showEdit: showEdit,
            form: form,
            aliasDraft: aliasDraft,
            addAlias: addAlias,
            removeAlias: removeAlias,
            openEdit: openEdit,
            saveCustomer: saveCustomer,
        };
    },
    __typeProps: {},
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
    __typeProps: {},
});
; /* PartiallyEnd: #4569/main.vue */
