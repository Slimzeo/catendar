import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import type { AISyncState, ChatActivity, ConversationStatus } from '@/types'
import { conversationApi } from '@/services/api'
import { useEventsStore } from '@/stores/events'
import { useCalendarStore, type CalendarView } from './calendar'
import { useSyncSourcesStore } from './syncSources'

function emptyStatus(): ConversationStatus {
  return {
    state: 'idle',
    message: '',
    codexPath: '',
    runId: '',
    lastSyncedAt: '',
    sources: 0,
    created: 0,
    updated: 0,
    pending: 0,
    skipped: 0
  }
}

function errorMessage(cause: unknown, fallback: string): string {
  if (cause instanceof Error) return cause.message
  if (typeof cause === 'string' && cause) return cause
  return fallback
}

function isTerminal(state: AISyncState): boolean {
  return state === 'succeeded' || state === 'failed' || state === 'cancelled'
}

function copyActivity(activity: ChatActivity): ChatActivity {
  return {
    ...activity,
    change: {
      ...activity.change,
      source: { ...activity.change.source }
    }
  }
}

export const useConversationSyncStore = defineStore('conversationSync', () => {
  const sources = useSyncSourcesStore()
  const calendar = useCalendarStore()
  const runOrigin = ref<CalendarView | null>(null)
  const status = ref<ConversationStatus>(emptyStatus())
  const activities = ref<ChatActivity[]>([])
  const loading = ref(false)
  const activityLoading = ref(false)
  const error = ref('')
  const starting = ref(false)
  const cancelling = ref(false)
  const saving = ref(false)
  const resolving = ref<number[]>([])
  const supported = conversationApi.supported()

  let pollTimer: ReturnType<typeof setTimeout> | null = null
  let disposed = false
  let initialized = false
  let inFlight = false
  let statusRequest = 0
  let activityRequest = 0
  let appliedStatusRequest = 0
  let appliedActivityRequest = 0
  // Bumped whenever a mutation starts or settles so reads started around it
  // cannot replace the newer state with a stale snapshot.
  let mutation = 0

  const running = computed(() => status.value.state === 'running')
  const busy = computed(() => (
    starting.value || cancelling.value || saving.value || resolving.value.length > 0
  ))
  watch([running, () => sources.allowsRun('codex', runOrigin.value)], ([active, allowed]) => {
    if (active && !allowed) void cancel()
  })
  const pendingActivities = computed(() => activities.value.filter(item => item.state === 'pending'))
  const pendingCount = computed(() => Math.max(status.value.pending, pendingActivities.value.length))
  const recentActivities = computed(() => activities.value.filter(item => item.state !== 'pending').slice(0, 8))

  function applyStatus(next: ConversationStatus) {
    const previous = status.value
    status.value = next
    if (initialized) {
      // A partial batch may have landed while the run is still going, and every
      // terminal path (including cancel) can change what the board should show.
      const countsChanged = next.created !== previous.created || next.updated !== previous.updated
      const reachedTerminal = isTerminal(next.state) && next.state !== previous.state
      if (countsChanged || reachedTerminal) void useEventsStore().fetchEvents()
      if (next.pending > previous.pending && !busy.value) void loadActivities()
    }
    initialized = true
  }

  async function readActivities() {
    const request = ++activityRequest
    const mutationAtStart = mutation
    activityLoading.value = true
    try {
      const rows = await conversationApi.activity()
      if (mutationAtStart !== mutation || request < appliedActivityRequest) return
      appliedActivityRequest = request
      activities.value = rows
      if (status.value.pending < pendingActivities.value.length) {
        status.value = { ...status.value, pending: pendingActivities.value.length }
      }
    } catch (cause) {
      if (mutationAtStart === mutation && request >= appliedActivityRequest && supported) {
        error.value = errorMessage(cause, 'Could not load Codex conversation activity')
      }
    } finally {
      if (request === activityRequest) activityLoading.value = false
    }
  }

  async function loadActivities() {
    if (busy.value) return
    await readActivities()
  }

  async function refresh(options: { silent?: boolean; activities?: boolean } = {}) {
    if (inFlight || busy.value) return
    inFlight = true
    const request = ++statusRequest
    const mutationAtStart = mutation
    if (!options.silent) loading.value = true
    try {
      const next = await conversationApi.status()
      if (mutationAtStart !== mutation || request < appliedStatusRequest) return
      appliedStatusRequest = request
      applyStatus(next)
      error.value = ''
      if (options.activities) await readActivities()
    } catch (cause) {
      if (mutationAtStart === mutation && supported) {
        error.value = errorMessage(cause, 'Could not read Codex conversation sync')
      }
    } finally {
      loading.value = false
      inFlight = false
      schedule()
    }
  }

  async function start() {
    if (!sources.active.codex || running.value || starting.value) return
    runOrigin.value = calendar.viewMode
    const token = ++mutation
    starting.value = true
    error.value = ''
    try {
      const next = await conversationApi.start()
      if (token !== mutation) return
      applyStatus(next)
      await readActivities()
      schedule()
    } catch (cause) {
      if (token === mutation) {
        error.value = errorMessage(cause, 'Could not start Codex conversation sync')
      }
    } finally {
      starting.value = false
      mutation += 1
      schedule()
    }
  }

  async function cancel() {
    if (!running.value || cancelling.value) return
    const token = ++mutation
    cancelling.value = true
    error.value = ''
    try {
      await conversationApi.cancel()
      if (token !== mutation) return
      const next = await conversationApi.status()
      if (token !== mutation) return
      applyStatus(next)
      await readActivities()
    } catch (cause) {
      if (token === mutation) {
        error.value = errorMessage(cause, 'Could not cancel Codex conversation sync')
      }
    } finally {
      cancelling.value = false
      mutation += 1
      schedule()
    }
  }

  async function saveSettings(codexPath: string) {
    if (saving.value) return
    const token = ++mutation
    saving.value = true
    error.value = ''
    try {
      const next = await conversationApi.saveSettings(codexPath)
      if (token !== mutation) return
      applyStatus(next)
    } catch (cause) {
      if (token === mutation) {
        error.value = errorMessage(cause, 'Could not save Codex conversation settings')
      }
      throw cause
    } finally {
      saving.value = false
      mutation += 1
      schedule()
    }
  }

  function isResolving(id: number): boolean {
    return resolving.value.includes(id)
  }

  async function resolveActivity(id: number, accept: boolean): Promise<ChatActivity> {
    if (isResolving(id)) throw new Error('This conversation change is already being resolved')
    const token = ++mutation
    const previous = activities.value.find(item => item.id === id)
    resolving.value = [...resolving.value, id]
    error.value = ''
    try {
      const updated = await conversationApi.resolve(id, accept)
      // The settled activity is authoritative even if another action (for
      // example cancelling the run) began while the resolution was in flight.
      const index = activities.value.findIndex(item => item.id === id)
      if (index >= 0) activities.value[index] = updated
      else activities.value = [updated, ...activities.value]
      if (previous?.state === 'pending' && updated.state !== 'pending' && status.value.pending > 0) {
        status.value = { ...status.value, pending: status.value.pending - 1 }
      }
      if (updated.state === 'applied') void useEventsStore().fetchEvents()
      if (token === mutation) {
        try {
          const next = await conversationApi.status()
          if (token === mutation) applyStatus(next)
        } catch {
          // The returned activity already carries the genuine outcome.
        }
      }
      return copyActivity(updated)
    } catch (cause) {
      if (token === mutation) {
        error.value = errorMessage(cause, 'Could not resolve the conversation change')
      }
      throw cause
    } finally {
      resolving.value = resolving.value.filter(item => item !== id)
      mutation += 1
      schedule()
    }
  }

  function schedule(delay = 1500) {
    if (disposed) return
    if (pollTimer) clearTimeout(pollTimer)
    pollTimer = null
    // Local status is only useful while a run is in progress. Idle or finished
    // stores stay quiet until the next explicit action or refresh.
    if (status.value.state !== 'running') return
    pollTimer = setTimeout(() => {
      pollTimer = null
      void poll()
    }, delay)
  }

  async function poll() {
    if (disposed) return
    await refresh({ silent: true })
  }

  function stopPolling() {
    disposed = true
    if (pollTimer) clearTimeout(pollTimer)
    pollTimer = null
  }

  return {
    status,
    activities,
    loading,
    activityLoading,
    error,
    starting,
    cancelling,
    saving,
    resolving,
    supported,
    running,
    busy,
    pendingActivities,
    pendingCount,
    recentActivities,
    loadActivities,
    refresh,
    start,
    cancel,
    saveSettings,
    isResolving,
    resolveActivity,
    stopPolling
  }
})
