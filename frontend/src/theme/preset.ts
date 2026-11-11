import { definePreset } from "@primeuix/themes";
import Aura from "@primeuix/themes/aura";

// Aura defaults primary to emerald; PrimeVue 3's aura-*-blue themes were blue, and the look must not change.
const blue = Object.fromEntries(
  [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950].map((n) => [n, `{blue.${n}}`]),
);

export const AppPreset = definePreset(Aura, { semantic: { primary: blue } });
