<script setup lang="ts">
import { NSwitch, useMessage } from 'naive-ui'
import type { CalendarView } from '@/stores/calendar'
import { sourceNames, sourceViews, useSyncSourcesStore, type SyncSource } from '@/stores/syncSources'

const preferences = useSyncSourcesStore()
const titles = { board: 'Board', month: 'Month' }
const message = useMessage()

function setSource(view: CalendarView, source: SyncSource, enabled: boolean) {
  try {
    preferences.setEnabled(view, source, enabled)
  } catch {
    message.error('Could not save source preferences on this device.')
  }
}
</script>

<template>
  <table class="source-options">
    <caption>AI Sync sources</caption>
    <thead>
      <tr>
        <th scope="col">Source</th>
        <th v-for="view in sourceViews" :key="view" scope="col">{{ titles[view] }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="(name, key) in sourceNames" :key="key">
        <th scope="row">{{ name }}</th>
        <td v-for="view in sourceViews" :key="view">
          <n-switch
            size="small"
            :aria-label="`${name} on ${titles[view]}`"
            :value="preferences.sources[view][key]"
            @update:value="value => setSource(view, key, value)"
          />
        </td>
      </tr>
    </tbody>
  </table>
  <p class="source-note">Turning off a source also stops an active sync started from that page.</p>
</template>

<style scoped>
.source-options {
  width: 100%;
  border-collapse: collapse;
  color: var(--text-primary);
  font-size: 13px;
}
.source-note { margin: 10px 0 0; color: var(--text-secondary); font-size: 12px; }
.source-options caption {
  padding-bottom: 14px;
  text-align: left;
  font-size: 15px;
  font-weight: 650;
}
.source-options th, .source-options td {
  padding: 10px 8px;
  border-bottom: 1px solid var(--border-soft);
  text-align: center;
}
.source-options th:first-child { padding-left: 0; text-align: left; }
.source-options thead th { color: var(--text-secondary); font-size: 12px; font-weight: 500; }
.source-options tbody th { font-weight: 550; }
</style>
