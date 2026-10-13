<template>
  <div class="mb-4 min-w-0">
    <div
      role="tablist"
      :aria-label="t('admin.accounts.platformTabs.label')"
      :aria-busy="loading"
      class="flex gap-1 overflow-x-auto border-b border-gray-200 dark:border-dark-700"
      @keydown="handleKeydown"
    >
      <button
        v-for="tab in tabs"
        :id="`account-platform-tab-${tab.platform || 'all'}`"
        :key="tab.platform"
        type="button"
        role="tab"
        aria-controls="account-platform-panel"
        :aria-selected="modelValue === tab.platform"
        :tabindex="modelValue === tab.platform ? 0 : -1"
        :data-platform="tab.platform"
        class="flex shrink-0 items-center gap-2 whitespace-nowrap border-b-2 px-4 py-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500"
        :class="modelValue === tab.platform
          ? 'border-primary-500 text-primary-700 dark:border-primary-400 dark:text-primary-300'
          : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-800 dark:text-gray-400 dark:hover:border-dark-500 dark:hover:text-gray-200'"
        @click="$emit('update:modelValue', tab.platform)"
      >
        <PlatformIcon v-if="tab.platform" :platform="tab.platform" size="md" />
        <span>{{ tab.label }}</span>
        <span
          v-if="tab.count !== null"
          class="rounded-full px-2 py-0.5 text-xs tabular-nums"
          :class="modelValue === tab.platform
            ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/40 dark:text-primary-300'
            : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'"
        >{{ tab.count }}</span>
      </button>
    </div>
    <p v-if="error" role="status" class="mt-2 text-xs text-amber-700 dark:text-amber-300">
      {{ t('admin.accounts.platformTabs.loadFailed') }}
      <button type="button" class="ml-2 underline" :disabled="loading" @click="$emit('retry')">
        {{ t('common.retry') }}
      </button>
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { listPlatformIds, platformDisplayName } from '@/constants/platformCatalog'
import type { AccountPlatformCount } from '@/api/admin/accounts'

const props = defineProps<{
  modelValue: string
  counts: AccountPlatformCount[] | null
  loading: boolean
  error: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [platform: string]
  retry: []
}>()
const { t } = useI18n()

const tabs = computed(() => {
  const order = listPlatformIds()
  const platforms = [...(props.counts ?? [])].sort((a, b) => {
    const left = order.indexOf(a.platform)
    const right = order.indexOf(b.platform)
    return (left < 0 ? order.length : left) - (right < 0 ? order.length : right) || a.platform.localeCompare(b.platform)
  })
  // If the last account on the current platform was deleted, keep that tab
  // selected with zero accounts until the user chooses another platform.
  if (props.modelValue && !platforms.some(item => item.platform === props.modelValue)) {
    platforms.push({ platform: props.modelValue, count: 0 })
  }
  return [
    {
      platform: '',
      label: t('admin.accounts.allPlatforms'),
      count: props.counts === null ? null : props.counts.reduce((sum, item) => sum + item.count, 0)
    },
    ...platforms.map(item => ({ ...item, label: platformDisplayName(item.platform) }))
  ]
})

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
  emit('update:modelValue', tabs.value[next].platform)
}
</script>
