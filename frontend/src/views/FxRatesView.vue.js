import { ref, computed, onMounted } from "vue";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Dialog from "primevue/dialog";
import Dropdown from "primevue/dropdown";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import DatePicker from "primevue/calendar";
import Message from "primevue/message";
import { api } from "@/api/client";
const currencyOptions = ["IDR", "SGD", "USD", "EUR"];
const queryClient = useQueryClient();
const baseCurrency = ref("IDR");
onMounted(async () => {
    try {
        const { data } = await api.get("/accounting/base-currency");
        baseCurrency.value = data.base_currency;
    }
    catch {
        // leave default
    }
});
const { data, isLoading } = useQuery({
    queryKey: ["fx-rates"],
    queryFn: async () => (await api.get("/accounting/fx-rates")).data,
});
function emptyForm() {
    return {
        from_currency: "USD",
        to_currency: baseCurrency.value,
        rate: null,
        as_of_date: new Date(),
        source: "MANUAL",
    };
}
const showCreate = ref(false);
const form = ref(emptyForm());
function openCreate() {
    form.value = emptyForm();
    createRate.reset();
    showCreate.value = true;
}
const createRate = useMutation({
    mutationFn: async () => {
        const payload = {
            from_currency: form.value.from_currency,
            to_currency: form.value.to_currency,
            rate: form.value.rate,
            as_of_date: form.value.as_of_date.toISOString().slice(0, 10),
            source: form.value.source || "MANUAL",
        };
        return api.post("/accounting/fx-rates", payload);
    },
    onSuccess: () => {
        showCreate.value = false;
        queryClient.invalidateQueries({ queryKey: ["fx-rates"] });
    },
});
const deleteRate = useMutation({
    mutationFn: async (id) => api.delete(`/accounting/fx-rates/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["fx-rates"] }),
});
function confirmDelete(row) {
    if (window.confirm(`Delete rate ${row.from_currency}→${row.to_currency} @ ${row.as_of_date}?`)) {
        deleteRate.mutate(row.rate_id);
    }
}
function formatRate(v) {
    const n = Number(v);
    if (!Number.isFinite(n))
        return v;
    return n.toLocaleString(undefined, {
        minimumFractionDigits: 2,
        maximumFractionDigits: 8,
    });
}
const canSubmit = computed(() => !!form.value.from_currency &&
    !!form.value.to_currency &&
    form.value.from_currency !== form.value.to_currency &&
    !!form.value.rate &&
    form.value.rate > 0 &&
    !!form.value.as_of_date);
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
__VLS_asFunctionalElement(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
(__VLS_ctx.baseCurrency);
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "page-actions" },
});
const __VLS_0 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    ...{ 'onClick': {} },
    label: "New rate",
    icon: "pi pi-plus",
}));
const __VLS_2 = __VLS_1({
    ...{ 'onClick': {} },
    label: "New rate",
    icon: "pi pi-plus",
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
let __VLS_4;
let __VLS_5;
let __VLS_6;
const __VLS_7 = {
    onClick: (__VLS_ctx.openCreate)
};
var __VLS_3;
const __VLS_8 = {}.DataTable;
/** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
// @ts-ignore
const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "rate_id",
    stripedRows: true,
    size: "small",
}));
const __VLS_10 = __VLS_9({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "rate_id",
    stripedRows: true,
    size: "small",
}, ...__VLS_functionalComponentArgsRest(__VLS_9));
__VLS_11.slots.default;
{
    const { empty: __VLS_thisSlot } = __VLS_11.slots;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-inbox" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
const __VLS_12 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
    field: "as_of_date",
    header: "As of",
    ...{ style: ({ width: '130px' }) },
}));
const __VLS_14 = __VLS_13({
    field: "as_of_date",
    header: "As of",
    ...{ style: ({ width: '130px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
const __VLS_16 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
    header: "Pair",
    ...{ style: ({ width: '130px' }) },
}));
const __VLS_18 = __VLS_17({
    header: "Pair",
    ...{ style: ({ width: '130px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_17));
__VLS_19.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_19.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({});
    (r.from_currency);
    (r.to_currency);
}
var __VLS_19;
const __VLS_20 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
    header: "Rate",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}));
const __VLS_22 = __VLS_21({
    header: "Rate",
    bodyStyle: ({ textAlign: 'right' }),
    headerStyle: ({ textAlign: 'right' }),
}, ...__VLS_functionalComponentArgsRest(__VLS_21));
__VLS_23.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_23.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatRate(r.rate));
}
var __VLS_23;
const __VLS_24 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
    field: "source",
    header: "Source",
    ...{ style: ({ width: '140px' }) },
}));
const __VLS_26 = __VLS_25({
    field: "source",
    header: "Source",
    ...{ style: ({ width: '140px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_25));
const __VLS_28 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
    field: "created_at",
    header: "Created",
    ...{ style: ({ width: '180px' }) },
}));
const __VLS_30 = __VLS_29({
    field: "created_at",
    header: "Created",
    ...{ style: ({ width: '180px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_29));
__VLS_31.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_31.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
        ...{ class: "muted" },
    });
    (new Date(r.created_at).toLocaleString());
}
var __VLS_31;
const __VLS_32 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
    header: "",
    ...{ style: ({ width: '70px' }) },
}));
const __VLS_34 = __VLS_33({
    header: "",
    ...{ style: ({ width: '70px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_33));
__VLS_35.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_35.slots;
    const { data: r } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_36 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
        ...{ 'onClick': {} },
        icon: "pi pi-trash",
        text: true,
        rounded: true,
        severity: "danger",
        title: "Delete",
        loading: (__VLS_ctx.deleteRate.isPending.value),
    }));
    const __VLS_38 = __VLS_37({
        ...{ 'onClick': {} },
        icon: "pi pi-trash",
        text: true,
        rounded: true,
        severity: "danger",
        title: "Delete",
        loading: (__VLS_ctx.deleteRate.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_37));
    let __VLS_40;
    let __VLS_41;
    let __VLS_42;
    const __VLS_43 = {
        onClick: (...[$event]) => {
            __VLS_ctx.confirmDelete(r);
        }
    };
    var __VLS_39;
}
var __VLS_35;
var __VLS_11;
const __VLS_44 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
    visible: (__VLS_ctx.showCreate),
    header: "New FX rate",
    modal: true,
    ...{ style: ({ width: '440px' }) },
}));
const __VLS_46 = __VLS_45({
    visible: (__VLS_ctx.showCreate),
    header: "New FX rate",
    modal: true,
    ...{ style: ({ width: '440px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_45));
__VLS_47.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (...[$event]) => {
            __VLS_ctx.createRate.mutate();
        } },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_48 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_49 = __VLS_asFunctionalComponent(__VLS_48, new __VLS_48({
    modelValue: (__VLS_ctx.form.from_currency),
    options: (__VLS_ctx.currencyOptions),
}));
const __VLS_50 = __VLS_49({
    modelValue: (__VLS_ctx.form.from_currency),
    options: (__VLS_ctx.currencyOptions),
}, ...__VLS_functionalComponentArgsRest(__VLS_49));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_52 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({
    modelValue: (__VLS_ctx.form.to_currency),
    options: (__VLS_ctx.currencyOptions),
}));
const __VLS_54 = __VLS_53({
    modelValue: (__VLS_ctx.form.to_currency),
    options: (__VLS_ctx.currencyOptions),
}, ...__VLS_functionalComponentArgsRest(__VLS_53));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_56 = {}.InputNumber;
/** @type {[typeof __VLS_components.InputNumber, ]} */ ;
// @ts-ignore
const __VLS_57 = __VLS_asFunctionalComponent(__VLS_56, new __VLS_56({
    modelValue: (__VLS_ctx.form.rate),
    minFractionDigits: (2),
    maxFractionDigits: (8),
    useGrouping: (true),
    min: (0),
    placeholder: "e.g. 16000",
}));
const __VLS_58 = __VLS_57({
    modelValue: (__VLS_ctx.form.rate),
    minFractionDigits: (2),
    maxFractionDigits: (8),
    useGrouping: (true),
    min: (0),
    placeholder: "e.g. 16000",
}, ...__VLS_functionalComponentArgsRest(__VLS_57));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_60 = {}.DatePicker;
/** @type {[typeof __VLS_components.DatePicker, ]} */ ;
// @ts-ignore
const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({
    modelValue: (__VLS_ctx.form.as_of_date),
    dateFormat: "yy-mm-dd",
    showIcon: true,
}));
const __VLS_62 = __VLS_61({
    modelValue: (__VLS_ctx.form.as_of_date),
    dateFormat: "yy-mm-dd",
    showIcon: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_61));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_64 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_65 = __VLS_asFunctionalComponent(__VLS_64, new __VLS_64({
    modelValue: (__VLS_ctx.form.source),
    maxlength: "50",
    placeholder: "MANUAL",
}));
const __VLS_66 = __VLS_65({
    modelValue: (__VLS_ctx.form.source),
    maxlength: "50",
    placeholder: "MANUAL",
}, ...__VLS_functionalComponentArgsRest(__VLS_65));
if (__VLS_ctx.form.from_currency === __VLS_ctx.form.to_currency) {
    const __VLS_68 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_69 = __VLS_asFunctionalComponent(__VLS_68, new __VLS_68({
        severity: "warn",
        closable: (false),
    }));
    const __VLS_70 = __VLS_69({
        severity: "warn",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_69));
    __VLS_71.slots.default;
    var __VLS_71;
}
if (__VLS_ctx.createRate.error.value) {
    const __VLS_72 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({
        severity: "error",
        closable: (false),
    }));
    const __VLS_74 = __VLS_73({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_73));
    __VLS_75.slots.default;
    (__VLS_ctx.createRate.error.value?.response?.data?.detail ?? 'Save failed');
    var __VLS_75;
}
{
    const { footer: __VLS_thisSlot } = __VLS_47.slots;
    const __VLS_76 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_77 = __VLS_asFunctionalComponent(__VLS_76, new __VLS_76({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_78 = __VLS_77({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_77));
    let __VLS_80;
    let __VLS_81;
    let __VLS_82;
    const __VLS_83 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showCreate = false;
        }
    };
    var __VLS_79;
    const __VLS_84 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_85 = __VLS_asFunctionalComponent(__VLS_84, new __VLS_84({
        ...{ 'onClick': {} },
        label: "Create",
        disabled: (!__VLS_ctx.canSubmit),
        loading: (__VLS_ctx.createRate.isPending.value),
    }));
    const __VLS_86 = __VLS_85({
        ...{ 'onClick': {} },
        label: "Create",
        disabled: (!__VLS_ctx.canSubmit),
        loading: (__VLS_ctx.createRate.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_85));
    let __VLS_88;
    let __VLS_89;
    let __VLS_90;
    const __VLS_91 = {
        onClick: (...[$event]) => {
            __VLS_ctx.createRate.mutate();
        }
    };
    var __VLS_87;
}
var __VLS_47;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['page-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-inbox']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
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
            Message: Message,
            currencyOptions: currencyOptions,
            baseCurrency: baseCurrency,
            data: data,
            isLoading: isLoading,
            showCreate: showCreate,
            form: form,
            openCreate: openCreate,
            createRate: createRate,
            deleteRate: deleteRate,
            confirmDelete: confirmDelete,
            formatRate: formatRate,
            canSubmit: canSubmit,
        };
    },
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
; /* PartiallyEnd: #4569/main.vue */
