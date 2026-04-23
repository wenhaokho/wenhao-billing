export interface CustomerFormData {
  name: string;
  matching_aliases: string[];
  active: boolean;

  contact_name: string;
  contact_first_name: string;
  contact_last_name: string;
  contact_email: string;
  contact_phone: string;
  contact_phone_2: string;

  account_number: string;
  website: string;
  notes: string;

  default_currency: string;

  billing_address: string;
  billing_address1: string;
  billing_address2: string;
  billing_country: string;
  billing_state: string;
  billing_city: string;
  billing_postal_code: string;

  ship_to_name: string;
  shipping_address1: string;
  shipping_address2: string;
  shipping_country: string;
  shipping_state: string;
  shipping_city: string;
  shipping_postal_code: string;
  shipping_phone: string;
  shipping_delivery_instructions: string;
}

export function emptyCustomerForm(): CustomerFormData {
  return {
    name: "",
    matching_aliases: [],
    active: true,
    contact_name: "",
    contact_first_name: "",
    contact_last_name: "",
    contact_email: "",
    contact_phone: "",
    contact_phone_2: "",
    account_number: "",
    website: "",
    notes: "",
    default_currency: "IDR",
    billing_address: "",
    billing_address1: "",
    billing_address2: "",
    billing_country: "",
    billing_state: "",
    billing_city: "",
    billing_postal_code: "",
    ship_to_name: "",
    shipping_address1: "",
    shipping_address2: "",
    shipping_country: "",
    shipping_state: "",
    shipping_city: "",
    shipping_postal_code: "",
    shipping_phone: "",
    shipping_delivery_instructions: "",
  };
}

export function buildCustomerPayload(form: CustomerFormData): Record<string, unknown> {
  const s = (v: string) => {
    const t = v?.trim?.() ?? "";
    return t === "" ? null : t;
  };
  return {
    name: form.name,
    matching_aliases: form.matching_aliases,
    active: form.active,
    contact_name: s(form.contact_name),
    contact_first_name: s(form.contact_first_name),
    contact_last_name: s(form.contact_last_name),
    contact_email: s(form.contact_email),
    contact_phone: s(form.contact_phone),
    contact_phone_2: s(form.contact_phone_2),
    account_number: s(form.account_number),
    website: s(form.website),
    notes: s(form.notes),
    default_currency: s(form.default_currency) ?? "IDR",
    billing_address: s(form.billing_address),
    billing_address1: s(form.billing_address1),
    billing_address2: s(form.billing_address2),
    billing_country: s(form.billing_country),
    billing_state: s(form.billing_state),
    billing_city: s(form.billing_city),
    billing_postal_code: s(form.billing_postal_code),
    ship_to_name: s(form.ship_to_name),
    shipping_address1: s(form.shipping_address1),
    shipping_address2: s(form.shipping_address2),
    shipping_country: s(form.shipping_country),
    shipping_state: s(form.shipping_state),
    shipping_city: s(form.shipping_city),
    shipping_postal_code: s(form.shipping_postal_code),
    shipping_phone: s(form.shipping_phone),
    shipping_delivery_instructions: s(form.shipping_delivery_instructions),
  };
}
