<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import {
  NAlert,
  NButton,
  NCard,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSwitch,
  useMessage
} from 'naive-ui'
import { aiSyncApi } from '@/services/api'
import ConversationSyncPanel from '@/components/ai-sync/ConversationSyncPanel.vue'
import KeyboardShortcutsPanel from '@/components/settings/KeyboardShortcutsPanel.vue'
import type { AIProvider, AISyncSetup, AISyncStatus, EmailAccountInput } from '@/types'

type SettingsTab = 'shortcuts' | 'ai-sync'

const props = withDefaults(defineProps<{
  show: boolean
  initialTab?: SettingsTab
  syncing?: boolean
  emailStatus?: AISyncStatus | null
}>(), {
  initialTab: 'shortcuts',
  syncing: false,
  emailStatus: null
})

const emit = defineEmits<{
  (event: 'update:show', value: boolean): void
  (event: 'saved'): void
  (event: 'email-sync'): void
  (event: 'cancel-email-sync'): void
}>()

const message = useMessage()
const loading = ref(false)
const saving = ref(false)
const testing = ref(false)
const loadError = ref('')
const setup = ref<AISyncSetup | null>(null)
const activeTab = ref<SettingsTab>('shortcuts')
const shortcutRecording = ref(false)
const tabButtons = ref<HTMLButtonElement[]>([])
const tabs: { id: SettingsTab; label: string }[] = [
  { id: 'shortcuts', label: 'Keyboard shortcuts' },
  { id: 'ai-sync', label: 'AI Sync' }
]
let aiLoadedForOpen = false

const email = reactive<EmailAccountInput>({
  address: '',
  username: '',
  imapHost: '',
  imapPort: 993,
  folder: 'INBOX',
  useTLS: true,
  secret: ''
})

const agent = reactive({
  provider: 'codex' as AIProvider,
  codexPath: '',
  claudePath: ''
})

const providerOptions = [
  { label: 'Codex', value: 'codex' },
  { label: 'Claude Code', value: 'claude' }
]

const selectedAvailability = computed(() => {
  return setup.value?.agent.agents.find(item => item.provider === agent.provider)
})

const selectedPath = computed({
  get: () => agent.provider === 'codex' ? agent.codexPath : agent.claudePath,
  set: value => {
    if (agent.provider === 'codex') agent.codexPath = value
    else agent.claudePath = value
  }
})

const emailSummary = computed(() => {
  const status = props.emailStatus
  if (!status || status.state === 'idle') return ''
  if (status.state === 'running') {
    const parts = ['Email sync running']
    if (status.emailCount) parts.push(`${status.emailCount} recent emails`)
    if (status.readEmails) parts.push(`${status.readEmails} bodies read`)
    return parts.join(' · ')
  }
  if (status.state === 'succeeded') {
    return `${status.created} created · ${status.skipped} skipped${status.rejected ? ` · ${status.rejected} rejected` : ''}`
  }
  return status.message || `Email sync ${status.state}`
})

watch(() => props.show, show => {
  if (!show) return
  activeTab.value = props.initialTab
  aiLoadedForOpen = false
  if (activeTab.value === 'ai-sync') openAIConfig()
}, { immediate: true })

watch(() => props.initialTab, tab => {
  if (!props.show) return
  activeTab.value = tab
  if (tab === 'ai-sync') ensureAIConfig()
})

function activateTab(tab: SettingsTab) {
  activeTab.value = tab
  if (tab === 'ai-sync') ensureAIConfig()
}

function handleTabKeydown(event: KeyboardEvent, index: number) {
  let nextIndex = index
  if (event.key === 'ArrowRight') nextIndex = (index + 1) % tabs.length
  else if (event.key === 'ArrowLeft') nextIndex = (index - 1 + tabs.length) % tabs.length
  else if (event.key === 'Home') nextIndex = 0
  else if (event.key === 'End') nextIndex = tabs.length - 1
  else return

  event.preventDefault()
  const nextTab = tabs[nextIndex]
  activateTab(nextTab.id)
  nextTick(() => tabButtons.value[nextIndex]?.focus())
}

function openAIConfig() {
  aiLoadedForOpen = true
  void loadSetup()
}

function ensureAIConfig() {
  if (!aiLoadedForOpen || loadError.value) openAIConfig()
}

async function loadSetup() {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    const result = await aiSyncApi.getSetup()
    setup.value = result
    Object.assign(email, {
      address: result.email.address,
      username: result.email.username,
      imapHost: result.email.imapHost,
      imapPort: result.email.imapPort || 993,
      folder: result.email.folder || 'INBOX',
      useTLS: result.email.useTLS,
      secret: ''
    })
    Object.assign(agent, result.agent.settings)
  } catch (error) {
    loadError.value = errorMessage(error, 'Could not load AI Sync settings')
  } finally {
    loading.value = false
  }
}

function applyEmailPreset() {
  const domain = email.address.trim().toLowerCase().split('@')[1]
  if (!domain) return
  const presets: Record<string, string> = {
    'qq.com': 'imap.qq.com',
    'gmail.com': 'imap.gmail.com',
    'outlook.com': 'outlook.office365.com',
    'hotmail.com': 'outlook.office365.com',
    'live.com': 'outlook.office365.com',
    'icloud.com': 'imap.mail.me.com'
  }
  if (!email.username) email.username = email.address.trim()
  if (!email.imapHost && presets[domain]) email.imapHost = presets[domain]
}

async function testConnection() {
  if (props.syncing) return
  testing.value = true
  try {
    applyEmailPreset()
    await aiSyncApi.testEmail({ ...email })
    message.success('Email connection succeeded')
  } catch (error) {
    message.error(errorMessage(error, 'Email connection failed'))
  } finally {
    testing.value = false
  }
}

async function save() {
  if (props.syncing) return
  saving.value = true
  try {
    applyEmailPreset()
    await aiSyncApi.saveEmail({ ...email })
    await aiSyncApi.saveSettings({
      provider: agent.provider,
      codexPath: agent.codexPath.trim(),
      claudePath: agent.claudePath.trim()
    })
    message.success('AI Sync settings saved')
    emit('saved')
    emit('update:show', false)
  } catch (error) {
    message.error(errorMessage(error, 'Could not save AI Sync settings'))
  } finally {
    saving.value = false
  }
}

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error) return error.message
  if (typeof error === 'string' && error) return error
  return fallback
}

function dismiss(): boolean {
  if (shortcutRecording.value || saving.value || testing.value) return false
  emit('update:show', false)
  return true
}

defineExpose({ dismiss })
</script>

<template>
  <n-modal
    :show="show"
    :mask-closable="!saving && !testing"
    :close-on-esc="!shortcutRecording && !saving && !testing"
    @update:show="value => emit('update:show', value)"
  >
    <n-card
      class="settings-card"
      role="dialog"
      aria-modal="true"
      aria-labelledby="settings-title"
      :bordered="false"
      closable
      :closable-disabled="saving || testing"
      @close="emit('update:show', false)"
    >
      <template #header><h2 id="settings-title">Settings</h2></template>

      <div class="settings-tabs" role="tablist" aria-label="Settings">
        <button
          v-for="(tab, index) in tabs"
          :key="tab.id"
          ref="tabButtons"
          type="button"
          role="tab"
          class="settings-tab"
          :id="`settings-tab-${tab.id}`"
          :aria-selected="activeTab === tab.id"
          :aria-controls="`settings-panel-${tab.id}`"
          :tabindex="activeTab === tab.id ? 0 : -1"
          @click="activateTab(tab.id)"
          @keydown="handleTabKeydown($event, index)"
        >
          {{ tab.label }}
        </button>
      </div>

      <section
        id="settings-panel-shortcuts"
        v-show="activeTab === 'shortcuts'"
        class="settings-panel"
        role="tabpanel"
        aria-labelledby="settings-tab-shortcuts"
        tabindex="0"
      >
        <keyboard-shortcuts-panel @recording="shortcutRecording = $event" />
      </section>

      <section
        id="settings-panel-ai-sync"
        v-show="activeTab === 'ai-sync'"
        class="settings-panel"
        role="tabpanel"
        aria-labelledby="settings-tab-ai-sync"
        tabindex="0"
      >
        <p class="ai-tab-intro">
          Codex conversations and email sync are independent sources. Configure either one below.
        </p>

        <conversation-sync-panel :active="show && activeTab === 'ai-sync'" />

        <n-alert v-if="syncing" type="info" :show-icon="false" class="sync-notice">
          Cancel email sync before saving email settings.
        </n-alert>

        <div v-if="loading" class="loading-state">Loading email configuration…</div>
        <n-alert v-else-if="loadError" type="error" :show-icon="false">{{ loadError }}</n-alert>

        <form v-else class="settings-form" @submit.prevent="save">
          <section class="settings-section">
            <div class="section-heading">
              <div>
                <h3>Email source</h3>
                <p>Standard IMAP. Message bodies stay out of catendar's database.</p>
              </div>
            </div>

            <div class="field-grid two-columns">
              <label class="field">
                <span>Email address</span>
                <n-input v-model:value="email.address" placeholder="you@example.com" @blur="applyEmailPreset" />
              </label>
              <label class="field">
                <span>Username</span>
                <n-input v-model:value="email.username" placeholder="Defaults to email address" />
              </label>
            </div>

            <div class="field-grid server-grid">
              <label class="field host-field">
                <span>IMAP host</span>
                <n-input v-model:value="email.imapHost" placeholder="imap.example.com" />
              </label>
              <label class="field">
                <span>Port</span>
                <n-input-number v-model:value="email.imapPort" :min="1" :max="65535" />
              </label>
              <label class="field">
                <span>Folder</span>
                <n-input v-model:value="email.folder" placeholder="INBOX" />
              </label>
            </div>

            <div class="credential-row">
              <label class="field secret-field">
                <span>Password / app authorization code</span>
                <n-input
                  v-model:value="email.secret"
                  type="password"
                  show-password-on="click"
                  :placeholder="setup?.email.hasCredential ? 'Leave blank to keep saved credential' : 'Stored in the system keychain'"
                />
              </label>
              <label class="tls-control">
                <span>TLS</span>
                <n-switch v-model:value="email.useTLS" />
              </label>
            </div>

            <div class="section-action">
              <n-button :loading="testing" :disabled="saving || syncing" @click="testConnection">Test connection</n-button>
              <n-button
                v-if="syncing"
                attr-type="button"
                @click="emit('cancel-email-sync')"
              >
                Cancel sync
              </n-button>
              <n-button
                v-else
                attr-type="button"
                type="primary"
                secondary
                :disabled="saving || testing"
                @click="emit('email-sync')"
              >
                Sync email
              </n-button>
              <span v-if="emailSummary" class="email-sync-state">{{ emailSummary }}</span>
              <span v-if="setup?.email.configured" class="configured-mark">Credential saved in keychain</span>
            </div>
          </section>

          <section class="settings-section">
            <div class="section-heading">
              <div>
                <h3>Email agent</h3>
                <p>The selected CLI is used for email sync. Codex conversations use the Codex path in the panel above.</p>
              </div>
            </div>

            <div class="field-grid two-columns agent-grid">
              <label class="field">
                <span>Agent</span>
                <n-select v-model:value="agent.provider" :options="providerOptions" />
              </label>
              <label class="field">
                <span>Executable path <small>optional</small></span>
                <n-input v-model:value="selectedPath" placeholder="Auto detect" />
              </label>
            </div>

            <div class="agent-status" :class="{ ready: selectedAvailability?.installed }">
              <span class="status-dot" />
              <span v-if="selectedAvailability?.installed">
                {{ selectedAvailability.version || selectedAvailability.path }}
              </span>
              <span v-else>{{ selectedAvailability?.error || 'CLI not detected' }}</span>
            </div>
          </section>

          <div class="privacy-note">
            Email credentials remain in catendar Core. Email text selected for planning is sent to the chosen agent's model service.
          </div>

          <footer class="form-actions">
            <n-button :disabled="saving || testing" @click="emit('update:show', false)">Cancel</n-button>
            <n-button type="primary" attr-type="submit" :loading="saving" :disabled="testing || syncing">Save settings</n-button>
          </footer>
        </form>
      </section>
    </n-card>
  </n-modal>
</template>

<style scoped>
.settings-card {
  width: min(680px, calc(100vw - 28px));
  max-height: calc(100vh - 24px);
}

.settings-card :deep(.n-card__content) {
  overflow-y: auto;
}

#settings-title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 22px;
  letter-spacing: -0.035em;
}

.settings-tabs {
  display: flex;
  gap: 3px;
  margin-bottom: 16px;
  padding: 3px;
  border-radius: 10px;
  background: var(--bg-secondary);
}

.settings-tab {
  flex: 1;
  padding: 6px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  font-weight: 600;
}

.settings-tab[aria-selected='true'] {
  background: var(--bg-elevated);
  box-shadow: var(--shadow-sm);
  color: var(--text-primary);
}

.settings-tab:focus-visible,
.settings-panel:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.settings-panel {
  border-radius: 8px;
}

.ai-tab-intro {
  margin: 0 0 12px;
  color: var(--text-tertiary);
  font-size: 11px;
  line-height: 1.5;
}

.sync-notice {
  margin-bottom: 12px;
}

.loading-state {
  padding: 48px 0;
  color: var(--text-tertiary);
  text-align: center;
}

.settings-form {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.settings-section {
  padding: 16px;
  border: 1px solid var(--border-soft);
  border-radius: 12px;
  background: color-mix(in srgb, var(--bg-secondary) 55%, transparent);
}

.section-heading {
  display: flex;
  align-items: flex-start;
  gap: 11px;
  margin-bottom: 15px;
}

.section-heading h3,
.section-heading p {
  margin: 0;
}

.section-heading h3 {
  font-size: 14px;
  font-weight: 700;
}

.section-heading p {
  margin-top: 2px;
  color: var(--text-tertiary);
  font-size: 11px;
}

.field-grid {
  display: grid;
  gap: 12px;
}

.two-columns {
  grid-template-columns: 1fr 1fr;
  margin-bottom: 12px;
}

.server-grid {
  grid-template-columns: minmax(0, 1.6fr) 100px minmax(110px, 0.7fr);
  margin-bottom: 12px;
}

.field {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 6px;
}

.field > span,
.tls-control > span {
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 650;
}

.field small {
  color: var(--text-tertiary);
  font-size: 9px;
  font-weight: 500;
}

.credential-row {
  display: flex;
  align-items: end;
  gap: 12px;
}

.secret-field {
  flex: 1;
}

.tls-control {
  display: flex;
  height: 34px;
  align-items: center;
  gap: 8px;
  padding: 0 9px;
  border: 1px solid var(--border-soft);
  border-radius: 8px;
  background: var(--bg-elevated);
}

.section-action {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 12px;
  flex-wrap: wrap;
}

.configured-mark {
  color: var(--text-tertiary);
  font-size: 10px;
}

.email-sync-state {
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-grid {
  margin-bottom: 10px;
}

.agent-status {
  display: flex;
  align-items: center;
  gap: 7px;
  color: var(--danger-color);
  font-size: 10px;
}

.agent-status.ready {
  color: #3d8b62;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 0 3px color-mix(in srgb, currentColor 13%, transparent);
}

.privacy-note {
  padding: 10px 12px;
  border-left: 2px solid var(--primary-color);
  background: var(--primary-muted);
  color: var(--text-secondary);
  font-size: 11px;
  line-height: 1.5;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 2px;
}

@media (max-width: 620px) {
  .two-columns,
  .server-grid {
    grid-template-columns: 1fr;
  }

  .credential-row {
    align-items: stretch;
    flex-direction: column;
  }

  .tls-control {
    width: fit-content;
  }
}
</style>
