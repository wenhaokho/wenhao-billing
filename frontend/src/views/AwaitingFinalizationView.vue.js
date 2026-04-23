import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tag from "primevue/tag";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
const queryClient = useQueryClient();
const { data, isLoading } = useQuery({
    queryKey: ["awaiting-finalization"],
    queryFn: async () => (await api.get("/invoices/awaiting-finalization")).data,
});
const finalize = useMutation({
    mutationFn: (id) => api.post(`/invoices/${id}/finalize`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["awaiting-finalization"] }),
});
const voidInvoice = useMutation({
    mutationFn: (id) => api.post(`/invoices/${id}/void`, { reason: "voided from finalization queue" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["awaiting-finalization"] }),
});
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
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
const __VLS_0 = {}.DataTable;
/** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "invoice_id",
    stripedRows: true,
}));
const __VLS_2 = __VLS_1({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "invoice_id",
    stripedRows: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
__VLS_3.slots.default;
{
    const { empty: __VLS_thisSlot } = __VLS_3.slots;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-check-circle" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
const __VLS_4 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_5 = __VLS_asFunctionalComponent(__VLS_4, new __VLS_4({
    field: "invoice_type",
    header: "Type",
}));
const __VLS_6 = __VLS_5({
    field: "invoice_type",
    header: "Type",
}, ...__VLS_functionalComponentArgsRest(__VLS_5));
__VLS_7.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_7.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_8 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
        value: (row.invoice_type),
        severity: "info",
    }));
    const __VLS_10 = __VLS_9({
        value: (row.invoice_type),
        severity: "info",
    }, ...__VLS_functionalComponentArgsRest(__VLS_9));
}
var __VLS_7;
const __VLS_12 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
    field: "currency",
    header: "Currency",
}));
const __VLS_14 = __VLS_13({
    field: "currency",
    header: "Currency",
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
const __VLS_16 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
    header: "Amount",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_18 = __VLS_17({
    header: "Amount",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_17));
__VLS_19.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_19.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(row.amount, row.currency));
}
var __VLS_19;
const __VLS_20 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
    field: "issue_date",
    header: "Issued",
}));
const __VLS_22 = __VLS_21({
    field: "issue_date",
    header: "Issued",
}, ...__VLS_functionalComponentArgsRest(__VLS_21));
const __VLS_24 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
    field: "due_date",
    header: "Due",
}));
const __VLS_26 = __VLS_25({
    field: "due_date",
    header: "Due",
}, ...__VLS_functionalComponentArgsRest(__VLS_25));
const __VLS_28 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
    header: "Actions",
}));
const __VLS_30 = __VLS_29({
    header: "Actions",
}, ...__VLS_functionalComponentArgsRest(__VLS_29));
__VLS_31.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_31.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "actions" },
    });
    const __VLS_32 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
        ...{ 'onClick': {} },
        label: "Finalize",
        icon: "pi pi-check",
        size: "small",
        loading: (__VLS_ctx.finalize.isPending.value),
    }));
    const __VLS_34 = __VLS_33({
        ...{ 'onClick': {} },
        label: "Finalize",
        icon: "pi pi-check",
        size: "small",
        loading: (__VLS_ctx.finalize.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_33));
    let __VLS_36;
    let __VLS_37;
    let __VLS_38;
    const __VLS_39 = {
        onClick: (...[$event]) => {
            __VLS_ctx.finalize.mutate(row.invoice_id);
        }
    };
    var __VLS_35;
    const __VLS_40 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_41 = __VLS_asFunctionalComponent(__VLS_40, new __VLS_40({
        ...{ 'onClick': {} },
        label: "Void",
        icon: "pi pi-times",
        size: "small",
        severity: "secondary",
        text: true,
        loading: (__VLS_ctx.voidInvoice.isPending.value),
    }));
    const __VLS_42 = __VLS_41({
        ...{ 'onClick': {} },
        label: "Void",
        icon: "pi pi-times",
        size: "small",
        severity: "secondary",
        text: true,
        loading: (__VLS_ctx.voidInvoice.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_41));
    let __VLS_44;
    let __VLS_45;
    let __VLS_46;
    const __VLS_47 = {
        onClick: (...[$event]) => {
            __VLS_ctx.voidInvoice.mutate(row.invoice_id);
        }
    };
    var __VLS_43;
}
var __VLS_31;
var __VLS_3;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-check-circle']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['actions']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            DataTable: DataTable,
            Column: Column,
            Button: Button,
            Tag: Tag,
            formatAmount: formatAmount,
            data: data,
            isLoading: isLoading,
            finalize: finalize,
            voidInvoice: voidInvoice,
        };
    },
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
; /* PartiallyEnd: #4569/main.vue */
