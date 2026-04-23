import { ref, computed } from "vue";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import DatePicker from "primevue/calendar";
import TabView from "primevue/tabview";
import TabPanel from "primevue/tabpanel";
import Tag from "primevue/tag";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
const asOf = ref(new Date());
const asOfStr = computed(() => asOf.value.toISOString().slice(0, 10));
const trialBalance = useQuery({
    queryKey: ["trial-balance", asOfStr],
    queryFn: async () => (await api.get("/accounting/reports/trial-balance", {
        params: { as_of: asOfStr.value },
    })).data,
});
const balanceSheet = useQuery({
    queryKey: ["balance-sheet", asOfStr],
    queryFn: async () => (await api.get("/accounting/reports/balance-sheet", {
        params: { as_of: asOfStr.value },
    })).data,
});
const fxReval = useQuery({
    queryKey: ["fx-revaluation", asOfStr],
    queryFn: async () => (await api.get("/accounting/reports/fx-revaluation", {
        params: { as_of: asOfStr.value },
    })).data,
});
function formatRate(v) {
    if (v === null)
        return "—";
    const n = Number(v);
    return Number.isFinite(n)
        ? n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 8 })
        : v;
}
function gainLossSeverity(v) {
    if (v === null)
        return "secondary";
    const n = Number(v);
    if (n > 0)
        return "success";
    if (n < 0)
        return "danger";
    return "secondary";
}
function typeSeverity(t) {
    switch (t) {
        case "ASSET": return "info";
        case "LIABILITY": return "warning";
        case "EQUITY": return "success";
        case "INCOME": return "success";
        case "EXPENSE":
        case "COGS": return "danger";
        default: return undefined;
    }
}
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['meta']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['bs-lines']} */ ;
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
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "page-actions" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({
    ...{ class: "as-of" },
});
const __VLS_0 = {}.DatePicker;
/** @type {[typeof __VLS_components.DatePicker, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    modelValue: (__VLS_ctx.asOf),
    dateFormat: "yy-mm-dd",
    showIcon: true,
}));
const __VLS_2 = __VLS_1({
    modelValue: (__VLS_ctx.asOf),
    dateFormat: "yy-mm-dd",
    showIcon: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
const __VLS_4 = {}.TabView;
/** @type {[typeof __VLS_components.TabView, typeof __VLS_components.TabView, ]} */ ;
// @ts-ignore
const __VLS_5 = __VLS_asFunctionalComponent(__VLS_4, new __VLS_4({}));
const __VLS_6 = __VLS_5({}, ...__VLS_functionalComponentArgsRest(__VLS_5));
__VLS_7.slots.default;
const __VLS_8 = {}.TabPanel;
/** @type {[typeof __VLS_components.TabPanel, typeof __VLS_components.TabPanel, ]} */ ;
// @ts-ignore
const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
    header: "Trial Balance",
}));
const __VLS_10 = __VLS_9({
    header: "Trial Balance",
}, ...__VLS_functionalComponentArgsRest(__VLS_9));
__VLS_11.slots.default;
if (__VLS_ctx.trialBalance.data.value) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "meta" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
    (__VLS_ctx.trialBalance.data.value.base_currency);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(__VLS_ctx.trialBalance.data.value.total_debit_base, __VLS_ctx.trialBalance.data.value.base_currency));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(__VLS_ctx.trialBalance.data.value.total_credit_base, __VLS_ctx.trialBalance.data.value.base_currency));
}
const __VLS_12 = {}.DataTable;
/** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
// @ts-ignore
const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
    value: (__VLS_ctx.trialBalance.data.value?.rows ?? []),
    loading: (__VLS_ctx.trialBalance.isLoading.value),
    dataKey: "account_id",
    stripedRows: true,
    size: "small",
}));
const __VLS_14 = __VLS_13({
    value: (__VLS_ctx.trialBalance.data.value?.rows ?? []),
    loading: (__VLS_ctx.trialBalance.isLoading.value),
    dataKey: "account_id",
    stripedRows: true,
    size: "small",
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
__VLS_15.slots.default;
{
    const { empty: __VLS_thisSlot } = __VLS_15.slots;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-inbox" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
const __VLS_16 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
    field: "code",
    header: "Code",
}));
const __VLS_18 = __VLS_17({
    field: "code",
    header: "Code",
}, ...__VLS_functionalComponentArgsRest(__VLS_17));
const __VLS_20 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
    field: "name",
    header: "Account",
}));
const __VLS_22 = __VLS_21({
    field: "name",
    header: "Account",
}, ...__VLS_functionalComponentArgsRest(__VLS_21));
const __VLS_24 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
    header: "Type",
}));
const __VLS_26 = __VLS_25({
    header: "Type",
}, ...__VLS_functionalComponentArgsRest(__VLS_25));
__VLS_27.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_27.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_28 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
        value: (r.type),
        severity: (__VLS_ctx.typeSeverity(r.type)),
    }));
    const __VLS_30 = __VLS_29({
        value: (r.type),
        severity: (__VLS_ctx.typeSeverity(r.type)),
    }, ...__VLS_functionalComponentArgsRest(__VLS_29));
}
var __VLS_27;
const __VLS_32 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
    field: "currency",
    header: "Ccy",
}));
const __VLS_34 = __VLS_33({
    field: "currency",
    header: "Ccy",
}, ...__VLS_functionalComponentArgsRest(__VLS_33));
const __VLS_36 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
    header: "Debit",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_38 = __VLS_37({
    header: "Debit",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_37));
__VLS_39.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_39.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(r.debit, r.currency));
}
var __VLS_39;
const __VLS_40 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_41 = __VLS_asFunctionalComponent(__VLS_40, new __VLS_40({
    header: "Credit",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_42 = __VLS_41({
    header: "Credit",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_41));
__VLS_43.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_43.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(r.credit, r.currency));
}
var __VLS_43;
const __VLS_44 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
    header: "Base Debit",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_46 = __VLS_45({
    header: "Base Debit",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_45));
__VLS_47.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_47.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num muted" },
    });
    (__VLS_ctx.formatAmount(r.base_debit, __VLS_ctx.trialBalance.data.value?.base_currency));
}
var __VLS_47;
const __VLS_48 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_49 = __VLS_asFunctionalComponent(__VLS_48, new __VLS_48({
    header: "Base Credit",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_50 = __VLS_49({
    header: "Base Credit",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_49));
__VLS_51.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_51.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num muted" },
    });
    (__VLS_ctx.formatAmount(r.base_credit, __VLS_ctx.trialBalance.data.value?.base_currency));
}
var __VLS_51;
var __VLS_15;
var __VLS_11;
const __VLS_52 = {}.TabPanel;
/** @type {[typeof __VLS_components.TabPanel, typeof __VLS_components.TabPanel, ]} */ ;
// @ts-ignore
const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({
    header: "Balance Sheet",
}));
const __VLS_54 = __VLS_53({
    header: "Balance Sheet",
}, ...__VLS_functionalComponentArgsRest(__VLS_53));
__VLS_55.slots.default;
if (__VLS_ctx.balanceSheet.data.value) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "meta" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
    (__VLS_ctx.balanceSheet.data.value.base_currency);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(__VLS_ctx.balanceSheet.data.value.assets_total_base, __VLS_ctx.balanceSheet.data.value.base_currency));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(__VLS_ctx.balanceSheet.data.value.liabilities_total_base, __VLS_ctx.balanceSheet.data.value.base_currency));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(__VLS_ctx.balanceSheet.data.value.equity_total_base, __VLS_ctx.balanceSheet.data.value.base_currency));
}
if (__VLS_ctx.balanceSheet.isLoading.value) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
}
else if (!(__VLS_ctx.balanceSheet.data.value?.groups?.length)) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-inbox" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
else {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "bs-groups" },
    });
    for (const [g] of __VLS_getVForSourceType((__VLS_ctx.balanceSheet.data.value.groups))) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            key: (g.type),
            ...{ class: "bs-group" },
        });
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "bs-group-head" },
        });
        const __VLS_56 = {}.Tag;
        /** @type {[typeof __VLS_components.Tag, ]} */ ;
        // @ts-ignore
        const __VLS_57 = __VLS_asFunctionalComponent(__VLS_56, new __VLS_56({
            value: (g.type),
            severity: (__VLS_ctx.typeSeverity(g.type)),
        }));
        const __VLS_58 = __VLS_57({
            value: (g.type),
            severity: (__VLS_ctx.typeSeverity(g.type)),
        }, ...__VLS_functionalComponentArgsRest(__VLS_57));
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "bs-total num" },
        });
        (__VLS_ctx.formatAmount(g.total_base, __VLS_ctx.balanceSheet.data.value.base_currency));
        (__VLS_ctx.balanceSheet.data.value.base_currency);
        __VLS_asFunctionalElement(__VLS_intrinsicElements.ul, __VLS_intrinsicElements.ul)({
            ...{ class: "bs-lines" },
        });
        for (const [line] of __VLS_getVForSourceType((g.lines))) {
            __VLS_asFunctionalElement(__VLS_intrinsicElements.li, __VLS_intrinsicElements.li)({
                key: (line.account_id),
            });
            __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
                ...{ class: "bs-code" },
            });
            __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({});
            (line.code);
            __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
                ...{ class: "bs-name" },
            });
            (line.name);
            __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
                ...{ class: "bs-amt num" },
            });
            (__VLS_ctx.formatAmount(line.base_balance, __VLS_ctx.balanceSheet.data.value.base_currency));
        }
    }
}
var __VLS_55;
const __VLS_60 = {}.TabPanel;
/** @type {[typeof __VLS_components.TabPanel, typeof __VLS_components.TabPanel, ]} */ ;
// @ts-ignore
const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({
    header: "FX Revaluation",
}));
const __VLS_62 = __VLS_61({
    header: "FX Revaluation",
}, ...__VLS_functionalComponentArgsRest(__VLS_61));
__VLS_63.slots.default;
if (__VLS_ctx.fxReval.data.value) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "meta" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
    (__VLS_ctx.fxReval.data.value.as_of);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
    (__VLS_ctx.fxReval.data.value.base_currency);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
        ...{ style: {} },
    });
    (__VLS_ctx.formatAmount(__VLS_ctx.fxReval.data.value.total_unrealised_gain, __VLS_ctx.fxReval.data.value.base_currency));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
        ...{ style: {} },
    });
    (__VLS_ctx.formatAmount(__VLS_ctx.fxReval.data.value.total_unrealised_loss, __VLS_ctx.fxReval.data.value.base_currency));
}
if (__VLS_ctx.fxReval.data.value?.missing_rates?.length) {
    const __VLS_64 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_65 = __VLS_asFunctionalComponent(__VLS_64, new __VLS_64({
        severity: "warn",
        closable: (false),
    }));
    const __VLS_66 = __VLS_65({
        severity: "warn",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_65));
    __VLS_67.slots.default;
    (__VLS_ctx.fxReval.data.value.missing_rates.join(", "));
    var __VLS_67;
}
const __VLS_68 = {}.DataTable;
/** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
// @ts-ignore
const __VLS_69 = __VLS_asFunctionalComponent(__VLS_68, new __VLS_68({
    value: (__VLS_ctx.fxReval.data.value?.lines ?? []),
    loading: (__VLS_ctx.fxReval.isLoading.value),
    dataKey: "account_id",
    stripedRows: true,
    size: "small",
}));
const __VLS_70 = __VLS_69({
    value: (__VLS_ctx.fxReval.data.value?.lines ?? []),
    loading: (__VLS_ctx.fxReval.isLoading.value),
    dataKey: "account_id",
    stripedRows: true,
    size: "small",
}, ...__VLS_functionalComponentArgsRest(__VLS_69));
__VLS_71.slots.default;
{
    const { empty: __VLS_thisSlot } = __VLS_71.slots;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-inbox" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
const __VLS_72 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({
    field: "code",
    header: "Code",
}));
const __VLS_74 = __VLS_73({
    field: "code",
    header: "Code",
}, ...__VLS_functionalComponentArgsRest(__VLS_73));
const __VLS_76 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_77 = __VLS_asFunctionalComponent(__VLS_76, new __VLS_76({
    field: "name",
    header: "Account",
}));
const __VLS_78 = __VLS_77({
    field: "name",
    header: "Account",
}, ...__VLS_functionalComponentArgsRest(__VLS_77));
const __VLS_80 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_81 = __VLS_asFunctionalComponent(__VLS_80, new __VLS_80({
    header: "Type",
}));
const __VLS_82 = __VLS_81({
    header: "Type",
}, ...__VLS_functionalComponentArgsRest(__VLS_81));
__VLS_83.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_83.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_84 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_85 = __VLS_asFunctionalComponent(__VLS_84, new __VLS_84({
        value: (r.type),
        severity: (__VLS_ctx.typeSeverity(r.type)),
    }));
    const __VLS_86 = __VLS_85({
        value: (r.type),
        severity: (__VLS_ctx.typeSeverity(r.type)),
    }, ...__VLS_functionalComponentArgsRest(__VLS_85));
}
var __VLS_83;
const __VLS_88 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_89 = __VLS_asFunctionalComponent(__VLS_88, new __VLS_88({
    field: "currency",
    header: "Ccy",
}));
const __VLS_90 = __VLS_89({
    field: "currency",
    header: "Ccy",
}, ...__VLS_functionalComponentArgsRest(__VLS_89));
const __VLS_92 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_93 = __VLS_asFunctionalComponent(__VLS_92, new __VLS_92({
    header: "Face balance",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_94 = __VLS_93({
    header: "Face balance",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_93));
__VLS_95.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_95.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(r.face_balance, r.currency));
}
var __VLS_95;
const __VLS_96 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_97 = __VLS_asFunctionalComponent(__VLS_96, new __VLS_96({
    header: "Original base",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_98 = __VLS_97({
    header: "Original base",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_97));
__VLS_99.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_99.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num muted" },
    });
    (__VLS_ctx.formatAmount(r.original_base, __VLS_ctx.fxReval.data.value?.base_currency));
}
var __VLS_99;
const __VLS_100 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_101 = __VLS_asFunctionalComponent(__VLS_100, new __VLS_100({
    header: "Rate",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_102 = __VLS_101({
    header: "Rate",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_101));
__VLS_103.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_103.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num muted" },
    });
    (__VLS_ctx.formatRate(r.rate));
    if (r.rate_as_of) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "rate-date" },
        });
        (r.rate_as_of);
    }
}
var __VLS_103;
const __VLS_104 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_105 = __VLS_asFunctionalComponent(__VLS_104, new __VLS_104({
    header: "Revalued base",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_106 = __VLS_105({
    header: "Revalued base",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_105));
__VLS_107.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_107.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    if (r.revalued_base !== null) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "num" },
        });
        (__VLS_ctx.formatAmount(r.revalued_base, __VLS_ctx.fxReval.data.value?.base_currency));
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
}
var __VLS_107;
const __VLS_108 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_109 = __VLS_asFunctionalComponent(__VLS_108, new __VLS_108({
    header: "Unrealised",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_110 = __VLS_109({
    header: "Unrealised",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_109));
__VLS_111.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_111.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    if (r.unrealised_gain_loss !== null) {
        const __VLS_112 = {}.Tag;
        /** @type {[typeof __VLS_components.Tag, ]} */ ;
        // @ts-ignore
        const __VLS_113 = __VLS_asFunctionalComponent(__VLS_112, new __VLS_112({
            value: (__VLS_ctx.formatAmount(r.unrealised_gain_loss, __VLS_ctx.fxReval.data.value?.base_currency)),
            severity: (__VLS_ctx.gainLossSeverity(r.unrealised_gain_loss)),
        }));
        const __VLS_114 = __VLS_113({
            value: (__VLS_ctx.formatAmount(r.unrealised_gain_loss, __VLS_ctx.fxReval.data.value?.base_currency)),
            severity: (__VLS_ctx.gainLossSeverity(r.unrealised_gain_loss)),
        }, ...__VLS_functionalComponentArgsRest(__VLS_113));
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
}
var __VLS_111;
var __VLS_71;
var __VLS_63;
var __VLS_7;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['page-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['as-of']} */ ;
/** @type {__VLS_StyleScopedClasses['meta']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-inbox']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['meta']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-inbox']} */ ;
/** @type {__VLS_StyleScopedClasses['bs-groups']} */ ;
/** @type {__VLS_StyleScopedClasses['bs-group']} */ ;
/** @type {__VLS_StyleScopedClasses['bs-group-head']} */ ;
/** @type {__VLS_StyleScopedClasses['bs-total']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['bs-lines']} */ ;
/** @type {__VLS_StyleScopedClasses['bs-code']} */ ;
/** @type {__VLS_StyleScopedClasses['bs-name']} */ ;
/** @type {__VLS_StyleScopedClasses['bs-amt']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['meta']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-inbox']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['rate-date']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            DataTable: DataTable,
            Column: Column,
            DatePicker: DatePicker,
            TabView: TabView,
            TabPanel: TabPanel,
            Tag: Tag,
            Message: Message,
            formatAmount: formatAmount,
            asOf: asOf,
            trialBalance: trialBalance,
            balanceSheet: balanceSheet,
            fxReval: fxReval,
            formatRate: formatRate,
            gainLossSeverity: gainLossSeverity,
            typeSeverity: typeSeverity,
        };
    },
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
; /* PartiallyEnd: #4569/main.vue */
