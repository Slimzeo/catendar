<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useWorkspacesStore } from '@/stores/workspaces'
import type { Workspace } from '@/types'
import WorkspaceModal from './WorkspaceModal.vue'

const emit = defineEmits<{
  (event: 'dialog-state', open: boolean): void
}>()

const workspacesStore = useWorkspacesStore()
const scroller = ref<HTMLElement | null>(null)
const editorOpen = ref(false)
const editingWorkspace = ref<Workspace | null>(null)

const selectedName = computed(() => workspacesStore.selected?.name || '')

watch(editorOpen, open => emit('dialog-state', open))

watch(() => workspacesStore.selectedId, async () => {
  await nextTick()
  scroller.value?.querySelector<HTMLElement>('[data-active="true"]')
    ?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
})

function openCreate() {
  editingWorkspace.value = null
  editorOpen.value = true
}

function openEdit() {
  const workspace = workspacesStore.selected
  if (!workspace) return
  editingWorkspace.value = workspace
  editorOpen.value = true
}
</script>

<template>
  <div class="workspace-bar">
    <div ref="scroller" class="workspace-scroll" role="group" aria-label="Project boards">
      <button
        type="button"
        class="workspace-tab"
        :data-active="workspacesStore.selectedId === 0 ? 'true' : 'false'"
        :aria-pressed="workspacesStore.selectedId === 0"
        title="All project boards"
        @click="workspacesStore.selectWorkspace(0)"
      >
        All
      </button>
      <button
        v-for="workspace in workspacesStore.workspaces"
        :key="workspace.id"
        type="button"
        class="workspace-tab"
        :data-workspace-id="workspace.id"
        :data-active="workspacesStore.selectedId === workspace.id ? 'true' : 'false'"
        :aria-pressed="workspacesStore.selectedId === workspace.id"
        :title="workspace.hint || workspace.name"
        @click="workspacesStore.selectWorkspace(workspace.id)"
      >
        {{ workspace.name }}
      </button>
    </div>

    <div class="workspace-actions">
      <button
        v-if="workspacesStore.selectedId !== 0"
        type="button"
        class="workspace-icon"
        :aria-label="`Edit ${selectedName}`"
        :title="`Edit ${selectedName}`"
        @click="openEdit"
      >
        <svg viewBox="0 0 20 20" aria-hidden="true">
          <circle cx="4.5" cy="10" r="1" /><circle cx="10" cy="10" r="1" /><circle cx="15.5" cy="10" r="1" />
        </svg>
      </button>
      <button
        type="button"
        class="workspace-icon workspace-add"
        aria-label="New project board"
        title="New project board"
        @click="openCreate"
      >
        <svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 4v12M4 10h12" /></svg>
      </button>
    </div>

    <workspace-modal
      v-model:show="editorOpen"
      :workspace="editingWorkspace"
    />
  </div>
</template>

<style scoped>
.workspace-bar {
  display: flex;
  min-height: 32px;
  align-items: center;
  gap: 4px;
  padding: 3px 10px;
  border-bottom: 1px solid var(--border-soft);
  background: color-mix(in srgb, var(--bg-secondary) 58%, var(--bg-toolbar));
}

.workspace-scroll {
  display: flex;
  min-width: 0;
  flex: 1;
  align-items: center;
  gap: 2px;
  overflow-x: auto;
  scrollbar-width: thin;
}

.workspace-scroll::-webkit-scrollbar {
  height: 3px;
}

.workspace-scroll::-webkit-scrollbar-thumb {
  border-radius: 3px;
  background: var(--border-strong);
}

.workspace-tab {
  flex: 0 0 auto;
  max-width: 164px;
  overflow: hidden;
  padding: 3px 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: 11.5px;
  font-weight: 550;
  line-height: 1.35;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
}

.workspace-tab:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.workspace-tab[data-active='true'] {
  background: var(--primary-muted);
  color: var(--primary-color);
  font-weight: 650;
}

.workspace-tab:focus-visible,
.workspace-icon:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.workspace-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 1px;
}

.workspace-icon {
  display: grid;
  width: 24px;
  height: 24px;
  padding: 0;
  place-items: center;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-tertiary);
  cursor: pointer;
}

.workspace-icon:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.workspace-icon svg {
  width: 15px;
  height: 15px;
  fill: currentColor;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.7;
}

.workspace-add svg {
  fill: none;
}

@media (max-width: 420px) {
  .workspace-bar {
    padding-inline: 7px;
  }

  .workspace-tab {
    max-width: 128px;
    padding-inline: 6px;
    font-size: 11px;
  }

  .workspace-icon {
    width: 22px;
    height: 22px;
  }
}
</style>
