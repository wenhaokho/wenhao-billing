import { computed } from "vue";
import Button from "primevue/button";
import Dropdown from "primevue/dropdown";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import { formatAmount } from "@/utils/money";
const props = defineProps();
const emit = defineEmits();
const rows = computed({
    get: () => props.modelValue,
    set: (v) => emit("update:modelValue", v),
});
const priceFractionDigits = computed(() => (props.currency === "IDR" ? 0 : 2));
function onItemPicked(row, value) {
    row.item_id = value;
    if (!value)
        return;
    const picked = props.catalog.find((c) => c.item_id === value);
    if (!picked)
        return;
    if (!row.description)
        row.description = picked.description ?? picked.name;
    if (row.unit_price == null && picked.default_unit_price != null) {
        row.unit_price = Number(picked.default_unit_price);
    }
}
function rowAmount(row) {
    const q = Number(row.quantity ?? 0);
    const p = Number(row.unit_price ?? 0);
    if (!Number.isFinite(q) || !Number.isFinite(p))
        return 0;
    return Math.round(q * p * 10000) / 10000;
}
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['lines-header']} */ ;
/** @type {__VLS_StyleScopedClasses['line-row']} */ ;
/** @type {__VLS_StyleScopedClasses['line-row']} */ ;
// CSS variable injection 
// CSS variable injection end 
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "lines" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "lines-header" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "col-item" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "col-qty" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "col-price" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "col-amount" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "col-action" },
});
for (const [row, idx] of __VLS_getVForSourceType((__VLS_ctx.rows))) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        key: (idx),
        ...{ class: "line-row" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "col-item" },
    });
    const __VLS_0 = {}.Dropdown;
    /** @type {[typeof __VLS_components.Dropdown, ]} */ ;
    // @ts-ignore
    const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
        ...{ 'onUpdate:modelValue': {} },
        modelValue: (row.item_id),
        options: (__VLS_ctx.catalog),
        optionLabel: "name",
        optionValue: "item_id",
        placeholder: "Pick an item (optional)",
        filter: true,
        showClear: true,
    }));
    const __VLS_2 = __VLS_1({
        ...{ 'onUpdate:modelValue': {} },
        modelValue: (row.item_id),
        options: (__VLS_ctx.catalog),
        optionLabel: "name",
        optionValue: "item_id",
        placeholder: "Pick an item (optional)",
        filter: true,
        showClear: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_1));
    let __VLS_4;
    let __VLS_5;
    let __VLS_6;
    const __VLS_7 = {
        'onUpdate:modelValue': ((v) => __VLS_ctx.onItemPicked(row, v))
    };
    var __VLS_3;
    const __VLS_8 = {}.InputText;
    /** @type {[typeof __VLS_components.InputText, ]} */ ;
    // @ts-ignore
    const __VLS_9 = __VLS_asFunctionalComponent(__VLS_8, new __VLS_8({
        modelValue: (row.description),
        placeholder: "Description",
        ...{ class: "desc" },
    }));
    const __VLS_10 = __VLS_9({
        modelValue: (row.description),
        placeholder: "Description",
        ...{ class: "desc" },
    }, ...__VLS_functionalComponentArgsRest(__VLS_9));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "col-qty" },
    });
    const __VLS_12 = {}.InputNumber;
    /** @type {[typeof __VLS_components.InputNumber, ]} */ ;
    // @ts-ignore
    const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
        modelValue: (row.quantity),
        mode: "decimal",
        min: (0),
        minFractionDigits: (0),
        maxFractionDigits: (4),
        useGrouping: (false),
    }));
    const __VLS_14 = __VLS_13({
        modelValue: (row.quantity),
        mode: "decimal",
        min: (0),
        minFractionDigits: (0),
        maxFractionDigits: (4),
        useGrouping: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_13));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "col-price" },
    });
    const __VLS_16 = {}.InputNumber;
    /** @type {[typeof __VLS_components.InputNumber, ]} */ ;
    // @ts-ignore
    const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
        modelValue: (row.unit_price),
        mode: "decimal",
        min: (0),
        minFractionDigits: (__VLS_ctx.priceFractionDigits),
        maxFractionDigits: (__VLS_ctx.priceFractionDigits),
        useGrouping: (true),
    }));
    const __VLS_18 = __VLS_17({
        modelValue: (row.unit_price),
        mode: "decimal",
        min: (0),
        minFractionDigits: (__VLS_ctx.priceFractionDigits),
        maxFractionDigits: (__VLS_ctx.priceFractionDigits),
        useGrouping: (true),
    }, ...__VLS_functionalComponentArgsRest(__VLS_17));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "col-amount num" },
    });
    (__VLS_ctx.formatAmount(__VLS_ctx.rowAmount(row), __VLS_ctx.currency));
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "col-action" },
    });
    const __VLS_20 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
        ...{ 'onClick': {} },
        icon: "pi pi-trash",
        text: true,
        rounded: true,
        severity: "danger",
        title: "Remove",
        disabled: (__VLS_ctx.rows.length <= 1),
    }));
    const __VLS_22 = __VLS_21({
        ...{ 'onClick': {} },
        icon: "pi pi-trash",
        text: true,
        rounded: true,
        severity: "danger",
        title: "Remove",
        disabled: (__VLS_ctx.rows.length <= 1),
    }, ...__VLS_functionalComponentArgsRest(__VLS_21));
    let __VLS_24;
    let __VLS_25;
    let __VLS_26;
    const __VLS_27 = {
        onClick: (...[$event]) => {
            __VLS_ctx.emit('remove', idx);
        }
    };
    var __VLS_23;
}
const __VLS_28 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
    ...{ 'onClick': {} },
    label: "Add an item",
    icon: "pi pi-plus",
    text: true,
    ...{ class: "add-btn" },
}));
const __VLS_30 = __VLS_29({
    ...{ 'onClick': {} },
    label: "Add an item",
    icon: "pi pi-plus",
    text: true,
    ...{ class: "add-btn" },
}, ...__VLS_functionalComponentArgsRest(__VLS_29));
let __VLS_32;
let __VLS_33;
let __VLS_34;
const __VLS_35 = {
    onClick: (...[$event]) => {
        __VLS_ctx.emit('add');
    }
};
var __VLS_31;
/** @type {__VLS_StyleScopedClasses['lines']} */ ;
/** @type {__VLS_StyleScopedClasses['lines-header']} */ ;
/** @type {__VLS_StyleScopedClasses['col-item']} */ ;
/** @type {__VLS_StyleScopedClasses['col-qty']} */ ;
/** @type {__VLS_StyleScopedClasses['col-price']} */ ;
/** @type {__VLS_StyleScopedClasses['col-amount']} */ ;
/** @type {__VLS_StyleScopedClasses['col-action']} */ ;
/** @type {__VLS_StyleScopedClasses['line-row']} */ ;
/** @type {__VLS_StyleScopedClasses['col-item']} */ ;
/** @type {__VLS_StyleScopedClasses['desc']} */ ;
/** @type {__VLS_StyleScopedClasses['col-qty']} */ ;
/** @type {__VLS_StyleScopedClasses['col-price']} */ ;
/** @type {__VLS_StyleScopedClasses['col-amount']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['col-action']} */ ;
/** @type {__VLS_StyleScopedClasses['add-btn']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            Button: Button,
            Dropdown: Dropdown,
            InputText: InputText,
            InputNumber: InputNumber,
            formatAmount: formatAmount,
            emit: emit,
            rows: rows,
            priceFractionDigits: priceFractionDigits,
            onItemPicked: onItemPicked,
            rowAmount: rowAmount,
        };
    },
    __typeEmits: {},
    __typeProps: {},
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
    __typeEmits: {},
    __typeProps: {},
});
; /* PartiallyEnd: #4569/main.vue */
