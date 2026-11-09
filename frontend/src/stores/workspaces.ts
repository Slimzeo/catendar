import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import type { Workspace } from '@/types'
import { workspaceApi } from '@/services/api'

export const ROUTINE_WORKSPACE_ID = 1

const initialWorkspaces: Workspace[] = [
  { id: ROUTINE_WORKSPACE_ID, name: 'Daily Routine', hint: '' },
  { id: 2, name: 'Seekino', hint: '' },
  { id: 3, name: '个人 IP 打造', hint: '' }
]

function errorMessage(cause: unknown, fallback: string): string {
  if (cause instanceof Error) return cause.message
  if (typeof cause === 'string' && cause) return cause
  return fallback
}

export const useWorkspacesStore = defineStore('workspaces', () => {
  const workspaces = ref<Workspace[]>(initialWorkspaces.map(item => ({ ...item })))
  const selectedId = ref(0)
  const loading = ref(false)
  const error = ref<string | null>(null)
  let latestFetch = 0

  const isAll = computed(() => selectedId.value === 0)
  const selected = computed(() => workspaces.value.find(item => item.id === selectedId.value) ?? null)
  const createWorkspaceId = computed(() => selectedId.value || ROUTINE_WORKSPACE_ID)

  function workspaceName(id: number): string {
    return workspaces.value.find(item => item.id === id)?.name || (id === ROUTINE_WORKSPACE_ID ? 'Daily Routine' : 'Project')
  }

  function showsWorkspace(id: number): boolean {
    return selectedId.value === 0 || selectedId.value === id
  }

  async function fetchWorkspaces() {
    const request = ++latestFetch
    loading.value = true
    error.value = null
    try {
      const rows = await workspaceApi.list()
      if (request !== latestFetch) return
      if (rows.length) workspaces.value = rows.map(item => ({ ...item }))
      if (selectedId.value !== 0 && !workspaces.value.some(item => item.id === selectedId.value)) {
        selectedId.value = 0
      }
    } catch (cause) {
      if (request === latestFetch) error.value = errorMessage(cause, 'Could not load project boards')
    } finally {
      if (request === latestFetch) loading.value = false
    }
  }

  function selectWorkspace(id: number) {
    if (id !== 0 && !workspaces.value.some(item => item.id === id)) return
    selectedId.value = id
  }

  async function addWorkspace(name: string, hint: string): Promise<Workspace> {
    const cleanName = name.trim()
    const cleanHint = hint.trim()
    if (!cleanName) throw new Error('Add a project name.')
    try {
      const created = await workspaceApi.create(cleanName, cleanHint)
      if (!workspaces.value.some(item => item.id === created.id)) workspaces.value.push(created)
      selectedId.value = created.id
      return created
    } catch (cause) {
      error.value = errorMessage(cause, 'Could not create the project board')
      throw cause instanceof Error ? cause : new Error(errorMessage(cause, 'Could not create the project board'))
    }
  }

  async function editWorkspace(id: number, name: string, hint: string): Promise<Workspace> {
    const cleanName = name.trim()
    const cleanHint = hint.trim()
    if (!cleanName) throw new Error('Add a project name.')
    try {
      const updated = await workspaceApi.update(id, cleanName, cleanHint)
      const index = workspaces.value.findIndex(item => item.id === id)
      if (index >= 0) workspaces.value[index] = { ...updated }
      return updated
    } catch (cause) {
      error.value = errorMessage(cause, 'Could not update the project board')
      throw cause instanceof Error ? cause : new Error(errorMessage(cause, 'Could not update the project board'))
    }
  }

  return {
    workspaces,
    selectedId,
    loading,
    error,
    isAll,
    selected,
    createWorkspaceId,
    workspaceName,
    showsWorkspace,
    fetchWorkspaces,
    selectWorkspace,
    addWorkspace,
    editWorkspace
  }
})
