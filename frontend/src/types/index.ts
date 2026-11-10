export type EventPhase = 'todo' | 'progressing' | 'done'

export type EventEditField = 'title' | 'description' | 'result'

export interface CalendarEvent {
  id: number
  title: string
  start: string
  end?: string
  color: string
  allDay: boolean
  description: string
  result?: string
  bold: boolean
  phase: EventPhase
  deadline?: string
  createdAt: string
  updatedAt: string
}

export interface EventInput {
  title: string
  start: string
  end?: string
  color: string
  allDay: boolean
  description: string
  result?: string
  bold: boolean
  phase?: EventPhase
  deadline?: string
  boardDay?: string
}

export interface DayCell {
  date: Date
  isCurrentMonth: boolean
  isToday: boolean
  isWeekend: boolean
}

export type AIProvider = 'codex' | 'claude'

export interface EmailAccount {
  id: string
  address: string
  username: string
  imapHost: string
  imapPort: number
  folder: string
  useTLS: boolean
  configured: boolean
  hasCredential: boolean
}

export interface EmailAccountInput {
  address: string
  username: string
  imapHost: string
  imapPort: number
  folder: string
  useTLS: boolean
  secret: string
}

export interface EmailMessage {
  id: string
  messageId: string
  subject: string
  sender: string
  receivedAt: string
  sentAt?: string
  sourceUrl: string
  text: string
  readError?: string
}

export interface AISettings {
  provider: AIProvider
  codexPath: string
  claudePath: string
}

export interface AgentAvailability {
  provider: AIProvider
  installed: boolean
  path: string
  version?: string
  error?: string
}

export interface AISyncSetup {
  email: EmailAccount
  agent: {
    settings: AISettings
    agents: AgentAvailability[]
  }
}

export type AISyncState = 'idle' | 'running' | 'succeeded' | 'failed' | 'cancelled'

export interface AISyncStatus {
  runId?: string
  state: AISyncState
  stage?: string
  message?: string
  provider?: AIProvider
  emailCount: number
  readEmails: number
  unreadableEmails: number
  created: number
  skipped: number
  rejected: number
  conflicts: number
  startedAt?: string
  finishedAt?: string
}

export interface ConversationStatus {
  state: AISyncState
  message: string
  codexPath: string
  runId: string
  lastSyncedAt: string
  sources: number
  created: number
  updated: number
  pending: number
  skipped: number
}

export interface ChatEvidence {
  id: string
  threadId: string
  threadTitle: string
  at: number
  quote: string
}

export interface ChatChange {
  source: ChatEvidence
  taskKey: string
  eventId: number
  title: string
  phase: EventPhase
  description: string
  result: string
  needsReview: boolean
  baseUpdated: string
}

export type ChatActivityState = 'pending' | 'applied' | 'dismissed'

export interface ChatActivity {
  current?: CalendarEvent
  id: number
  day: string
  state: ChatActivityState
  reason: string
  beforePhase: string
  change: ChatChange
}
