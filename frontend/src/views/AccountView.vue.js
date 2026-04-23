import { ref, watch, onMounted } from "vue";
import { useRoute, useRouter } from "vue-router";
import InputText from "primevue/inputtext";
import Password from "primevue/password";
import Button from "primevue/button";
import Message from "primevue/message";
import TabView from "primevue/tabview";
import TabPanel from "primevue/tabpanel";
import { useAuthStore } from "@/stores/auth";
const auth = useAuthStore();
const route = useRoute();
const router = useRouter();
// TabView uses 0-indexed activeIndex; map ?tab=<name> to/from index.
const tabOrder = ["profile", "password", "session"];
const activeIndex = ref(0);
function syncTabFromQuery() {
    const q = route.query.tab;
    const idx = tabOrder.indexOf(q);
    activeIndex.value = idx === -1 ? 0 : idx;
}
onMounted(syncTabFromQuery);
watch(() => route.query.tab, syncTabFromQuery);
function onTabChange(e) {
    activeIndex.value = e.index;
    const tab = e.index === 0 ? undefined : tabOrder[e.index];
    router.replace({ query: tab ? { tab } : {} });
}
// ----- profile form -----
const profile = ref({
    email: auth.user?.email ?? "",
    display_name: auth.user?.display_name ?? "",
});
const profileSaving = ref(false);
const profileError = ref(null);
const profileSaved = ref(false);
watch(() => auth.user, (u) => {
    if (u) {
        profile.value.email = u.email;
        profile.value.display_name = u.display_name ?? "";
    }
}, { immediate: true });
async function saveProfile() {
    profileError.value = null;
    profileSaved.value = false;
    profileSaving.value = true;
    try {
        await auth.updateProfile({
            email: profile.value.email,
            display_name: profile.value.display_name.trim() || null,
        });
        profileSaved.value = true;
    }
    catch (e) {
        profileError.value = e?.response?.data?.detail ?? "Failed to update profile";
    }
    finally {
        profileSaving.value = false;
    }
}
// ----- password form -----
const pw = ref({ current: "", next: "", confirm: "" });
const pwSaving = ref(false);
const pwError = ref(null);
const pwSaved = ref(false);
async function changePassword() {
    pwError.value = null;
    pwSaved.value = false;
    if (pw.value.next.length < 6) {
        pwError.value = "New password must be at least 6 characters.";
        return;
    }
    if (pw.value.next !== pw.value.confirm) {
        pwError.value = "New password and confirmation don't match.";
        return;
    }
    pwSaving.value = true;
    try {
        await auth.changePassword(pw.value.current, pw.value.next);
        pw.value = { current: "", next: "", confirm: "" };
        pwSaved.value = true;
    }
    catch (e) {
        pwError.value = e?.response?.data?.detail ?? "Failed to change password";
    }
    finally {
        pwSaving.value = false;
    }
}
function fmtDate(iso) {
    if (!iso)
        return "—";
    return new Date(iso).toLocaleString();
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
/** @type {__VLS_StyleScopedClasses['session-meta']} */ ;
/** @type {__VLS_StyleScopedClasses['session-meta']} */ ;
/** @type {__VLS_StyleScopedClasses['session-meta']} */ ;
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
const __VLS_0 = {}.TabView;
/** @type {[typeof __VLS_components.TabView, typeof __VLS_components.TabView, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    ...{ 'onTabChange': {} },
    activeIndex: (__VLS_ctx.activeIndex),
}));
const __VLS_2 = __VLS_1({
    ...{ 'onTabChange': {} },
    activeIndex: (__VLS_ctx.activeIndex),
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
let __VLS_4;
let __VLS_5;
let __VLS_6;
const __VLS_7 = {
    onTabChange: (__VLS_ctx.onTabChange)
};
__VLS_3.slots.default;
const __VLS_8 = {}.TabPanel;
/** @type {[typeof __VLS_components.TabPanel, typeof __VLS_components.TabPanel, ]} */ ;
// @ts-ignore
const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
    header: "Profile",
}));
const __VLS_10 = __VLS_9({
    header: "Profile",
}, ...__VLS_functionalComponentArgsRest(__VLS_9));
__VLS_11.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "panel" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "identity" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "avatar-big" },
});
((__VLS_ctx.auth.displayLabel || '??').slice(0, 2).toUpperCase());
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "identity-name" },
});
(__VLS_ctx.auth.displayLabel);
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "identity-sub" },
});
(__VLS_ctx.auth.user?.role);
(__VLS_ctx.fmtDate(__VLS_ctx.auth.user?.created_at));
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (__VLS_ctx.saveProfile) },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_12 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
    modelValue: (__VLS_ctx.profile.display_name),
    placeholder: "e.g. Wenhao Kho",
    maxlength: "255",
}));
const __VLS_14 = __VLS_13({
    modelValue: (__VLS_ctx.profile.display_name),
    placeholder: "e.g. Wenhao Kho",
    maxlength: "255",
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
__VLS_asFunctionalElement(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_16 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
    modelValue: (__VLS_ctx.profile.email),
    type: "email",
    required: true,
}));
const __VLS_18 = __VLS_17({
    modelValue: (__VLS_ctx.profile.email),
    type: "email",
    required: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_17));
__VLS_asFunctionalElement(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
if (__VLS_ctx.profileError) {
    const __VLS_20 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
        severity: "error",
        closable: (false),
    }));
    const __VLS_22 = __VLS_21({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_21));
    __VLS_23.slots.default;
    (__VLS_ctx.profileError);
    var __VLS_23;
}
if (__VLS_ctx.profileSaved) {
    const __VLS_24 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
        severity: "success",
        closable: (true),
    }));
    const __VLS_26 = __VLS_25({
        severity: "success",
        closable: (true),
    }, ...__VLS_functionalComponentArgsRest(__VLS_25));
    __VLS_27.slots.default;
    var __VLS_27;
}
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "actions" },
});
const __VLS_28 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
    type: "submit",
    label: "Save changes",
    icon: "pi pi-check",
    loading: (__VLS_ctx.profileSaving),
}));
const __VLS_30 = __VLS_29({
    type: "submit",
    label: "Save changes",
    icon: "pi pi-check",
    loading: (__VLS_ctx.profileSaving),
}, ...__VLS_functionalComponentArgsRest(__VLS_29));
var __VLS_11;
const __VLS_32 = {}.TabPanel;
/** @type {[typeof __VLS_components.TabPanel, typeof __VLS_components.TabPanel, ]} */ ;
// @ts-ignore
const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
    header: "Password",
}));
const __VLS_34 = __VLS_33({
    header: "Password",
}, ...__VLS_functionalComponentArgsRest(__VLS_33));
__VLS_35.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "panel" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
    ...{ onSubmit: (__VLS_ctx.changePassword) },
    ...{ class: "form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_36 = {}.Password;
/** @type {[typeof __VLS_components.Password, ]} */ ;
// @ts-ignore
const __VLS_37 = __VLS_asFunctionalComponent(__VLS_36, new __VLS_36({
    modelValue: (__VLS_ctx.pw.current),
    feedback: (false),
    toggleMask: true,
    required: true,
    inputClass: "pw-input",
}));
const __VLS_38 = __VLS_37({
    modelValue: (__VLS_ctx.pw.current),
    feedback: (false),
    toggleMask: true,
    required: true,
    inputClass: "pw-input",
}, ...__VLS_functionalComponentArgsRest(__VLS_37));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_40 = {}.Password;
/** @type {[typeof __VLS_components.Password, ]} */ ;
// @ts-ignore
const __VLS_41 = __VLS_asFunctionalComponent(__VLS_40, new __VLS_40({
    modelValue: (__VLS_ctx.pw.next),
    toggleMask: true,
    required: true,
    minLength: (6),
    inputClass: "pw-input",
}));
const __VLS_42 = __VLS_41({
    modelValue: (__VLS_ctx.pw.next),
    toggleMask: true,
    required: true,
    minLength: (6),
    inputClass: "pw-input",
}, ...__VLS_functionalComponentArgsRest(__VLS_41));
__VLS_asFunctionalElement(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_44 = {}.Password;
/** @type {[typeof __VLS_components.Password, ]} */ ;
// @ts-ignore
const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
    modelValue: (__VLS_ctx.pw.confirm),
    feedback: (false),
    toggleMask: true,
    required: true,
    inputClass: "pw-input",
}));
const __VLS_46 = __VLS_45({
    modelValue: (__VLS_ctx.pw.confirm),
    feedback: (false),
    toggleMask: true,
    required: true,
    inputClass: "pw-input",
}, ...__VLS_functionalComponentArgsRest(__VLS_45));
if (__VLS_ctx.pwError) {
    const __VLS_48 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_49 = __VLS_asFunctionalComponent(__VLS_48, new __VLS_48({
        severity: "error",
        closable: (false),
    }));
    const __VLS_50 = __VLS_49({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_49));
    __VLS_51.slots.default;
    (__VLS_ctx.pwError);
    var __VLS_51;
}
if (__VLS_ctx.pwSaved) {
    const __VLS_52 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({
        severity: "success",
        closable: (true),
    }));
    const __VLS_54 = __VLS_53({
        severity: "success",
        closable: (true),
    }, ...__VLS_functionalComponentArgsRest(__VLS_53));
    __VLS_55.slots.default;
    var __VLS_55;
}
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "actions" },
});
const __VLS_56 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_57 = __VLS_asFunctionalComponent(__VLS_56, new __VLS_56({
    type: "submit",
    label: "Change password",
    icon: "pi pi-key",
    loading: (__VLS_ctx.pwSaving),
}));
const __VLS_58 = __VLS_57({
    type: "submit",
    label: "Change password",
    icon: "pi pi-key",
    loading: (__VLS_ctx.pwSaving),
}, ...__VLS_functionalComponentArgsRest(__VLS_57));
var __VLS_35;
const __VLS_60 = {}.TabPanel;
/** @type {[typeof __VLS_components.TabPanel, typeof __VLS_components.TabPanel, ]} */ ;
// @ts-ignore
const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({
    header: "Session",
}));
const __VLS_62 = __VLS_61({
    header: "Session",
}, ...__VLS_functionalComponentArgsRest(__VLS_61));
__VLS_63.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "panel" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.dl, __VLS_intrinsicElements.dl)({
    ...{ class: "session-meta" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.code, __VLS_intrinsicElements.code)({});
(__VLS_ctx.auth.user?.user_id ?? '—');
__VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
(__VLS_ctx.auth.user?.email ?? '—');
__VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
(__VLS_ctx.auth.user?.role ?? '—');
__VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
(__VLS_ctx.fmtDate(__VLS_ctx.auth.user?.created_at));
__VLS_asFunctionalElement(__VLS_intrinsicElements.dt, __VLS_intrinsicElements.dt)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.dd, __VLS_intrinsicElements.dd)({});
var __VLS_63;
var __VLS_3;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['panel']} */ ;
/** @type {__VLS_StyleScopedClasses['identity']} */ ;
/** @type {__VLS_StyleScopedClasses['avatar-big']} */ ;
/** @type {__VLS_StyleScopedClasses['identity-name']} */ ;
/** @type {__VLS_StyleScopedClasses['identity-sub']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['actions']} */ ;
/** @type {__VLS_StyleScopedClasses['panel']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['actions']} */ ;
/** @type {__VLS_StyleScopedClasses['panel']} */ ;
/** @type {__VLS_StyleScopedClasses['session-meta']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            InputText: InputText,
            Password: Password,
            Button: Button,
            Message: Message,
            TabView: TabView,
            TabPanel: TabPanel,
            auth: auth,
            activeIndex: activeIndex,
            onTabChange: onTabChange,
            profile: profile,
            profileSaving: profileSaving,
            profileError: profileError,
            profileSaved: profileSaved,
            saveProfile: saveProfile,
            pw: pw,
            pwSaving: pwSaving,
            pwError: pwError,
            pwSaved: pwSaved,
            changePassword: changePassword,
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
