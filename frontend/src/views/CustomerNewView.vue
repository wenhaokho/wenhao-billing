<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import Message from "primevue/message";
import { api } from "@/api/client";
import CustomerForm from "@/components/CustomerForm.vue";
import {
  emptyCustomerForm,
  buildCustomerPayload,
  type CustomerFormData,
} from "@/components/customerForm";

interface Customer {
  customer_id: string;
  name: string;
}

const router = useRouter();
const queryClient = useQueryClient();

const form = ref<CustomerFormData>(emptyCustomerForm());
const saveError = ref<string | null>(null);

const createCustomer = useMutation({
  mutationFn: async () =>
    api.post<Customer>("/customers", buildCustomerPayload(form.value)),
  onSuccess: (res) => {
    queryClient.invalidateQueries({ queryKey: ["customers"] });
    router.push({
      name: "customer-detail",
      params: { customerId: res.data.customer_id },
    });
  },
  onError: (err: any) => {
    saveError.value = err?.response?.data?.detail ?? "Failed to create customer";
  },
});

function submit() {
  saveError.value = null;
  createCustomer.mutate();
}

function cancel() {
  router.push({ name: "customers" });
}
</script>

<template>
  <section class="new-customer">
    <div class="back">
      <Button
        label="Customers"
        icon="pi pi-arrow-left"
        text
        size="small"
        @click="cancel"
      />
    </div>

    <div class="cd-header">
      <h1 class="cd-title">New customer</h1>
      <div class="cd-actions">
        <Button label="Cancel" text @click="cancel" />
        <Button
          label="Create customer"
          :disabled="!form.name"
          :loading="createCustomer.isPending.value"
          @click="submit"
        />
      </div>
    </div>

    <Message
      v-if="saveError"
      severity="error"
      :closable="true"
      @close="saveError = null"
    >
      {{ saveError }}
    </Message>

    <div class="form-wrap">
      <CustomerForm v-model="form" />
    </div>
  </section>
</template>

<style scoped>
.new-customer {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  padding-bottom: 2rem;
}
.back { margin-bottom: -0.5rem; }
.back :deep(.p-button-label) {
  font-size: 0.9rem;
}
.cd-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
}
.cd-title {
  margin: 0;
  font-size: 1.5rem;
  text-transform: uppercase;
  letter-spacing: 0.02em;
}
.cd-actions {
  display: flex;
  gap: 0.5rem;
}
.form-wrap {
  margin-top: 0.5rem;
}
</style>
