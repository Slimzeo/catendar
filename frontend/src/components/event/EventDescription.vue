<script setup lang="ts">
import { computed } from 'vue'
import type { DescriptionSegment } from '@/utils/description'
import { descriptionSegments, isEmailLink } from '@/utils/description'
import { useSyncSourcesStore } from '@/stores/syncSources'
import { openExternal } from '@/wails'

const props = defineProps<{
  text: string
  clamp?: number
}>()

const emit = defineEmits<{
  (event: 'open-email', sourceURL: string): void
}>()

const syncSources = useSyncSourcesStore()
const segments = computed(() => descriptionSegments(props.text))
const descriptionStyle = computed(() => ({
  '--description-lines': String(props.clamp ?? 3)
}))

function openSegment(segment: DescriptionSegment) {
  if (!segment.href) return
  if (isEmailLink(segment.href)) {
    if (syncSources.active.email) emit('open-email', segment.href)
    return
  }
  openExternal(segment.href)
}
</script>

<template>
  <p class="event-description" :style="descriptionStyle">
    <template v-for="(segment, index) in segments" :key="index">
      <a
        v-if="segment.type === 'url' && (syncSources.active.email || !isEmailLink(segment.href || ''))"
        :href="segment.href"
        @click.stop.prevent="openSegment(segment)"
      >{{ segment.value }}</a>
      <span v-else>{{ segment.value }}</span>
    </template>
  </p>
</template>

<style scoped>
.event-description {
  display: -webkit-box;
  margin: 0;
  overflow: hidden;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 1.55;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: var(--description-lines, 3);
  text-wrap: pretty;
}

.event-description a {
  color: var(--primary-color);
  text-decoration: underline;
  text-decoration-color: color-mix(in srgb, var(--primary-color) 40%, transparent);
  text-underline-offset: 2px;
}
</style>
