<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusTag from "@/components/StatusTag.vue";
import type { StatusTone } from "@/theme/status";
import type { IconName } from "@/theme/icons";
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

type Capability = { icon: IconName; label: string; detail: string };

const reads: Capability[] = [
  { icon: "search", label: "Ask anything", detail: "“What does Acme still owe?”, “revenue this quarter in SGD”, “what's overdue?”" },
];
const writes: Capability[] = [
  { icon: "file-pen", label: "Draft & issue", detail: "Create invoices, quotations, bills, customers, projects, items — and finalize invoices." },
  { icon: "refresh-cw", label: "Recurring & reconciliation", detail: "Spin up recurring templates and resolve clear payment matches (mismatches always stop for you)." },
];
const gated: Capability[] = [
  { icon: "send", label: "Emails to customers", detail: "Sending an invoice or reminder shows you a preview first — nothing goes out until you confirm." },
  { icon: "trash-2", label: "Deletions", detail: "Any delete is previewed and requires your explicit confirmation." },
];

// The three capability cards (mockup): a tone badge, the mode with its
// glyph, then the items, each with its own decorative icon.
const capabilityGroups: {
  code: string;
  tag: string;
  tone: StatusTone;
  mode: string;
  modeIcon: IconName;
  items: Capability[];
}[] = [
  { code: "READS", tag: "Reads", tone: "blue", mode: "No confirmation", modeIcon: "eye", items: reads },
  { code: "WRITES", tag: "Writes", tone: "green", mode: "Automatic", modeIcon: "sparkles", items: writes },
  { code: "CONFIRM_FIRST", tag: "Confirm first", tone: "amber", mode: "You approve", modeIcon: "shield", items: gated },
];

const securityPoints = computed<{ icon: IconName; title: string; body: string }[]>(() => [
  {
    icon: "lock",
    title: "Your login, nothing new",
    body: "Access is granted through the same admin sign-in you already use — no separate password or API key to manage.",
  },
  {
    icon: "user-pen",
    title: "Admin-only",
    body: "Only an admin account can connect and drive the assistant. Non-admin sessions are refused.",
  },
  {
    icon: "badge-check",
    title: "Consent + revocable",
    body: "Every connection passes an explicit consent screen, and access tokens can be revoked at any time.",
  },
]);
</script>

<template>
  <div class="mcp-page">
    <PageHeader eyebrow="06 / Admin" title="AI Assistant">
      <template #lede>
        Connect Claude to your billing so you can create invoices, answer
        questions, and run your books by just asking.
      </template>
    </PageHeader>

    <!-- Hero (mockup): deep red block, gold eyebrow, Manrope headline. -->
    <section class="hero">
      <div class="hero-copy">
        <span class="hero-eyebrow">Model Context Protocol</span>
        <h2>Talk to <em>your books.</em></h2>
        <p>
          Once connected, tell Claude what you want in plain English &mdash;
          <em>&ldquo;invoice Acme for 40 hours of design at $100/hr&rdquo;</em>
          &mdash; and it drives this system directly, through the same rules and
          ledger you rely on here.
        </p>
      </div>
      <div class="hero-glyph" aria-hidden="true">
        <AppIcon name="sparkles" :size="40" />
      </div>
    </section>

    <!-- Connect: URL on the left, numbered steps on the right. -->
    <section class="connect">
      <div class="connect-url">
        <h3 class="connect-title">Connect Claude Desktop</h3>
        <p class="section-sub">Add this as a custom connector, then sign in once.</p>

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
            <AppIcon :name="copied ? 'check' : 'copy'" />
            <span>{{ copied ? "Copied" : "Copy" }}</span>
          </button>
        </div>
        <p class="url-hint">
          Running locally? Use <code>{{ DEV_CONNECTOR_URL }}</code> instead.
        </p>
      </div>

      <ol class="steps">
        <li v-for="(step, i) in steps" :key="i" class="step">
          <span class="step-num">{{ String(i + 1).padStart(2, "0") }}</span>
          <div class="step-body">
            <div class="step-title">{{ step.title }}</div>
            <div class="step-detail">{{ step.body }}</div>
          </div>
        </li>
      </ol>
    </section>

    <!-- What it can do -->
    <section class="capabilities">
      <div v-for="g in capabilityGroups" :key="g.tag" class="cap-col card" data-testid="ai-capability">
        <div class="cap-head">
          <StatusTag :status="g.code" :tone="g.tone" :label="g.tag" />
          <span class="cap-note"><AppIcon :name="g.modeIcon" :size="13" />{{ g.mode }}</span>
        </div>
        <div v-for="c in g.items" :key="c.label" class="cap-item">
          <span class="cap-icon" :class="`tone-${g.tone}`"><AppIcon :name="c.icon" :size="16" /></span>
          <div>
            <div class="cap-label">{{ c.label }}</div>
            <div class="cap-detail">{{ c.detail }}</div>
          </div>
        </div>
      </div>
    </section>

    <!-- Security -->
    <section class="card security">
      <h3 class="card-title">How access works</h3>
      <p class="section-sub">Secured through your existing login &mdash; nothing new to manage.</p>
      <div class="sec-grid">
        <div v-for="s in securityPoints" :key="s.title" class="sec-item">
          <AppIcon :name="s.icon" />
          <div class="sec-title">{{ s.title }}</div>
          <div class="sec-body">{{ s.body }}</div>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.mcp-page { display: flex; flex-direction: column; }
.mcp-page :deep(.page-head) { margin-bottom: 32px; }

/* ---- hero ---- */
.hero {
  background: var(--wb-red-deep);
  color: #fff;
  padding: 48px 48px 44px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 32px;
  align-items: center;
}
.hero-copy { max-width: 620px; }
.hero-eyebrow {
  font: 500 12px/1.6 var(--wb-font-sans);
  letter-spacing: 1.5px;
  text-transform: uppercase;
  color: var(--wb-gold);
}
.hero h2 {
  font: 500 52px/1.05 var(--wb-font-display);
  letter-spacing: -2.2px;
  color: #fff;
  margin: 12px 0 0;
}
.hero h2 em {
  font-family: Georgia, "Times New Roman", serif;
  font-weight: 400;
  color: var(--wb-gold);
  letter-spacing: -1.5px;
}
.hero p { margin: 20px 0 0; font-size: 17px; line-height: 1.6; color: rgba(255, 255, 255, 0.88); }
.hero p em { color: #fff; }
.hero-glyph {
  width: 120px;
  height: 120px;
  border: 1px solid rgba(255, 255, 255, 0.22);
  display: grid;
  place-items: center;
  color: var(--wb-gold);
}

/* ---- connect ---- */
.connect {
  background: var(--wb-surface);
  border: 1px solid var(--wb-line);
  border-top: 0;
  padding: 36px 48px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 48px;
}
.connect-title {
  margin: 0;
  font: 500 22px/1.2 var(--wb-font-display);
  letter-spacing: -0.6px;
  color: var(--wb-ink);
}
.section-sub { margin: 4px 0 0; color: var(--wb-muted); font-size: 14px; }
.url-label {
  display: block;
  font: 500 11px/1.4 var(--wb-font-sans);
  letter-spacing: 1.3px;
  text-transform: uppercase;
  color: var(--wb-muted-soft);
  margin: 28px 0 8px;
}
.url-row { display: flex; }
.url-input {
  flex: 1;
  min-width: 0;
  height: 44px;
  font: 500 14px var(--wb-font-mono);
  color: var(--wb-ink);
  background: var(--wb-surface-2);
  border: 1px solid var(--wb-line);
  border-right: 0;
  border-radius: 0;
  padding: 0 14px;
}
.url-input:focus { outline: none; box-shadow: var(--focus-ring); }
.copy-btn {
  flex-shrink: 0;
  height: 44px;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 0 20px;
  border: 0;
  border-radius: 0;
  background: var(--wb-ink);
  color: var(--wb-bg);
  font: 500 14px var(--wb-font-sans);
  cursor: pointer;
}
.copy-btn:hover { background: var(--wb-red-deep); color: #fff; }
.copy-btn:focus-visible { outline: 1px solid var(--wb-red); outline-offset: 2px; }
.copy-btn.copied { background: var(--wb-green-fg); color: var(--wb-on-tone); }
.url-hint { margin: 10px 0 0; font-size: 13px; color: var(--wb-muted); }
.url-hint code {
  font: 500 12px var(--wb-font-mono);
  background: var(--wb-tint);
  padding: 2px 6px;
  color: var(--wb-ink);
}

/* ---- steps ---- */
.steps { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; }
.step {
  display: grid;
  grid-template-columns: 36px 1fr;
  gap: 16px;
  padding: 16px 0;
  border-bottom: 1px solid var(--wb-line);
}
.step:first-child { padding-top: 0; }
.step-num { font: 500 15px var(--wb-font-display); color: var(--wb-red); font-variant-numeric: tabular-nums; }
.step-title { font-weight: 500; color: var(--wb-ink); }
.step-detail { color: var(--wb-muted); font-size: 14px; line-height: 1.5; margin-top: 3px; }

/* ---- capabilities ---- */
.capabilities {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 24px;
  margin-top: 24px;
}
.cap-col { padding: 28px; }
.cap-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.tone-blue { background: var(--wb-blue-bg); color: var(--wb-blue-fg); }
.tone-green { background: var(--wb-green-bg); color: var(--wb-green-fg); }
.tone-amber { background: var(--wb-amber-bg); color: var(--wb-amber-fg); }
.cap-note {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font: 500 11px var(--wb-font-sans);
  letter-spacing: 1.2px;
  text-transform: uppercase;
  color: var(--wb-muted-soft);
}
.cap-item { margin-top: 22px; display: grid; grid-template-columns: 32px minmax(0, 1fr); gap: 14px; align-items: start; }
.cap-icon { width: 32px; height: 32px; display: grid; place-items: center; }
.cap-label { font-weight: 500; font-size: 16px; color: var(--wb-ink); }
.cap-detail { color: var(--wb-muted); font-size: 14px; line-height: 1.55; margin-top: 4px; }

/* ---- security (today's content; not in the mockup) ---- */
.security { padding: 28px; margin-top: 24px; }
.sec-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 24px; margin-top: 20px; }
.sec-item { padding: 20px; border: 1px solid var(--wb-line); background: var(--wb-surface-2); }
.sec-item > .app-icon { color: var(--wb-red); width: 18px; height: 18px; margin-bottom: 10px; display: block; }
.sec-title { font-weight: 500; font-size: 15px; color: var(--wb-ink); margin-bottom: 4px; }
.sec-body { color: var(--wb-muted); font-size: 14px; line-height: 1.5; }

@media (max-width: 900px) {
  .connect { grid-template-columns: 1fr; gap: 32px; padding: 28px; }
  .sec-grid { grid-template-columns: 1fr; }
  .hero { padding: 36px 28px; }
  .hero h2 { font-size: 40px; letter-spacing: -1.6px; }
}
@media (max-width: 720px) {
  .hero { grid-template-columns: 1fr; }
  .hero-glyph { display: none; }
  .hero h2 { font-size: 34px; letter-spacing: -1.2px; }
  .hero p { font-size: 15px; }
  .connect { padding: 20px; }
  .cap-col, .security { padding: 20px; }
}
</style>
