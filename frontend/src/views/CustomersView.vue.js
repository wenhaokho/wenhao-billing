import { ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Dialog from "primevue/dialog";
import InputText from "primevue/inputtext";
import Textarea from "primevue/textarea";
import Chip from "primevue/chip";
import Tag from "primevue/tag";
import Message from "primevue/message";
import InputSwitch from "primevue/inputswitch";
import { api } from "@/api/client";
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
const router = useRouter();
const route = useRoute();
const queryClient = useQueryClient();
const showEdit = ref(false);
const editingId = ref(null);
const form = ref(emptyForm());
const formAliasDraft = ref("");
const showInactive = ref(false);
const { data, isLoading } = useQuery({
    queryKey: ["customers", showInactive],
    queryFn: async () => {
        const params = showInactive.value ? {} : { active: true };
        return (await api.get("/customers", { params })).data;
    },
});
function buildBody() {
    return {
        name: form.value.name,
        matching_aliases: form.value.aliases,
        active: form.value.active,
        contact_name: form.value.contact_name.trim() || null,
        contact_email: form.value.contact_email.trim() || null,
        contact_phone: form.value.contact_phone.trim() || null,
        billing_address: form.value.billing_address.trim() || null,
    };
}
const saveCustomer = useMutation({
    mutationFn: async () => {
        const body = buildBody();
        if (editingId.value) {
            return api.patch(`/customers/${editingId.value}`, body);
        }
        return api.post("/customers", body);
    },
    onSuccess: () => {
        showEdit.value = false;
        editingId.value = null;
        form.value = emptyForm();
        formAliasDraft.value = "";
        queryClient.invalidateQueries({ queryKey: ["customers"] });
        queryClient.invalidateQueries({ queryKey: ["customer"] });
    },
});
const deactivateCustomer = useMutation({
    mutationFn: async (id) => api.delete(`/customers/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["customers"] }),
});
const reactivateCustomer = useMutation({
    mutationFn: async (id) => api.patch(`/customers/${id}`, { active: true }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["customers"] }),
});
function addAlias() {
    const v = formAliasDraft.value.trim();
    if (!v)
        return;
    form.value.aliases = [...form.value.aliases, v];
    formAliasDraft.value = "";
}
function removeAlias(i) {
    form.value.aliases = form.value.aliases.filter((_, idx) => idx !== i);
}
function openCreate() {
    editingId.value = null;
    form.value = emptyForm();
    formAliasDraft.value = "";
    saveCustomer.reset();
    showEdit.value = true;
}
function openEdit(row) {
    editingId.value = row.customer_id;
    form.value = {
        name: row.name,
        contact_name: row.contact_name ?? "",
        contact_email: row.contact_email ?? "",
        contact_phone: row.contact_phone ?? "",
        billing_address: row.billing_address ?? "",
        aliases: [...row.matching_aliases],
        active: row.active,
    };
    formAliasDraft.value = "";
    saveCustomer.reset();
    showEdit.value = true;
}
function openDetail(row) {
    router.push({ name: "customer-detail", params: { customerId: row.customer_id } });
}
function confirmDeactivate(row) {
    if (window.confirm(`Deactivate "${row.name}"? It will be hidden from active lists but kept for ledger history.`)) {
        deactivateCustomer.mutate(row.customer_id);
    }
}
// Handle `?edit=<id>` deep-link from the detail page's Edit button.
watch([() => route.query.edit, () => data.value], ([editId, rows]) => {
    if (!editId || !rows)
        return;
    const target = rows.find((c) => c.customer_id === editId);
    if (target) {
        openEdit(target);
        router.replace({ query: {} });
    }
}, { immediate: true });
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
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
    label: "New customer",
    icon: "pi pi-plus",
}));
const __VLS_6 = __VLS_5({
    ...{ 'onClick': {} },
    label: "New customer",
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
    dataKey: "customer_id",
    stripedRows: true,
}));
const __VLS_14 = __VLS_13({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "customer_id",
    stripedRows: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
__VLS_15.slots.default;
{
    const { empty: __VLS_thisSlot } = __VLS_15.slots;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-users" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.strong, __VLS_intrinsicElements.strong)({});
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
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
    header: "Matching aliases",
}));
const __VLS_26 = __VLS_25({
    header: "Matching aliases",
}, ...__VLS_functionalComponentArgsRest(__VLS_25));
__VLS_27.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_27.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    if (row.matching_aliases.length === 0) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "muted" },
        });
    }
    else {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "chips" },
        });
        for (const [a] of __VLS_getVForSourceType((row.matching_aliases))) {
            const __VLS_28 = {}.Chip;
            /** @type {[typeof __VLS_components.Chip, ]} */ ;
            // @ts-ignore
            const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
                key: (a),
                label: (a),
            }));
            const __VLS_30 = __VLS_29({
                key: (a),
                label: (a),
            }, ...__VLS_functionalComponentArgsRest(__VLS_29));
        }
    }
}
var __VLS_27;
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
    ...{ style: ({ width: '200px' }) },
}));
const __VLS_42 = __VLS_41({
    header: "",
    ...{ style: ({ width: '200px' }) },
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
            loading: (__VLS_ctx.deactivateCustomer.isPending.value),
        }));
        const __VLS_62 = __VLS_61({
            ...{ 'onClick': {} },
            icon: "pi pi-ban",
            text: true,
            rounded: true,
            severity: "danger",
            title: "Deactivate",
            loading: (__VLS_ctx.deactivateCustomer.isPending.value),
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
            loading: (__VLS_ctx.reactivateCustomer.isPending.value),
        }));
        const __VLS_70 = __VLS_69({
            ...{ 'onClick': {} },
            icon: "pi pi-undo",
            text: true,
            rounded: true,
            severity: "success",
            title: "Reactivate",
            loading: (__VLS_ctx.reactivateCustomer.isPending.value),
        }, ...__VLS_functionalComponentArgsRest(__VLS_69));
        let __VLS_72;
        let __VLS_73;
        let __VLS_74;
        const __VLS_75 = {
            onClick: (...[$event]) => {
                if (!!(row.active))
                    return;
                __VLS_ctx.reactivateCustomer.mutate(row.customer_id);
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
    header: (__VLS_ctx.editingId ? 'Edit customer' : 'New customer'),
    modal: true,
    ...{ style: ({ width: '560px' }) },
}));
const __VLS_78 = __VLS_77({
    visible: (__VLS_ctx.showEdit),
    header: (__VLS_ctx.editingId ? 'Edit customer' : 'New customer'),
    modal: true,
    ...{ style: ({ width: '560px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_77));
__VLS_79.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (...[$event]) => {
            __VLS_ctx.saveCustomer.mutate();
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
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_92 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_93 = __VLS_asFunctionalComponent(__VLS_92, new __VLS_92({
    modelValue: (__VLS_ctx.form.contact_phone),
}));
const __VLS_94 = __VLS_93({
    modelValue: (__VLS_ctx.form.contact_phone),
}, ...__VLS_functionalComponentArgsRest(__VLS_93));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_96 = {}.Textarea;
/** @type {[typeof __VLS_components.Textarea, ]} */ ;
// @ts-ignore
const __VLS_97 = __VLS_asFunctionalComponent(__VLS_96, new __VLS_96({
    modelValue: (__VLS_ctx.form.billing_address),
    rows: "3",
}));
const __VLS_98 = __VLS_97({
    modelValue: (__VLS_ctx.form.billing_address),
    rows: "3",
}, ...__VLS_functionalComponentArgsRest(__VLS_97));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_100 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_101 = __VLS_asFunctionalComponent(__VLS_100, new __VLS_100({
    ...{ 'onKeydown': {} },
    modelValue: (__VLS_ctx.formAliasDraft),
    placeholder: "e.g. Acme Corp, ACME",
}));
const __VLS_102 = __VLS_101({
    ...{ 'onKeydown': {} },
    modelValue: (__VLS_ctx.formAliasDraft),
    placeholder: "e.g. Acme Corp, ACME",
}, ...__VLS_functionalComponentArgsRest(__VLS_101));
let __VLS_104;
let __VLS_105;
let __VLS_106;
const __VLS_107 = {
    onKeydown: (__VLS_ctx.addAlias)
};
var __VLS_103;
if (__VLS_ctx.form.aliases.length) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "chips" },
    });
    for (const [a, i] of __VLS_getVForSourceType((__VLS_ctx.form.aliases))) {
        const __VLS_108 = {}.Chip;
        /** @type {[typeof __VLS_components.Chip, ]} */ ;
        // @ts-ignore
        const __VLS_109 = __VLS_asFunctionalComponent(__VLS_108, new __VLS_108({
            ...{ 'onRemove': {} },
            key: (a),
            label: (a),
            removable: true,
        }));
        const __VLS_110 = __VLS_109({
            ...{ 'onRemove': {} },
            key: (a),
            label: (a),
            removable: true,
        }, ...__VLS_functionalComponentArgsRest(__VLS_109));
        let __VLS_112;
        let __VLS_113;
        let __VLS_114;
        const __VLS_115 = {
            onRemove: (...[$event]) => {
                if (!(__VLS_ctx.form.aliases.length))
                    return;
                __VLS_ctx.removeAlias(i);
            }
        };
        var __VLS_111;
    }
}
if (__VLS_ctx.saveCustomer.error.value) {
    const __VLS_116 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_117 = __VLS_asFunctionalComponent(__VLS_116, new __VLS_116({
        severity: "error",
        closable: (false),
    }));
    const __VLS_118 = __VLS_117({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_117));
    __VLS_119.slots.default;
    (__VLS_ctx.saveCustomer.error.value?.response?.data?.detail ?? 'Save failed');
    var __VLS_119;
}
{
    const { footer: __VLS_thisSlot } = __VLS_79.slots;
    const __VLS_120 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_121 = __VLS_asFunctionalComponent(__VLS_120, new __VLS_120({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_122 = __VLS_121({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_121));
    let __VLS_124;
    let __VLS_125;
    let __VLS_126;
    const __VLS_127 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showEdit = false;
        }
    };
    var __VLS_123;
    const __VLS_128 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_129 = __VLS_asFunctionalComponent(__VLS_128, new __VLS_128({
        ...{ 'onClick': {} },
        label: (__VLS_ctx.editingId ? 'Save' : 'Create'),
        disabled: (!__VLS_ctx.form.name),
        loading: (__VLS_ctx.saveCustomer.isPending.value),
    }));
    const __VLS_130 = __VLS_129({
        ...{ 'onClick': {} },
        label: (__VLS_ctx.editingId ? 'Save' : 'Create'),
        disabled: (!__VLS_ctx.form.name),
        loading: (__VLS_ctx.saveCustomer.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_129));
    let __VLS_132;
    let __VLS_133;
    let __VLS_134;
    const __VLS_135 = {
        onClick: (...[$event]) => {
            __VLS_ctx.saveCustomer.mutate();
        }
    };
    var __VLS_131;
}
var __VLS_79;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['page-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['toggle']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-users']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['small']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['chips']} */ ;
/** @type {__VLS_StyleScopedClasses['row-actions']} */ ;
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
            Dialog: Dialog,
            InputText: InputText,
            Textarea: Textarea,
            Chip: Chip,
            Tag: Tag,
            Message: Message,
            InputSwitch: InputSwitch,
            showEdit: showEdit,
            editingId: editingId,
            form: form,
            formAliasDraft: formAliasDraft,
            showInactive: showInactive,
            data: data,
            isLoading: isLoading,
            saveCustomer: saveCustomer,
            deactivateCustomer: deactivateCustomer,
            reactivateCustomer: reactivateCustomer,
            addAlias: addAlias,
            removeAlias: removeAlias,
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
