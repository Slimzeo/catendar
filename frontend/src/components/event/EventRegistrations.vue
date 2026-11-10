<script setup lang="ts">
import { computed } from 'vue'
import type { EventRegistration } from '@/types'
import { orderedRegistrations, safeRegistrationUrl } from '@/utils/registrations'
import { useCalendarStore } from '@/stores/calendar'
import { openExternal } from '@/wails'

const props = withDefaults(defineProps<{
  registrations?: EventRegistration[]
  compact?: boolean
  context?: string
}>(), {
  registrations: () => [],
  compact: false,
  context: ''
})

const emit = defineEmits<{ (event: 'show-all'): void }>()
const calendar = useCalendarStore()
const compactLimit = 3
const activities = computed(() => orderedRegistrations(props.registrations, calendar.focusedDate))
const rows = computed(() => {
  const visible = props.compact ? activities.value.slice(0, compactLimit) : activities.value
  return visible.map(registration => ({
    registration,
    url: safeRegistrationUrl(registration.url),
    registrationDue: registration.deadlineText || registration.deadline,
    submissionDue: registration.submissionDeadlineText || registration.submissionDeadline
  }))
})

const label = computed(() => (
  props.context ? `Registrations for ${props.context}` : 'Registrations'
))

function openRegistration(url: string | undefined) {
  if (!url) return
  openExternal(url)
}
</script>

<template>
  <ul class="registrations" :class="{ compact }" :aria-label="label">
    <li v-for="(row, index) in rows" :key="`${row.registration.name}-${index}`" class="registration">
      <a
        v-if="row.url"
        :href="row.url"
        class="registration-link"
        data-registration-link
        :title="row.registration.name"
        @click.stop.prevent="openRegistration(row.url)"
      >
        <span class="registration-name">{{ row.registration.name }}</span>
        <svg class="external-icon" viewBox="0 0 20 20" aria-hidden="true">
          <path d="M8 4H4v12h12v-4M10.5 4H16v5.5M16 4l-7.5 7.5" />
        </svg>
      </a>
      <span v-else class="registration-name">{{ row.registration.name }}</span>
      <span class="registration-times">
        <span v-if="row.registrationDue" class="registration-deadline" :title="row.registrationDue">Registration · {{ row.registrationDue }}</span>
        <span v-if="row.submissionDue" class="registration-deadline" :title="row.submissionDue">Submission · {{ row.submissionDue }}</span>
        <span v-if="!row.registrationDue && !row.submissionDue" class="registration-deadline unknown">Deadline unknown</span>
      </span>
    </li>
    <li v-if="props.compact && activities.length > compactLimit">
      <button type="button" class="registration-more" @click.stop="emit('show-all')">View all {{ activities.length }} activities</button>
    </li>
  </ul>
</template>

<style scoped>
.registrations {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.registration {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 8px;
  color: var(--text-secondary);
  font-size: var(--registration-font-size, 11.5px);
  line-height: 1.4;
}

.registration-link,
.registration-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.registration-link {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--primary-color);
  text-decoration: underline;
  text-decoration-color: color-mix(in srgb, var(--primary-color) 40%, transparent);
  text-underline-offset: 2px;
}

.registration-link:hover,
.registration-link:focus-visible {
  color: var(--text-primary);
}

.registration-link:focus-visible {
  border-radius: 3px;
  outline: 2px solid var(--focus-ring);
  outline-offset: 1px;
}

.external-icon {
  width: 9px;
  height: 9px;
  flex: 0 0 auto;
  fill: none;
  stroke: currentColor;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 1.9;
  opacity: 0.7;
}

.registration-times {
  display: flex;
  flex-direction: column;
  margin-left: auto;
  max-width: 60%;
  min-width: 0;
  align-items: flex-end;
}

.registration-deadline {
  min-width: 0;
  max-width: 100%;
  flex: 0 1 auto;
  overflow: hidden;
  color: var(--text-tertiary);
  font-size: calc(var(--registration-font-size, 11.5px) - 1px);
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.registration-deadline.unknown {
  opacity: 0.78;
}

.registrations:not(.compact) .registration {
  flex-wrap: wrap;
  align-items: baseline;
}

.registrations:not(.compact) .registration-link,
.registrations:not(.compact) .registration-name {
  overflow: visible;
  overflow-wrap: anywhere;
  text-overflow: clip;
  white-space: normal;
}

.registrations:not(.compact) .registration-times,
.registrations:not(.compact) .registration-deadline {
  max-width: 100%;
  overflow: visible;
  overflow-wrap: anywhere;
  text-overflow: clip;
  white-space: normal;
}

.registration-more {
  appearance: none;
  padding: 2px 0;
  background: none;
  border: 0;
  font: inherit;
  color: var(--primary-color);
  cursor: pointer;
  font-size: calc(var(--registration-font-size, 11.5px) - 1px);
}
</style>
