import { ref, computed } from "vue";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Dialog from "primevue/dialog";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import Textarea from "primevue/textarea";
import Dropdown from "primevue/dropdown";
import Tag from "primevue/tag";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatMoney } from "@/utils/money";
const queryClient = useQueryClient();
const itemTypes = [
    { label: "Fixed fee", value: "FIXED_FEE" },
    { label: "Service (recurring)", value: "SERVICE" },
    { label: "Usage (metered)", value: "USAGE" },
];
const currencyOptions = ["IDR", "SGD", "USD"];
function emptyForm() {
    return {
        sku: "",
        name: "",
        item_type: "SERVICE",
        description: "",
        default_currency: "IDR",
        default_unit_price: null,
        revenue_account_id: null,
        active: true,
    };
}
const showEdit = ref(false);
const editingId = ref(null);
const form = ref(emptyForm());
const showDetail = ref(false);
const detailItem = ref(null);
const { data: items, isLoading } = useQuery({
    queryKey: ["items"],
    queryFn: async () => (await api.get("/items")).data,
});
const { data: accounts } = useQuery({
    queryKey: ["chart-of-accounts"],
    queryFn: async () => (await api.get("/accounting/chart-of-accounts")).data,
});
const incomeAccounts = computed(() => (accounts.value ?? []).filter((a) => a.type === "INCOME"));
const accountNameById = computed(() => {
    const map = {};
    for (const a of accounts.value ?? [])
        map[a.account_id] = `${a.code} · ${a.name}`;
    return map;
});
function buildBody() {
    const body = { ...form.value };
    for (const k of Object.keys(body)) {
        if (body[k] === "" || body[k] === null)
            delete body[k];
    }
    body.item_type = form.value.item_type;
    body.name = form.value.name;
    body.default_currency = form.value.default_currency;
    body.active = form.value.active;
    return body;
}
const saveItem = useMutation({
    mutationFn: async () => {
        if (editingId.value) {
            return api.patch(`/items/${editingId.value}`, buildBody());
        }
        return api.post("/items", buildBody());
    },
    onSuccess: () => {
        showEdit.value = false;
        editingId.value = null;
        form.value = emptyForm();
        queryClient.invalidateQueries({ queryKey: ["items"] });
    },
});
const deactivateItem = useMutation({
    mutationFn: async (id) => api.delete(`/items/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["items"] }),
});
const reactivateItem = useMutation({
    mutationFn: async (id) => api.patch(`/items/${id}`, { active: true }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["items"] }),
});
function openCreate() {
    editingId.value = null;
    form.value = emptyForm();
    saveItem.reset();
    showEdit.value = true;
}
function openEdit(row) {
    editingId.value = row.item_id;
    form.value = {
        sku: row.sku ?? "",
        name: row.name,
        item_type: row.item_type,
        description: row.description ?? "",
        default_currency: row.default_currency,
        default_unit_price: row.default_unit_price ? Number(row.default_unit_price) : null,
        revenue_account_id: row.revenue_account_id,
        active: row.active,
    };
    saveItem.reset();
    showEdit.value = true;
}
function openDetail(row) {
    detailItem.value = row;
    showDetail.value = true;
}
function confirmDeactivate(row) {
    if (window.confirm(`Deactivate "${row.name}"? It will be hidden from active lists but kept for ledger history.`)) {
        deactivateItem.mutate(row.item_id);
    }
}
function typeSeverity(t) {
    switch (t) {
        case "FIXED_FEE": return "info";
        case "SERVICE": return "success";
        case "USAGE": return "warning";
        default: return undefined;
    }
}
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['detail']} */ ;
/** @type {__VLS_StyleScopedClasses['detail']} */ ;
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
const __VLS_0 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    ...{ 'onClick': {} },
    label: "New item",
    icon: "pi pi-plus",
}));
const __VLS_2 = __VLS_1({
    ...{ 'onClick': {} },
    label: "New item",
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
    value: (__VLS_ctx.items ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "item_id",
    stripedRows: true,
}));
const __VLS_10 = __VLS_9({
    value: (__VLS_ctx.items ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "item_id",
    stripedRows: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_9));
__VLS_11.slots.default;
{
    const { empty: __VLS_thisSlot } = __VLS_11.slots;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-tags" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
const __VLS_12 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
    field: "sku",
    header: "SKU",
}));
const __VLS_14 = __VLS_13({
    field: "sku",
    header: "SKU",
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
__VLS_15.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_15.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    if (row.sku) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({});
        (row.sku);
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
}
var __VLS_15;
const __VLS_16 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
    field: "name",
    header: "Name",
}));
const __VLS_18 = __VLS_17({
    field: "name",
    header: "Name",
}, ...__VLS_functionalComponentArgsRest(__VLS_17));
const __VLS_20 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
    header: "Type",
}));
const __VLS_22 = __VLS_21({
    header: "Type",
}, ...__VLS_functionalComponentArgsRest(__VLS_21));
__VLS_23.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_23.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_24 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
        value: (row.item_type),
        severity: (__VLS_ctx.typeSeverity(row.item_type)),
    }));
    const __VLS_26 = __VLS_25({
        value: (row.item_type),
        severity: (__VLS_ctx.typeSeverity(row.item_type)),
    }, ...__VLS_functionalComponentArgsRest(__VLS_25));
}
var __VLS_23;
const __VLS_28 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
    header: "Unit price",
}));
const __VLS_30 = __VLS_29({
    header: "Unit price",
}, ...__VLS_functionalComponentArgsRest(__VLS_29));
__VLS_31.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_31.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    if (row.default_unit_price) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "num" },
        });
        (__VLS_ctx.formatMoney(row.default_unit_price, row.default_currency));
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
}
var __VLS_31;
const __VLS_32 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
    header: "Revenue account",
}));
const __VLS_34 = __VLS_33({
    header: "Revenue account",
}, ...__VLS_functionalComponentArgsRest(__VLS_33));
__VLS_35.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_35.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    if (row.revenue_account_id) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted small" },
        });
        (__VLS_ctx.accountNameById[row.revenue_account_id] ?? '#' + row.revenue_account_id);
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
}
var __VLS_35;
const __VLS_36 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
    header: "Status",
}));
const __VLS_38 = __VLS_37({
    header: "Status",
}, ...__VLS_functionalComponentArgsRest(__VLS_37));
__VLS_39.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_39.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_40 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_41 = __VLS_asFunctionalComponent(__VLS_40, new __VLS_40({
        value: (row.active ? 'Active' : 'Inactive'),
        severity: (row.active ? 'success' : 'secondary'),
    }));
    const __VLS_42 = __VLS_41({
        value: (row.active ? 'Active' : 'Inactive'),
        severity: (row.active ? 'success' : 'secondary'),
    }, ...__VLS_functionalComponentArgsRest(__VLS_41));
}
var __VLS_39;
const __VLS_44 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
    header: "",
    ...{ style: ({ width: '180px' }) },
}));
const __VLS_46 = __VLS_45({
    header: "",
    ...{ style: ({ width: '180px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_45));
__VLS_47.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_47.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "row-actions" },
    });
    const __VLS_48 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_49 = __VLS_asFunctionalComponent(__VLS_48, new __VLS_48({
        ...{ 'onClick': {} },
        icon: "pi pi-eye",
        text: true,
        rounded: true,
        title: "View",
    }));
    const __VLS_50 = __VLS_49({
        ...{ 'onClick': {} },
        icon: "pi pi-eye",
        text: true,
        rounded: true,
        title: "View",
    }, ...__VLS_functionalComponentArgsRest(__VLS_49));
    let __VLS_52;
    let __VLS_53;
    let __VLS_54;
    const __VLS_55 = {
        onClick: (...[$event]) => {
            __VLS_ctx.openDetail(row);
        }
    };
    var __VLS_51;
    const __VLS_56 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_57 = __VLS_asFunctionalComponent(__VLS_56, new __VLS_56({
        ...{ 'onClick': {} },
        icon: "pi pi-pencil",
        text: true,
        rounded: true,
        title: "Edit",
    }));
    const __VLS_58 = __VLS_57({
        ...{ 'onClick': {} },
        icon: "pi pi-pencil",
        text: true,
        rounded: true,
        title: "Edit",
    }, ...__VLS_functionalComponentArgsRest(__VLS_57));
    let __VLS_60;
    let __VLS_61;
    let __VLS_62;
    const __VLS_63 = {
        onClick: (...[$event]) => {
            __VLS_ctx.openEdit(row);
        }
    };
    var __VLS_59;
    if (row.active) {
        const __VLS_64 = {}.Button;
        /** @type {[typeof __VLS_components.Button, ]} */ ;
        // @ts-ignore
        const __VLS_65 = __VLS_asFunctionalComponent(__VLS_64, new __VLS_64({
            ...{ 'onClick': {} },
            icon: "pi pi-ban",
            text: true,
            rounded: true,
            severity: "danger",
            title: "Deactivate",
            loading: (__VLS_ctx.deactivateItem.isPending.value),
        }));
        const __VLS_66 = __VLS_65({
            ...{ 'onClick': {} },
            icon: "pi pi-ban",
            text: true,
            rounded: true,
            severity: "danger",
            title: "Deactivate",
            loading: (__VLS_ctx.deactivateItem.isPending.value),
        }, ...__VLS_functionalComponentArgsRest(__VLS_65));
        let __VLS_68;
        let __VLS_69;
        let __VLS_70;
        const __VLS_71 = {
            onClick: (...[$event]) => {
                if (!(row.active))
                    return;
                __VLS_ctx.confirmDeactivate(row);
            }
        };
        var __VLS_67;
    }
    else {
        const __VLS_72 = {}.Button;
        /** @type {[typeof __VLS_components.Button, ]} */ ;
        // @ts-ignore
        const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({
            ...{ 'onClick': {} },
            icon: "pi pi-undo",
            text: true,
            rounded: true,
            severity: "success",
            title: "Reactivate",
            loading: (__VLS_ctx.reactivateItem.isPending.value),
        }));
        const __VLS_74 = __VLS_73({
            ...{ 'onClick': {} },
            icon: "pi pi-undo",
            text: true,
            rounded: true,
            severity: "success",
            title: "Reactivate",
            loading: (__VLS_ctx.reactivateItem.isPending.value),
        }, ...__VLS_functionalComponentArgsRest(__VLS_73));
        let __VLS_76;
        let __VLS_77;
        let __VLS_78;
        const __VLS_79 = {
            onClick: (...[$event]) => {
                if (!!(row.active))
                    return;
                __VLS_ctx.reactivateItem.mutate(row.item_id);
            }
        };
        var __VLS_75;
    }
}
var __VLS_47;
var __VLS_11;
const __VLS_80 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_81 = __VLS_asFunctionalComponent(__VLS_80, new __VLS_80({
    visible: (__VLS_ctx.showEdit),
    header: (__VLS_ctx.editingId ? 'Edit item' : 'New item'),
    modal: true,
    ...{ style: ({ width: '560px' }) },
}));
const __VLS_82 = __VLS_81({
    visible: (__VLS_ctx.showEdit),
    header: (__VLS_ctx.editingId ? 'Edit item' : 'New item'),
    modal: true,
    ...{ style: ({ width: '560px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_81));
__VLS_83.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (...[$event]) => {
            __VLS_ctx.saveItem.mutate();
        } },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_84 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_85 = __VLS_asFunctionalComponent(__VLS_84, new __VLS_84({
    modelValue: (__VLS_ctx.form.sku),
    maxlength: "50",
}));
const __VLS_86 = __VLS_85({
    modelValue: (__VLS_ctx.form.sku),
    maxlength: "50",
}, ...__VLS_functionalComponentArgsRest(__VLS_85));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_88 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_89 = __VLS_asFunctionalComponent(__VLS_88, new __VLS_88({
    modelValue: (__VLS_ctx.form.item_type),
    options: (__VLS_ctx.itemTypes),
    optionLabel: "label",
    optionValue: "value",
}));
const __VLS_90 = __VLS_89({
    modelValue: (__VLS_ctx.form.item_type),
    options: (__VLS_ctx.itemTypes),
    optionLabel: "label",
    optionValue: "value",
}, ...__VLS_functionalComponentArgsRest(__VLS_89));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_92 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_93 = __VLS_asFunctionalComponent(__VLS_92, new __VLS_92({
    modelValue: (__VLS_ctx.form.name),
    required: true,
    autofocus: true,
}));
const __VLS_94 = __VLS_93({
    modelValue: (__VLS_ctx.form.name),
    required: true,
    autofocus: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_93));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_96 = {}.Textarea;
/** @type {[typeof __VLS_components.Textarea, ]} */ ;
// @ts-ignore
const __VLS_97 = __VLS_asFunctionalComponent(__VLS_96, new __VLS_96({
    modelValue: (__VLS_ctx.form.description),
    rows: "2",
}));
const __VLS_98 = __VLS_97({
    modelValue: (__VLS_ctx.form.description),
    rows: "2",
}, ...__VLS_functionalComponentArgsRest(__VLS_97));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_100 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_101 = __VLS_asFunctionalComponent(__VLS_100, new __VLS_100({
    modelValue: (__VLS_ctx.form.default_currency),
    options: (__VLS_ctx.currencyOptions),
}));
const __VLS_102 = __VLS_101({
    modelValue: (__VLS_ctx.form.default_currency),
    options: (__VLS_ctx.currencyOptions),
}, ...__VLS_functionalComponentArgsRest(__VLS_101));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_104 = {}.InputNumber;
/** @type {[typeof __VLS_components.InputNumber, ]} */ ;
// @ts-ignore
const __VLS_105 = __VLS_asFunctionalComponent(__VLS_104, new __VLS_104({
    modelValue: (__VLS_ctx.form.default_unit_price),
    mode: "decimal",
    minFractionDigits: (__VLS_ctx.form.default_currency === 'IDR' ? 0 : 2),
    maxFractionDigits: (__VLS_ctx.form.default_currency === 'IDR' ? 0 : 2),
    useGrouping: (true),
    min: (0),
}));
const __VLS_106 = __VLS_105({
    modelValue: (__VLS_ctx.form.default_unit_price),
    mode: "decimal",
    minFractionDigits: (__VLS_ctx.form.default_currency === 'IDR' ? 0 : 2),
    maxFractionDigits: (__VLS_ctx.form.default_currency === 'IDR' ? 0 : 2),
    useGrouping: (true),
    min: (0),
}, ...__VLS_functionalComponentArgsRest(__VLS_105));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_108 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_109 = __VLS_asFunctionalComponent(__VLS_108, new __VLS_108({
    modelValue: (__VLS_ctx.form.revenue_account_id),
    options: (__VLS_ctx.incomeAccounts),
    optionLabel: ((a) => `${a.code} · ${a.name}`),
    optionValue: "account_id",
    placeholder: "Pick a 4xxx income account",
    showClear: true,
}));
const __VLS_110 = __VLS_109({
    modelValue: (__VLS_ctx.form.revenue_account_id),
    options: (__VLS_ctx.incomeAccounts),
    optionLabel: ((a) => `${a.code} · ${a.name}`),
    optionValue: "account_id",
    placeholder: "Pick a 4xxx income account",
    showClear: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_109));
if (__VLS_ctx.saveItem.error.value) {
    const __VLS_112 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_113 = __VLS_asFunctionalComponent(__VLS_112, new __VLS_112({
        severity: "error",
        closable: (false),
    }));
    const __VLS_114 = __VLS_113({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_113));
    __VLS_115.slots.default;
    (__VLS_ctx.saveItem.error.value?.response?.data?.detail ?? 'Save failed');
    var __VLS_115;
}
{
    const { footer: __VLS_thisSlot } = __VLS_83.slots;
    const __VLS_116 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_117 = __VLS_asFunctionalComponent(__VLS_116, new __VLS_116({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_118 = __VLS_117({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_117));
    let __VLS_120;
    let __VLS_121;
    let __VLS_122;
    const __VLS_123 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showEdit = false;
        }
    };
    var __VLS_119;
    const __VLS_124 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_125 = __VLS_asFunctionalComponent(__VLS_124, new __VLS_124({
        ...{ 'onClick': {} },
        label: (__VLS_ctx.editingId ? 'Save' : 'Create'),
        disabled: (!__VLS_ctx.form.name || !__VLS_ctx.form.item_type),
        loading: (__VLS_ctx.saveItem.isPending.value),
    }));
    const __VLS_126 = __VLS_125({
        ...{ 'onClick': {} },
        label: (__VLS_ctx.editingId ? 'Save' : 'Create'),
        disabled: (!__VLS_ctx.form.name || !__VLS_ctx.form.item_type),
        loading: (__VLS_ctx.saveItem.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_125));
    let __VLS_128;
    let __VLS_129;
    let __VLS_130;
    const __VLS_131 = {
        onClick: (...[$event]) => {
            __VLS_ctx.saveItem.mutate();
        }
    };
    var __VLS_127;
}
var __VLS_83;
const __VLS_132 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_133 = __VLS_asFunctionalComponent(__VLS_132, new __VLS_132({
    visible: (__VLS_ctx.showDetail),
    header: "Item detail",
    modal: true,
    ...{ style: ({ width: '520px' }) },
}));
const __VLS_134 = __VLS_133({
    visible: (__VLS_ctx.showDetail),
    header: "Item detail",
    modal: true,
    ...{ style: ({ width: '520px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_133));
__VLS_135.slots.default;
if (__VLS_ctx.detailItem) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "detail" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dl, __VLS_intrinsicElements.dl)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.detailItem.name);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    if (__VLS_ctx.detailItem.sku) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({});
        (__VLS_ctx.detailItem.sku);
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    const __VLS_136 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_137 = __VLS_asFunctionalComponent(__VLS_136, new __VLS_136({
        value: (__VLS_ctx.detailItem.item_type),
        severity: (__VLS_ctx.typeSeverity(__VLS_ctx.detailItem.item_type)),
    }));
    const __VLS_138 = __VLS_137({
        value: (__VLS_ctx.detailItem.item_type),
        severity: (__VLS_ctx.typeSeverity(__VLS_ctx.detailItem.item_type)),
    }, ...__VLS_functionalComponentArgsRest(__VLS_137));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.detailItem.description ?? '—');
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.detailItem.default_currency);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({
        ...{ class: "num" },
    });
    (__VLS_ctx.formatMoney(__VLS_ctx.detailItem.default_unit_price, __VLS_ctx.detailItem.default_currency));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    if (__VLS_ctx.detailItem.revenue_account_id) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
        (__VLS_ctx.accountNameById[__VLS_ctx.detailItem.revenue_account_id] ?? '#' + __VLS_ctx.detailItem.revenue_account_id);
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    const __VLS_140 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_141 = __VLS_asFunctionalComponent(__VLS_140, new __VLS_140({
        value: (__VLS_ctx.detailItem.active ? 'Active' : 'Inactive'),
        severity: (__VLS_ctx.detailItem.active ? 'success' : 'secondary'),
    }));
    const __VLS_142 = __VLS_141({
        value: (__VLS_ctx.detailItem.active ? 'Active' : 'Inactive'),
        severity: (__VLS_ctx.detailItem.active ? 'success' : 'secondary'),
    }, ...__VLS_functionalComponentArgsRest(__VLS_141));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (new Date(__VLS_ctx.detailItem.created_at).toLocaleString());
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({
        ...{ class: "small" },
    });
    (__VLS_ctx.detailItem.item_id);
}
{
    const { footer: __VLS_thisSlot } = __VLS_135.slots;
    const __VLS_144 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_145 = __VLS_asFunctionalComponent(__VLS_144, new __VLS_144({
        ...{ 'onClick': {} },
        label: "Close",
        text: true,
    }));
    const __VLS_146 = __VLS_145({
        ...{ 'onClick': {} },
        label: "Close",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_145));
    let __VLS_148;
    let __VLS_149;
    let __VLS_150;
    const __VLS_151 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showDetail = false;
        }
    };
    var __VLS_147;
    if (__VLS_ctx.detailItem) {
        const __VLS_152 = {}.Button;
        /** @type {[typeof __VLS_components.Button, ]} */ ;
        // @ts-ignore
        const __VLS_153 = __VLS_asFunctionalComponent(__VLS_152, new __VLS_152({
            ...{ 'onClick': {} },
            label: "Edit",
            icon: "pi pi-pencil",
        }));
        const __VLS_154 = __VLS_153({
            ...{ 'onClick': {} },
            label: "Edit",
            icon: "pi pi-pencil",
        }, ...__VLS_functionalComponentArgsRest(__VLS_153));
        let __VLS_156;
        let __VLS_157;
        let __VLS_158;
        const __VLS_159 = {
            onClick: (() => { const r = __VLS_ctx.detailItem; __VLS_ctx.showDetail = false; __VLS_ctx.openEdit(r); })
        };
        var __VLS_155;
    }
}
var __VLS_135;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['page-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-tags']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['small']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['row-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
/** @type {__VLS_StyleScopedClasses['detail']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['small']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            DataTable: DataTable,
            Column: Column,
            Button: Button,
            Dialog: Dialog,
            InputText: InputText,
            InputNumber: InputNumber,
            Textarea: Textarea,
            Dropdown: Dropdown,
            Tag: Tag,
            Message: Message,
            formatMoney: formatMoney,
            itemTypes: itemTypes,
            currencyOptions: currencyOptions,
            showEdit: showEdit,
            editingId: editingId,
            form: form,
            showDetail: showDetail,
            detailItem: detailItem,
            items: items,
            isLoading: isLoading,
            incomeAccounts: incomeAccounts,
            accountNameById: accountNameById,
            saveItem: saveItem,
            deactivateItem: deactivateItem,
            reactivateItem: reactivateItem,
            openCreate: openCreate,
            openEdit: openEdit,
            openDetail: openDetail,
            confirmDeactivate: confirmDeactivate,
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
