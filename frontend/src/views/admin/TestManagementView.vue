<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.title') }}</h2><p v-if="activeTab !== 'intelligence'" class="mt-1 text-sm text-gray-500">{{ t('admin.tests.experience.activeHint') }}</p></div>
        <button class="btn btn-secondary" @click="refresh"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
      </header>
      <nav class="flex gap-1 overflow-x-auto border-b border-gray-200 pb-2 dark:border-dark-700" role="tablist" :aria-label="t('admin.tests.title')">
        <button v-for="tab in tabs" :key="tab.key" type="button" role="tab" :data-quality-section="tab.key" :aria-selected="activeTab === tab.key" class="shrink-0 rounded-lg px-4 py-2 text-sm font-medium" :class="activeTab === tab.key ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-50 dark:hover:bg-dark-800'" @click="navigate(tab.key)">{{ t(tab.label) }}</button>
      </nav>
      <ActiveQualityPanel v-show="activeTab === 'plans' || activeTab === 'types'" ref="active" :tab="activeTab" @catalog="setCatalog" />
      <IntelligencePolicyPanel v-if="activeTab === 'intelligence'" ref="intelligencePanel" :groups="groups" :accounts="accounts" :plans="allPlans" :catalog-ready="catalogReady" :initial-plan-id="Number(route.query.plan_id) || undefined" @changed="reloadCatalog" />
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
import IntelligencePolicyPanel from '@/components/tests/IntelligencePolicyPanel.vue'
import QualityOperationsPanel from '@/components/tests/QualityOperationsPanel.vue'
import type { AdminGroup, AccountListItem, TestPlan } from '@/types'
const { t } = useI18n()
const route = useRoute(), router = useRouter()
const tabs = [
  { key: 'intelligence', label: 'channelMonitorV2.intelligence.monitor' },
  { key: 'plans', label: 'admin.tests.experience.scheduled' },
  { key: 'types', label: 'admin.tests.types' },
  { key: 'operations', label: 'admin.tests.operations.title' },
]
const allPlans = ref<TestPlan[]>([])
const groups = ref<AdminGroup[]>([])
const catalogReady = ref(false)
const activeTab = computed(() => {
  if (route.query.plan_id && allPlans.value.some(p => p.id === Number(route.query.plan_id) && p.protection?.intelligence)) return 'intelligence'
  if (tabs.some(tab => tab.key === route.query.tab)) return String(route.query.tab)
  return route.query.plan_id || route.query.tab ? 'plans' : 'intelligence'
})
const intelligencePanel = ref<InstanceType<typeof IntelligencePolicyPanel> | null>(null)
const accounts = ref<AccountListItem[]>([])
const active = ref<InstanceType<typeof ActiveQualityPanel> | null>(null)
const operations = ref<InstanceType<typeof QualityOperationsPanel> | null>(null)
function setCatalog(loadedGroups: AdminGroup[], value: AccountListItem[], plans: TestPlan[] = []) {
  groups.value = loadedGroups
  accounts.value = value
  allPlans.value = plans
  catalogReady.value = true
}
async function reloadCatalog(){ await active.value?.refresh() }
function navigate(tab: string) {
  void router.replace({ query: { tab } })
}
function refresh() {
  if (activeTab.value === 'operations') void operations.value?.refresh()
  else { void reloadCatalog(); if (activeTab.value === 'intelligence') void intelligencePanel.value?.refresh() }
}
</script>
