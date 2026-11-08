<script setup lang="ts">
import { ref, watch } from 'vue'
import { NButton, NCard, NModal } from 'naive-ui'
import { formatShortcut } from '@/shortcuts'
import { useShortcutsStore } from '@/stores/shortcuts'

defineProps<{ title: string; kind: 'task' | 'event'; busy: boolean }>()
const emit = defineEmits<{ cancel: []; confirm: [] }>()
const shortcuts = useShortcutsStore()
const heading = ref<HTMLElement | null>(null)
watch(heading, element => element?.focus(), { flush: 'post' })
</script>

<template>
  <n-modal
    :show="true"
    :auto-focus="false"
    :close-on-esc="false"
    :mask-closable="!busy"
    @update:show="show => { if (!show) emit('cancel') }"
  >
    <n-card
      class="delete-dialog"
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="delete-heading"
      aria-describedby="delete-target"
      :bordered="false"
    >
      <h2 id="delete-heading" ref="heading" tabindex="-1">Delete {{ kind }}?</h2>
      <p id="delete-target">{{ title }}</p>
      <div class="dialog-actions">
        <n-button :disabled="busy" @click="emit('cancel')">Cancel</n-button>
        <n-button
          type="error"
          :loading="busy"
          @keydown.enter.prevent
          @keydown.space.prevent
          @click="emit('confirm')"
        >
          Delete <kbd>{{ formatShortcut(shortcuts.bindings.confirmDelete) }}</kbd>
        </n-button>
      </div>
    </n-card>
  </n-modal>
</template>

<style scoped>
.delete-dialog {
  width: min(380px, calc(100vw - 24px));
  max-height: calc(100vh - 20px);
  overflow: auto;
}

h2 {
  margin: 0;
  outline: none;
  color: var(--text-primary);
  font: 650 18px/1.35 var(--font-display);
}

p {
  margin: 8px 0 20px;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.dialog-actions {
  display: flex;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
}

kbd {
  margin-left: 7px;
  font: inherit;
  font-size: 11px;
  opacity: 0.85;
}
</style>
