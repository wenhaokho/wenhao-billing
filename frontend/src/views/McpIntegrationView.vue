<script setup lang="ts">
import { computed, ref } from "vue";

// The MCP endpoint is served by the backend origin. In production the backend
// and frontend sit behind the same host (billing.wenhao.id); in local dev the
// backend is on :8000. We show the production connector URL and surface the dev
// URL as a hint rather than guessing from window.location (frontend != backend).
const PROD_CONNECTOR_URL = "https://billing.wenhao.id/mcp";
const DEV_CONNECTOR_URL = "http://localhost:8000/mcp";

const connectorUrl = PROD_CONNECTOR_URL;

const copied = ref(false);
let copyTimer: ReturnType<typeof setTimeout> | undefined;

async function copyUrl() {
  try {
    await navigator.clipboard.writeText(connectorUrl);
  } catch {
    // Clipboard API unavailable (e.g. insecure context) — select-fallback.
    const el = document.getElementById("mcp-url-input") as HTMLInputElement | null;
    el?.select();
    document.execCommand?.("copy");
  }
  copied.value = true;
  clearTimeout(copyTimer);
  copyTimer = setTimeout(() => (copied.value = false), 1800);
}

const steps = [
  {
    title: "Open Claude Desktop settings",
    body: "Go to Settings → Connectors → Add custom connector.",
  },
  {
    title: "Paste the connector URL",
    body: "Use the URL above as the connector's HTTP endpoint, then confirm.",
  },
  {
    title: "Sign in when prompted",
    body: "Claude opens this app's login in your browser. Sign in with your admin credentials — the same ones you use here.",
  },
  {
    title: "Approve access",
    body: "You'll see a one-time consent screen. Approve it, and Claude is connected.",
  },
];

type Capability = { icon: string; label: string; detail: string };

const reads: Capability[] = [
  { icon: "pi-search", label: "Ask anything", detail: "“What does Acme still owe?”, “revenue this quarter in SGD”, “what's overdue?”" },
];
const writes: Capability[] = [
  { icon: "pi-file-edit", label: "Draft & issue", detail: "Create invoices, quotations, bills, customers, projects, items — and finalize invoices." },
  { icon: "pi-sync", label: "Recurring & reconciliation", detail: "Spin up recurring templates and resolve clear payment matches (mismatches always stop for you)." },
];
const gated: Capability[] = [
  { icon: "pi-send", label: "Emails to customers", detail: "Sending an invoice or reminder shows you a preview first — nothing goes out until you confirm." },
  { icon: "pi-trash", label: "Deletions", detail: "Any delete is previewed and requires your explicit confirmation." },
];

const securityPoints = computed(() => [
  {
    icon: "pi-lock",
    title: "Your login, nothing new",
    body: "Access is granted through the same admin sign-in you already use — no separate password or API key to manage.",
  },
  {
    icon: "pi-user-edit",
    title: "Admin-only",
    body: "Only an admin account can connect and drive the assistant. Non-admin sessions are refused.",
  },
  {
    icon: "pi-verified",
    title: "Consent + revocable",
    body: "Every connection passes an explicit consent screen, and access tokens can be revoked at any time.",
  },
]);
</script>

<template>
  <div class="mcp-page">
    <div class="page-header">
      <div>
        <h1><i class="pi pi-sparkles title-spark" /> AI Assistant</h1>
        <p class="subtitle">
          Connect Claude to your billing so you can create invoices, answer
          questions, and run your books by just asking.
        </p>
      </div>
    </div>

    <!-- Intro / hero -->
    <section class="card hero">
      <div class="hero-copy">
        <span class="eyebrow">Model Context Protocol</span>
        <h2>Talk to your books.</h2>
        <p>
          Once connected, tell Claude what you want in plain English &mdash;
          <em>&ldquo;invoice Acme for 40 hours of design at $100/hr&rdquo;</em>
          &mdash; and it drives this system directly, through the same rules and
          ledger you rely on here.
        </p>
      </div>
      <div class="hero-glyph" aria-hidden="true">
        <i class="pi pi-sparkles" />
      </div>
    </section>

    <!-- Connect -->
    <section class="card card-pad connect">
      <div class="section-head">
        <span class="step-badge"><i class="pi pi-link" /></span>
        <div>
          <h3>Connect Claude Desktop</h3>
          <p class="section-sub">Add this as a custom connector, then sign in once.</p>
        </div>
      </div>

      <label class="url-label" for="mcp-url-input">Connector URL</label>
      <div class="url-row">
        <input
          id="mcp-url-input"
          class="url-input"
          type="text"
          :value="connectorUrl"
          readonly
          spellcheck="false"
          @focus="($event.target as HTMLInputElement).select()"
        />
        <button
          type="button"
          class="copy-btn"
          :class="{ copied }"
          :aria-label="copied ? 'Copied' : 'Copy connector URL'"
          @click="copyUrl"
        >
          <i :class="copied ? 'pi pi-check' : 'pi pi-copy'" />
          <span>{{ copied ? "Copied" : "Copy" }}</span>
        </button>
      </div>
      <p class="url-hint">
        <i class="pi pi-info-circle" />
        Running locally? Use <code>{{ DEV_CONNECTOR_URL }}</code> instead.
      </p>

      <ol class="steps">
        <li v-for="(step, i) in steps" :key="i" class="step">
          <span class="step-num">{{ i + 1 }}</span>
          <div class="step-body">
            <div class="step-title">{{ step.title }}</div>
            <div class="step-detail">{{ step.body }}</div>
          </div>
        </li>
      </ol>
    </section>

    <!-- What it can do -->
    <section class="capabilities">
      <div class="cap-col card card-pad">
        <div class="cap-head">
          <span class="cap-tag tag-read">Reads</span>
          <span class="cap-note">no confirmation</span>
        </div>
        <div v-for="c in reads" :key="c.label" class="cap-item">
          <i :class="['pi', c.icon]" />
          <div>
            <div class="cap-label">{{ c.label }}</div>
            <div class="cap-detail">{{ c.detail }}</div>
          </div>
        </div>
      </div>

      <div class="cap-col card card-pad">
        <div class="cap-head">
          <span class="cap-tag tag-write">Writes</span>
          <span class="cap-note">automatic</span>
        </div>
        <div v-for="c in writes" :key="c.label" class="cap-item">
          <i :class="['pi', c.icon]" />
          <div>
            <div class="cap-label">{{ c.label }}</div>
            <div class="cap-detail">{{ c.detail }}</div>
          </div>
        </div>
      </div>

      <div class="cap-col card card-pad">
        <div class="cap-head">
          <span class="cap-tag tag-gated">Confirm first</span>
          <span class="cap-note">you approve</span>
        </div>
        <div v-for="c in gated" :key="c.label" class="cap-item">
          <i :class="['pi', c.icon]" />
          <div>
            <div class="cap-label">{{ c.label }}</div>
            <div class="cap-detail">{{ c.detail }}</div>
          </div>
        </div>
      </div>
    </section>

    <!-- Security -->
    <section class="card card-pad security">
      <div class="section-head">
        <span class="step-badge badge-shield"><i class="pi pi-shield" /></span>
        <div>
          <h3>How access works</h3>
          <p class="section-sub">Secured through your existing login &mdash; nothing new to manage.</p>
        </div>
      </div>
      <div class="sec-grid">
        <div v-for="s in securityPoints" :key="s.title" class="sec-item">
          <i :class="['pi', s.icon]" />
          <div class="sec-title">{{ s.title }}</div>
          <div class="sec-body">{{ s.body }}</div>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.mcp-page {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
  animation: page-rise 0.4s ease both;
}
@keyframes page-rise {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: none; }
}

.page-header h1 {
  display: flex;
  align-items: center;
  gap: 0.55rem;
}
.title-spark {
  color: var(--color-primary);
  font-size: 1.2rem;
}

/* ---- hero ---- */
.hero {
  position: relative;
  overflow: hidden;
  padding: 1.75rem 1.5rem;
  display: flex;
  align-items: center;
  gap: 1.5rem;
  background:
    radial-gradient(120% 140% at 100% 0%, var(--color-primary-soft) 0%, transparent 55%),
    var(--color-surface);
}
.hero-copy { position: relative; z-index: 1; max-width: 60ch; }
.eyebrow {
  display: inline-block;
  font-size: 0.68rem;
  font-weight: 700;
  letter-spacing: 0.11em;
  text-transform: uppercase;
  color: var(--color-primary);
  margin-bottom: 0.5rem;
}
.hero h2 {
  font-size: 1.5rem;
  margin: 0 0 0.5rem;
  letter-spacing: -0.02em;
}
.hero p { margin: 0; color: var(--color-text-muted); line-height: 1.55; }
.hero em { color: var(--color-text); font-style: italic; }
.hero-glyph {
  margin-left: auto;
  flex-shrink: 0;
  width: 96px;
  height: 96px;
  display: grid;
  place-items: center;
  border-radius: var(--radius-lg);
  background: linear-gradient(135deg, var(--color-primary), #60a5fa);
  box-shadow: var(--shadow-md);
}
.hero-glyph .pi { font-size: 2.4rem; color: #fff; }

/* ---- section heads ---- */
.section-head { display: flex; align-items: flex-start; gap: 0.85rem; margin-bottom: 1.25rem; }
.section-head h3 { margin: 0 0 0.15rem; font-size: 1.05rem; }
.section-sub { margin: 0; color: var(--color-text-muted); font-size: 0.85rem; }
.step-badge {
  flex-shrink: 0;
  width: 38px; height: 38px;
  display: grid; place-items: center;
  border-radius: var(--radius-md);
  background: var(--color-primary-soft);
  color: var(--color-primary);
  font-size: 1.05rem;
}
.badge-shield { background: var(--color-success-soft); color: var(--color-success); }

/* ---- connector URL ---- */
.url-label {
  display: block;
  font-size: 0.72rem;
  font-weight: 700;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--color-text-subtle);
  margin-bottom: 0.4rem;
}
.url-row { display: flex; gap: 0.5rem; }
.url-input {
  flex: 1;
  min-width: 0;
  font-family: ui-monospace, "SF Mono", "Cascadia Code", Menlo, Consolas, monospace;
  font-size: 0.9rem;
  color: var(--color-text);
  background: var(--color-surface-alt);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  padding: 0.6rem 0.75rem;
  letter-spacing: 0.01em;
}
.url-input:focus { outline: none; box-shadow: var(--focus-ring); border-color: var(--color-primary); }
.copy-btn {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0 0.9rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--color-primary);
  background: var(--color-primary);
  color: #fff;
  font: inherit;
  font-weight: 600;
  font-size: 0.85rem;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s, transform 0.08s;
}
.copy-btn:hover { background: #1d4ed8; border-color: #1d4ed8; }
.copy-btn:active { transform: scale(0.97); }
.copy-btn:focus-visible { outline: none; box-shadow: var(--focus-ring); }
.copy-btn.copied {
  background: var(--color-success);
  border-color: var(--color-success);
}
.url-hint {
  margin: 0.6rem 0 0;
  font-size: 0.8rem;
  color: var(--color-text-muted);
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
.url-hint code {
  font-family: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  background: var(--color-surface-alt);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 0.05rem 0.35rem;
  font-size: 0.78rem;
  color: var(--color-text);
}

/* ---- steps ---- */
.steps {
  list-style: none;
  margin: 1.5rem 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.step { display: flex; gap: 0.85rem; padding: 0.85rem 0; position: relative; }
.step:not(:last-child) { border-bottom: 1px dashed var(--color-border); }
.step-num {
  flex-shrink: 0;
  width: 26px; height: 26px;
  display: grid; place-items: center;
  border-radius: 50%;
  background: var(--color-surface-alt);
  border: 1px solid var(--color-border-strong);
  color: var(--color-text);
  font-size: 0.8rem;
  font-weight: 700;
}
.step-title { font-weight: 600; font-size: 0.92rem; margin-bottom: 0.1rem; }
.step-detail { color: var(--color-text-muted); font-size: 0.85rem; line-height: 1.5; }

/* ---- capabilities ---- */
.capabilities {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1rem;
}
.cap-col { display: flex; flex-direction: column; }
.cap-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.9rem;
  padding-bottom: 0.75rem;
  border-bottom: 1px solid var(--color-border);
}
.cap-tag {
  font-size: 0.72rem;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  padding: 0.2rem 0.55rem;
  border-radius: 999px;
}
.tag-read { background: var(--color-primary-soft); color: var(--color-primary); }
.tag-write { background: var(--color-success-soft); color: var(--color-success); }
.tag-gated { background: var(--color-warning-soft); color: var(--color-warning); }
.cap-note { font-size: 0.72rem; color: var(--color-text-subtle); text-transform: uppercase; letter-spacing: 0.05em; }
.cap-item { display: flex; gap: 0.7rem; padding: 0.6rem 0; align-items: flex-start; }
.cap-item > .pi { color: var(--color-text-muted); font-size: 1rem; margin-top: 0.15rem; width: 18px; text-align: center; }
.cap-label { font-weight: 600; font-size: 0.88rem; margin-bottom: 0.15rem; }
.cap-detail { color: var(--color-text-muted); font-size: 0.82rem; line-height: 1.5; }

/* ---- security ---- */
.sec-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 1rem; }
.sec-item {
  padding: 1rem;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  background: var(--color-surface-alt);
}
.sec-item > .pi { color: var(--color-success); font-size: 1.15rem; margin-bottom: 0.5rem; display: block; }
.sec-title { font-weight: 600; font-size: 0.9rem; margin-bottom: 0.25rem; }
.sec-body { color: var(--color-text-muted); font-size: 0.82rem; line-height: 1.5; }

@media (max-width: 900px) {
  .capabilities, .sec-grid { grid-template-columns: 1fr; }
  .hero-glyph { width: 72px; height: 72px; }
  .hero-glyph .pi { font-size: 1.8rem; }
}
</style>
