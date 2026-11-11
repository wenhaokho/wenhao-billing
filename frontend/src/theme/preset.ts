import { definePreset } from "@primeuix/themes";
import Aura from "@primeuix/themes/aura";

// Brand preset (spec "Visual system"). Colours live in the semantic layer,
// shapes in the component layer; whatever the token API cannot express is in
// theme/overrides.css. Token names checked against @primeuix/themes 2.0.3 Aura.

// Built around the brand red: 500 = red #a51c30, 600 = red-deep #981c2e (hover),
// 400 = the dark-mode red #c9414f (Aura's dark primary is primary.400),
// 100 = the red chip background #f3dfe1. The rest are tints/shades of 500.
const brandRed = {
  50: "#faf1f3",
  100: "#f3dfe1",
  200: "#e4bbc1",
  300: "#d77e88",
  400: "#c9414f",
  500: "#a51c30",
  600: "#981c2e",
  700: "#871727",
  800: "#6d1220",
  900: "#530e18",
  950: "#35090f",
};

// Light: the cream/ink scale. 0 surface, 50 bg, 100 surface-2, 200 tint,
// 300 line, 400 muted-soft, 500 muted, 700 ink.
const creamInk = {
  0: "#ffffff",
  50: "#fcfbf6",
  100: "#f6f2ea",
  200: "#ede7de",
  300: "#e1d7c8",
  400: "#9d8a6e",
  500: "#75665f",
  600: "#64403e",
  700: "#501921",
  800: "#3d1419",
  900: "#2a0f12",
  950: "#17110f",
};

// Dark: Aura reads surface.950 as the field background, 900 as content,
// 800 as hover and 700 as borders. 950 bg, 900 surface, 700 line from the
// mockup; the rest derived to keep the light scale's steps.
const darkInk = {
  0: "#ffffff",
  50: "#fcfbf6",
  100: "#f2ede4",
  200: "#e6ddd2",
  300: "#cfc4b8",
  400: "#b3a59a",
  500: "#8c7b6e",
  600: "#5c4a45",
  700: "#3b2d2b",
  800: "#2a201e",
  900: "#1f1716",
  950: "#17110f",
};

// Overlays (dialogs, menus, panels, toasts) are the one exception to "no shadows";
// the value switches with dark mode in app.css.
const softShadow = "var(--wb-overlay-shadow)";

// Success / warn / danger buttons (Record payment, Mark as paid, Reset,
// Confirm reversal) take the status tones instead of Aura's green/orange/red
// palettes. The --wb-* values switch with dark mode in app.css.
const toneFill = (fg: string, on = "var(--wb-on-tone)") => ({
  background: fg,
  hoverBackground: `color-mix(in srgb, ${fg}, black 12%)`,
  activeBackground: `color-mix(in srgb, ${fg}, black 20%)`,
  borderColor: fg,
  hoverBorderColor: `color-mix(in srgb, ${fg}, black 12%)`,
  activeBorderColor: `color-mix(in srgb, ${fg}, black 20%)`,
  color: on,
  hoverColor: on,
  activeColor: on,
  focusRing: { color: fg, shadow: "none" },
});
const toneButtons = {
  success: toneFill("var(--wb-green-fg)"),
  warn: toneFill("var(--wb-amber-fg)"),
  // Ruling R10: a destructive action must never read as the routine (filled
  // red) primary, so danger is outlined: no fill, a 1px red line and red text,
  // red tint on hover. --wb-red-fg is #a51c30 in light (7.6:1 on white) and
  // #ec8f99 in dark (7.6:1 on the dark surface).
  danger: {
    background: "transparent",
    hoverBackground: "var(--wb-red-bg)",
    activeBackground: "var(--wb-red-bg)",
    borderColor: "var(--wb-red-fg)",
    hoverBorderColor: "var(--wb-red-fg)",
    activeBorderColor: "var(--wb-red-fg)",
    color: "var(--wb-red-fg)",
    hoverColor: "var(--wb-red-fg)",
    activeColor: "var(--wb-red-fg)",
    focusRing: { color: "var(--wb-red-fg)", shadow: "none" },
  },
};
const toneText = (fg: string, bg: string) => ({ hoverBackground: bg, activeBackground: bg, color: fg });
const toneTextButtons = {
  text: {
    success: toneText("var(--wb-green-fg)", "var(--wb-green-bg)"),
    warn: toneText("var(--wb-amber-fg)", "var(--wb-amber-bg)"),
    danger: toneText("var(--wb-red)", "var(--wb-red-bg)"),
  },
};

// Fields and tabs keep the spec's inset 1px ring (semantic.focusRing). On
// buttons, checkboxes, radios and toggles that ring would sit on the control's
// own border, in the control's own colour, and vanish (WCAG 2.4.7), so they
// draw a 2px ring outside a 2px gap. The colour is each control's own focus
// colour (the fill for buttons, the primary for toggles), visible against the
// page through the gap.
const outsideRing = { width: "2px", style: "solid", offset: "2px" };

export const AppPreset = definePreset(Aura, {
  semantic: {
    primary: brandRed,
    // Spec: "Focus ring: inset 1px".
    focusRing: { width: "1px", style: "solid", color: "{primary.color}", offset: "-1px", shadow: "none" },
    formField: { borderRadius: "2px" },
    content: { borderRadius: "0" },
    list: { option: { borderRadius: "0" } },
    navigation: { item: { borderRadius: "0" } },
    overlay: {
      select: { borderRadius: "0", shadow: softShadow },
      popover: { borderRadius: "0", shadow: softShadow },
      modal: { borderRadius: "0", shadow: softShadow },
      navigation: { shadow: softShadow },
    },
    colorScheme: {
      light: {
        surface: creamInk,
        primary: { color: "{primary.500}", contrastColor: "#ffffff", hoverColor: "{primary.600}", activeColor: "{primary.700}" },
        highlight: { background: "{primary.50}", focusBackground: "{primary.100}", color: "{primary.700}", focusColor: "{primary.800}" },
        text: { color: "{surface.700}", hoverColor: "{surface.800}", mutedColor: "{surface.500}", hoverMutedColor: "{surface.600}" },
        content: { background: "{surface.0}", hoverBackground: "{surface.100}", borderColor: "{surface.300}" },
        overlay: {
          select: { borderColor: "{surface.300}" },
          popover: { borderColor: "{surface.300}" },
          modal: { borderColor: "{surface.300}" },
        },
        formField: { borderColor: "{surface.300}", hoverBorderColor: "{surface.400}", color: "{surface.700}", disabledBackground: "{surface.100}" },
        list: { option: { focusBackground: "{surface.100}" } },
        navigation: { item: { focusBackground: "{surface.100}", activeBackground: "{surface.100}" } },
      },
      dark: {
        surface: darkInk,
        primary: { color: "{primary.400}", contrastColor: "#ffffff", hoverColor: "#b5384a", activeColor: "{primary.500}" },
        highlight: {
          background: "color-mix(in srgb, {primary.400}, transparent 84%)",
          focusBackground: "color-mix(in srgb, {primary.400}, transparent 76%)",
          color: "{surface.50}",
          focusColor: "{surface.50}",
        },
        text: { color: "{surface.50}", hoverColor: "{surface.50}", mutedColor: "{surface.400}", hoverMutedColor: "{surface.300}" },
        content: { background: "{surface.900}", hoverBackground: "{surface.800}", borderColor: "{surface.700}" },
        overlay: {
          select: { background: "{surface.900}", borderColor: "{surface.700}" },
          popover: { background: "{surface.900}", borderColor: "{surface.700}" },
          modal: { background: "{surface.900}", borderColor: "{surface.700}" },
        },
        // Disabled fields: surface-2, the dark twin of light's surface.100 (Aura's
        // default surface.700 reads as a solid grey slab on the dark page).
        formField: {
          color: "{surface.50}",
          borderColor: "{surface.700}",
          hoverBorderColor: "{surface.600}",
          disabledBackground: "{surface.800}",
        },
      },
    },
  },
  components: {
    button: {
      root: { borderRadius: "30px", roundedBorderRadius: "30px", iconOnlyWidth: "42px", paddingX: "1.25rem", focusRing: outsideRing },
      colorScheme: {
        light: {
          root: {
            // Secondary: transparent with a 1px line border, hover tint.
            secondary: {
              background: "transparent",
              hoverBackground: "{surface.200}",
              activeBackground: "{surface.300}",
              borderColor: "{surface.300}",
              hoverBorderColor: "{surface.300}",
              activeBorderColor: "{surface.400}",
              color: "{surface.700}",
              hoverColor: "{surface.700}",
              activeColor: "{surface.800}",
            },
            ...toneButtons,
          },
          ...toneTextButtons,
        },
        dark: {
          root: {
            secondary: {
              background: "transparent",
              hoverBackground: "{surface.800}",
              activeBackground: "{surface.700}",
              borderColor: "{surface.700}",
              hoverBorderColor: "{surface.600}",
              activeBorderColor: "{surface.600}",
              color: "{surface.50}",
              hoverColor: "{surface.50}",
              activeColor: "{surface.50}",
            },
            ...toneButtons,
          },
          ...toneTextButtons,
        },
      },
    },
    checkbox: { root: { focusRing: outsideRing } },
    radiobutton: { root: { focusRing: outsideRing } },
    toggleswitch: { root: { focusRing: outsideRing } },
    card: { root: { borderRadius: "0", shadow: "none" } },
    // Chips: the tone pairs come from the --wb-* variables in app.css, which
    // already switch with .app-dark.
    tag: {
      root: { borderRadius: "2px", roundedBorderRadius: "2px" },
      colorScheme: {
        light: {
          success: { background: "var(--wb-green-bg)", color: "var(--wb-green-fg)" },
          info: { background: "var(--wb-blue-bg)", color: "var(--wb-blue-fg)" },
          warn: { background: "var(--wb-amber-bg)", color: "var(--wb-amber-fg)" },
          danger: { background: "var(--wb-red-bg)", color: "var(--wb-red-fg)" },
          secondary: { background: "var(--wb-tint)", color: "var(--wb-muted)" },
          contrast: { background: "var(--wb-ink)", color: "var(--wb-surface)" },
        },
        dark: {
          success: { background: "var(--wb-green-bg)", color: "var(--wb-green-fg)" },
          info: { background: "var(--wb-blue-bg)", color: "var(--wb-blue-fg)" },
          warn: { background: "var(--wb-amber-bg)", color: "var(--wb-amber-fg)" },
          danger: { background: "var(--wb-red-bg)", color: "var(--wb-red-fg)" },
          secondary: { background: "var(--wb-tint)", color: "var(--wb-muted)" },
          contrast: { background: "var(--wb-ink)", color: "var(--wb-surface)" },
        },
      },
    },
  },
});
