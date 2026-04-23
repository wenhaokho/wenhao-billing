import { ref, onMounted } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import Password from "primevue/password";
import Button from "primevue/button";
import Message from "primevue/message";
import InputText from "primevue/inputtext";
import { useAuthStore } from "@/stores/auth";
const auth = useAuthStore();
const route = useRoute();
const router = useRouter();
const token = ref("");
const pw = ref("");
const confirm = ref("");
const submitting = ref(false);
const error = ref(null);
const success = ref(false);
onMounted(() => {
    token.value = route.query.token ?? "";
});
async function submit() {
    error.value = null;
    if (!token.value.trim()) {
        error.value = "Reset token is required.";
        return;
    }
    if (pw.value.length < 6) {
        error.value = "Password must be at least 6 characters.";
        return;
    }
    if (pw.value !== confirm.value) {
        error.value = "Passwords don't match.";
        return;
    }
    submitting.value = true;
    try {
        await auth.resetPassword(token.value.trim(), pw.value);
        success.value = true;
        // Short pause so the user sees the success state, then bounce to login.
        setTimeout(() => router.push("/login"), 1500);
    }
    catch (e) {
        error.value = e?.response?.data?.detail ?? "Reset failed";
    }
    finally {
        submitting.value = false;
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
/** @type {__VLS_StyleScopedClasses['footer-hint']} */ ;
/** @type {__VLS_StyleScopedClasses['footer-hint']} */ ;
// CSS variable injection 
// CSS variable injection end 
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "login-bg" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "login-card" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "brand" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "brand-mark" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "brand-name" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "brand-sub" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({
    ...{ class: "subtitle" },
});
if (!__VLS_ctx.success) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.form, __VLS_intrinsicElements.form)({
        ...{ onSubmit: (__VLS_ctx.submit) },
        ...{ class: "form" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
    const __VLS_0 = {}.InputText;
    /** @type {[typeof __VLS_components.InputText, ]} */ ;
    // @ts-ignore
    const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
        modelValue: (__VLS_ctx.token),
        readonly: (!!__VLS_ctx.route.query.token),
        required: true,
        autofocus: true,
    }));
    const __VLS_2 = __VLS_1({
        modelValue: (__VLS_ctx.token),
        readonly: (!!__VLS_ctx.route.query.token),
        required: true,
        autofocus: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_1));
    if (__VLS_ctx.route.query.token) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
    const __VLS_4 = {}.Password;
    /** @type {[typeof __VLS_components.Password, ]} */ ;
    // @ts-ignore
    const __VLS_5 = __VLS_asFunctionalComponent(__VLS_4, new __VLS_4({
        modelValue: (__VLS_ctx.pw),
        toggleMask: true,
        required: true,
        inputClass: "pw-input",
    }));
    const __VLS_6 = __VLS_5({
        modelValue: (__VLS_ctx.pw),
        toggleMask: true,
        required: true,
        inputClass: "pw-input",
    }, ...__VLS_functionalComponentArgsRest(__VLS_5));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.small, __VLS_intrinsicElements.small)({});
    __VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
    const __VLS_8 = {}.Password;
    /** @type {[typeof __VLS_components.Password, ]} */ ;
    // @ts-ignore
    const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
        modelValue: (__VLS_ctx.confirm),
        feedback: (false),
        toggleMask: true,
        required: true,
        inputClass: "pw-input",
    }));
    const __VLS_10 = __VLS_9({
        modelValue: (__VLS_ctx.confirm),
        feedback: (false),
        toggleMask: true,
        required: true,
        inputClass: "pw-input",
    }, ...__VLS_functionalComponentArgsRest(__VLS_9));
    if (__VLS_ctx.error) {
        const __VLS_12 = {}.Message;
        /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
        // @ts-ignore
        const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
            severity: "error",
            closable: (false),
        }));
        const __VLS_14 = __VLS_13({
            severity: "error",
            closable: (false),
        }, ...__VLS_functionalComponentArgsRest(__VLS_13));
        __VLS_15.slots.default;
        (__VLS_ctx.error);
        var __VLS_15;
    }
    const __VLS_16 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
        type: "submit",
        label: "Reset password",
        icon: "pi pi-check",
        loading: (__VLS_ctx.submitting),
    }));
    const __VLS_18 = __VLS_17({
        type: "submit",
        label: "Reset password",
        icon: "pi pi-check",
        loading: (__VLS_ctx.submitting),
    }, ...__VLS_functionalComponentArgsRest(__VLS_17));
}
else {
    const __VLS_20 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
        severity: "success",
        closable: (false),
    }));
    const __VLS_22 = __VLS_21({
        severity: "success",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_21));
    __VLS_23.slots.default;
    var __VLS_23;
}
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "footer-hint" },
});
const __VLS_24 = {}.RouterLink;
/** @type {[typeof __VLS_components.RouterLink, typeof __VLS_components.RouterLink, ]} */ ;
// @ts-ignore
const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
    to: "/login",
}));
const __VLS_26 = __VLS_25({
    to: "/login",
}, ...__VLS_functionalComponentArgsRest(__VLS_25));
__VLS_27.slots.default;
var __VLS_27;
/** @type {__VLS_StyleScopedClasses['login-bg']} */ ;
/** @type {__VLS_StyleScopedClasses['login-card']} */ ;
/** @type {__VLS_StyleScopedClasses['brand']} */ ;
/** @type {__VLS_StyleScopedClasses['brand-mark']} */ ;
/** @type {__VLS_StyleScopedClasses['brand-name']} */ ;
/** @type {__VLS_StyleScopedClasses['brand-sub']} */ ;
/** @type {__VLS_StyleScopedClasses['subtitle']} */ ;
/** @type {__VLS_StyleScopedClasses['form']} */ ;
/** @type {__VLS_StyleScopedClasses['footer-hint']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            RouterLink: RouterLink,
            Password: Password,
            Button: Button,
            Message: Message,
            InputText: InputText,
            route: route,
            token: token,
            pw: pw,
            confirm: confirm,
            submitting: submitting,
            error: error,
            success: success,
            submit: submit,
        };
    },
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
; /* PartiallyEnd: #4569/main.vue */
