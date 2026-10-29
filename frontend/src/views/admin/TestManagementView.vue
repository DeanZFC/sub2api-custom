<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.title') }}</h2><p class="mt-1 text-sm text-gray-500">{{ t('admin.tests.experience.activeHint') }}</p></div>
        <button class="btn btn-secondary" @click="refresh"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
      </header>
      <nav class="flex gap-1 overflow-x-auto border-b border-gray-200 pb-2 dark:border-dark-700" role="tablist" :aria-label="t('admin.tests.title')">
        <button v-for="tab in tabs" :key="tab.key" type="button" role="tab" :data-quality-section="tab.key" :aria-selected="activeTab === tab.key" class="shrink-0 rounded-lg px-4 py-2 text-sm font-medium" :class="activeTab === tab.key ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-50 dark:hover:bg-dark-800'" @click="navigate(tab.key)">{{ t(tab.label) }}</button>
      </nav>
      <ActiveQualityPanel v-show="activeTab === 'plans' || activeTab === 'types'" ref="active" :tab="activeTab" @catalog="setCatalog" />
      <QualityOperationsPanel v-if="activeTab === 'operations'" ref="operations" :visible="true" :accounts="accounts" />
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ActiveQualityPanel from '@/components/tests/ActiveQualityPanel.vue'
import QualityOperationsPanel from '@/components/tests/QualityOperationsPanel.vue'
import type { AdminGroup, AccountListItem } from '@/types'
const { t } = useI18n()
const route = useRoute(), router = useRouter()
const tabs = [
  { key: 'plans', label: 'admin.tests.experience.scheduled' },
  { key: 'types', label: 'admin.tests.types' },
  { key: 'operations', label: 'admin.tests.operations.title' },
]
const activeTab = computed(() => tabs.some(tab => tab.key === route.query.tab) ? String(route.query.tab) : 'plans')
const accounts = ref<AccountListItem[]>([])
const active = ref<InstanceType<typeof ActiveQualityPanel> | null>(null)
const operations = ref<InstanceType<typeof QualityOperationsPanel> | null>(null)
function setCatalog(_groups: AdminGroup[], value: AccountListItem[]) { accounts.value = value }
function navigate(tab: string) {
  void router.replace({ query: { tab } })
}
function refresh() {
  if (activeTab.value === 'operations') void operations.value?.refresh()
  else void active.value?.refresh()
}
</script>
