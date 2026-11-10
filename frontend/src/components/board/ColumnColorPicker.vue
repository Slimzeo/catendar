<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import type { EventPhase } from '@/types'
import ColorPalettePicker from '@/components/event/ColorPalettePicker.vue'

const props = defineProps<{ phase: EventPhase; color: string }>()
const emit = defineEmits<{ (event: 'select', color: string): void }>()
const open = ref(false)
const wrapper = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)

async function togglePicker() {
  open.value = !open.value
  if (!open.value) return
  await nextTick()
  wrapper.value?.querySelector<HTMLButtonElement>('[aria-checked="true"]')?.focus({ preventScroll: true })
}

function closePicker() {
  open.value = false
  trigger.value?.focus({ preventScroll: true })
}

function closeWhenOutside(event: PointerEvent | FocusEvent) {
  const target = event.type === 'focusout' ? (event as FocusEvent).relatedTarget : event.target
  if (open.value && (!(target instanceof Node) || !wrapper.value?.contains(target))) open.value = false
}

onMounted(() => document.addEventListener('pointerdown', closeWhenOutside, true))
onBeforeUnmount(() => document.removeEventListener('pointerdown', closeWhenOutside, true))
</script>

<template>
  <div ref="wrapper" class="column-color-picker" :data-open="open" @focusout="closeWhenOutside">
    <button
      ref="trigger" type="button" class="column-color-trigger"
      :title="`Change ${props.phase} column color`" :aria-label="`Change ${props.phase} column color`"
      :aria-expanded="open" :aria-controls="open ? `column-color-${phase}` : undefined"
      @click.stop="togglePicker"
    >
      <span class="color-dot" :style="{ '--swatch-color': color }" aria-hidden="true" />
    </button>
    <div
      v-if="open" :id="`column-color-${phase}`" class="column-color-popover"
      @keydown.esc.stop.prevent="closePicker" @click.stop="closePicker"
    >
      <color-palette-picker :model-value="color" :label="`${phase} column color`" compact @update:model-value="value => emit('select', value)" />
    </div>
  </div>
</template>

<style scoped>
.column-color-picker {
  position: relative;
  display: flex;
  flex: 0 0 auto;
}

.column-color-trigger {
  display: grid;
  width: 26px;
  height: 26px;
  padding: 0;
  place-items: center;
  border: 0;
  border-radius: 7px;
  background: transparent;
  cursor: pointer;
}

.column-color-trigger:hover,
.column-color-trigger:focus-visible {
  background: var(--bg-hover);
}

.column-color-trigger:focus-visible {
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.color-dot {
  width: 13px;
  height: 13px;
  border: 1px solid color-mix(in srgb, var(--swatch-color) 72%, black);
  border-radius: 50%;
  background: var(--swatch-color);
  box-shadow: 0 0 0 2px color-mix(in srgb, var(--swatch-color) 12%, transparent);
}

.column-color-popover {
  position: absolute;
  top: calc(100% + 5px);
  right: 0;
  z-index: 20;
  width: max-content;
  max-width: min(190px, calc(100vw - 36px));
  max-height: min(60vh, 220px);
  padding: 7px;
  overflow: auto;
  border: 1px solid var(--border-soft);
  border-radius: 10px;
  background: var(--bg-elevated);
  box-shadow: var(--shadow-md);
}

</style>
