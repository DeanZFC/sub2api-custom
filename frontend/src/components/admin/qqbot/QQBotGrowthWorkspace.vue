<template>
  <div class="space-y-4">
    <nav
      role="tablist"
      :aria-label="t('qqbot.growth.tab')"
      class="flex flex-wrap gap-1 border-b border-gray-200 dark:border-dark-700"
    >
      <button
        v-for="tab in tabs"
        :id="`qqbot-growth-tab-${tab.key}`"
        :key="tab.key"
        type="button"
        role="tab"
        :aria-selected="activeTab.key === tab.key"
        :aria-controls="`qqbot-growth-panel-${tab.key}`"
        :tabindex="activeTab.key === tab.key ? 0 : -1"
        class="min-h-[44px] border-b-2 px-3 py-2 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
        :class="activeTab.key === tab.key
          ? 'border-primary-500 text-primary-700 dark:text-primary-300'
          : 'border-transparent text-gray-600 hover:text-gray-900 dark:text-gray-300 dark:hover:text-white'"
        @click="selectTab(tab.key)"
        @keydown="handleKeydown($event, tab.key)"
      >
        {{ t(tab.label) }}
      </button>
    </nav>
    <div
      :id="`qqbot-growth-panel-${activeTab.key}`"
      role="tabpanel"
      :aria-labelledby="`qqbot-growth-tab-${activeTab.key}`"
    >
      <KeepAlive>
        <component :is="activeTab.component" :key="activeTab.key" />
      </KeepAlive>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import QQBotGrowthSettings from './QQBotGrowthSettings.vue'
import QQBotInviteSettings from './QQBotInviteSettings.vue'
import QQBotRankingSettings from './QQBotRankingSettings.vue'

const props = defineProps<{ section?: 'rankings' | 'invite' }>()
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const tabs = [
  { key: 'tasks', label: 'qqbot.growth.tasksTab', component: QQBotGrowthSettings },
  { key: 'rankings', label: 'qqbot.rankings.tab', component: QQBotRankingSettings },
  { key: 'invite', label: 'qqbot.inviteTitle', component: QQBotInviteSettings }
] as const
type GrowthTab = (typeof tabs)[number]['key']
const activeTab = computed(() => tabs.find(tab => tab.key === props.section) ?? tabs[0])

async function selectTab(tab: GrowthTab) {
  if (activeTab.value.key !== tab) {
    await router.push({ query: { ...route.query, tab: 'growth', section: tab === 'tasks' ? undefined : tab } })
  }
}

async function handleKeydown(event: KeyboardEvent, tab: GrowthTab) {
  const index = tabs.findIndex(item => item.key === tab)
  let next: number
  switch (event.key) {
    case 'ArrowRight': next = (index + 1) % tabs.length; break
    case 'ArrowLeft': next = (index + tabs.length - 1) % tabs.length; break
    case 'Home': next = 0; break
    case 'End': next = tabs.length - 1; break
    default: return
  }
  event.preventDefault()
  const target = tabs[next]!
  await selectTab(target.key)
  await nextTick()
  document.getElementById(`qqbot-growth-tab-${target.key}`)?.focus()
}
</script>
