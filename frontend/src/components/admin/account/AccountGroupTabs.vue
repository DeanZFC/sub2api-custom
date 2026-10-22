<template>
  <div
    role="tablist"
    :aria-label="t('admin.accounts.columns.groups')"
    class="mb-4 flex min-w-0 gap-2 overflow-x-auto pb-1"
    @keydown="handleKeydown"
  >
    <button
      v-for="tab in tabs"
      :id="`account-group-tab-${tab.value || 'all'}`"
      :key="tab.value"
      type="button"
      role="tab"
      aria-controls="account-group-panel"
      :aria-selected="modelValue === tab.value"
      :tabindex="modelValue === tab.value ? 0 : -1"
      :data-group="tab.value"
      :title="tab.label"
      class="max-w-full shrink-0 truncate rounded-lg border px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500 sm:max-w-64"
      :class="modelValue === tab.value
        ? 'border-primary-200 bg-primary-50 text-primary-700 dark:border-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
        : 'border-transparent text-gray-500 hover:bg-gray-100 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-gray-200'"
      @click="$emit('update:modelValue', tab.value)"
    >
      {{ tab.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminGroup } from '@/types'

const props = defineProps<{
  modelValue: string
  groups: AdminGroup[]
}>()
const emit = defineEmits<{
  'update:modelValue': [group: string]
}>()
const { t } = useI18n()

const tabs = computed(() => [
  { value: '', label: t('admin.accounts.allGroups') },
  { value: 'ungrouped', label: t('admin.accounts.ungroupedGroup') },
  ...props.groups.map(group => ({ value: String(group.id), label: group.name }))
])

const handleKeydown = (event: KeyboardEvent) => {
  const buttons = Array.from((event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('[role="tab"]'))
  const index = buttons.indexOf(event.target as HTMLButtonElement)
  if (index < 0) return
  let next: number
  switch (event.key) {
    case 'ArrowRight': next = (index + 1) % buttons.length; break
    case 'ArrowLeft': next = (index - 1 + buttons.length) % buttons.length; break
    case 'Home': next = 0; break
    case 'End': next = buttons.length - 1; break
    default: return
  }
  event.preventDefault()
  buttons[next]?.focus()
  emit('update:modelValue', tabs.value[next].value)
}
</script>
