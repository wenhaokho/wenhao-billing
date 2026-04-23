import { ref, computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Dialog from "primevue/dialog";
import Dropdown from "primevue/dropdown";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import DatePicker from "primevue/calendar";
import Tag from "primevue/tag";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
const router = useRouter();
const queryClient = useQueryClient();
// ---- filter controls ----
const statusFilter = ref(null);
const statusOptions = [
    { label: "All", value: null },
    { label: "Draft", value: "DRAFT" },
    { label: "Sent", value: "SENT" },
    { label: "Partial", value: "PARTIAL" },
    { label: "Paid", value: "PAID" },
    { label: "Void", value: "VOID" },
];
const currencyOptions = ["IDR", "SGD", "USD"];
const { data: invoices, isLoading } = useQuery({
    queryKey: ["invoices", statusFilter],
    queryFn: async () => {
        const params = {};
        if (statusFilter.value)
            params.status = statusFilter.value;
        return (await api.get("/invoices", { params })).data;
    },
});
const { data: customers } = useQuery({
    queryKey: ["customers"],
    queryFn: async () => (await api.get("/customers")).data,
});
const customerNameById = computed(() => {
    const map = {};
    for (const c of customers.value ?? [])
        map[c.customer_id] = c.name;
    return map;
});
// ---- milestone create dialog ----
const showCreate = ref(false);
const form = ref({
    customer_id: null,
    currency: "IDR",
    amount: null,
    milestone_ref: "",
    issue_date: new Date(),
    due_in_days: 14,
});
const createMilestone = useMutation({
    mutationFn: async () => {
        const body = {
            customer_id: form.value.customer_id,
            currency: form.value.currency,
            amount: form.value.amount,
            milestone_ref: form.value.milestone_ref,
            issue_date: form.value.issue_date.toISOString().slice(0, 10),
            due_in_days: form.value.due_in_days,
        };
        return api.post("/invoices/milestone", body);
    },
    onSuccess: () => {
        showCreate.value = false;
        form.value = {
            customer_id: null,
            currency: "IDR",
            amount: null,
            milestone_ref: "",
            issue_date: new Date(),
            due_in_days: 14,
        };
        queryClient.invalidateQueries({ queryKey: ["invoices"] });
        queryClient.invalidateQueries({ queryKey: ["awaiting-finalization"] });
    },
});
function statusSeverity(status) {
    switch (status) {
        case "PAID": return "success";
        case "SENT": return "info";
        case "PARTIAL": return "warning";
        case "VOID": return "secondary";
        case "DRAFT": return "contrast";
        default: return undefined;
    }
}
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
// CSS variable injection 
// CSS variable injection end 
__VLS_asFunctionalElement(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({
    ...{ class: "page-header" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({
    ...{ class: "subtitle" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "page-actions" },
});
const __VLS_0 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    ...{ 'onClick': {} },
    label: "New invoice",
    icon: "pi pi-plus",
}));
const __VLS_2 = __VLS_1({
    ...{ 'onClick': {} },
    label: "New invoice",
    icon: "pi pi-plus",
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
let __VLS_4;
let __VLS_5;
let __VLS_6;
const __VLS_7 = {
    onClick: (...[$event]) => {
        __VLS_ctx.router.push('/invoices/new');
    }
};
var __VLS_3;
const __VLS_8 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
    ...{ 'onClick': {} },
    label: "New recurring",
    icon: "pi pi-refresh",
    severity: "secondary",
}));
const __VLS_10 = __VLS_9({
    ...{ 'onClick': {} },
    label: "New recurring",
    icon: "pi pi-refresh",
    severity: "secondary",
}, ...__VLS_functionalComponentArgsRest(__VLS_9));
let __VLS_12;
let __VLS_13;
let __VLS_14;
const __VLS_15 = {
    onClick: (...[$event]) => {
        __VLS_ctx.router.push('/invoices/recurring/new');
    }
};
var __VLS_11;
const __VLS_16 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
    ...{ 'onClick': {} },
    label: "Quick milestone",
    icon: "pi pi-flag",
    text: true,
}));
const __VLS_18 = __VLS_17({
    ...{ 'onClick': {} },
    label: "Quick milestone",
    icon: "pi pi-flag",
    text: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_17));
let __VLS_20;
let __VLS_21;
let __VLS_22;
const __VLS_23 = {
    onClick: (...[$event]) => {
        __VLS_ctx.showCreate = true;
    }
};
var __VLS_19;
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "filters" },
});
const __VLS_24 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
    modelValue: (__VLS_ctx.statusFilter),
    options: (__VLS_ctx.statusOptions),
    optionLabel: "label",
    optionValue: "value",
    placeholder: "Filter by status",
    showClear: true,
}));
const __VLS_26 = __VLS_25({
    modelValue: (__VLS_ctx.statusFilter),
    options: (__VLS_ctx.statusOptions),
    optionLabel: "label",
    optionValue: "value",
    placeholder: "Filter by status",
    showClear: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_25));
const __VLS_28 = {}.DataTable;
/** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
// @ts-ignore
const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
    ...{ 'onRowClick': {} },
    value: (__VLS_ctx.invoices ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "invoice_id",
    stripedRows: true,
}));
const __VLS_30 = __VLS_29({
    ...{ 'onRowClick': {} },
    value: (__VLS_ctx.invoices ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "invoice_id",
    stripedRows: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_29));
let __VLS_32;
let __VLS_33;
let __VLS_34;
const __VLS_35 = {
    onRowClick: ((ev) => ev.data.status === 'DRAFT' && __VLS_ctx.router.push(`/invoices/${ev.data.invoice_id}/edit`))
};
__VLS_31.slots.default;
{
    const { empty: __VLS_thisSlot } = __VLS_31.slots;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-file" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
const __VLS_36 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
    header: "Invoice #",
}));
const __VLS_38 = __VLS_37({
    header: "Invoice #",
}, ...__VLS_functionalComponentArgsRest(__VLS_37));
__VLS_39.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_39.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    if (row.invoice_number) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({});
        (row.invoice_number);
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
}
var __VLS_39;
const __VLS_40 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_41 = __VLS_asFunctionalComponent(__VLS_40, new __VLS_40({
    header: "Customer",
}));
const __VLS_42 = __VLS_41({
    header: "Customer",
}, ...__VLS_functionalComponentArgsRest(__VLS_41));
__VLS_43.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_43.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    (__VLS_ctx.customerNameById[row.customer_id] ?? row.customer_id.slice(0, 8));
}
var __VLS_43;
const __VLS_44 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
    field: "invoice_type",
    header: "Type",
}));
const __VLS_46 = __VLS_45({
    field: "invoice_type",
    header: "Type",
}, ...__VLS_functionalComponentArgsRest(__VLS_45));
const __VLS_48 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_49 = __VLS_asFunctionalComponent(__VLS_48, new __VLS_48({
    header: "Status",
}));
const __VLS_50 = __VLS_49({
    header: "Status",
}, ...__VLS_functionalComponentArgsRest(__VLS_49));
__VLS_51.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_51.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
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
var __VLS_51;
const __VLS_56 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_57 = __VLS_asFunctionalComponent(__VLS_56, new __VLS_56({
    header: "Amount",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_58 = __VLS_57({
    header: "Amount",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_57));
__VLS_59.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_59.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(row.amount, row.currency));
}
var __VLS_59;
const __VLS_60 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({
    header: "Balance",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_62 = __VLS_61({
    header: "Balance",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_61));
__VLS_63.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_63.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(row.balance_due, row.currency));
}
var __VLS_63;
const __VLS_64 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_65 = __VLS_asFunctionalComponent(__VLS_64, new __VLS_64({
    field: "currency",
    header: "Ccy",
}));
const __VLS_66 = __VLS_65({
    field: "currency",
    header: "Ccy",
}, ...__VLS_functionalComponentArgsRest(__VLS_65));
const __VLS_68 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_69 = __VLS_asFunctionalComponent(__VLS_68, new __VLS_68({
    field: "issue_date",
    header: "Issued",
}));
const __VLS_70 = __VLS_69({
    field: "issue_date",
    header: "Issued",
}, ...__VLS_functionalComponentArgsRest(__VLS_69));
const __VLS_72 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({
    field: "due_date",
    header: "Due",
}));
const __VLS_74 = __VLS_73({
    field: "due_date",
    header: "Due",
}, ...__VLS_functionalComponentArgsRest(__VLS_73));
var __VLS_31;
const __VLS_76 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_77 = __VLS_asFunctionalComponent(__VLS_76, new __VLS_76({
    visible: (__VLS_ctx.showCreate),
    header: "New milestone invoice",
    modal: true,
    ...{ style: ({ width: '520px' }) },
}));
const __VLS_78 = __VLS_77({
    visible: (__VLS_ctx.showCreate),
    header: "New milestone invoice",
    modal: true,
    ...{ style: ({ width: '520px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_77));
__VLS_79.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (...[$event]) => {
            __VLS_ctx.createMilestone.mutate();
        } },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_80 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_81 = __VLS_asFunctionalComponent(__VLS_80, new __VLS_80({
    modelValue: (__VLS_ctx.form.customer_id),
    options: (__VLS_ctx.customers ?? []),
    optionLabel: "name",
    optionValue: "customer_id",
    placeholder: "Choose a customer",
    filter: true,
    required: true,
}));
const __VLS_82 = __VLS_81({
    modelValue: (__VLS_ctx.form.customer_id),
    options: (__VLS_ctx.customers ?? []),
    optionLabel: "name",
    optionValue: "customer_id",
    placeholder: "Choose a customer",
    filter: true,
    required: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_81));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_84 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_85 = __VLS_asFunctionalComponent(__VLS_84, new __VLS_84({
    modelValue: (__VLS_ctx.form.currency),
    options: (__VLS_ctx.currencyOptions),
}));
const __VLS_86 = __VLS_85({
    modelValue: (__VLS_ctx.form.currency),
    options: (__VLS_ctx.currencyOptions),
}, ...__VLS_functionalComponentArgsRest(__VLS_85));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_88 = {}.InputNumber;
/** @type {[typeof __VLS_components.InputNumber, ]} */ ;
// @ts-ignore
const __VLS_89 = __VLS_asFunctionalComponent(__VLS_88, new __VLS_88({
    modelValue: (__VLS_ctx.form.amount),
    mode: "decimal",
    minFractionDigits: (__VLS_ctx.form.currency === 'IDR' ? 0 : 2),
    maxFractionDigits: (__VLS_ctx.form.currency === 'IDR' ? 0 : 2),
    useGrouping: (true),
}));
const __VLS_90 = __VLS_89({
    modelValue: (__VLS_ctx.form.amount),
    mode: "decimal",
    minFractionDigits: (__VLS_ctx.form.currency === 'IDR' ? 0 : 2),
    maxFractionDigits: (__VLS_ctx.form.currency === 'IDR' ? 0 : 2),
    useGrouping: (true),
}, ...__VLS_functionalComponentArgsRest(__VLS_89));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_92 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_93 = __VLS_asFunctionalComponent(__VLS_92, new __VLS_92({
    modelValue: (__VLS_ctx.form.milestone_ref),
    placeholder: "e.g. Phase 2 kickoff",
    required: true,
}));
const __VLS_94 = __VLS_93({
    modelValue: (__VLS_ctx.form.milestone_ref),
    placeholder: "e.g. Phase 2 kickoff",
    required: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_93));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_96 = {}.DatePicker;
/** @type {[typeof __VLS_components.DatePicker, ]} */ ;
// @ts-ignore
const __VLS_97 = __VLS_asFunctionalComponent(__VLS_96, new __VLS_96({
    modelValue: (__VLS_ctx.form.issue_date),
    dateFormat: "yy-mm-dd",
}));
const __VLS_98 = __VLS_97({
    modelValue: (__VLS_ctx.form.issue_date),
    dateFormat: "yy-mm-dd",
}, ...__VLS_functionalComponentArgsRest(__VLS_97));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_100 = {}.InputNumber;
/** @type {[typeof __VLS_components.InputNumber, ]} */ ;
// @ts-ignore
const __VLS_101 = __VLS_asFunctionalComponent(__VLS_100, new __VLS_100({
    modelValue: (__VLS_ctx.form.due_in_days),
    min: (0),
    max: (365),
}));
const __VLS_102 = __VLS_101({
    modelValue: (__VLS_ctx.form.due_in_days),
    min: (0),
    max: (365),
}, ...__VLS_functionalComponentArgsRest(__VLS_101));
if (__VLS_ctx.createMilestone.error.value) {
    const __VLS_104 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_105 = __VLS_asFunctionalComponent(__VLS_104, new __VLS_104({
        severity: "error",
        closable: (false),
    }));
    const __VLS_106 = __VLS_105({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_105));
    __VLS_107.slots.default;
    (__VLS_ctx.createMilestone.error.value?.response?.data?.detail ?? 'Create failed');
    var __VLS_107;
}
{
    const { footer: __VLS_thisSlot } = __VLS_79.slots;
    const __VLS_108 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_109 = __VLS_asFunctionalComponent(__VLS_108, new __VLS_108({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_110 = __VLS_109({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_109));
    let __VLS_112;
    let __VLS_113;
    let __VLS_114;
    const __VLS_115 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showCreate = false;
        }
    };
    var __VLS_111;
    const __VLS_116 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_117 = __VLS_asFunctionalComponent(__VLS_116, new __VLS_116({
        ...{ 'onClick': {} },
        label: "Create DRAFT",
        disabled: (!__VLS_ctx.form.customer_id || !__VLS_ctx.form.amount || !__VLS_ctx.form.milestone_ref),
        loading: (__VLS_ctx.createMilestone.isPending.value),
    }));
    const __VLS_118 = __VLS_117({
        ...{ 'onClick': {} },
        label: "Create DRAFT",
        disabled: (!__VLS_ctx.form.customer_id || !__VLS_ctx.form.amount || !__VLS_ctx.form.milestone_ref),
        loading: (__VLS_ctx.createMilestone.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_117));
    let __VLS_120;
    let __VLS_121;
    let __VLS_122;
    const __VLS_123 = {
        onClick: (...[$event]) => {
            __VLS_ctx.createMilestone.mutate();
        }
    };
    var __VLS_119;
}
var __VLS_79;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['page-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['filters']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-file']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            DataTable: DataTable,
            Column: Column,
            Button: Button,
            Dialog: Dialog,
            Dropdown: Dropdown,
            InputText: InputText,
            InputNumber: InputNumber,
            DatePicker: DatePicker,
            Tag: Tag,
            Message: Message,
            formatAmount: formatAmount,
            router: router,
            statusFilter: statusFilter,
            statusOptions: statusOptions,
            currencyOptions: currencyOptions,
            invoices: invoices,
            isLoading: isLoading,
            customers: customers,
            customerNameById: customerNameById,
            showCreate: showCreate,
            form: form,
            createMilestone: createMilestone,
            statusSeverity: statusSeverity,
        };
    },
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
; /* PartiallyEnd: #4569/main.vue */
