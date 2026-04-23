import { computed, ref } from "vue";
function round4(n) {
    return Math.round(n * 10000) / 10000;
}
export function emptyLineItem(position = 0) {
    return {
        item_id: null,
        description: "",
        quantity: 1,
        unit_price: null,
        position,
    };
}
export function useInvoiceForm(opts) {
    const lineItems = ref([emptyLineItem(0)]);
    function addLine() {
        lineItems.value.push(emptyLineItem(lineItems.value.length));
    }
    function removeLine(index) {
        lineItems.value.splice(index, 1);
        lineItems.value.forEach((row, i) => (row.position = i));
        if (lineItems.value.length === 0)
            addLine();
    }
    function lineAmount(row) {
        const q = Number(row.quantity ?? 0);
        const p = Number(row.unit_price ?? 0);
        if (!Number.isFinite(q) || !Number.isFinite(p))
            return 0;
        return round4(q * p);
    }
    const subtotal = computed(() => round4(lineItems.value.reduce((acc, row) => acc + lineAmount(row), 0)));
    const discountAmount = computed(() => {
        const v = Number(opts.discountValue.value ?? 0);
        if (!v || !opts.discountType.value)
            return 0;
        if (opts.discountType.value === "PERCENT")
            return round4(subtotal.value * (v / 100));
        return round4(v);
    });
    const total = computed(() => {
        const t = round4(subtotal.value - discountAmount.value);
        return t < 0 ? 0 : t;
    });
    return {
        lineItems,
        addLine,
        removeLine,
        lineAmount,
        subtotal,
        discountAmount,
        total,
    };
}
