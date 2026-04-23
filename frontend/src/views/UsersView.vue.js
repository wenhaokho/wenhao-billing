import { ref } from "vue";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Dialog from "primevue/dialog";
import InputText from "primevue/inputtext";
import Password from "primevue/password";
import Tag from "primevue/tag";
import Message from "primevue/message";
import { api } from "@/api/client";
import { useAuthStore } from "@/stores/auth";
const auth = useAuthStore();
const queryClient = useQueryClient();
const showCreate = ref(false);
const showReset = ref(false);
const target = ref(null);
const newUser = ref({ email: "", password: "", role: "admin" });
const resetPw = ref("");
const { data, isLoading } = useQuery({
    queryKey: ["users"],
    queryFn: async () => (await api.get("/users")).data,
});
const createUser = useMutation({
    mutationFn: () => api.post("/users", newUser.value),
    onSuccess: () => {
        showCreate.value = false;
        newUser.value = { email: "", password: "", role: "admin" };
        queryClient.invalidateQueries({ queryKey: ["users"] });
    },
});
const resetPassword = useMutation({
    mutationFn: () => api.post(`/users/${target.value.user_id}/reset-password`, { password: resetPw.value }),
    onSuccess: () => {
        showReset.value = false;
        target.value = null;
        resetPw.value = "";
    },
});
function openReset(u) {
    target.value = u;
    resetPw.value = "";
    showReset.value = true;
}
function fmtDate(iso) {
    return new Date(iso).toLocaleDateString();
}
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
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
    label: "Invite user",
    icon: "pi pi-plus",
}));
const __VLS_2 = __VLS_1({
    ...{ 'onClick': {} },
    label: "Invite user",
    icon: "pi pi-plus",
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
let __VLS_4;
let __VLS_5;
let __VLS_6;
const __VLS_7 = {
    onClick: (...[$event]) => {
        __VLS_ctx.showCreate = true;
    }
};
var __VLS_3;
const __VLS_8 = {}.DataTable;
/** @type {[typeof __VLS_components.DataTable, typeof __VLS_components.DataTable, ]} */ ;
// @ts-ignore
const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "user_id",
    stripedRows: true,
}));
const __VLS_10 = __VLS_9({
    value: (__VLS_ctx.data ?? []),
    loading: (__VLS_ctx.isLoading),
    dataKey: "user_id",
    stripedRows: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_9));
__VLS_11.slots.default;
{
    const { empty: __VLS_thisSlot } = __VLS_11.slots;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "empty-state" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-users" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
}
const __VLS_12 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
    field: "email",
    header: "Email",
}));
const __VLS_14 = __VLS_13({
    field: "email",
    header: "Email",
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
__VLS_15.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_15.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
    (row.email);
    if (row.email === __VLS_ctx.auth.user?.email) {
        const __VLS_16 = {}.Tag;
        /** @type {[typeof __VLS_components.Tag, ]} */ ;
        // @ts-ignore
        const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
            ...{ class: "you" },
            value: "you",
            severity: "info",
        }));
        const __VLS_18 = __VLS_17({
            ...{ class: "you" },
            value: "you",
            severity: "info",
        }, ...__VLS_functionalComponentArgsRest(__VLS_17));
    }
}
var __VLS_15;
const __VLS_20 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
    header: "Role",
}));
const __VLS_22 = __VLS_21({
    header: "Role",
}, ...__VLS_functionalComponentArgsRest(__VLS_21));
__VLS_23.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_23.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_24 = {}.Tag;
    /** @type {[typeof __VLS_components.Tag, ]} */ ;
    // @ts-ignore
    const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
        value: (row.role),
        severity: "secondary",
    }));
    const __VLS_26 = __VLS_25({
        value: (row.role),
        severity: "secondary",
    }, ...__VLS_functionalComponentArgsRest(__VLS_25));
}
var __VLS_23;
const __VLS_28 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
    header: "Created",
}));
const __VLS_30 = __VLS_29({
    header: "Created",
}, ...__VLS_functionalComponentArgsRest(__VLS_29));
__VLS_31.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_31.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    (__VLS_ctx.fmtDate(row.created_at));
}
var __VLS_31;
const __VLS_32 = {}.Column;
/** @type {[typeof __VLS_components.Column, typeof __VLS_components.Column, ]} */ ;
// @ts-ignore
const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
    header: "",
}));
const __VLS_34 = __VLS_33({
    header: "",
}, ...__VLS_functionalComponentArgsRest(__VLS_33));
__VLS_35.slots.default;
{
    const { body: __VLS_thisSlot } = __VLS_35.slots;
    const { data: row } = __VLS_getSlotParam(__VLS_thisSlot);
    const __VLS_36 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
        ...{ 'onClick': {} },
        label: "Reset password",
        icon: "pi pi-key",
        size: "small",
        text: true,
    }));
    const __VLS_38 = __VLS_37({
        ...{ 'onClick': {} },
        label: "Reset password",
        icon: "pi pi-key",
        size: "small",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_37));
    let __VLS_40;
    let __VLS_41;
    let __VLS_42;
    const __VLS_43 = {
        onClick: (...[$event]) => {
            __VLS_ctx.openReset(row);
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
    header: "Invite user",
    modal: true,
    ...{ style: ({ width: '460px' }) },
}));
const __VLS_46 = __VLS_45({
    visible: (__VLS_ctx.showCreate),
    header: "Invite user",
    modal: true,
    ...{ style: ({ width: '460px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_45));
__VLS_47.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (...[$event]) => {
            __VLS_ctx.createUser.mutate();
        } },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_48 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_49 = __VLS_asFunctionalComponent(__VLS_48, new __VLS_48({
    modelValue: (__VLS_ctx.newUser.email),
    type: "email",
    required: true,
    autofocus: true,
}));
const __VLS_50 = __VLS_49({
    modelValue: (__VLS_ctx.newUser.email),
    type: "email",
    required: true,
    autofocus: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_49));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_52 = {}.Password;
/** @type {[typeof __VLS_components.Password, ]} */ ;
// @ts-ignore
const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({
    modelValue: (__VLS_ctx.newUser.password),
    feedback: (false),
    toggleMask: true,
}));
const __VLS_54 = __VLS_53({
    modelValue: (__VLS_ctx.newUser.password),
    feedback: (false),
    toggleMask: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_53));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_56 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_57 = __VLS_asFunctionalComponent(__VLS_56, new __VLS_56({
    modelValue: (__VLS_ctx.newUser.role),
}));
const __VLS_58 = __VLS_57({
    modelValue: (__VLS_ctx.newUser.role),
}, ...__VLS_functionalComponentArgsRest(__VLS_57));
if (__VLS_ctx.createUser.error.value) {
    const __VLS_60 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({
        severity: "error",
        closable: (false),
    }));
    const __VLS_62 = __VLS_61({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_61));
    __VLS_63.slots.default;
    (__VLS_ctx.createUser.error.value?.response?.data?.detail ?? 'Create failed');
    var __VLS_63;
}
{
    const { footer: __VLS_thisSlot } = __VLS_47.slots;
    const __VLS_64 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_65 = __VLS_asFunctionalComponent(__VLS_64, new __VLS_64({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_66 = __VLS_65({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_65));
    let __VLS_68;
    let __VLS_69;
    let __VLS_70;
    const __VLS_71 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showCreate = false;
        }
    };
    var __VLS_67;
    const __VLS_72 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({
        ...{ 'onClick': {} },
        label: "Create",
        disabled: (!__VLS_ctx.newUser.email || !__VLS_ctx.newUser.password),
        loading: (__VLS_ctx.createUser.isPending.value),
    }));
    const __VLS_74 = __VLS_73({
        ...{ 'onClick': {} },
        label: "Create",
        disabled: (!__VLS_ctx.newUser.email || !__VLS_ctx.newUser.password),
        loading: (__VLS_ctx.createUser.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_73));
    let __VLS_76;
    let __VLS_77;
    let __VLS_78;
    const __VLS_79 = {
        onClick: (...[$event]) => {
            __VLS_ctx.createUser.mutate();
        }
    };
    var __VLS_75;
}
var __VLS_47;
const __VLS_80 = {}.Dialog;
/** @type {[typeof __VLS_components.Dialog, typeof __VLS_components.Dialog, ]} */ ;
// @ts-ignore
const __VLS_81 = __VLS_asFunctionalComponent(__VLS_80, new __VLS_80({
    visible: (__VLS_ctx.showReset),
    header: (`Reset password — ${__VLS_ctx.target?.email ?? ''}`),
    modal: true,
    ...{ style: ({ width: '420px' }) },
}));
const __VLS_82 = __VLS_81({
    visible: (__VLS_ctx.showReset),
    header: (`Reset password — ${__VLS_ctx.target?.email ?? ''}`),
    modal: true,
    ...{ style: ({ width: '420px' }) },
}, ...__VLS_functionalComponentArgsRest(__VLS_81));
__VLS_83.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (...[$event]) => {
            __VLS_ctx.resetPassword.mutate();
        } },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_84 = {}.Password;
/** @type {[typeof __VLS_components.Password, ]} */ ;
// @ts-ignore
const __VLS_85 = __VLS_asFunctionalComponent(__VLS_84, new __VLS_84({
    modelValue: (__VLS_ctx.resetPw),
    feedback: (false),
    toggleMask: true,
}));
const __VLS_86 = __VLS_85({
    modelValue: (__VLS_ctx.resetPw),
    feedback: (false),
    toggleMask: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_85));
if (__VLS_ctx.resetPassword.error.value) {
    const __VLS_88 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_89 = __VLS_asFunctionalComponent(__VLS_88, new __VLS_88({
        severity: "error",
        closable: (false),
    }));
    const __VLS_90 = __VLS_89({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_89));
    __VLS_91.slots.default;
    (__VLS_ctx.resetPassword.error.value?.response?.data?.detail ?? 'Reset failed');
    var __VLS_91;
}
{
    const { footer: __VLS_thisSlot } = __VLS_83.slots;
    const __VLS_92 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_93 = __VLS_asFunctionalComponent(__VLS_92, new __VLS_92({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }));
    const __VLS_94 = __VLS_93({
        ...{ 'onClick': {} },
        label: "Cancel",
        text: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_93));
    let __VLS_96;
    let __VLS_97;
    let __VLS_98;
    const __VLS_99 = {
        onClick: (...[$event]) => {
            __VLS_ctx.showReset = false;
        }
    };
    var __VLS_95;
    const __VLS_100 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_101 = __VLS_asFunctionalComponent(__VLS_100, new __VLS_100({
        ...{ 'onClick': {} },
        label: "Reset",
        severity: "warn",
        disabled: (!__VLS_ctx.resetPw),
        loading: (__VLS_ctx.resetPassword.isPending.value),
    }));
    const __VLS_102 = __VLS_101({
        ...{ 'onClick': {} },
        label: "Reset",
        severity: "warn",
        disabled: (!__VLS_ctx.resetPw),
        loading: (__VLS_ctx.resetPassword.isPending.value),
    }, ...__VLS_functionalComponentArgsRest(__VLS_101));
    let __VLS_104;
    let __VLS_105;
    let __VLS_106;
    const __VLS_107 = {
        onClick: (...[$event]) => {
            __VLS_ctx.resetPassword.mutate();
        }
    };
    var __VLS_103;
}
var __VLS_83;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['page-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['empty-state']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-users']} */ ;
/** @type {__VLS_StyleScopedClasses['you']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            DataTable: DataTable,
            Column: Column,
            Button: Button,
            Dialog: Dialog,
            InputText: InputText,
            Password: Password,
            Tag: Tag,
            Message: Message,
            auth: auth,
            showCreate: showCreate,
            showReset: showReset,
            target: target,
            newUser: newUser,
            resetPw: resetPw,
            data: data,
            isLoading: isLoading,
            createUser: createUser,
            resetPassword: resetPassword,
            openReset: openReset,
            fmtDate: fmtDate,
        };
    },
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
; /* PartiallyEnd: #4569/main.vue */
