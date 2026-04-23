import { ref, computed } from "vue";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Dialog from "primevue/dialog";
import InputText from "primevue/inputtext";
import Dropdown from "primevue/dropdown";
import Tag from "primevue/tag";
import Message from "primevue/message";
import { api } from "@/api/client";
const queryClient = useQueryClient();
const accountTypes = [
    { label: "Asset", value: "ASSET" },
    { label: "Liability", value: "LIABILITY" },
    { label: "Equity", value: "EQUITY" },
    { label: "Income", value: "INCOME" },
    { label: "COGS", value: "COGS" },
    { label: "Expense", value: "EXPENSE" },
];
function emptyForm() {
    return { code: "", name: "", type: "ASSET", active: true };
}
const showEdit = ref(false);
const editingId = ref(null);
const form = ref(emptyForm());
const showDetail = ref(false);
const detailAccount = ref(null);
const { data, isLoading } = useQuery({
    queryKey: ["chart-of-accounts"],
    queryFn: async () => (await api.get("/accounting/chart-of-accounts")).data,
});
const grouped = computed(() => {
    const g = {};
    for (const a of data.value ?? []) {
        (g[a.type] ??= []).push(a);
    }
    return g;
});
const typeOrder = ["ASSET", "LIABILITY", "EQUITY", "INCOME", "COGS", "EXPENSE"];
const orderedTypes = computed(() => typeOrder.filter((t) => grouped.value[t]?.length));
const saveAccount = useMutation({
    mutationFn: async () => {
        if (editingId.value) {
            return api.patch(`/accounting/chart-of-accounts/${editingId.value}`, form.value);
        }
        return api.post("/accounting/chart-of-accounts", form.value);
    },
    onSuccess: () => {
        showEdit.value = false;
        editingId.value = null;
        form.value = emptyForm();
        queryClient.invalidateQueries({ queryKey: ["chart-of-accounts"] });
    },
});
const deactivateAccount = useMutation({
    mutationFn: async (id) => api.delete(`/accounting/chart-of-accounts/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["chart-of-accounts"] }),
});
const reactivateAccount = useMutation({
    mutationFn: async (id) => api.patch(`/accounting/chart-of-accounts/${id}`, { active: true }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["chart-of-accounts"] }),
});
function openCreate() {
    editingId.value = null;
    form.value = emptyForm();
    saveAccount.reset();
    showEdit.value = true;
}
function openEdit(row) {
    editingId.value = row.account_id;
    form.value = {
        code: row.code,
        name: row.name,
        type: row.type,
        active: row.active,
    };
    saveAccount.reset();
    showEdit.value = true;
}
function openDetail(row) {
    detailAccount.value = row;
    showDetail.value = true;
}
function confirmDeactivate(row) {
    if (window.confirm(`Deactivate "${row.code} · ${row.name}"? Existing journal lines remain; new postings will be blocked.`)) {
        deactivateAccount.mutate(row.account_id);
    }
}
function typeSeverity(t) {
    switch (t) {
        case "ASSET": return "info";
        case "LIABILITY": return "warning";
        case "INCOME": return "success";
        case "COGS": return "danger";
        case "EXPENSE": return "danger";
        default: return "secondary";
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
    label: "New account",
    icon: "pi pi-plus",
}));
const __VLS_2 = __VLS_1({
    ...{ 'onClick': {} },
    label: "New account",
    icon: "pi pi-plus",
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
let __VLS_4;
let __VLS_5;
let __VLS_6;
const __VLS_7 = {
    onClick: (__VLS_ctx.openCreate)
};
var __VLS_3;
if (__VLS_ctx.isLoading) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "card card-pad" },
    });
}
else {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "groups" },
    });
    for (const [type] of __VLS_getVForSourceType((__VLS_ctx.orderedTypes))) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            key: (type),
            ...{ class: "group" },
        });
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "group-head" },
        });
        const __VLS_8 = {}.Tag;
        /** @type {[typeof __VLS_components.Tag, ]} */ ;
        // @ts-ignore
        const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
            value: (type),
            severity: (__VLS_ctx.typeSeverity(type)),
        }));
        const __VLS_10 = __VLS_9({
            value: (type),
            severity: (__VLS_ctx.typeSeverity(type)),
        }, ...__VLS_functionalComponentArgsRest(__VLS_9));
        __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
            ...{ class: "count" },
        });
        (__VLS_ctx.grouped[type].length);
        const __VLS_12 = {}.DataTable;
        /** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
        // @ts-ignore
        const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
            value: (__VLS_ctx.grouped[type]),
            dataKey: "account_id",
            size: "small",
            stripedRows: true,
        }));
        const __VLS_14 = __VLS_13({
            value: (__VLS_ctx.grouped[type]),
            dataKey: "account_id",
            size: "small",
            stripedRows: true,
        }, ...__VLS_functionalComponentArgsRest(__VLS_13));
        __VLS_15.slots.default;
        const __VLS_16 = {}.Column;
        /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
        // @ts-ignore
        const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
            field: "code",
            header: "Code",
            ...{ style: ({ width: '100px' }) },
        }));
        const __VLS_18 = __VLS_17({
            field: "code",
            header: "Code",
            ...{ style: ({ width: '100px' }) },
        }, ...__VLS_functionalComponentArgsRest(__VLS_17));
        __VLS_19.slots.default;
        {
            const { body: __VLS_thisSlot } = __VLS_19.slots;
            const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
            __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({});
            (row.code);
        }
        var __VLS_19;
        const __VLS_20 = {}.Column;
        /** @type {[typeof __VLS_components.Column, ]} */ ;
        // @ts-ignore
        const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
            field: "name",
            header: "Name",
        }));
        const __VLS_22 = __VLS_21({
            field: "name",
            header: "Name",
        }, ...__VLS_functionalComponentArgsRest(__VLS_21));
        const __VLS_24 = {}.Column;
        /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
        // @ts-ignore
        const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
            header: "Status",
            ...{ style: ({ width: '110px' }) },
        }));
        const __VLS_26 = __VLS_25({
            header: "Status",
            ...{ style: ({ width: '110px' }) },
        }, ...__VLS_functionalComponentArgsRest(__VLS_25));
        __VLS_27.slots.default;
        {
            const { body: __VLS_thisSlot } = __VLS_27.slots;
            const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
            const __VLS_28 = {}.Tag;
            /** @type {[typeof __VLS_components.Tag, ]} */ ;
            // @ts-ignore
            const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
                value: (row.active ? 'Active' : 'Inactive'),
                severity: (row.active ? 'success' : 'secondary'),
            }));
            const __VLS_30 = __VLS_29({
                value: (row.active ? 'Active' : 'Inactive'),
                severity: (row.active ? 'success' : 'secondary'),
            }, ...__VLS_functionalComponentArgsRest(__VLS_29));
        }
        var __VLS_27;
        const __VLS_32 = {}.Column;
        /** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
        // @ts-ignore
        const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
            header: "",
            ...{ style: ({ width: '160px' }) },
        }));
        const __VLS_34 = __VLS_33({
            header: "",
            ...{ style: ({ width: '160px' }) },
        }, ...__VLS_functionalComponentArgsRest(__VLS_33));
        __VLS_35.slots.default;
        {
            const { body: __VLS_thisSlot } = __VLS_35.slots;
            const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
            __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
                ...{ class: "row-actions" },
            });
            const __VLS_36 = {}.Button;
            /** @type {[typeof __VLS_components.Button, ]} */ ;
            // @ts-ignore
            const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
                ...{ 'onClick': {} },
                icon: "pi pi-eye",
                text: true,
                rounded: true,
                title: "View",
            }));
            const __VLS_38 = __VLS_37({
                ...{ 'onClick': {} },
                icon: "pi pi-eye",
                text: true,
                rounded: true,
                title: "View",
            }, ...__VLS_functionalComponentArgsRest(__VLS_37));
            let __VLS_40;
            let __VLS_41;
            let __VLS_42;
            const __VLS_43 = {
                onClick: (...[$event]) => {
                    if (!!(__VLS_ctx.isLoading))
                        return;
                    __VLS_ctx.openDetail(row);
                }
            };
            var __VLS_39;
            const __VLS_44 = {}.Button;
            /** @type {[typeof __VLS_components.Button, ]} */ ;
            // @ts-ignore
            const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
                ...{ 'onClick': {} },
                icon: "pi pi-pencil",
                text: true,
                rounded: true,
                title: "Edit",
            }));
            const __VLS_46 = __VLS_45({
                ...{ 'onClick': {} },
                icon: "pi pi-pencil",
                text: true,
                rounded: true,
                title: "Edit",
            }, ...__VLS_functionalComponentArgsRest(__VLS_45));
            let __VLS_48;
            let __VLS_49;
            let __VLS_50;
            const __VLS_51 = {
                onClick: (...[$event]) => {
                    if (!!(__VLS_ctx.isLoading))
                        return;
                    __VLS_ctx.openEdit(row);
                }
            };
            var __VLS_47;
            if (row.active) {
                const __VLS_52 = {}.Button;
                /** @type {[typeof __VLS_components.Button, ]} */ ;
                // @ts-ignore
                const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({
                    ...{ 'onClick': {} },
                    icon: "pi pi-ban",
                    text: true,
                    rounded: true,
                    severity: "danger",
                    title: "Deactivate",
                    loading: (__VLS_ctx.deactivateAccount.isPending.value),
                }));
                const __VLS_54 = __VLS_53({
                    ...{ 'onClick': {} },
                    icon: "pi pi-ban",
                    text: true,
                    rounded: true,
                    severity: "danger",
                    title: "Deactivate",
                    loading: (__VLS_ctx.deactivateAccount.isPending.value),
                }, ...__VLS_functionalComponentArgsRest(__VLS_53));
                let __VLS_56;
                let __VLS_57;
                let __VLS_58;
                const __VLS_59 = {
                    onClick: (...[$event]) => {
                        if (!!(__VLS_ctx.isLoading))
                            return;
                        if (!(row.active))
                            return;
                        __VLS_ctx.confirmDeactivate(row);
                    }
                };
                var __VLS_55;
            }
            else {
                const __VLS_60 = {}.Button;
                /** @type {[typeof __VLS_components.Button, ]} */ ;
                // @ts-ignore
                const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({
                    ...{ 'onClick': {} },
                    icon: "pi pi-undo",
                    text: true,
                    rounded: true,
                    severity: "success",
                    title: "Reactivate",
                    loading: (__VLS_ctx.reactivateAccount.isPending.value),
                }));
                const __VLS_62 = __VLS_61({
                    ...{ 'onClick': {} },
                    icon: "pi pi-undo",
                    text: true,
                    rounded: true,
                    severity: "success",
                    title: "Reactivate",
                    loading: (__VLS_ctx.reactivateAccount.isPending.value),
                }, ...__VLS_functionalComponentArgsRest(__VLS_61));
                let __VLS_64;
                let __VLS_65;
                let __VLS_66;
                const __VLS_67 = {
                    onClick: (...[$event]) => {
                        if (!!(__VLS_ctx.isLoading))
                            return;
                        if (!!(row.active))
                            return;
                        __VLS_ctx.reactivateAccount.mutate(row.account_id);
                    }
                };
                var __VLS_63;
            }
        }
        var __VLS_35;
        var __VLS_15;
    }
}
const __VLS_68 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_69 = __VLS_asFunctionalComponent(__VLS_68, new __VLS_68({
    visible: (__VLS_ctx.showEdit),
    header: (__VLS_ctx.editingId ? 'Edit account' : 'New account'),
    modal: true,
    ...{ style: ({ width: '440px' }) },
}));
const __VLS_70 = __VLS_69({
    visible: (__VLS_ctx.showEdit),
    header: (__VLS_ctx.editingId ? 'Edit account' : 'New account'),
    modal: true,
    ...{ style: ({ width: '440px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_69));
__VLS_71.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (...[$event]) => {
            __VLS_ctx.saveAccount.mutate();
        } },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "two-col" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_72 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({
    modelValue: (__VLS_ctx.form.code),
    maxlength: "10",
    required: true,
    autofocus: true,
}));
const __VLS_74 = __VLS_73({
    modelValue: (__VLS_ctx.form.code),
    maxlength: "10",
    required: true,
    autofocus: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_73));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_76 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_77 = __VLS_asFunctionalComponent(__VLS_76, new __VLS_76({
    modelValue: (__VLS_ctx.form.type),
    options: (__VLS_ctx.accountTypes),
    optionLabel: "label",
    optionValue: "value",
}));
const __VLS_78 = __VLS_77({
    modelValue: (__VLS_ctx.form.type),
    options: (__VLS_ctx.accountTypes),
    optionLabel: "label",
    optionValue: "value",
}, ...__VLS_functionalComponentArgsRest(__VLS_77));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_80 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_81 = __VLS_asFunctionalComponent(__VLS_80, new __VLS_80({
    modelValue: (__VLS_ctx.form.name),
    maxlength: "100",
    required: true,
}));
const __VLS_82 = __VLS_81({
    modelValue: (__VLS_ctx.form.name),
    maxlength: "100",
    required: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_81));
if (__VLS_ctx.saveAccount.error.value) {
    const __VLS_84 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_85 = __VLS_asFunctionalComponent(__VLS_84, new __VLS_84({
        severity: "error",
        closable: (false),
    }));
    const __VLS_86 = __VLS_85({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_85));
    __VLS_87.slots.default;
    (__VLS_ctx.saveAccount.error.value?.response?.data?.detail ?? 'Save failed');
    var __VLS_87;
}
{
    const { footer: __VLS_thisSlot } = __VLS_71.slots;
    const __VLS_88 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_89 = __VLS_asFunctionalComponent(__VLS_88, new __VLS_88({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_90 = __VLS_89({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_89));
    let __VLS_92;
    let __VLS_93;
    let __VLS_94;
    const __VLS_95 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showEdit = false;
        }
    };
    var __VLS_91;
    const __VLS_96 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_97 = __VLS_asFunctionalComponent(__VLS_96, new __VLS_96({
        ...{ 'onClick': {} },
        label: (__VLS_ctx.editingId ? 'Save' : 'Create'),
        disabled: (!__VLS_ctx.form.code || !__VLS_ctx.form.name || !__VLS_ctx.form.type),
        loading: (__VLS_ctx.saveAccount.isPending.value),
    }));
    const __VLS_98 = __VLS_97({
        ...{ 'onClick': {} },
        label: (__VLS_ctx.editingId ? 'Save' : 'Create'),
        disabled: (!__VLS_ctx.form.code || !__VLS_ctx.form.name || !__VLS_ctx.form.type),
        loading: (__VLS_ctx.saveAccount.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_97));
    let __VLS_100;
    let __VLS_101;
    let __VLS_102;
    const __VLS_103 = {
        onClick: (...[$event]) => {
            __VLS_ctx.saveAccount.mutate();
        }
    };
    var __VLS_99;
}
var __VLS_71;
const __VLS_104 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_105 = __VLS_asFunctionalComponent(__VLS_104, new __VLS_104({
    visible: (__VLS_ctx.showDetail),
    header: "Account detail",
    modal: true,
    ...{ style: ({ width: '420px' }) },
}));
const __VLS_106 = __VLS_105({
    visible: (__VLS_ctx.showDetail),
    header: "Account detail",
    modal: true,
    ...{ style: ({ width: '420px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_105));
__VLS_107.slots.default;
if (__VLS_ctx.detailAccount) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "detail" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dl, __VLS_intrinsicElements.dl)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({});
    (__VLS_ctx.detailAccount.code);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    (__VLS_ctx.detailAccount.name);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    const __VLS_108 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_109 = __VLS_asFunctionalComponent(__VLS_108, new __VLS_108({
        value: (__VLS_ctx.detailAccount.type),
        severity: (__VLS_ctx.typeSeverity(__VLS_ctx.detailAccount.type)),
    }));
    const __VLS_110 = __VLS_109({
        value: (__VLS_ctx.detailAccount.type),
        severity: (__VLS_ctx.typeSeverity(__VLS_ctx.detailAccount.type)),
    }, ...__VLS_functionalComponentArgsRest(__VLS_109));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    const __VLS_112 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_113 = __VLS_asFunctionalComponent(__VLS_112, new __VLS_112({
        value: (__VLS_ctx.detailAccount.active ? 'Active' : 'Inactive'),
        severity: (__VLS_ctx.detailAccount.active ? 'success' : 'secondary'),
    }));
    const __VLS_114 = __VLS_113({
        value: (__VLS_ctx.detailAccount.active ? 'Active' : 'Inactive'),
        severity: (__VLS_ctx.detailAccount.active ? 'success' : 'secondary'),
    }, ...__VLS_functionalComponentArgsRest(__VLS_113));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({
        ...{ class: "small" },
    });
    (__VLS_ctx.detailAccount.account_id);
}
{
    const { footer: __VLS_thisSlot } = __VLS_107.slots;
    const __VLS_116 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_117 = __VLS_asFunctionalComponent(__VLS_116, new __VLS_116({
        ...{ 'onClick': {} },
        label: "Close",
        text: true,
    }));
    const __VLS_118 = __VLS_117({
        ...{ 'onClick': {} },
        label: "Close",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_117));
    let __VLS_120;
    let __VLS_121;
    let __VLS_122;
    const __VLS_123 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showDetail = false;
        }
    };
    var __VLS_119;
    if (__VLS_ctx.detailAccount) {
        const __VLS_124 = {}.Button;
        /** @type {[typeof __VLS_components.Button, ]} */ ;
        // @ts-ignore
        const __VLS_125 = __VLS_asFunctionalComponent(__VLS_124, new __VLS_124({
            ...{ 'onClick': {} },
            label: "Edit",
            icon: "pi pi-pencil",
        }));
        const __VLS_126 = __VLS_125({
            ...{ 'onClick': {} },
            label: "Edit",
            icon: "pi pi-pencil",
        }, ...__VLS_functionalComponentArgsRest(__VLS_125));
        let __VLS_128;
        let __VLS_129;
        let __VLS_130;
        const __VLS_131 = {
            onClick: (() => { const r = __VLS_ctx.detailAccount; __VLS_ctx.showDetail = false; __VLS_ctx.openEdit(r); })
        };
        var __VLS_127;
    }
}
var __VLS_107;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['page-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['card']} */ ;
/** @type {__VLS_StyleScopedClasses['card-pad']} */ ;
/** @type {__VLS_StyleScopedClasses['groups']} */ ;
/** @type {__VLS_StyleScopedClasses['group']} */ ;
/** @type {__VLS_StyleScopedClasses['group-head']} */ ;
/** @type {__VLS_StyleScopedClasses['count']} */ ;
/** @type {__VLS_StyleScopedClasses['row-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['two-col']} */ ;
/** @type {__VLS_StyleScopedClasses['detail']} */ ;
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
            Dropdown: Dropdown,
            Tag: Tag,
            Message: Message,
            accountTypes: accountTypes,
            showEdit: showEdit,
            editingId: editingId,
            form: form,
            showDetail: showDetail,
            detailAccount: detailAccount,
            isLoading: isLoading,
            grouped: grouped,
            orderedTypes: orderedTypes,
            saveAccount: saveAccount,
            deactivateAccount: deactivateAccount,
            reactivateAccount: reactivateAccount,
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
