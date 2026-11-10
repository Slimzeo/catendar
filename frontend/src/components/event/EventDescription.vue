<script setup lang="ts">
import { computed } from 'vue'
import type { DescriptionSegment } from '@/utils/description'
import { descriptionSegments, isEmailLink, safeDescriptionHref } from '@/utils/description'
import { useSyncSourcesStore } from '@/stores/syncSources'
import { openExternal } from '@/wails'

const props = defineProps<{
  text: string
  clamp?: number
  full?: boolean
}>()

const emit = defineEmits<{
  (event: 'open-email', sourceURL: string): void
}>()

const syncSources = useSyncSourcesStore()
// Avoid focusable links from paragraphs hidden by a compact card's line clamp.
const visibleText = computed(() => {
  if (props.full) return props.text
  const limit = props.clamp ?? 3
  const lines = props.text.split('\n', limit + 1)
  if (lines.length <= limit) return props.text
  return lines.slice(0, limit).join('\n').trimEnd()
})
const segments = computed<DescriptionSegment[]>(() => descriptionSegments(visibleText.value).map(segment => {
  if (segment.type !== 'url' || !segment.href) return segment
  return { ...segment, href: safeDescriptionHref(segment.href) }
}))
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
  <p class="event-description" :class="{ full }" :style="descriptionStyle">
    <template v-for="(segment, index) in segments" :key="index">
      <a
        v-if="segment.type === 'url' && segment.href && (syncSources.active.email || !isEmailLink(segment.href || ''))"
        :href="segment.href"
        @click.stop.prevent="openSegment(segment)"
      >{{ segment.value }}</a>
      <span v-else>{{ segment.value }}</span>
    </template><span v-if="visibleText.length < text.length">…</span>
  </p>
</template>

<style scoped>
.event-description {
  display: -webkit-box;
  margin: 0;
  overflow: hidden;
  color: var(--description-color, var(--text-secondary));
  font-size: var(--description-font-size, 12px);
  line-height: 1.55;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: var(--description-lines, 3);
  text-wrap: pretty;
}

.event-description.full {
  display: block;
  overflow: visible;
  -webkit-line-clamp: none;
}

.event-description a {
  color: var(--description-link-color, var(--primary-color));
  text-decoration: underline;
  text-decoration-color: color-mix(in srgb, currentColor 55%, transparent);
  text-underline-offset: 2px;
}
</style>
