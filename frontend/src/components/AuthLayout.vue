<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
defineProps<{
  title: string;
  subtitle?: string;
  heroHeadline?: string;
  heroTagline?: string;
}>();
</script>

<template>
  <div class="auth-shell">
    <!-- Brand panel: the sidebar's burgundy, the "W" tile, gold eyebrow. -->
    <aside class="hero">
      <div class="hero-inner">
        <div class="brand">
          <div class="brand-mark" aria-hidden="true">W</div>
          <div>
            <div class="brand-name">Wenhao Billing</div>
            <div class="brand-sub">Software house</div>
          </div>
        </div>

        <div class="hero-copy">
          <h2>{{ heroHeadline ?? "Accounting operations, quietly automated." }}</h2>
          <p>
            {{
              heroTagline ??
              "Billing, reconciliations, and client reporting in one workspace built for small software houses."
            }}
          </p>
        </div>

        <ul class="hero-points">
          <li><AppIcon name="circle-check" /> Automated monthly retainers</li>
          <li><AppIcon name="circle-check" /> Reconciled against bank feeds</li>
          <li><AppIcon name="circle-check" /> Client-ready statements in one click</li>
        </ul>

        <div class="hero-footer">&copy; {{ new Date().getFullYear() }} Wenhao Billing</div>
      </div>
    </aside>

    <main class="form-panel">
      <div class="auth-card">
        <div class="form-head">
          <h1>{{ title }}</h1>
          <p v-if="subtitle">{{ subtitle }}</p>
        </div>

        <slot />

        <div v-if="$slots.footer" class="form-foot">
          <slot name="footer" />
        </div>
      </div>
    </main>
  </div>
</template>

<style scoped>
.auth-shell {
  min-height: 100vh;
  min-height: 100dvh;
  width: 100%;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  background: var(--wb-bg);
}

.hero {
  color: var(--wb-sidebar-fg);
  background: var(--wb-sidebar-bg);
  display: flex;
  align-items: stretch;
}
.hero-inner {
  padding: 48px 56px;
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 600px;
}
.brand { display: flex; align-items: center; gap: 12px; margin-bottom: auto; }
.brand-mark {
  width: 40px;
  height: 40px;
  flex: none;
  background: var(--wb-red);
  color: #fff;
  display: grid;
  place-items: center;
  font: 800 20px var(--wb-font-display);
}
.brand-name { font: 600 16px/1.2 var(--wb-font-display); letter-spacing: -0.3px; color: #fff; }
.brand-sub {
  font: 500 10px/1.4 var(--wb-font-sans);
  letter-spacing: 1.5px;
  text-transform: uppercase;
  color: var(--wb-sidebar-eyebrow);
}

.hero-copy { margin: 80px 0 36px; }
.hero-copy h2 {
  color: #fff;
  font: 500 44px/1.08 var(--wb-font-display);
  letter-spacing: -1.6px;
  margin: 0 0 18px;
}
.hero-copy p {
  color: var(--wb-sidebar-fg);
  opacity: 0.85;
  font-size: 16px;
  line-height: 1.6;
  margin: 0;
  max-width: 38ch;
}
.hero-points {
  list-style: none;
  padding: 24px 0 0;
  margin: 0 0 auto;
  border-top: 1px solid var(--wb-sidebar-line);
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.hero-points li { display: flex; align-items: center; gap: 10px; font-size: 14px; }
.hero-points .app-icon { color: var(--wb-gold); width: 16px; height: 16px; }
.hero-footer {
  margin-top: 40px;
  color: var(--wb-sidebar-eyebrow);
  font: 500 11px var(--wb-font-sans);
  letter-spacing: 1.2px;
  text-transform: uppercase;
}

.form-panel {
  display: grid;
  place-items: center;
  padding: 48px 32px;
}
/* The sign-in card: square, 1px line border, no shadow. */
.auth-card {
  width: 100%;
  max-width: 440px;
  background: var(--wb-surface);
  border: 1px solid var(--wb-line);
  border-radius: 0;
  padding: 40px;
}
.form-head h1 {
  font: 500 32px/1.1 var(--wb-font-display);
  letter-spacing: -1px;
  color: var(--wb-ink);
  margin: 0 0 8px;
}
.form-head p { margin: 0 0 28px; color: var(--wb-muted); font-size: 15px; line-height: 1.5; }

.form-foot {
  margin-top: 28px;
  padding-top: 20px;
  border-top: 1px solid var(--wb-line);
  color: var(--wb-muted-soft);
  font-size: 13px;
  text-align: center;
}
.form-foot :deep(a) {
  color: var(--wb-muted);
  text-decoration: none;
}
.form-foot :deep(a:hover) { color: var(--wb-red); }

@media (max-width: 900px) {
  .auth-shell { grid-template-columns: 1fr; }
  .hero { display: none; }
  .form-panel { padding: 56px 16px 32px; align-items: start; }
}
@media (max-width: 480px) {
  .auth-card { padding: 28px 20px; }
  .form-head h1 { font-size: 28px; }
}
@media (max-width: 1100px) and (min-width: 901px) {
  .hero-inner { padding: 40px 32px; }
  .hero-copy h2 { font-size: 36px; }
}
</style>
