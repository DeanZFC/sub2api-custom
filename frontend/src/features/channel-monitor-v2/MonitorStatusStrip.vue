<template>
  <div class="monitor-status-strip flex min-w-0 items-end gap-1" :class="compact ? 'h-6' : 'h-9'" :aria-label="label">
    <span
      v-for="(slot, index) in slots"
      :key="index"
      class="min-w-0 flex-1 rounded-[3px] outline-offset-2 transition-opacity hover:opacity-75 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
      :class="[colors[slot.state], slot.state === 'unknown' ? 'h-1.5' : (compact ? 'h-6' : 'h-9')]"
      :title="slot.label"
      :aria-label="slot.label"
      :data-state="slot.state"
      role="img"
      tabindex="0"
    />
  </div>
</template>

<script setup lang="ts">
import type { HealthState } from '@/api/channelMonitorV2'
defineProps<{ slots: Array<{ state: HealthState; label: string }>; label: string; compact?: boolean }>()
const colors: Record<HealthState, string> = {
  healthy: 'bg-emerald-400 dark:bg-emerald-500',
  warning: 'bg-amber-400 dark:bg-amber-500',
  critical: 'bg-rose-400 dark:bg-rose-500',
  unknown: 'bg-gray-300 dark:bg-dark-600',
}
</script>
