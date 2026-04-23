<script setup lang="ts">
defineProps<{
  title: string;
  subtitle?: string;
  heroHeadline?: string;
  heroTagline?: string;
}>();
</script>

<template>
  <div class="auth-shell">
    <aside class="hero">
      <div class="hero-inner">
        <div class="brand">
          <div class="brand-mark">WB</div>
          <div class="brand-name">wenhao-billing</div>
        </div>

        <div class="hero-copy">
          <h2>{{ heroHeadline ?? "Accounting operations, quietly automated." }}</h2>
          <p>
            {{
              heroTagline ??
              "Billing, reconciliations, and client reporting in one workspace built for small agency teams."
            }}
          </p>
        </div>

        <ul class="hero-points">
          <li><i class="pi pi-check-circle" /> Automated monthly retainers</li>
          <li><i class="pi pi-check-circle" /> Reconciled against bank feeds</li>
          <li><i class="pi pi-check-circle" /> Client-ready statements in one click</li>
        </ul>

        <div class="hero-footer">&copy; {{ new Date().getFullYear() }} wenhao-billing</div>
      </div>
      <div class="hero-glow glow-1" />
      <div class="hero-glow glow-2" />
      <div class="hero-grid" />
    </aside>

    <main class="form-panel">
      <div class="form-wrap">
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
  width: 100%;
  display: grid;
  grid-template-columns: 1.05fr 1fr;
  background: var(--color-bg);
}

.hero {
  position: relative;
  overflow: hidden;
  color: #e5edff;
  background: linear-gradient(135deg, #0b1d3a 0%, #122a55 45%, #1e3a8a 100%);
  display: flex;
  align-items: stretch;
}
.hero-inner {
  position: relative;
  z-index: 2;
  padding: 3rem 3.5rem;
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 560px;
}
.brand { display: flex; align-items: center; gap: 0.75rem; margin-bottom: auto; }
.brand-mark {
  width: 44px; height: 44px; border-radius: 12px;
  background: linear-gradient(135deg, #60a5fa, #3b82f6);
  color: white; display: grid; place-items: center;
  font-weight: 700; letter-spacing: 0.02em;
  box-shadow: 0 6px 20px rgba(59, 130, 246, 0.4);
}
.brand-name { font-weight: 600; font-size: 1.05rem; color: #fff; }

.hero-copy { margin: 5rem 0 2rem; }
.hero-copy h2 {
  color: #fff;
  font-size: 2rem;
  line-height: 1.2;
  letter-spacing: -0.02em;
  font-weight: 600;
  margin: 0 0 1rem;
}
.hero-copy p {
  color: #cbd5e1;
  font-size: 0.95rem;
  line-height: 1.6;
  margin: 0;
  max-width: 36ch;
}
.hero-points { list-style: none; padding: 0; margin: 0 0 auto; display: flex; flex-direction: column; gap: 0.65rem; }
.hero-points li { display: flex; align-items: center; gap: 0.6rem; color: #dbeafe; font-size: 0.88rem; }
.hero-points .pi { color: #60a5fa; font-size: 0.95rem; }
.hero-footer { margin-top: 2.5rem; color: #94a3b8; font-size: 0.75rem; letter-spacing: 0.02em; }

.hero-glow { position: absolute; border-radius: 50%; filter: blur(80px); opacity: 0.55; pointer-events: none; }
.glow-1 { width: 480px; height: 480px; background: #3b82f6; top: -140px; right: -160px; }
.glow-2 { width: 420px; height: 420px; background: #6366f1; bottom: -160px; left: -120px; opacity: 0.45; }
.hero-grid {
  position: absolute; inset: 0;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.04) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.04) 1px, transparent 1px);
  background-size: 48px 48px;
  mask-image: radial-gradient(ellipse at 30% 40%, black 40%, transparent 75%);
  -webkit-mask-image: radial-gradient(ellipse at 30% 40%, black 40%, transparent 75%);
  z-index: 1;
}

.form-panel {
  display: grid;
  place-items: center;
  padding: 3rem 2rem;
  background: var(--color-surface);
}
.form-wrap { width: 100%; max-width: 380px; }
.form-head h1 { font-size: 1.75rem; letter-spacing: -0.02em; margin: 0 0 0.4rem; font-weight: 600; }
.form-head p { margin: 0 0 2rem; color: var(--color-text-muted); font-size: 0.92rem; }

.form-foot {
  margin-top: 2rem;
  padding-top: 1.25rem;
  border-top: 1px solid var(--color-border);
  color: var(--color-text-subtle);
  font-size: 0.8rem;
  text-align: center;
}
.form-foot :deep(a) {
  color: var(--color-text-muted);
  text-decoration: none;
}
.form-foot :deep(a:hover) { color: var(--color-primary); }

@media (max-width: 900px) {
  .auth-shell { grid-template-columns: 1fr; }
  .hero { display: none; }
  .form-panel { padding: 2rem 1.25rem; align-items: start; padding-top: 3.5rem; }
}

@media (max-width: 1100px) and (min-width: 901px) {
  .hero-inner { padding: 2.5rem 2rem; }
  .hero-copy h2 { font-size: 1.65rem; }
}
</style>
