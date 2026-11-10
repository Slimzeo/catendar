import type {
  AISettings,
  AISyncSetup,
  AISyncState,
  AISyncStatus,
  CalendarEvent,
  ChatActivity,
  ConversationStatus,
  EmailAccount,
  EmailAccountInput,
  EmailMessage,
  EventInput,
  EventPhase
} from '@/types'
import { isEventPhase } from '@/constants/eventPhases'

// Wails generates individual functions in frontend/wailsjs/go/main/CalendarApp
import {
  GetEvents,
  GetBoardEvents,
  ReorderBoardEvent,
  CreateEvent,
  UpdateEvent,
  DeleteEvent,
  SetEventPhase,
  SetBoardEventPhase
} from '../../wailsjs/go/main/CalendarApp'
import {
  CancelAISync,
  GetAISyncSetup,
  GetAISyncStatus,
  GetSourceEmail,
  SaveAISettings,
  SaveEmailAccount,
  StartAISync,
  TestEmailAccount,
  GetConversationSync,
  StartConversationSync,
  CancelConversationSync,
  SaveConversationSyncSettings,
  GetConversationActivity,
  ResolveConversationChange
} from '../../wailsjs/go/main/AISyncApp'

type RawEvent = Awaited<ReturnType<typeof CreateEvent>>

function normalizeEvent(raw: RawEvent): CalendarEvent {
  return {
    ...raw,
    phase: isEventPhase(raw.phase) ? raw.phase : 'todo',
    deadline: typeof raw.deadline === 'string' && raw.deadline ? raw.deadline : undefined
  }
}

export const eventApi = {
  getEvents: async (start: string, end: string): Promise<CalendarEvent[]> => {
    if (!('go' in window)) return []
    const events = await GetEvents(start, end)
    return events.map(normalizeEvent)
  },

  getBoardEvents: async (day: string): Promise<CalendarEvent[]> => {
    if (!('go' in window)) return []
    const events = await GetBoardEvents(day)
    return events.map(normalizeEvent)
  },

  createEvent: async (input: EventInput): Promise<CalendarEvent> => {
    const created = await CreateEvent(input)
    return normalizeEvent(created)
  },

  updateEvent: async (id: number, input: EventInput): Promise<CalendarEvent> => {
    const updated = await UpdateEvent(id, input)
    return normalizeEvent(updated)
  },

  setEventPhase: async (id: number, phase: EventPhase): Promise<CalendarEvent> => {
    const updated = await SetEventPhase(id, phase)
    return normalizeEvent(updated)
  },

  setBoardEventPhase: async (id: number, phase: EventPhase, day: string): Promise<CalendarEvent> => {
    const updated = await SetBoardEventPhase(id, phase, day)
    return normalizeEvent(updated)
  },

  reorderBoardEvent: (day: string, phase: EventPhase, id: number, beforeId: number): Promise<void> =>
    ReorderBoardEvent(day, phase, id, beforeId),

  deleteEvent: async (id: number): Promise<void> => {
    await DeleteEvent(id)
  }
}

const emptyConversationStatus = (overrides: Partial<ConversationStatus> = {}): ConversationStatus => ({
  state: 'idle',
  message: '',
  codexPath: '',
  runId: '',
  lastSyncedAt: '',
  sources: 0,
  created: 0,
  updated: 0,
  pending: 0,
  skipped: 0,
  ...overrides
})

function normalizeConversationStatus(raw: Partial<ConversationStatus> | null | undefined): ConversationStatus {
  const states: AISyncState[] = ['idle', 'running', 'succeeded', 'failed', 'cancelled']
  const state = raw?.state && states.includes(raw.state) ? raw.state : 'idle'
  return emptyConversationStatus({
    state,
    message: raw?.message ?? '',
    codexPath: raw?.codexPath ?? '',
    runId: raw?.runId ?? '',
    lastSyncedAt: raw?.lastSyncedAt ?? '',
    sources: raw?.sources ?? 0,
    created: raw?.created ?? 0,
    updated: raw?.updated ?? 0,
    pending: raw?.pending ?? 0,
    skipped: raw?.skipped ?? 0
  })
}

function normalizeActivity(activity: ChatActivity): ChatActivity {
  return {
    ...activity,
    change: { ...activity.change },
    current: activity.current ? { ...activity.current } : undefined
  }
}

export const conversationApi = {
  supported: () => 'go' in window,
  status: async (): Promise<ConversationStatus> => 'go' in window
    ? normalizeConversationStatus(await GetConversationSync() as ConversationStatus)
    : emptyConversationStatus(),
  start: async () => normalizeConversationStatus(await StartConversationSync() as ConversationStatus),
  cancel: CancelConversationSync,
  saveSettings: async (codexPath: string) => normalizeConversationStatus(await SaveConversationSyncSettings(codexPath) as ConversationStatus),
  activity: async (): Promise<ChatActivity[]> => {
    if (!('go' in window)) return []
    const rows = await GetConversationActivity() as ChatActivity[]
    return rows.map(normalizeActivity)
  },
  resolve: async (id: number, accept: boolean) => normalizeActivity(await ResolveConversationChange(id, accept) as ChatActivity)
}

const browserSetup: AISyncSetup = {
  email: {
    id: 'primary',
    address: '',
    username: '',
    imapHost: '',
    imapPort: 993,
    folder: 'INBOX',
    useTLS: true,
    configured: false,
    hasCredential: false
  },
  agent: {
    settings: { provider: 'codex', codexPath: '', claudePath: '' },
    agents: [
      { provider: 'codex', installed: false, path: '', error: 'Desktop runtime required' },
      { provider: 'claude', installed: false, path: '', error: 'Desktop runtime required' }
    ]
  }
}

export const aiSyncApi = {
  getSetup: async (): Promise<AISyncSetup> => {
    if (!('go' in window)) return browserSetup
    return GetAISyncSetup() as Promise<AISyncSetup>
  },

  saveEmail: async (input: EmailAccountInput): Promise<EmailAccount> => {
    return SaveEmailAccount(input) as Promise<EmailAccount>
  },

  testEmail: async (input: EmailAccountInput): Promise<void> => {
    await TestEmailAccount(input)
  },

  getSourceEmail: async (sourceURL: string): Promise<EmailMessage> => {
    return GetSourceEmail(sourceURL) as Promise<EmailMessage>
  },

  saveSettings: async (settings: AISettings): Promise<void> => {
    await SaveAISettings(settings)
  },

  start: async (): Promise<AISyncStatus> => {
    return StartAISync() as Promise<AISyncStatus>
  },

  status: async (): Promise<AISyncStatus> => {
    if (!('go' in window)) {
      return {
        state: 'idle',
        emailCount: 0,
        readEmails: 0,
        unreadableEmails: 0,
        created: 0,
        skipped: 0,
        rejected: 0,
        conflicts: 0
      }
    }
    return GetAISyncStatus() as Promise<AISyncStatus>
  },

  cancel: async (): Promise<boolean> => {
    return CancelAISync()
  }
}
