import { ref } from "vue";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Tag from "primevue/tag";
import Button from "primevue/button";
import Dialog from "primevue/dialog";
import InputText from "primevue/inputtext";
import Textarea from "primevue/textarea";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount, formatMoney } from "@/utils/money";
const queryClient = useQueryClient();
const selected = ref(null);
const invoiceId = ref("");
const reverseReason = ref("");
const showReverse = ref(false);
const { data, isLoading } = useQuery({
    queryKey: ["payments-awaiting-review"],
    queryFn: async () => (await api.get("/payments/awaiting-review")).data,
});
const { data: log } = useQuery({
    queryKey: ["payment-log", () => selected.value?.payment_id],
    queryFn: async () => (await api.get(`/payments/${selected.value.payment_id}/log`)).data,
    enabled: () => !!selected.value,
});
const approve = useMutation({
    mutationFn: async () => {
        await api.post(`/payments/${selected.value.payment_id}/manual-review`, {
            invoice_id: invoiceId.value,
            adjustment_type: "NONE",
        });
    },
    onSuccess: () => {
        selected.value = null;
        invoiceId.value = "";
        queryClient.invalidateQueries({ queryKey: ["payments-awaiting-review"] });
    },
});
const reverse = useMutation({
    mutationFn: async () => {
        await api.post(`/payments/${selected.value.payment_id}/reverse`, {
            reason: reverseReason.value,
        });
    },
    onSuccess: () => {
        showReverse.value = false;
        reverseReason.value = "";
        selected.value = null;
        queryClient.invalidateQueries({ queryKey: ["payments-awaiting-review"] });
    },
});
function statusSeverity(status) {
    if (status === "CLEARED")
        return "success";
    if (status === "FLAGGED")
        return "warning";
    return "danger";
}
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['layout']} */ ;
/** @type {__VLS_StyleScopedClasses['detail']} */ ;
/** @type {__VLS_StyleScopedClasses['detail']} */ ;
/** @type {__VLS_StyleScopedClasses['reasons']} */ ;
/** @type {__VLS_StyleScopedClasses['approve-row']} */ ;
/** @type {__VLS_StyleScopedClasses['placeholder']} */ ;
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
    ...{ class: "layout" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "list" },
});
const __VLS_0 = {}.DataTable;
/** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "payment_id",
    selectionMode: "single",
    selection: (__VLS_ctx.selected),
    stripedRows: true,
}));
const __VLS_2 = __VLS_1({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "payment_id",
    selectionMode: "single",
    selection: (__VLS_ctx.selected),
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
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_5 = __VLS_asFunctionalComponent(__VLS_4, new __VLS_4({
    field: "payer_name",
    header: "Payer",
}));
const __VLS_6 = __VLS_5({
    field: "payer_name",
    header: "Payer",
}, ...__VLS_functionalComponentArgsRest(__VLS_5));
const __VLS_8 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
    header: "Amount",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_10 = __VLS_9({
    header: "Amount",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_9));
__VLS_11.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_11.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatAmount(r.amount, r.currency));
}
var __VLS_11;
const __VLS_12 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
    field: "currency",
    header: "Ccy",
}));
const __VLS_14 = __VLS_13({
    field: "currency",
    header: "Ccy",
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
const __VLS_16 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
    field: "payment_date",
    header: "Date",
}));
const __VLS_18 = __VLS_17({
    field: "payment_date",
    header: "Date",
}, ...__VLS_functionalComponentArgsRest(__VLS_17));
const __VLS_20 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
    field: "confidence_score",
    header: "Score",
}));
const __VLS_22 = __VLS_21({
    field: "confidence_score",
    header: "Score",
}, ...__VLS_functionalComponentArgsRest(__VLS_21));
const __VLS_24 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
    field: "adjustment_type",
    header: "Adjustment",
}));
const __VLS_26 = __VLS_25({
    field: "adjustment_type",
    header: "Adjustment",
}, ...__VLS_functionalComponentArgsRest(__VLS_25));
__VLS_27.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_27.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_28 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
        value: (r.adjustment_type),
        severity: "info",
    }));
    const __VLS_30 = __VLS_29({
        value: (r.adjustment_type),
        severity: "info",
    }, ...__VLS_functionalComponentArgsRest(__VLS_29));
}
var __VLS_27;
var __VLS_3;
if (__VLS_ctx.selected) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.aside, __VLS_intrinsicElements.aside)({
        ...{ class: "detail card" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "detail-head" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({});
    (__VLS_ctx.selected.payment_id.slice(0, 8));
    const __VLS_32 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
        value: (__VLS_ctx.selected.status),
        severity: (__VLS_ctx.statusSeverity(__VLS_ctx.selected.status)),
    }));
    const __VLS_34 = __VLS_33({
        value: (__VLS_ctx.selected.status),
        severity: (__VLS_ctx.statusSeverity(__VLS_ctx.selected.status)),
    }, ...__VLS_functionalComponentArgsRest(__VLS_33));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dl, __VLS_intrinsicElements.dl)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.selected.payer_name);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.selected.payer_reference ?? '—');
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatMoney(__VLS_ctx.selected.amount, __VLS_ctx.selected.currency));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.selected.payment_date);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.selected.confidence_score ?? '—');
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    const __VLS_36 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
        value: (__VLS_ctx.selected.adjustment_type),
        severity: "info",
    }));
    const __VLS_38 = __VLS_37({
        value: (__VLS_ctx.selected.adjustment_type),
        severity: "info",
    }, ...__VLS_functionalComponentArgsRest(__VLS_37));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "section" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.h3, __VLS_intrinsicElements.h3)({});
    if ((__VLS_ctx.log?.[0]?.reasons ?? []).length) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.ul, __VLS_intrinsicElements.ul)({
            ...{ class: "reasons" },
        });
        for (const [r, i] of __VLS_getVForSourceType(((__VLS_ctx.log?.[0]?.reasons ?? [])))) {
            __VLS_asFunctionalElement(__VLS_intrinsicElements.li, __VLS_intrinsicElements.li)({
                key: (i),
            });
            (r);
        }
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({
            ...{ class: "muted" },
        });
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "section" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.h3, __VLS_intrinsicElements.h3)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "approve-row" },
    });
    const __VLS_40 = {}.InputText;
    /** @type {[typeof __VLS_components.InputText, ]} */ ;
    // @ts-ignore
    const __VLS_41 = __VLS_asFunctionalComponent(__VLS_40, new __VLS_40({
        modelValue: (__VLS_ctx.invoiceId),
        placeholder: "invoice UUID",
    }));
    const __VLS_42 = __VLS_41({
        modelValue: (__VLS_ctx.invoiceId),
        placeholder: "invoice UUID",
    }, ...__VLS_functionalComponentArgsRest(__VLS_41));
    const __VLS_44 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
        ...{ 'onClick': {} },
        label: "Approve & Post",
        icon: "pi pi-check",
        disabled: (!__VLS_ctx.invoiceId),
        loading: (__VLS_ctx.approve.isPending.value),
    }));
    const __VLS_46 = __VLS_45({
        ...{ 'onClick': {} },
        label: "Approve & Post",
        icon: "pi pi-check",
        disabled: (!__VLS_ctx.invoiceId),
        loading: (__VLS_ctx.approve.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_45));
    let __VLS_48;
    let __VLS_49;
    let __VLS_50;
    const __VLS_51 = {
        onClick: (...[$event]) => {
            if (!(__VLS_ctx.selected))
                return;
            __VLS_ctx.approve.mutate();
        }
    };
    var __VLS_47;
    if (__VLS_ctx.approve.error.value) {
        const __VLS_52 = {}.Message;
        /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
        // @ts-ignore
        const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({
            severity: "error",
            closable: (false),
        }));
        const __VLS_54 = __VLS_53({
            severity: "error",
            closable: (false),
        }, ...__VLS_functionalComponentArgsRest(__VLS_53));
        __VLS_55.slots.default;
        (__VLS_ctx.approve.error.value?.response?.data?.detail ?? 'Approval failed');
        var __VLS_55;
    }
    if (__VLS_ctx.selected.status === 'CLEARED') {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "section" },
        });
        const __VLS_56 = {}.Button;
        /** @type {[typeof __VLS_components.Button, ]} */ ;
        // @ts-ignore
        const __VLS_57 = __VLS_asFunctionalComponent(__VLS_56, new __VLS_56({
            ...{ 'onClick': {} },
            label: "Reverse this payment",
            icon: "pi pi-undo",
            severity: "danger",
            text: true,
        }));
        const __VLS_58 = __VLS_57({
            ...{ 'onClick': {} },
            label: "Reverse this payment",
            icon: "pi pi-undo",
            severity: "danger",
            text: true,
        }, ...__VLS_functionalComponentArgsRest(__VLS_57));
        let __VLS_60;
        let __VLS_61;
        let __VLS_62;
        const __VLS_63 = {
            onClick: (...[$event]) => {
                if (!(__VLS_ctx.selected))
                    return;
                if (!(__VLS_ctx.selected.status === 'CLEARED'))
                    return;
                __VLS_ctx.showReverse = true;
            }
        };
        var __VLS_59;
    }
}
else {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.aside, __VLS_intrinsicElements.aside)({
        ...{ class: "detail card card-pad placeholder" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-arrow-left" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
const __VLS_64 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_65 = __VLS_asFunctionalComponent(__VLS_64, new __VLS_64({
    visible: (__VLS_ctx.showReverse),
    header: "Reverse payment",
    modal: true,
    ...{ style: ({ width: '480px' }) },
}));
const __VLS_66 = __VLS_65({
    visible: (__VLS_ctx.showReverse),
    header: "Reverse payment",
    modal: true,
    ...{ style: ({ width: '480px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_65));
__VLS_67.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({
    ...{ class: "muted" },
});
const __VLS_68 = {}.Textarea;
/** @type {[typeof __VLS_components.Textarea, ]} */ ;
// @ts-ignore
const __VLS_69 = __VLS_asFunctionalComponent(__VLS_68, new __VLS_68({
    modelValue: (__VLS_ctx.reverseReason),
    rows: "3",
    placeholder: "Reason",
    ...{ class: "w-full" },
}));
const __VLS_70 = __VLS_69({
    modelValue: (__VLS_ctx.reverseReason),
    rows: "3",
    placeholder: "Reason",
    ...{ class: "w-full" },
}, ...__VLS_functionalComponentArgsRest(__VLS_69));
{
    const { footer: __VLS_thisSlot } = __VLS_67.slots;
    const __VLS_72 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_74 = __VLS_73({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_73));
    let __VLS_76;
    let __VLS_77;
    let __VLS_78;
    const __VLS_79 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showReverse = false;
        }
    };
    var __VLS_75;
    const __VLS_80 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_81 = __VLS_asFunctionalComponent(__VLS_80, new __VLS_80({
        ...{ 'onClick': {} },
        label: "Confirm reversal",
        severity: "danger",
        disabled: (!__VLS_ctx.reverseReason),
        loading: (__VLS_ctx.reverse.isPending.value),
    }));
    const __VLS_82 = __VLS_81({
        ...{ 'onClick': {} },
        label: "Confirm reversal",
        severity: "danger",
        disabled: (!__VLS_ctx.reverseReason),
        loading: (__VLS_ctx.reverse.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_81));
    let __VLS_84;
    let __VLS_85;
    let __VLS_86;
    const __VLS_87 = {
        onClick: (...[$event]) => {
            __VLS_ctx.reverse.mutate();
        }
    };
    var __VLS_83;
}
var __VLS_67;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['layout']} */ ;
/** @type {__VLS_StyleScopedClasses['list']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-check-circle']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['detail']} */ ;
/** @type {__VLS_StyleScopedClasses['card']} */ ;
/** @type {__VLS_StyleScopedClasses['detail-head']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['section']} */ ;
/** @type {__VLS_StyleScopedClasses['reasons']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['section']} */ ;
/** @type {__VLS_StyleScopedClasses['approve-row']} */ ;
/** @type {__VLS_StyleScopedClasses['section']} */ ;
/** @type {__VLS_StyleScopedClasses['detail']} */ ;
/** @type {__VLS_StyleScopedClasses['card']} */ ;
/** @type {__VLS_StyleScopedClasses['card-pad']} */ ;
/** @type {__VLS_StyleScopedClasses['placeholder']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-arrow-left']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['w-full']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            DataTable: DataTable,
            Column: Column,
            Tag: Tag,
            Button: Button,
            Dialog: Dialog,
            InputText: InputText,
            Textarea: Textarea,
            Message: Message,
            formatAmount: formatAmount,
            formatMoney: formatMoney,
            selected: selected,
            invoiceId: invoiceId,
            reverseReason: reverseReason,
            showReverse: showReverse,
            data: data,
            isLoading: isLoading,
            log: log,
            approve: approve,
            reverse: reverse,
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
