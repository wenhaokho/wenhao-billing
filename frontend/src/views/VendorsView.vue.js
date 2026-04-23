import { ref } from "vue";
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
import InputSwitch from "primevue/inputswitch";
import { api } from "@/api/client";
const queryClient = useQueryClient();
const currencyOptions = ["IDR", "SGD", "USD"];
function emptyForm() {
    return {
        name: "",
        contact_name: "",
        contact_email: "",
        default_currency: "IDR",
        payment_terms_days: 30,
        tax_id: "",
        notes: "",
        active: true,
    };
}
const showEdit = ref(false);
const editingId = ref(null);
const form = ref(emptyForm());
const showDetail = ref(false);
const detailVendor = ref(null);
const showInactive = ref(false);
const { data, isLoading } = useQuery({
    queryKey: ["vendors", showInactive],
    queryFn: async () => {
        const params = showInactive.value ? {} : { active: true };
        return (await api.get("/vendors", { params })).data;
    },
});
function buildBody() {
    const body = { ...form.value };
    for (const k of Object.keys(body)) {
        if (body[k] === "")
            delete body[k];
    }
    body.active = form.value.active;
    return body;
}
const saveVendor = useMutation({
    mutationFn: async () => {
        if (editingId.value) {
            return api.patch(`/vendors/${editingId.value}`, buildBody());
        }
        return api.post("/vendors", buildBody());
    },
    onSuccess: () => {
        showEdit.value = false;
        editingId.value = null;
        form.value = emptyForm();
        queryClient.invalidateQueries({ queryKey: ["vendors"] });
    },
});
const deactivateVendor = useMutation({
    mutationFn: async (id) => api.delete(`/vendors/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["vendors"] }),
});
const reactivateVendor = useMutation({
    mutationFn: async (id) => api.patch(`/vendors/${id}`, { active: true }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["vendors"] }),
});
function openCreate() {
    editingId.value = null;
    form.value = emptyForm();
    saveVendor.reset();
    showEdit.value = true;
}
function openEdit(row) {
    editingId.value = row.vendor_id;
    form.value = {
        name: row.name,
        contact_name: row.contact_name ?? "",
        contact_email: row.contact_email ?? "",
        default_currency: row.default_currency,
        payment_terms_days: row.payment_terms_days,
        tax_id: row.tax_id ?? "",
        notes: row.notes ?? "",
        active: row.active,
    };
    saveVendor.reset();
    showEdit.value = true;
}
function openDetail(row) {
    detailVendor.value = row;
    showDetail.value = true;
}
function confirmDeactivate(row) {
    if (window.confirm(`Deactivate "${row.name}"? It will be hidden from active lists but kept for ledger history.`)) {
        deactivateVendor.mutate(row.vendor_id);
    }
}
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
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
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({
    ...{ class: "toggle" },
});
const __VLS_0 = {}.InputSwitch;
/** @type {[typeof __VLS_components.InputSwitch, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    modelValue: (__VLS_ctx.showInactive),
}));
const __VLS_2 = __VLS_1({
    modelValue: (__VLS_ctx.showInactive),
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
__VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
const __VLS_4 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_5 = __VLS_asFunctionalComponent(__VLS_4, new __VLS_4({
    ...{ 'onClick': {} },
    label: "New vendor",
    icon: "pi pi-plus",
}));
const __VLS_6 = __VLS_5({
    ...{ 'onClick': {} },
    label: "New vendor",
    icon: "pi pi-plus",
}, ...__VLS_functionalComponentArgsRest(__VLS_5));
let __VLS_8;
let __VLS_9;
let __VLS_10;
const __VLS_11 = {
    onClick: (__VLS_ctx.openCreate)
};
var __VLS_7;
const __VLS_12 = {}.DataTable;
/** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
// @ts-ignore
const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "vendor_id",
    stripedRows: true,
}));
const __VLS_14 = __VLS_13({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "vendor_id",
    stripedRows: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
__VLS_15.slots.default;
{
    const { empty: __VLS_thisSlot } = __VLS_15.slots;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-briefcase" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
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
    header: "Contact",
}));
const __VLS_22 = __VLS_21({
    header: "Contact",
}, ...__VLS_functionalComponentArgsRest(__VLS_21));
__VLS_23.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_23.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    if (row.contact_name || row.contact_email) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
        if (row.contact_name) {
            __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
            (row.contact_name);
        }
        if (row.contact_email) {
            __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
                ...{ class: "muted small" },
            });
            (row.contact_email);
        }
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
}
var __VLS_23;
const __VLS_24 = {}.Column;
/** @type {[typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
    field: "default_currency",
    header: "Ccy",
}));
const __VLS_26 = __VLS_25({
    field: "default_currency",
    header: "Ccy",
}, ...__VLS_functionalComponentArgsRest(__VLS_25));
const __VLS_28 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
    header: "Terms",
}));
const __VLS_30 = __VLS_29({
    header: "Terms",
}, ...__VLS_functionalComponentArgsRest(__VLS_29));
__VLS_31.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_31.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    (row.payment_terms_days);
}
var __VLS_31;
const __VLS_32 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
    header: "Status",
}));
const __VLS_34 = __VLS_33({
    header: "Status",
}, ...__VLS_functionalComponentArgsRest(__VLS_33));
__VLS_35.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_35.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_36 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
        value: (row.active ? 'Active' : 'Inactive'),
        severity: (row.active ? 'success' : 'secondary'),
    }));
    const __VLS_38 = __VLS_37({
        value: (row.active ? 'Active' : 'Inactive'),
        severity: (row.active ? 'success' : 'secondary'),
    }, ...__VLS_functionalComponentArgsRest(__VLS_37));
}
var __VLS_35;
const __VLS_40 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_41 = __VLS_asFunctionalComponent(__VLS_40, new __VLS_40({
    header: "",
    ...{ style: ({ width: '180px' }) },
}));
const __VLS_42 = __VLS_41({
    header: "",
    ...{ style: ({ width: '180px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_41));
__VLS_43.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_43.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "row-actions" },
    });
    const __VLS_44 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
        ...{ 'onClick': {} },
        icon: "pi pi-eye",
        text: true,
        rounded: true,
        title: "View",
    }));
    const __VLS_46 = __VLS_45({
        ...{ 'onClick': {} },
        icon: "pi pi-eye",
        text: true,
        rounded: true,
        title: "View",
    }, ...__VLS_functionalComponentArgsRest(__VLS_45));
    let __VLS_48;
    let __VLS_49;
    let __VLS_50;
    const __VLS_51 = {
        onClick: (...[$event]) => {
            __VLS_ctx.openDetail(row);
        }
    };
    var __VLS_47;
    const __VLS_52 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({
        ...{ 'onClick': {} },
        icon: "pi pi-pencil",
        text: true,
        rounded: true,
        title: "Edit",
    }));
    const __VLS_54 = __VLS_53({
        ...{ 'onClick': {} },
        icon: "pi pi-pencil",
        text: true,
        rounded: true,
        title: "Edit",
    }, ...__VLS_functionalComponentArgsRest(__VLS_53));
    let __VLS_56;
    let __VLS_57;
    let __VLS_58;
    const __VLS_59 = {
        onClick: (...[$event]) => {
            __VLS_ctx.openEdit(row);
        }
    };
    var __VLS_55;
    if (row.active) {
        const __VLS_60 = {}.Button;
        /** @type {[typeof __VLS_components.Button, ]} */ ;
        // @ts-ignore
        const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({
            ...{ 'onClick': {} },
            icon: "pi pi-ban",
            text: true,
            rounded: true,
            severity: "danger",
            title: "Deactivate",
            loading: (__VLS_ctx.deactivateVendor.isPending.value),
        }));
        const __VLS_62 = __VLS_61({
            ...{ 'onClick': {} },
            icon: "pi pi-ban",
            text: true,
            rounded: true,
            severity: "danger",
            title: "Deactivate",
            loading: (__VLS_ctx.deactivateVendor.isPending.value),
        }, ...__VLS_functionalComponentArgsRest(__VLS_61));
        let __VLS_64;
        let __VLS_65;
        let __VLS_66;
        const __VLS_67 = {
            onClick: (...[$event]) => {
                if (!(row.active))
                    return;
                __VLS_ctx.confirmDeactivate(row);
            }
        };
        var __VLS_63;
    }
    else {
        const __VLS_68 = {}.Button;
        /** @type {[typeof __VLS_components.Button, ]} */ ;
        // @ts-ignore
        const __VLS_69 = __VLS_asFunctionalComponent(__VLS_68, new __VLS_68({
            ...{ 'onClick': {} },
            icon: "pi pi-undo",
            text: true,
            rounded: true,
            severity: "success",
            title: "Reactivate",
            loading: (__VLS_ctx.reactivateVendor.isPending.value),
        }));
        const __VLS_70 = __VLS_69({
            ...{ 'onClick': {} },
            icon: "pi pi-undo",
            text: true,
            rounded: true,
            severity: "success",
            title: "Reactivate",
            loading: (__VLS_ctx.reactivateVendor.isPending.value),
        }, ...__VLS_functionalComponentArgsRest(__VLS_69));
        let __VLS_72;
        let __VLS_73;
        let __VLS_74;
        const __VLS_75 = {
            onClick: (...[$event]) => {
                if (!!(row.active))
                    return;
                __VLS_ctx.reactivateVendor.mutate(row.vendor_id);
            }
        };
        var __VLS_71;
    }
}
var __VLS_43;
var __VLS_15;
const __VLS_76 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_77 = __VLS_asFunctionalComponent(__VLS_76, new __VLS_76({
    visible: (__VLS_ctx.showEdit),
    header: (__VLS_ctx.editingId ? 'Edit vendor' : 'New vendor'),
    modal: true,
    ...{ style: ({ width: '520px' }) },
}));
const __VLS_78 = __VLS_77({
    visible: (__VLS_ctx.showEdit),
    header: (__VLS_ctx.editingId ? 'Edit vendor' : 'New vendor'),
    modal: true,
    ...{ style: ({ width: '520px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_77));
__VLS_79.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (...[$event]) => {
            __VLS_ctx.saveVendor.mutate();
        } },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_80 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_81 = __VLS_asFunctionalComponent(__VLS_80, new __VLS_80({
    modelValue: (__VLS_ctx.form.name),
    required: true,
    autofocus: true,
}));
const __VLS_82 = __VLS_81({
    modelValue: (__VLS_ctx.form.name),
    required: true,
    autofocus: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_81));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_84 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_85 = __VLS_asFunctionalComponent(__VLS_84, new __VLS_84({
    modelValue: (__VLS_ctx.form.contact_name),
}));
const __VLS_86 = __VLS_85({
    modelValue: (__VLS_ctx.form.contact_name),
}, ...__VLS_functionalComponentArgsRest(__VLS_85));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_88 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_89 = __VLS_asFunctionalComponent(__VLS_88, new __VLS_88({
    modelValue: (__VLS_ctx.form.contact_email),
    type: "email",
}));
const __VLS_90 = __VLS_89({
    modelValue: (__VLS_ctx.form.contact_email),
    type: "email",
}, ...__VLS_functionalComponentArgsRest(__VLS_89));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_92 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_93 = __VLS_asFunctionalComponent(__VLS_92, new __VLS_92({
    modelValue: (__VLS_ctx.form.default_currency),
    options: (__VLS_ctx.currencyOptions),
}));
const __VLS_94 = __VLS_93({
    modelValue: (__VLS_ctx.form.default_currency),
    options: (__VLS_ctx.currencyOptions),
}, ...__VLS_functionalComponentArgsRest(__VLS_93));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_96 = {}.InputNumber;
/** @type {[typeof __VLS_components.InputNumber, ]} */ ;
// @ts-ignore
const __VLS_97 = __VLS_asFunctionalComponent(__VLS_96, new __VLS_96({
    modelValue: (__VLS_ctx.form.payment_terms_days),
    min: (0),
    max: (365),
}));
const __VLS_98 = __VLS_97({
    modelValue: (__VLS_ctx.form.payment_terms_days),
    min: (0),
    max: (365),
}, ...__VLS_functionalComponentArgsRest(__VLS_97));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_100 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_101 = __VLS_asFunctionalComponent(__VLS_100, new __VLS_100({
    modelValue: (__VLS_ctx.form.tax_id),
}));
const __VLS_102 = __VLS_101({
    modelValue: (__VLS_ctx.form.tax_id),
}, ...__VLS_functionalComponentArgsRest(__VLS_101));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_104 = {}.Textarea;
/** @type {[typeof __VLS_components.Textarea, ]} */ ;
// @ts-ignore
const __VLS_105 = __VLS_asFunctionalComponent(__VLS_104, new __VLS_104({
    modelValue: (__VLS_ctx.form.notes),
    rows: "2",
}));
const __VLS_106 = __VLS_105({
    modelValue: (__VLS_ctx.form.notes),
    rows: "2",
}, ...__VLS_functionalComponentArgsRest(__VLS_105));
if (__VLS_ctx.saveVendor.error.value) {
    const __VLS_108 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_109 = __VLS_asFunctionalComponent(__VLS_108, new __VLS_108({
        severity: "error",
        closable: (false),
    }));
    const __VLS_110 = __VLS_109({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_109));
    __VLS_111.slots.default;
    (__VLS_ctx.saveVendor.error.value?.response?.data?.detail ?? 'Save failed');
    var __VLS_111;
}
{
    const { footer: __VLS_thisSlot } = __VLS_79.slots;
    const __VLS_112 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_113 = __VLS_asFunctionalComponent(__VLS_112, new __VLS_112({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_114 = __VLS_113({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_113));
    let __VLS_116;
    let __VLS_117;
    let __VLS_118;
    const __VLS_119 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showEdit = false;
        }
    };
    var __VLS_115;
    const __VLS_120 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_121 = __VLS_asFunctionalComponent(__VLS_120, new __VLS_120({
        ...{ 'onClick': {} },
        label: (__VLS_ctx.editingId ? 'Save' : 'Create'),
        disabled: (!__VLS_ctx.form.name),
        loading: (__VLS_ctx.saveVendor.isPending.value),
    }));
    const __VLS_122 = __VLS_121({
        ...{ 'onClick': {} },
        label: (__VLS_ctx.editingId ? 'Save' : 'Create'),
        disabled: (!__VLS_ctx.form.name),
        loading: (__VLS_ctx.saveVendor.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_121));
    let __VLS_124;
    let __VLS_125;
    let __VLS_126;
    const __VLS_127 = {
        onClick: (...[$event]) => {
            __VLS_ctx.saveVendor.mutate();
        }
    };
    var __VLS_123;
}
var __VLS_79;
const __VLS_128 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_129 = __VLS_asFunctionalComponent(__VLS_128, new __VLS_128({
    visible: (__VLS_ctx.showDetail),
    header: "Vendor detail",
    modal: true,
    ...{ style: ({ width: '520px' }) },
}));
const __VLS_130 = __VLS_129({
    visible: (__VLS_ctx.showDetail),
    header: "Vendor detail",
    modal: true,
    ...{ style: ({ width: '520px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_129));
__VLS_131.slots.default;
if (__VLS_ctx.detailVendor) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "detail" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dl, __VLS_intrinsicElements.dl)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.detailVendor.name);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    if (__VLS_ctx.detailVendor.contact_name) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
        (__VLS_ctx.detailVendor.contact_name);
    }
    if (__VLS_ctx.detailVendor.contact_email) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "muted small" },
        });
        (__VLS_ctx.detailVendor.contact_email);
    }
    if (!__VLS_ctx.detailVendor.contact_name && !__VLS_ctx.detailVendor.contact_email) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.detailVendor.default_currency);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.detailVendor.payment_terms_days);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.detailVendor.tax_id ?? '—');
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.detailVendor.notes ?? '—');
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    const __VLS_132 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_133 = __VLS_asFunctionalComponent(__VLS_132, new __VLS_132({
        value: (__VLS_ctx.detailVendor.active ? 'Active' : 'Inactive'),
        severity: (__VLS_ctx.detailVendor.active ? 'success' : 'secondary'),
    }));
    const __VLS_134 = __VLS_133({
        value: (__VLS_ctx.detailVendor.active ? 'Active' : 'Inactive'),
        severity: (__VLS_ctx.detailVendor.active ? 'success' : 'secondary'),
    }, ...__VLS_functionalComponentArgsRest(__VLS_133));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (new Date(__VLS_ctx.detailVendor.created_at).toLocaleString());
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({
        ...{ class: "small" },
    });
    (__VLS_ctx.detailVendor.vendor_id);
}
{
    const { footer: __VLS_thisSlot } = __VLS_131.slots;
    const __VLS_136 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_137 = __VLS_asFunctionalComponent(__VLS_136, new __VLS_136({
        ...{ 'onClick': {} },
        label: "Close",
        text: true,
    }));
    const __VLS_138 = __VLS_137({
        ...{ 'onClick': {} },
        label: "Close",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_137));
    let __VLS_140;
    let __VLS_141;
    let __VLS_142;
    const __VLS_143 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showDetail = false;
        }
    };
    var __VLS_139;
    if (__VLS_ctx.detailVendor) {
        const __VLS_144 = {}.Button;
        /** @type {[typeof __VLS_components.Button, ]} */ ;
        // @ts-ignore
        const __VLS_145 = __VLS_asFunctionalComponent(__VLS_144, new __VLS_144({
            ...{ 'onClick': {} },
            label: "Edit",
            icon: "pi pi-pencil",
        }));
        const __VLS_146 = __VLS_145({
            ...{ 'onClick': {} },
            label: "Edit",
            icon: "pi pi-pencil",
        }, ...__VLS_functionalComponentArgsRest(__VLS_145));
        let __VLS_148;
        let __VLS_149;
        let __VLS_150;
        const __VLS_151 = {
            onClick: (() => { const r = __VLS_ctx.detailVendor; __VLS_ctx.showDetail = false; __VLS_ctx.openEdit(r); })
        };
        var __VLS_147;
    }
}
var __VLS_131;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['page-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['toggle']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-briefcase']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['small']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['row-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
/** @type {__VLS_StyleScopedClasses['detail']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['small']} */ ;
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
            InputSwitch: InputSwitch,
            currencyOptions: currencyOptions,
            showEdit: showEdit,
            editingId: editingId,
            form: form,
            showDetail: showDetail,
            detailVendor: detailVendor,
            showInactive: showInactive,
            data: data,
            isLoading: isLoading,
            saveVendor: saveVendor,
            deactivateVendor: deactivateVendor,
            reactivateVendor: reactivateVendor,
            openCreate: openCreate,
            openEdit: openEdit,
            openDetail: openDetail,
            confirmDeactivate: confirmDeactivate,
        };
    },
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
; /* PartiallyEnd: #4569/main.vue */
