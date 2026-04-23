import { RouterLink, RouterView, useRouter, useRoute } from "vue-router";
import { computed, onMounted, ref } from "vue";
import Avatar from "primevue/avatar";
import Menu from "primevue/menu";
import { useAuthStore } from "@/stores/auth";
const auth = useAuthStore();
const router = useRouter();
const route = useRoute();
onMounted(() => auth.fetchMe());
const userMenu = ref(null);
const userMenuItems = computed(() => [
    {
        label: auth.displayLabel,
        items: [
            {
                label: "My account",
                icon: "pi pi-id-card",
                command: () => router.push("/account"),
            },
            {
                label: "Change password",
                icon: "pi pi-key",
                command: () => router.push("/account?tab=password"),
            },
            { separator: true },
            {
                label: "Sign out",
                icon: "pi pi-sign-out",
                command: () => logout(),
            },
        ],
    },
]);
function toggleUserMenu(event) {
    userMenu.value?.toggle(event);
}
async function logout() {
    await auth.logout();
    router.push("/login");
}
const navGroups = [
    {
        label: "Overview",
        items: [{ to: "/dashboard", icon: "pi pi-home", label: "Dashboard" }],
    },
    {
        label: "Master Data",
        items: [
            { to: "/customers", icon: "pi pi-users", label: "Customers" },
            { to: "/vendors", icon: "pi pi-briefcase", label: "Vendors" },
            { to: "/items", icon: "pi pi-tags", label: "Items & Services" },
            { to: "/chart-of-accounts", icon: "pi pi-book", label: "Chart of Accounts" },
        ],
    },
    {
        label: "Sales",
        items: [{ to: "/invoices", icon: "pi pi-file", label: "Invoices" }],
    },
    {
        label: "Operations",
        items: [
            { to: "/queue", icon: "pi pi-inbox", label: "Awaiting Finalization" },
            { to: "/review", icon: "pi pi-exclamation-triangle", label: "Manual Review" },
        ],
    },
    {
        label: "Accounting",
        items: [
            { to: "/reports", icon: "pi pi-chart-bar", label: "Reports" },
            { to: "/fx-rates", icon: "pi pi-dollar", label: "FX Rates" },
        ],
    },
    {
        label: "Admin",
        items: [{ to: "/users", icon: "pi pi-user", label: "Users" }],
    },
];
const pageTitle = computed(() => {
    const map = {
        dashboard: "Dashboard",
        customers: "Customers",
        "customer-detail": "Customer",
        vendors: "Vendors",
        items: "Items & Services",
        "chart-of-accounts": "Chart of Accounts",
        invoices: "Invoices",
        queue: "Awaiting Finalization",
        review: "Manual Review",
        "review-one": "Manual Review",
        users: "Users",
        reports: "Reports",
        "fx-rates": "FX Rates",
        account: "My Account",
    };
    return map[route.name ?? ""] ?? "";
});
const userInitials = computed(() => {
    const label = auth.displayLabel;
    if (label.includes("@"))
        return label.slice(0, 2).toUpperCase();
    // display_name: use initials of the first two words
    const parts = label.trim().split(/\s+/).filter(Boolean);
    if (parts.length >= 2)
        return (parts[0][0] + parts[1][0]).toUpperCase();
    return label.slice(0, 2).toUpperCase();
});
const isAuthed = computed(() => !!auth.user);
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['nav-item']} */ ;
/** @type {__VLS_StyleScopedClasses['nav-item']} */ ;
/** @type {__VLS_StyleScopedClasses['nav-item']} */ ;
/** @type {__VLS_StyleScopedClasses['nav-item']} */ ;
/** @type {__VLS_StyleScopedClasses['router-link-active']} */ ;
/** @type {__VLS_StyleScopedClasses['env-pill']} */ ;
/** @type {__VLS_StyleScopedClasses['user-chip']} */ ;
/** @type {__VLS_StyleScopedClasses['user-chip']} */ ;
/** @type {__VLS_StyleScopedClasses['user-chip']} */ ;
/** @type {__VLS_StyleScopedClasses['shell']} */ ;
/** @type {__VLS_StyleScopedClasses['sidebar']} */ ;
/** @type {__VLS_StyleScopedClasses['nav-group-label']} */ ;
/** @type {__VLS_StyleScopedClasses['sidebar-footer']} */ ;
/** @type {__VLS_StyleScopedClasses['nav-item']} */ ;
/** @type {__VLS_StyleScopedClasses['nav-item']} */ ;
/** @type {__VLS_StyleScopedClasses['user-meta']} */ ;
// CSS variable injection 
// CSS variable injection end 
if (__VLS_ctx.isAuthed) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "shell" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.aside, __VLS_intrinsicElements.aside)({
        ...{ class: "sidebar" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "brand" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "brand-mark" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "brand-text" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "brand-name" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "brand-sub" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.nav, __VLS_intrinsicElements.nav)({
        ...{ class: "nav" },
    });
    for (const [group] of __VLS_getVForSourceType((__VLS_ctx.navGroups))) {
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            key: (group.label),
            ...{ class: "nav-group" },
        });
        __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
            ...{ class: "nav-group-label" },
        });
        (group.label);
        for (const [item] of __VLS_getVForSourceType((group.items))) {
            const __VLS_0 = {}.RouterLink;
            /** @type {[typeof __VLS_components.RouterLink, typeof __VLS_components.RouterLink, ]} */ ;
            // @ts-ignore
            const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
                key: (item.to),
                to: (item.to),
                ...{ class: "nav-item" },
            }));
            const __VLS_2 = __VLS_1({
                key: (item.to),
                to: (item.to),
                ...{ class: "nav-item" },
            }, ...__VLS_functionalComponentArgsRest(__VLS_1));
            __VLS_3.slots.default;
            __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
                ...{ class: (item.icon) },
            });
            __VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({});
            (item.label);
            var __VLS_3;
        }
    }
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "sidebar-footer" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "env-pill" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.span)({
        ...{ class: "dot" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "main-col" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({
        ...{ class: "topbar" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "topbar-left" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.h2, __VLS_intrinsicElements.h2)({
        ...{ class: "topbar-title" },
    });
    (__VLS_ctx.pageTitle);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "topbar-right" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.button, __VLS_intrinsicElements.button)({
        ...{ onClick: (__VLS_ctx.toggleUserMenu) },
        type: "button",
        ...{ class: "user-chip" },
        'aria-haspopup': "true",
        'aria-label': "Open account menu",
    });
    const __VLS_4 = {}.Avatar;
    /** @type {[typeof __VLS_components.Avatar, ]} */ ;
    // @ts-ignore
    const __VLS_5 = __VLS_asFunctionalComponent(__VLS_4, new __VLS_4({
        label: (__VLS_ctx.userInitials),
        shape: "circle",
        ...{ class: "avatar" },
    }));
    const __VLS_6 = __VLS_5({
        label: (__VLS_ctx.userInitials),
        shape: "circle",
        ...{ class: "avatar" },
    }, ...__VLS_functionalComponentArgsRest(__VLS_5));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "user-meta" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "user-email" },
    });
    (__VLS_ctx.auth.displayLabel);
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "user-role" },
    });
    (__VLS_ctx.auth.user?.role ?? 'admin');
    __VLS_asFunctionalElement(__VLS_intrinsicElements.i)({
        ...{ class: "pi pi-chevron-down chevron" },
    });
    const __VLS_8 = {}.Menu;
    /** @type {[typeof __VLS_components.Menu, ]} */ ;
    // @ts-ignore
    const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
        ref: "userMenu",
        model: (__VLS_ctx.userMenuItems),
        popup: (true),
    }));
    const __VLS_10 = __VLS_9({
        ref: "userMenu",
        model: (__VLS_ctx.userMenuItems),
        popup: (true),
    }, ...__VLS_functionalComponentArgsRest(__VLS_9));
    /** @type {typeof __VLS_ctx.userMenu} */ ;
    var __VLS_12 = {};
    var __VLS_11;
    __VLS_asFunctionalElement(__VLS_intrinsicElements.main, __VLS_intrinsicElements.main)({
        ...{ class: "page" },
    });
    const __VLS_14 = {}.RouterView;
    /** @type {[typeof __VLS_components.RouterView, ]} */ ;
    // @ts-ignore
    const __VLS_15 = __VLS_asFunctionalComponent(__VLS_14, new __VLS_14({}));
    const __VLS_16 = __VLS_15({}, ...__VLS_functionalComponentArgsRest(__VLS_15));
}
else {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "anon-shell" },
    });
    const __VLS_18 = {}.RouterView;
    /** @type {[typeof __VLS_components.RouterView, ]} */ ;
    // @ts-ignore
    const __VLS_19 = __VLS_asFunctionalComponent(__VLS_18, new __VLS_18({}));
    const __VLS_20 = __VLS_19({}, ...__VLS_functionalComponentArgsRest(__VLS_19));
}
/** @type {__VLS_StyleScopedClasses['shell']} */ ;
/** @type {__VLS_StyleScopedClasses['sidebar']} */ ;
/** @type {__VLS_StyleScopedClasses['brand']} */ ;
/** @type {__VLS_StyleScopedClasses['brand-mark']} */ ;
/** @type {__VLS_StyleScopedClasses['brand-text']} */ ;
/** @type {__VLS_StyleScopedClasses['brand-name']} */ ;
/** @type {__VLS_StyleScopedClasses['brand-sub']} */ ;
/** @type {__VLS_StyleScopedClasses['nav']} */ ;
/** @type {__VLS_StyleScopedClasses['nav-group']} */ ;
/** @type {__VLS_StyleScopedClasses['nav-group-label']} */ ;
/** @type {__VLS_StyleScopedClasses['nav-item']} */ ;
/** @type {__VLS_StyleScopedClasses['sidebar-footer']} */ ;
/** @type {__VLS_StyleScopedClasses['env-pill']} */ ;
/** @type {__VLS_StyleScopedClasses['dot']} */ ;
/** @type {__VLS_StyleScopedClasses['main-col']} */ ;
/** @type {__VLS_StyleScopedClasses['topbar']} */ ;
/** @type {__VLS_StyleScopedClasses['topbar-left']} */ ;
/** @type {__VLS_StyleScopedClasses['topbar-title']} */ ;
/** @type {__VLS_StyleScopedClasses['topbar-right']} */ ;
/** @type {__VLS_StyleScopedClasses['user-chip']} */ ;
/** @type {__VLS_StyleScopedClasses['avatar']} */ ;
/** @type {__VLS_StyleScopedClasses['user-meta']} */ ;
/** @type {__VLS_StyleScopedClasses['user-email']} */ ;
/** @type {__VLS_StyleScopedClasses['user-role']} */ ;
/** @type {__VLS_StyleScopedClasses['pi']} */ ;
/** @type {__VLS_StyleScopedClasses['pi-chevron-down']} */ ;
/** @type {__VLS_StyleScopedClasses['chevron']} */ ;
/** @type {__VLS_StyleScopedClasses['page']} */ ;
/** @type {__VLS_StyleScopedClasses['anon-shell']} */ ;
// @ts-ignore
var __VLS_13 = __VLS_12;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            RouterLink: RouterLink,
            RouterView: RouterView,
            Avatar: Avatar,
            Menu: Menu,
            auth: auth,
            userMenu: userMenu,
            userMenuItems: userMenuItems,
            toggleUserMenu: toggleUserMenu,
            navGroups: navGroups,
            pageTitle: pageTitle,
            userInitials: userInitials,
            isAuthed: isAuthed,
        };
    },
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
});
; /* PartiallyEnd: #4569/main.vue */
