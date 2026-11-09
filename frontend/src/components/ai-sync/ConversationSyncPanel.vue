<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { format } from 'date-fns'
import { NButton, NInput, useMessage } from 'naive-ui'
import type { ChatActivity } from '@/types'
import { useConversationSyncStore } from '@/stores/conversationSync'
import { useWorkspacesStore } from '@/stores/workspaces'

const props = withDefaults(defineProps<{
  active?: boolean
}>(), {
  active: true
})

const conversationStore = useConversationSyncStore()
const workspacesStore = useWorkspacesStore()
const message = useMessage()
const codexPath = ref('')
const dirty = ref(false)

const stateLabel = computed(() => {
  switch (conversationStore.status.state) {
    case 'running': return 'Syncing'
    case 'succeeded': return 'Synced'
    case 'failed': return 'Failed'
    case 'cancelled': return 'Cancelled'
    default: return 'Idle'
  }
})

const summary = computed(() => {
  const status = conversationStore.status
  const parts: string[] = []
  if (status.sources) parts.push(`${status.sources} messages`)
  if (status.created) parts.push(`${status.created} created`)
  if (status.updated) parts.push(`${status.updated} updated`)
  if (status.pending) parts.push(`${status.pending} pending`)
  if (status.skipped) parts.push(`${status.skipped} skipped`)
  return parts.length ? parts.join(' · ') : 'No conversation changes yet.'
})

const lastSyncLabel = computed(() => (
  conversationStore.status.lastSyncedAt
    ? `Last sync ${formatTimestamp(conversationStore.status.lastSyncedAt)}`
    : 'Not synced yet'
))

watch(() => conversationStore.status, status => {
  if (dirty.value) return
  codexPath.value = status.codexPath
}, { immediate: true, deep: true })

watch(() => props.active, active => {
  if (active) void load()
})

onMounted(() => {
  if (props.active) void load()
})

async function load() {
  await conversationStore.refresh({ activities: true })
  if (!conversationStore.activities.length) await conversationStore.loadActivities()
}

function formatTimestamp(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : format(date, 'MMM d, HH:mm')
}

function formatSourceTime(seconds: number): string {
  if (!seconds) return ''
  const date = new Date(seconds * 1000)
  return Number.isNaN(date.getTime()) ? '' : format(date, 'MMM d, HH:mm')
}

function workspaceName(id: number): string {
  return workspacesStore.workspaceName(id)
}

function phaseLabel(value: string): string {
  return value || 'todo'
}

function activityStateLabel(activity: ChatActivity): string {
  if (activity.state === 'applied') return 'Applied'
  if (activity.state === 'dismissed') return 'Kept current'
  return 'Needs review'
}

function markDirty() {
  dirty.value = true
}

async function saveSettings() {
  try {
    await conversationStore.saveSettings(codexPath.value.trim())
    dirty.value = false
    message.success('Codex conversation settings saved')
  } catch {
    message.error(conversationStore.error || 'Could not save Codex conversation settings')
  }
}

async function startSync() {
  await conversationStore.start()
  if (conversationStore.status.state === 'failed' && conversationStore.status.message) {
    message.error(conversationStore.status.message)
  }
}

async function cancelSync() {
  await conversationStore.cancel()
}

async function resolve(activity: ChatActivity, accept: boolean) {
  try {
    const updated = await conversationStore.resolveActivity(activity.id, accept)
    if (updated.state === 'applied') message.success(updated.reason || 'Change applied')
    else if (updated.state === 'dismissed') message.info(updated.reason || 'Current task kept')
    else message.warning(updated.reason || 'This change could not be applied')
  } catch {
    message.error(conversationStore.error || 'Could not resolve the conversation change')
  }
}
</script>

<template>
  <section class="conversation-panel" aria-labelledby="conversation-title">
    <header class="section-heading">
      <div class="heading-copy">
        <h3 id="conversation-title">Codex conversations</h3>
        <p>
          Click AI Sync to organize the last 72 hours of user messages and final replies with your signed-in Codex. Clear updates go to today’s board.
        </p>
      </div>
      <span class="state-pill" :class="conversationStore.status.state" role="status">
        {{ stateLabel }}
      </span>
    </header>

    <div class="status-row" aria-live="polite">
      <span class="status-message">
        {{ conversationStore.status.message || summary }}
      </span>
      <span class="last-sync">{{ lastSyncLabel }}</span>
    </div>

    <p v-if="!conversationStore.supported" class="preview-note">
      Codex conversation sync runs in the desktop app.
    </p>

    <div class="settings-row">
      <label class="path-field">
        <span>Codex path <small>optional</small></span>
        <n-input
          v-model:value="codexPath"
          size="small"
          placeholder="Auto detect"
          :disabled="!conversationStore.supported || conversationStore.saving"
          @input="markDirty"
        />
      </label>
      <n-button
        attr-type="button"
        size="small"
        :loading="conversationStore.saving"
        :disabled="!conversationStore.supported"
        @click="saveSettings"
      >
        Save
      </n-button>
    </div>

    <p v-if="conversationStore.error" class="panel-error" role="alert">{{ conversationStore.error }}</p>

    <div class="run-row">
      <n-button
        v-if="conversationStore.running"
        attr-type="button"
        size="small"
        :loading="conversationStore.cancelling"
        @click="cancelSync"
      >
        Cancel
      </n-button>
      <n-button
        v-else
        attr-type="button"
        type="primary"
        size="small"
        :loading="conversationStore.starting"
        :disabled="!conversationStore.supported || conversationStore.status.state === 'running'"
        @click="startSync"
      >
        Sync conversations
      </n-button>
      <span class="run-summary">{{ summary }}</span>
    </div>

    <section class="activity-section" aria-labelledby="conversation-pending-title">
      <h4 id="conversation-pending-title">
        Pending conflicts
        <span class="count">{{ conversationStore.pendingCount }}</span>
      </h4>
      <p v-if="conversationStore.activityLoading && !conversationStore.pendingActivities.length" class="empty-line">
        Checking conversation changes…
      </p>
      <p v-else-if="!conversationStore.pendingActivities.length" class="empty-line">
        No changes need review.
      </p>

      <article
        v-for="activity in conversationStore.pendingActivities"
        :key="activity.id"
        class="activity"
        :data-activity-id="activity.id"
        :data-activity-state="activity.state"
      >
        <div class="activity-head">
          <strong>{{ activity.change.title }}</strong>
          <span class="activity-target">
            {{ workspaceName(activity.change.workspaceId) }} · {{ phaseLabel(activity.change.phase) }}
          </span>
        </div>
        <p class="activity-meta">
          <span v-if="activity.beforePhase">Before {{ activity.beforePhase }} · </span>
          {{ activity.reason || 'This change needs confirmation.' }}
        </p>
        <div v-if="activity.current" class="current-card">
          <strong>Current card</strong> · {{ workspaceName(activity.current.workspaceId) }} · {{ activity.current.phase }}
          <p v-if="activity.change.title !== activity.current.title">{{ activity.current.title }}</p>
          <p v-if="activity.change.description && activity.change.description !== activity.current.description">Description · {{ activity.current.description || 'Empty' }}</p>
          <p v-if="activity.change.result && activity.change.result !== activity.current.result">Result · {{ activity.current.result || 'Empty' }}</p>
        </div>
        <dl v-if="activity.change.description || activity.change.result" class="change-details">
          <div v-if="activity.change.description" class="change-detail">
            <dt>Proposed description</dt>
            <dd class="change-description">{{ activity.change.description }}</dd>
          </div>
          <div v-if="activity.change.result" class="change-detail">
            <dt>Proposed result</dt>
            <dd class="change-result">{{ activity.change.result }}</dd>
          </div>
        </dl>
        <blockquote class="activity-quote full">{{ activity.change.source.quote }}</blockquote>
        <p class="source-line">
          {{ activity.change.source.threadTitle || 'Codex conversation' }}
          <span v-if="activity.change.source.at"> · {{ formatSourceTime(activity.change.source.at) }}</span>
        </p>
        <div class="activity-actions">
          <n-button
            attr-type="button"
            size="tiny"
            type="primary"
            :disabled="conversationStore.resolving.length > 0"
            :aria-label="`Apply ${activity.change.title}`"
            @click="resolve(activity, true)"
          >
            Apply
          </n-button>
          <n-button
            attr-type="button"
            size="tiny"
            :disabled="conversationStore.resolving.length > 0"
            :aria-label="`Keep current ${activity.change.title}`"
            @click="resolve(activity, false)"
          >
            Keep current
          </n-button>
        </div>
      </article>
    </section>

    <section class="activity-section" aria-labelledby="conversation-applied-title">
      <h4 id="conversation-applied-title">Applied changes</h4>
      <p v-if="!conversationStore.recentActivities.length" class="empty-line">No applied changes yet.</p>

      <article
        v-for="activity in conversationStore.recentActivities"
        :key="activity.id"
        class="activity applied"
        :data-activity-id="activity.id"
        :data-activity-state="activity.state"
      >
        <div class="activity-head">
          <strong>{{ activity.change.title }}</strong>
          <span class="activity-target">
            {{ workspaceName(activity.change.workspaceId) }} · {{ phaseLabel(activity.change.phase) }}
          </span>
          <span class="outcome" :class="activity.state">{{ activityStateLabel(activity) }}</span>
        </div>
        <p class="activity-meta">
          <span v-if="activity.beforePhase">Before {{ activity.beforePhase }} · </span>
          {{ activity.reason || 'Applied by conversation sync.' }}
        </p>
        <blockquote class="activity-quote">{{ activity.change.source.quote }}</blockquote>
        <p class="source-line">
          {{ activity.change.source.threadTitle || 'Codex conversation' }}
          <span v-if="activity.change.source.at"> · {{ formatSourceTime(activity.change.source.at) }}</span>
        </p>
      </article>
    </section>
  </section>
</template>

<style scoped>
.conversation-panel {
  margin-bottom: 20px;
  padding: 16px;
  border: 1px solid color-mix(in srgb, var(--primary-color) 24%, var(--border-soft));
  border-radius: 12px;
  background: color-mix(in srgb, var(--primary-muted) 46%, var(--bg-elevated));
}

.section-heading {
  display: flex;
  align-items: flex-start;
  gap: 11px;
  margin-bottom: 10px;
}

.heading-copy {
  min-width: 0;
  flex: 1;
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
  line-height: 1.45;
}

.state-pill {
  flex: 0 0 auto;
  padding: 2px 7px;
  border: 1px solid var(--border-soft);
  border-radius: 999px;
  background: var(--bg-elevated);
  color: var(--text-tertiary);
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.state-pill.running {
  border-color: color-mix(in srgb, var(--primary-color) 38%, var(--border-soft));
  color: var(--primary-color);
}

.state-pill.succeeded {
  color: #3d8b62;
}

.state-pill.failed,
.state-pill.cancelled {
  color: var(--danger-color);
}

.status-row {
  display: flex;
  min-width: 0;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 8px;
  border-radius: 7px;
  background: color-mix(in srgb, var(--bg-secondary) 72%, transparent);
  font-size: 10.5px;
}

.status-message {
  min-width: 0;
  overflow: hidden;
  color: var(--text-secondary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.last-sync {
  flex: 0 0 auto;
  color: var(--text-tertiary);
  font-variant-numeric: tabular-nums;
}

.preview-note {
  margin: 8px 0 0;
  color: var(--text-tertiary);
  font-size: 10.5px;
}

.settings-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  gap: 10px;
  margin-top: 10px;
}

.path-field small {
  color: var(--text-tertiary);
  font-size: 9px;
  line-height: 1.35;
}

.path-field {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
}

.path-field > span {
  color: var(--text-secondary);
  font-size: 10px;
  font-weight: 650;
}

.panel-error {
  margin: 8px 0 0;
  color: var(--danger-color);
  font-size: 10.5px;
  line-height: 1.45;
}

.run-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 9px;
  margin-top: 10px;
}

.run-summary {
  min-width: 0;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: 10px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.activity-section {
  margin-top: 13px;
  padding-top: 10px;
  border-top: 1px solid var(--border-soft);
}

.activity-section h4 {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0 0 6px;
  color: var(--text-secondary);
  font-size: 10.5px;
  font-weight: 700;
  letter-spacing: 0.02em;
}

.count {
  display: inline-grid;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  place-items: center;
  border-radius: 8px;
  background: var(--bg-secondary);
  color: var(--text-tertiary);
  font-size: 9px;
  font-weight: 700;
}

.empty-line {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 10.5px;
}

.activity {
  margin-top: 7px;
  padding: 8px 10px;
  border: 1px solid var(--border-soft);
  border-radius: 9px;
  background: var(--bg-elevated);
}

.activity.applied {
  opacity: 0.82;
}

.activity-head {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 8px;
}

.activity-head strong {
  min-width: 0;
  overflow: hidden;
  color: var(--text-primary);
  font-size: 11.5px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.activity-target {
  flex: 0 0 auto;
  color: var(--primary-color);
  font-size: 9.5px;
  font-weight: 650;
}

.outcome {
  margin-left: auto;
  color: var(--text-tertiary);
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.03em;
  text-transform: uppercase;
}

.outcome.applied {
  color: #3d8b62;
}

.activity-meta {
  margin: 3px 0 0;
  color: var(--text-tertiary);
  font-size: 9.5px;
  line-height: 1.4;
}

.change-details {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 7px 0 0;
}

.change-detail dt {
  margin: 0;
  color: var(--text-tertiary);
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.03em;
  text-transform: uppercase;
}

.change-detail dd {
  margin: 2px 0 0;
  color: var(--text-secondary);
  font-size: 10.5px;
  line-height: 1.45;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.activity-quote {
  margin: 5px 0 0;
  padding: 0 0 0 7px;
  border-left: 2px solid var(--border-strong);
  color: var(--text-secondary);
  font-size: 10.5px;
  line-height: 1.45;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: anywhere;
}

.activity-quote.full {
  display: block;
  overflow: visible;
  -webkit-line-clamp: unset;
  white-space: pre-wrap;
}

.current-card {
  margin: 7px 0;
  padding: 6px 8px;
  border-radius: 6px;
  background: var(--bg-secondary);
  color: var(--text-secondary);
  font-size: 11px;
}
.current-card p { margin: 3px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }

.source-line {
  margin: 4px 0 0;
  color: var(--text-tertiary);
  font-size: 9.5px;
}

.activity-actions {
  display: flex;
  gap: 5px;
  margin-top: 7px;
}

@media (max-width: 620px) {
  .conversation-panel {
    padding: 13px 11px;
  }

  .settings-row {
    grid-template-columns: 1fr;
    align-items: stretch;
  }
}
</style>
