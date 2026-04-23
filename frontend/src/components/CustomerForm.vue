<script setup lang="ts">
import { reactive, watch, ref, computed } from "vue";
import InputText from "primevue/inputtext";
import Textarea from "primevue/textarea";
import Dropdown from "primevue/dropdown";
import Button from "primevue/button";
import Chip from "primevue/chip";
import Checkbox from "primevue/checkbox";
import type { CustomerFormData } from "./customerForm";

const props = defineProps<{
  modelValue: CustomerFormData;
  showAliases?: boolean;
}>();

const emit = defineEmits<{
  (e: "update:modelValue", v: CustomerFormData): void;
}>();

const local = reactive<CustomerFormData>({ ...props.modelValue });

watch(
  () => props.modelValue,
  (v) => {
    Object.assign(local, v);
  },
  { deep: true },
);

watch(
  local,
  (v) => {
    emit("update:modelValue", { ...v });
  },
  { deep: true },
);

const currencyOptions = [
  { label: "IDR — Indonesian Rupiah", value: "IDR" },
  { label: "SGD — Singapore Dollar", value: "SGD" },
  { label: "USD — US Dollar", value: "USD" },
];

const aliasDraft = ref("");

function addAlias() {
  const v = aliasDraft.value.trim();
  if (!v) return;
  local.matching_aliases = [...local.matching_aliases, v];
  aliasDraft.value = "";
}

function removeAlias(i: number) {
  local.matching_aliases = local.matching_aliases.filter((_, idx) => idx !== i);
}

function clearBilling() {
  local.billing_address1 = "";
  local.billing_address2 = "";
  local.billing_country = "";
  local.billing_state = "";
  local.billing_city = "";
  local.billing_postal_code = "";
}

function clearShipping() {
  local.shipping_address1 = "";
  local.shipping_address2 = "";
  local.shipping_country = "";
  local.shipping_state = "";
  local.shipping_city = "";
  local.shipping_postal_code = "";
}

function shippingMatchesBilling(): boolean {
  return (
    local.shipping_address1 === local.billing_address1 &&
    local.shipping_address2 === local.billing_address2 &&
    local.shipping_country === local.billing_country &&
    local.shipping_state === local.billing_state &&
    local.shipping_city === local.billing_city &&
    local.shipping_postal_code === local.billing_postal_code
  );
}

function copyBillingToShipping() {
  local.shipping_address1 = local.billing_address1;
  local.shipping_address2 = local.billing_address2;
  local.shipping_country = local.billing_country;
  local.shipping_state = local.billing_state;
  local.shipping_city = local.billing_city;
  local.shipping_postal_code = local.billing_postal_code;
}

const sameAsBilling = ref(false);

// Secondary phone: shown only if the user asks for it (Wave pattern) or if
// the incoming record already has a value in it.
const showSecondPhone = ref(false);
watch(
  () => props.modelValue.contact_phone_2,
  (v) => {
    if (v) showSecondPhone.value = true;
  },
  { immediate: true },
);
function addSecondPhone() {
  showSecondPhone.value = true;
}
function removeSecondPhone() {
  local.contact_phone_2 = "";
  showSecondPhone.value = false;
}

// Initialise the checkbox from incoming data: if all shipping fields already
// mirror the billing fields (and at least one is non-empty), check the box.
watch(
  () => props.modelValue,
  () => {
    const anyBilling =
      (local.billing_address1 || local.billing_address2 || local.billing_city ||
        local.billing_state || local.billing_country || local.billing_postal_code) !== "";
    if (anyBilling && shippingMatchesBilling()) sameAsBilling.value = true;
  },
  { immediate: true },
);

// Keep shipping synced with billing while the checkbox is on.
watch(
  () => [
    local.billing_address1,
    local.billing_address2,
    local.billing_country,
    local.billing_state,
    local.billing_city,
    local.billing_postal_code,
  ],
  () => {
    if (sameAsBilling.value) copyBillingToShipping();
  },
);

function onToggleSameAsBilling(v: boolean) {
  if (v) copyBillingToShipping();
}
</script>

<template>
  <div class="customer-form">
    <!-- Basic information -->
    <section class="card">
      <header class="card-head">
        <h3>Basic information</h3>
      </header>

      <label class="field">
        <span class="field-label">Customer</span>
        <InputText v-model="local.name" placeholder="Acme Pte. Ltd." />
        <small class="help">Name of a business or person.</small>
      </label>

      <div class="field-label primary-contact-label">Primary contact</div>
      <div class="two-col">
        <label class="field">
          <span class="field-label muted">First name</span>
          <InputText v-model="local.contact_first_name" />
        </label>
        <label class="field">
          <span class="field-label muted">Last name</span>
          <InputText v-model="local.contact_last_name" />
        </label>
      </div>

      <label class="field">
        <span class="field-label">Email</span>
        <InputText v-model="local.contact_email" type="email" />
      </label>

      <label class="field">
        <span class="field-label">Phone</span>
        <InputText v-model="local.contact_phone" />
        <Button
          v-if="!showSecondPhone"
          label="Add phone"
          icon="pi pi-plus"
          text
          size="small"
          class="inline-add-btn"
          @click="addSecondPhone"
        />
      </label>

      <label v-if="showSecondPhone" class="field">
        <span class="field-label-row">
          <span class="field-label">Phone 2</span>
          <Button
            icon="pi pi-times"
            text
            rounded
            size="small"
            title="Remove second phone"
            @click="removeSecondPhone"
          />
        </span>
        <InputText v-model="local.contact_phone_2" />
      </label>

      <label class="field">
        <span class="field-label">Account number</span>
        <InputText v-model="local.account_number" />
      </label>

      <label class="field">
        <span class="field-label">Website</span>
        <InputText v-model="local.website" placeholder="https://" />
      </label>

      <label class="field">
        <span class="field-label">Notes</span>
        <Textarea v-model="local.notes" rows="4" autoResize />
      </label>

      <label v-if="showAliases !== false" class="field">
        <span class="field-label">Matching aliases</span>
        <InputText
          v-model="aliasDraft"
          placeholder="Press Enter to add. e.g. Acme Corp, ACME"
          @keydown.enter.prevent="addAlias"
        />
        <small class="help">
          Alternate names the AI matcher will recognise on incoming payments.
        </small>
        <div v-if="local.matching_aliases.length" class="chips">
          <Chip
            v-for="(a, i) in local.matching_aliases"
            :key="a"
            :label="a"
            removable
            @remove="removeAlias(i)"
          />
        </div>
      </label>
    </section>

    <!-- Billing -->
    <section class="card">
      <header class="card-head">
        <h3>Billing</h3>
      </header>

      <label class="field">
        <span class="field-label">Currency</span>
        <Dropdown
          v-model="local.default_currency"
          :options="currencyOptions"
          option-label="label"
          option-value="value"
        />
        <small class="help">
          Invoices for this customer will default to this currency.
        </small>
      </label>

      <div class="address-head">
        <span class="field-label">Billing address</span>
        <Button
          label="Clear address"
          text
          size="small"
          class="clear-btn"
          @click="clearBilling"
        />
      </div>

      <label class="field">
        <span class="field-label muted">Address</span>
        <InputText v-model="local.billing_address1" />
      </label>
      <label class="field">
        <span class="field-label muted">Address 2</span>
        <InputText v-model="local.billing_address2" />
      </label>
      <div class="two-col">
        <label class="field">
          <span class="field-label muted">Country</span>
          <InputText v-model="local.billing_country" placeholder="ID" maxlength="2" />
        </label>
        <label class="field">
          <span class="field-label muted">Province / State / Region</span>
          <InputText v-model="local.billing_state" />
        </label>
      </div>
      <div class="two-col">
        <label class="field">
          <span class="field-label muted">City</span>
          <InputText v-model="local.billing_city" />
        </label>
        <label class="field">
          <span class="field-label muted">Postal</span>
          <InputText v-model="local.billing_postal_code" />
        </label>
      </div>
    </section>

    <!-- Shipping -->
    <section class="card">
      <header class="card-head">
        <h3>Shipping</h3>
      </header>

      <label class="field">
        <span class="field-label">Ship to</span>
        <InputText
          v-model="local.ship_to_name"
          :placeholder="local.name || 'Name of business or person'"
        />
        <small class="help">Leave blank to use the customer name from Basic information.</small>
      </label>

      <div class="address-head">
        <span class="field-label">Shipping address</span>
        <Button
          v-if="!sameAsBilling"
          label="Clear address"
          text
          size="small"
          class="clear-btn"
          @click="clearShipping"
        />
      </div>

      <label class="same-as-billing">
        <Checkbox
          v-model="sameAsBilling"
          binary
          @update:modelValue="onToggleSameAsBilling"
        />
        <span>Same as billing address</span>
      </label>

      <template v-if="!sameAsBilling">
        <label class="field">
          <span class="field-label muted">Address</span>
          <InputText v-model="local.shipping_address1" />
        </label>
        <label class="field">
          <span class="field-label muted">Address 2</span>
          <InputText v-model="local.shipping_address2" />
        </label>
        <div class="two-col">
          <label class="field">
            <span class="field-label muted">Country</span>
            <InputText v-model="local.shipping_country" placeholder="ID" maxlength="2" />
          </label>
          <label class="field">
            <span class="field-label muted">Province / State / Region</span>
            <InputText v-model="local.shipping_state" />
          </label>
        </div>
        <div class="two-col">
          <label class="field">
            <span class="field-label muted">City</span>
            <InputText v-model="local.shipping_city" />
          </label>
          <label class="field">
            <span class="field-label muted">Postal</span>
            <InputText v-model="local.shipping_postal_code" />
          </label>
        </div>
      </template>

      <label class="field">
        <span class="field-label">Phone</span>
        <InputText
          v-model="local.shipping_phone"
          :placeholder="local.contact_phone || ''"
        />
        <small class="help">Leave blank to use the primary contact phone from Basic information.</small>
      </label>

      <label class="field">
        <span class="field-label">Delivery instructions</span>
        <Textarea v-model="local.shipping_delivery_instructions" rows="3" autoResize />
      </label>
    </section>
  </div>
</template>

<style scoped>
.customer-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  max-width: 760px;
}

.card {
  background: var(--color-surface);
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 12px;
  padding: 1.25rem 1.25rem 1rem;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
}

.card-head h3 {
  margin: 0 0 0.25rem 0;
  font-size: 1rem;
  color: var(--color-text, #0f172a);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  font-size: 0.875rem;
  color: var(--color-text);
  min-width: 0;
}
.field :deep(.p-inputtext),
.field :deep(.p-inputtextarea),
.field :deep(.p-dropdown) {
  width: 100%;
}
.field-label {
  font-weight: 600;
  color: var(--color-text);
  font-size: 0.85rem;
}
.field-label.muted {
  font-weight: 500;
  color: var(--color-text-muted, #64748b);
  font-size: 0.78rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.help {
  color: var(--color-text-muted, #64748b);
  font-size: 0.78rem;
}

.primary-contact-label {
  margin-top: 0.25rem;
}

.two-col {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1rem;
  min-width: 0;
}
.two-col > .field {
  min-width: 0;
}

.address-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 0.25rem;
}
.clear-btn :deep(.p-button-label) {
  font-size: 0.8rem;
}

.same-as-billing {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.85rem;
  color: var(--color-text);
  cursor: pointer;
  user-select: none;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem;
  margin-top: 0.3rem;
}

.inline-add-btn {
  align-self: flex-start;
  margin-top: 0.15rem;
  padding: 0.1rem 0.3rem;
}
.inline-add-btn :deep(.p-button-label) { font-size: 0.78rem; }
.field-label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
</style>
