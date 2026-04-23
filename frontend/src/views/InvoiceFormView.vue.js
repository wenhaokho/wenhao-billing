import { ref, computed, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation } from "@tanstack/vue-query";
import Button from "primevue/button";
import Panel from "primevue/panel";
import Dropdown from "primevue/dropdown";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import DatePicker from "primevue/calendar";
import Textarea from "primevue/textarea";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import { useInvoiceForm, emptyLineItem, } from "@/composables/useInvoiceForm";
import InvoiceLineItemsTable from "@/components/InvoiceLineItemsTable.vue";
const props = defineProps();
const router = useRouter();
const currencyOptions = ["IDR", "SGD", "USD"];
const customerId = ref(null);
const invoiceNumber = ref(null);
const poSoNumber = ref("");
const issueDate = ref(new Date());
const dueDate = ref(null);
const currency = ref("IDR");
const notes = ref("");
const paymentTerms = ref("On Receipt");
const discountEnabled = ref(false);
const discountType = ref(null);
const discountValue = ref(null);
const { lineItems, addLine, removeLine, subtotal, total } = useInvoiceForm({
    currency,
    discountType,
    discountValue,
});
const isEdit = computed(() => !!props.id);
const { data: customers } = useQuery({
    queryKey: ["customers"],
    queryFn: async () => (await api.get("/customers")).data,
});
const { data: catalog } = useQuery({
    queryKey: ["items"],
    queryFn: async () => (await api.get("/items")).data,
});
const { data: existing } = useQuery({
    queryKey: ["invoice", props.id],
    enabled: isEdit,
    queryFn: async () => {
        if (!props.id)
            return null;
        return (await api.get(`/invoices/${props.id}`)).data;
    },
});
watch(existing, (inv) => {
    if (!inv)
        return;
    customerId.value = inv.customer_id;
    invoiceNumber.value = inv.invoice_number;
    poSoNumber.value = inv.po_so_number ?? "";
    issueDate.value = inv.issue_date ? new Date(inv.issue_date) : null;
    dueDate.value = inv.due_date ? new Date(inv.due_date) : null;
    currency.value = inv.currency;
    notes.value = inv.notes ?? "";
    paymentTerms.value = inv.payment_terms ?? "On Receipt";
    discountType.value = inv.discount_type ?? null;
    discountValue.value = inv.discount_value != null ? Number(inv.discount_value) : null;
    discountEnabled.value = !!inv.discount_type;
    if (inv.line_items?.length) {
        lineItems.value = inv.line_items
            .slice()
            .sort((a, b) => a.position - b.position)
            .map((ln, idx) => ({
            item_id: ln.item_id,
            description: ln.description,
            quantity: Number(ln.quantity),
            unit_price: Number(ln.unit_price),
            position: ln.position ?? idx,
        }));
    }
    else {
        lineItems.value = [emptyLineItem(0)];
    }
});
function toISODate(d) {
    if (!d)
        return null;
    const yyyy = d.getFullYear();
    const mm = String(d.getMonth() + 1).padStart(2, "0");
    const dd = String(d.getDate()).padStart(2, "0");
    return `${yyyy}-${mm}-${dd}`;
}
function buildPayload() {
    const clean = lineItems.value
        .filter((ln) => (ln.description && ln.description.trim()) || ln.item_id)
        .map((ln, idx) => ({
        item_id: ln.item_id,
        description: ln.description,
        quantity: Number(ln.quantity ?? 0),
        unit_price: Number(ln.unit_price ?? 0),
        position: idx,
    }));
    return {
        customer_id: customerId.value,
        invoice_type: "MILESTONE",
        currency: currency.value,
        invoice_number: invoiceNumber.value || null,
        po_so_number: poSoNumber.value || null,
        issue_date: toISODate(issueDate.value),
        due_date: toISODate(dueDate.value),
        payment_terms: paymentTerms.value || null,
        notes: notes.value || null,
        discount_type: discountEnabled.value ? discountType.value : null,
        discount_value: discountEnabled.value ? discountValue.value : null,
        line_items: clean,
    };
}
const save = useMutation({
    mutationFn: async () => {
        const body = buildPayload();
        if (isEdit.value) {
            return (await api.patch(`/invoices/${props.id}`, body)).data;
        }
        return (await api.post("/invoices", body)).data;
    },
    onSuccess: () => {
        router.push("/invoices");
    },
});
const canSave = computed(() => !!currency.value && lineItems.value.some((ln) => (ln.description?.trim() || ln.item_id)));
debugger; /* PartiallyEnd: #3632/scriptSetup.vue */
const __VLS_ctx = {};
let __VLS_components;
let __VLS_directives;
/** @type {__VLS_StyleScopedClasses['totals-row']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-row']} */ ;
// CSS variable injection 
// CSS variable injection end 
__VLS_asFunctionalElement(__VLS_intrinsicElements.section, __VLS_intrinsicElements.section)({
    ...{ class: "invoice-form" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.header, __VLS_intrinsicElements.header)({
    ...{ class: "page-header" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({});
__VLS_asFunctionalElement(__VLS_intrinsicElements.h1, __VLS_intrinsicElements.h1)({});
(__VLS_ctx.isEdit ? "Edit invoice" : "New invoice");
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "page-actions" },
});
const __VLS_0 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_1 = __VLS_asFunctionalComponent(__VLS_0, new __VLS_0({
    label: "Preview",
    icon: "pi pi-eye",
    text: true,
    disabled: true,
}));
const __VLS_2 = __VLS_1({
    label: "Preview",
    icon: "pi pi-eye",
    text: true,
    disabled: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_1));
const __VLS_4 = {}.Button;
/** @type {[typeof __VLS_components.Button, ]} */ ;
// @ts-ignore
const __VLS_5 = __VLS_asFunctionalComponent(__VLS_4, new __VLS_4({
    ...{ 'onClick': {} },
    label: (__VLS_ctx.isEdit ? 'Save changes' : 'Save as DRAFT'),
    icon: "pi pi-save",
    loading: (__VLS_ctx.save.isPending.value),
    disabled: (!__VLS_ctx.canSave),
}));
const __VLS_6 = __VLS_5({
    ...{ 'onClick': {} },
    label: (__VLS_ctx.isEdit ? 'Save changes' : 'Save as DRAFT'),
    icon: "pi pi-save",
    loading: (__VLS_ctx.save.isPending.value),
    disabled: (!__VLS_ctx.canSave),
}, ...__VLS_functionalComponentArgsRest(__VLS_5));
let __VLS_8;
let __VLS_9;
let __VLS_10;
const __VLS_11 = {
    onClick: (...[$event]) => {
        __VLS_ctx.save.mutate();
    }
};
var __VLS_7;
const __VLS_12 = {}.Panel;
/** @type {[typeof __VLS_components.Panel, typeof __VLS_components.Panel, ]} */ ;
// @ts-ignore
const __VLS_13 = __VLS_asFunctionalComponent(__VLS_12, new __VLS_12({
    header: "Business address and contact details, title, summary, and logo",
    toggleable: true,
    collapsed: true,
}));
const __VLS_14 = __VLS_13({
    header: "Business address and contact details, title, summary, and logo",
    toggleable: true,
    collapsed: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_13));
__VLS_15.slots.default;
__VLS_asFunctionalElement(__VLS_intrinsicElements.p, __VLS_intrinsicElements.p)({
    ...{ class: "muted small" },
});
var __VLS_15;
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "top-grid" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "col-left" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_16 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_17 = __VLS_asFunctionalComponent(__VLS_16, new __VLS_16({
    modelValue: (__VLS_ctx.customerId),
    options: (__VLS_ctx.customers ?? []),
    optionLabel: "name",
    optionValue: "customer_id",
    placeholder: "Choose a customer",
    filter: true,
    showClear: true,
}));
const __VLS_18 = __VLS_17({
    modelValue: (__VLS_ctx.customerId),
    options: (__VLS_ctx.customers ?? []),
    optionLabel: "name",
    optionValue: "customer_id",
    placeholder: "Choose a customer",
    filter: true,
    showClear: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_17));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "col-right" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_20 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_21 = __VLS_asFunctionalComponent(__VLS_20, new __VLS_20({
    modelValue: (__VLS_ctx.invoiceNumber),
    placeholder: (`Auto-generated (WH${new Date().getFullYear()}####)`),
}));
const __VLS_22 = __VLS_21({
    modelValue: (__VLS_ctx.invoiceNumber),
    placeholder: (`Auto-generated (WH${new Date().getFullYear()}####)`),
}, ...__VLS_functionalComponentArgsRest(__VLS_21));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_24 = {}.InputText;
/** @type {[typeof __VLS_components.InputText, ]} */ ;
// @ts-ignore
const __VLS_25 = __VLS_asFunctionalComponent(__VLS_24, new __VLS_24({
    modelValue: (__VLS_ctx.poSoNumber),
}));
const __VLS_26 = __VLS_25({
    modelValue: (__VLS_ctx.poSoNumber),
}, ...__VLS_functionalComponentArgsRest(__VLS_25));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_28 = {}.DatePicker;
/** @type {[typeof __VLS_components.DatePicker, ]} */ ;
// @ts-ignore
const __VLS_29 = __VLS_asFunctionalComponent(__VLS_28, new __VLS_28({
    modelValue: (__VLS_ctx.issueDate),
    dateFormat: "yy-mm-dd",
    showIcon: true,
}));
const __VLS_30 = __VLS_29({
    modelValue: (__VLS_ctx.issueDate),
    dateFormat: "yy-mm-dd",
    showIcon: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_29));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({});
const __VLS_32 = {}.DatePicker;
/** @type {[typeof __VLS_components.DatePicker, ]} */ ;
// @ts-ignore
const __VLS_33 = __VLS_asFunctionalComponent(__VLS_32, new __VLS_32({
    modelValue: (__VLS_ctx.dueDate),
    dateFormat: "yy-mm-dd",
    showIcon: true,
}));
const __VLS_34 = __VLS_33({
    modelValue: (__VLS_ctx.dueDate),
    dateFormat: "yy-mm-dd",
    showIcon: true,
}, ...__VLS_functionalComponentArgsRest(__VLS_33));
__VLS_asFunctionalElement(__VLS_intrinsicElements.span, __VLS_intrinsicElements.span)({
    ...{ class: "helper" },
});
/** @type {[typeof InvoiceLineItemsTable, ]} */ ;
// @ts-ignore
const __VLS_36 = __VLS_asFunctionalComponent(InvoiceLineItemsTable, new InvoiceLineItemsTable({
    ...{ 'onAdd': {} },
    ...{ 'onRemove': {} },
    modelValue: (__VLS_ctx.lineItems),
    catalog: (__VLS_ctx.catalog ?? []),
    currency: (__VLS_ctx.currency),
}));
const __VLS_37 = __VLS_36({
    ...{ 'onAdd': {} },
    ...{ 'onRemove': {} },
    modelValue: (__VLS_ctx.lineItems),
    catalog: (__VLS_ctx.catalog ?? []),
    currency: (__VLS_ctx.currency),
}, ...__VLS_functionalComponentArgsRest(__VLS_36));
let __VLS_39;
let __VLS_40;
let __VLS_41;
const __VLS_42 = {
    onAdd: (__VLS_ctx.addLine)
};
const __VLS_43 = {
    onRemove: ((i) => __VLS_ctx.removeLine(i))
};
var __VLS_38;
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals-row" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals-label" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals-value num" },
});
(__VLS_ctx.formatAmount(__VLS_ctx.subtotal, __VLS_ctx.currency));
if (!__VLS_ctx.discountEnabled) {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "totals-row" },
    });
    const __VLS_44 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_45 = __VLS_asFunctionalComponent(__VLS_44, new __VLS_44({
        ...{ 'onClick': {} },
        label: "Add a discount",
        text: true,
        icon: "pi pi-percentage",
    }));
    const __VLS_46 = __VLS_45({
        ...{ 'onClick': {} },
        label: "Add a discount",
        text: true,
        icon: "pi pi-percentage",
    }, ...__VLS_functionalComponentArgsRest(__VLS_45));
    let __VLS_48;
    let __VLS_49;
    let __VLS_50;
    const __VLS_51 = {
        onClick: (...[$event]) => {
            if (!(!__VLS_ctx.discountEnabled))
                return;
            __VLS_ctx.discountEnabled = true;
            __VLS_ctx.discountType = 'PERCENT';
        }
    };
    var __VLS_47;
}
else {
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "totals-row discount" },
    });
    __VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
        ...{ class: "discount-input" },
    });
    const __VLS_52 = {}.Dropdown;
    /** @type {[typeof __VLS_components.Dropdown, ]} */ ;
    // @ts-ignore
    const __VLS_53 = __VLS_asFunctionalComponent(__VLS_52, new __VLS_52({
        modelValue: (__VLS_ctx.discountType),
        options: ([{ label: '%', value: 'PERCENT' }, { label: 'Amount', value: 'AMOUNT' }]),
        optionLabel: "label",
        optionValue: "value",
    }));
    const __VLS_54 = __VLS_53({
        modelValue: (__VLS_ctx.discountType),
        options: ([{ label: '%', value: 'PERCENT' }, { label: 'Amount', value: 'AMOUNT' }]),
        optionLabel: "label",
        optionValue: "value",
    }, ...__VLS_functionalComponentArgsRest(__VLS_53));
    const __VLS_56 = {}.InputNumber;
    /** @type {[typeof __VLS_components.InputNumber, ]} */ ;
    // @ts-ignore
    const __VLS_57 = __VLS_asFunctionalComponent(__VLS_56, new __VLS_56({
        modelValue: (__VLS_ctx.discountValue),
        mode: "decimal",
        min: (0),
        maxFractionDigits: (4),
    }));
    const __VLS_58 = __VLS_57({
        modelValue: (__VLS_ctx.discountValue),
        mode: "decimal",
        min: (0),
        maxFractionDigits: (4),
    }, ...__VLS_functionalComponentArgsRest(__VLS_57));
    const __VLS_60 = {}.Button;
    /** @type {[typeof __VLS_components.Button, ]} */ ;
    // @ts-ignore
    const __VLS_61 = __VLS_asFunctionalComponent(__VLS_60, new __VLS_60({
        ...{ 'onClick': {} },
        icon: "pi pi-times",
        text: true,
        rounded: true,
    }));
    const __VLS_62 = __VLS_61({
        ...{ 'onClick': {} },
        icon: "pi pi-times",
        text: true,
        rounded: true,
    }, ...__VLS_functionalComponentArgsRest(__VLS_61));
    let __VLS_64;
    let __VLS_65;
    let __VLS_66;
    const __VLS_67 = {
        onClick: (...[$event]) => {
            if (!!(!__VLS_ctx.discountEnabled))
                return;
            __VLS_ctx.discountEnabled = false;
            __VLS_ctx.discountType = null;
            __VLS_ctx.discountValue = null;
        }
    };
    var __VLS_63;
}
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals-row total" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals-label" },
});
const __VLS_68 = {}.Dropdown;
/** @type {[typeof __VLS_components.Dropdown, ]} */ ;
// @ts-ignore
const __VLS_69 = __VLS_asFunctionalComponent(__VLS_68, new __VLS_68({
    modelValue: (__VLS_ctx.currency),
    options: (__VLS_ctx.currencyOptions),
    ...{ class: "ccy-select" },
}));
const __VLS_70 = __VLS_69({
    modelValue: (__VLS_ctx.currency),
    options: (__VLS_ctx.currencyOptions),
    ...{ class: "ccy-select" },
}, ...__VLS_functionalComponentArgsRest(__VLS_69));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals-value num" },
});
(__VLS_ctx.formatAmount(__VLS_ctx.total, __VLS_ctx.currency));
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals-row due" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals-label" },
});
__VLS_asFunctionalElement(__VLS_intrinsicElements.div, __VLS_intrinsicElements.div)({
    ...{ class: "totals-value num" },
});
(__VLS_ctx.formatAmount(__VLS_ctx.total, __VLS_ctx.currency));
__VLS_asFunctionalElement(__VLS_intrinsicElements.label, __VLS_intrinsicElements.label)({
    ...{ class: "notes" },
});
const __VLS_72 = {}.Textarea;
/** @type {[typeof __VLS_components.Textarea, ]} */ ;
// @ts-ignore
const __VLS_73 = __VLS_asFunctionalComponent(__VLS_72, new __VLS_72({
    modelValue: (__VLS_ctx.notes),
    rows: "5",
    placeholder: "e.g. Price is included of 11% VAT. Price is excluded of any bank or transfer fees.",
}));
const __VLS_74 = __VLS_73({
    modelValue: (__VLS_ctx.notes),
    rows: "5",
    placeholder: "e.g. Price is included of 11% VAT. Price is excluded of any bank or transfer fees.",
}, ...__VLS_functionalComponentArgsRest(__VLS_73));
if (__VLS_ctx.save.error.value) {
    const __VLS_76 = {}.Message;
    /** @type {[typeof __VLS_components.Message, typeof __VLS_components.Message, ]} */ ;
    // @ts-ignore
    const __VLS_77 = __VLS_asFunctionalComponent(__VLS_76, new __VLS_76({
        severity: "error",
        closable: (false),
    }));
    const __VLS_78 = __VLS_77({
        severity: "error",
        closable: (false),
    }, ...__VLS_functionalComponentArgsRest(__VLS_77));
    __VLS_79.slots.default;
    (__VLS_ctx.save.error.value?.response?.data?.detail ?? 'Save failed');
    var __VLS_79;
}
/** @type {__VLS_StyleScopedClasses['invoice-form']} */ ;
/** @type {__VLS_StyleScopedClasses['page-header']} */ ;
/** @type {__VLS_StyleScopedClasses['page-actions']} */ ;
/** @type {__VLS_StyleScopedClasses['muted']} */ ;
/** @type {__VLS_StyleScopedClasses['small']} */ ;
/** @type {__VLS_StyleScopedClasses['top-grid']} */ ;
/** @type {__VLS_StyleScopedClasses['col-left']} */ ;
/** @type {__VLS_StyleScopedClasses['col-right']} */ ;
/** @type {__VLS_StyleScopedClasses['helper']} */ ;
/** @type {__VLS_StyleScopedClasses['totals']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-row']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-label']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-value']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-row']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-row']} */ ;
/** @type {__VLS_StyleScopedClasses['discount']} */ ;
/** @type {__VLS_StyleScopedClasses['discount-input']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-row']} */ ;
/** @type {__VLS_StyleScopedClasses['total']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-label']} */ ;
/** @type {__VLS_StyleScopedClasses['ccy-select']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-value']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-row']} */ ;
/** @type {__VLS_StyleScopedClasses['due']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-label']} */ ;
/** @type {__VLS_StyleScopedClasses['totals-value']} */ ;
/** @type {__VLS_StyleScopedClasses['num']} */ ;
/** @type {__VLS_StyleScopedClasses['notes']} */ ;
var __VLS_dollars;
const __VLS_self = (await import('vue')).defineComponent({
    setup() {
        return {
            Button: Button,
            Panel: Panel,
            Dropdown: Dropdown,
            InputText: InputText,
            InputNumber: InputNumber,
            DatePicker: DatePicker,
            Textarea: Textarea,
            Message: Message,
            formatAmount: formatAmount,
            InvoiceLineItemsTable: InvoiceLineItemsTable,
            currencyOptions: currencyOptions,
            customerId: customerId,
            invoiceNumber: invoiceNumber,
            poSoNumber: poSoNumber,
            issueDate: issueDate,
            dueDate: dueDate,
            currency: currency,
            notes: notes,
            discountEnabled: discountEnabled,
            discountType: discountType,
            discountValue: discountValue,
            lineItems: lineItems,
            addLine: addLine,
            removeLine: removeLine,
            subtotal: subtotal,
            total: total,
            isEdit: isEdit,
            customers: customers,
            catalog: catalog,
            save: save,
            canSave: canSave,
        };
    },
    __typeProps: {},
});
export default (await import('vue')).defineComponent({
    setup() {
        return {};
    },
    __typeProps: {},
});
; /* PartiallyEnd: #4569/main.vue */
