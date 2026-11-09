<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { NButton, NCard, NInput, NModal } from 'naive-ui'
import { useWorkspacesStore } from '@/stores/workspaces'
import type { Workspace } from '@/types'

const props = withDefaults(defineProps<{
  show: boolean
  workspace?: Workspace | null
}>(), {
  workspace: null
})

const emit = defineEmits<{
  (event: 'update:show', value: boolean): void
  (event: 'saved', workspace: Workspace): void
}>()

const workspacesStore = useWorkspacesStore()
const name = ref('')
const hint = ref('')
const saving = ref(false)
const error = ref('')
const nameInput = ref<{ focus: () => void } | null>(null)

watch(() => props.show, show => {
  if (!show) return
  name.value = props.workspace?.name || ''
  hint.value = props.workspace?.hint || ''
  error.value = ''
  saving.value = false
  nextTick(() => nameInput.value?.focus())
})

function close() {
  if (saving.value) return
  emit('update:show', false)
}

async function submit() {
  if (saving.value) return
  const cleanName = name.value.trim()
  if (!cleanName) {
    error.value = 'Add a project name.'
    nameInput.value?.focus()
    return
  }

  saving.value = true
  error.value = ''
  try {
    const workspace = props.workspace
      ? await workspacesStore.editWorkspace(props.workspace.id, cleanName, hint.value)
      : await workspacesStore.addWorkspace(cleanName, hint.value)
    emit('saved', workspace)
    emit('update:show', false)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'Could not save the project board'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <n-modal
    :show="show"
    :auto-focus="false"
    :mask-closable="!saving"
    :close-on-esc="!saving"
    @update:show="value => { if (!saving) emit('update:show', value) }"
  >
    <n-card
      class="workspace-card"
      role="dialog"
      aria-modal="true"
      aria-labelledby="workspace-dialog-title"
      :bordered="false"
      closable
      :closable-disabled="saving"
      @close="close"
    >
      <template #header>
        <h2 id="workspace-dialog-title">
          {{ workspace ? 'Project board settings' : 'New project board' }}
        </h2>
      </template>

      <form class="workspace-form" @submit.prevent="submit">
        <label class="field">
          <span>Name</span>
          <n-input
            ref="nameInput"
            v-model:value="name"
            :input-props="{ id: 'workspace-name' }"
            placeholder="Project or client name"
            :disabled="saving"
            @input="error = ''"
          />
        </label>

        <label class="field">
          <span>Hint <small>optional</small></span>
          <n-input
            v-model:value="hint"
            type="textarea"
            :input-props="{ id: 'workspace-hint' }"
            :rows="2"
            placeholder="Keywords Codex can use to match conversations"
            :disabled="saving"
            @input="error = ''"
          />
        </label>

        <p v-if="error" class="form-error" role="alert">{{ error }}</p>

        <footer class="form-actions">
          <n-button :disabled="saving" @click="close">Cancel</n-button>
          <n-button type="primary" attr-type="submit" :loading="saving">
            {{ workspace ? 'Save project' : 'Create project' }}
          </n-button>
        </footer>
      </form>
    </n-card>
  </n-modal>
</template>

<style scoped>
.workspace-card {
  width: min(400px, calc(100vw - 24px));
}

#workspace-dialog-title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 18px;
  letter-spacing: -0.025em;
}

.workspace-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.field {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 6px;
}

.field > span {
  color: var(--text-secondary);
  font-size: 11px;
  font-weight: 650;
}

.field small {
  color: var(--text-tertiary);
  font-size: 9px;
  font-weight: 500;
}

.form-error {
  margin: 0;
  color: var(--danger-color);
  font-size: 11px;
  line-height: 1.4;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding-top: 2px;
}

@media (max-width: 420px) {
  .workspace-card {
    width: calc(100vw - 16px);
  }
}
</style>
