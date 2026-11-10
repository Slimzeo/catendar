<script setup lang="ts">
import { computed, ref } from 'vue'
import { eventColors } from '@/constants/eventColors'

const props = withDefaults(defineProps<{ label?: string; compact?: boolean }>(), { label: 'Event color' })
const focused = ref<string>('')
const model = defineModel<string>({ required: true })

const availableColors = computed(() => {
  if (eventColors.some(color => color.value === model.value)) return eventColors
  return [{ name: 'Current color', value: model.value }, ...eventColors]
})

function navigate(event: KeyboardEvent) {
  const group = event.currentTarget as HTMLElement
  const radios = Array.from(group.querySelectorAll<HTMLButtonElement>('[role="radio"]'))
  const index = radios.indexOf(document.activeElement as HTMLButtonElement)
  if (index < 0) return
  const step = props.compact ? 5 : 1
  const moves: Record<string, number> = {
    ArrowLeft: index - 1, ArrowRight: index + 1,
    ArrowUp: index - step, ArrowDown: index + step,
    Home: 0, End: radios.length - 1
  }
  if (!(event.key in moves)) return
  event.preventDefault()
  event.stopPropagation()
  const next = (moves[event.key] + radios.length) % radios.length
  radios[next].focus({ preventScroll: true })
}
</script>

<template>
  <div
    class="color-grid" :class="{ compact }" role="radiogroup" :aria-label="label"
    @keydown="navigate" @focusout="focused = ''"
  >
    <button
      v-for="item in availableColors"
      :key="item.value"
      type="button"
      class="color-swatch"
      :class="{ selected: model === item.value }"
      :style="{ '--swatch-color': item.value }"
      :aria-label="item.name"
      :tabindex="(focused || model) === item.value ? 0 : -1"
      @focus="focused = item.value"
      :aria-checked="model === item.value"
      role="radio"
      :title="item.name"
      @click="model = item.value"
    >
      <svg v-if="model === item.value" viewBox="0 0 16 16" aria-hidden="true">
        <path d="m3.2 8.1 3 3.1 6.6-6.6" />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.color-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 2px;
}

.color-swatch {
  display: grid;
  width: 30px;
  height: 30px;
  padding: 0;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--swatch-color) 75%, black);
  border-radius: 50%;
  background: var(--swatch-color);
  color: white;
  cursor: pointer;
  box-shadow: 0 0 0 2px var(--bg-elevated);
  transition: transform 140ms ease, box-shadow 140ms ease;
}

.color-swatch:hover {
  transform: translateY(-1px);
}

.color-swatch.selected {
  box-shadow: 0 0 0 2px var(--bg-elevated), 0 0 0 4px var(--primary-color);
}

.color-swatch:focus-visible {
  outline: 3px solid var(--focus-ring);
  outline-offset: 3px;
}

.color-swatch svg {
  width: 16px;
  height: 16px;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.2;
}

.color-grid.compact {
  display: grid;
  grid-template-columns: repeat(5, 22px);
  gap: 6px;
  padding: 4px;
}

.compact .color-swatch { width: 22px; height: 22px; }
.compact .color-swatch svg { width: 13px; height: 13px; }
</style>
