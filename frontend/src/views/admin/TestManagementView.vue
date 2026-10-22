<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.title') }}</h2><p class="mt-1 text-sm text-gray-500">{{ t('admin.tests.quality.subtitle') }}</p></div>
        <button class="btn btn-secondary" @click="refresh"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
      </header>
      <nav class="flex gap-1 overflow-x-auto border-b border-gray-200 pb-2 dark:border-dark-700" role="tablist" :aria-label="t('admin.tests.title')">
        <button v-for="tab in tabs" :key="tab.key" type="button" role="tab" :aria-selected="activeTab === tab.key" class="shrink-0 rounded-lg px-4 py-2 text-sm font-medium" :class="activeTab === tab.key ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-50 dark:hover:bg-dark-800'" @click="navigate(tab.key)">{{ t(tab.label) }}</button>
      </nav>
      <ChannelQualityOverview v-show="activeTab === 'overview'" ref="overview" :visible="activeTab === 'overview'" :groups="groups" :account-id="accountId" @navigate="navigate" />
      <ActiveQualityPanel v-show="activeTab === 'plans' || activeTab === 'types'" ref="active" :tab="activeTab" @catalog="setCatalog" />
      <PassiveQualityPanel v-show="activeTab === 'passive' || activeTab === 'records'" ref="passive" :tab="activeTab" :groups="groups" :accounts="accounts" @tab="navigate" />
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
import PassiveQualityPanel from '@/components/tests/PassiveQualityPanel.vue'
import ChannelQualityOverview from '@/components/tests/ChannelQualityOverview.vue'
import QualityOperationsPanel from '@/components/tests/QualityOperationsPanel.vue'
import type { AdminGroup, AccountListItem } from '@/types'
const { t } = useI18n()
const route = useRoute(), router = useRouter()
const tabs = [
  { key: 'overview', label: 'admin.tests.quality.overview' },
  { key: 'plans', label: 'admin.tests.quality.active' },
  { key: 'passive', label: 'admin.tests.quality.passive' },
  { key: 'records', label: 'admin.tests.passive.records' },
  { key: 'types', label: 'admin.tests.quality.questionBank' },
  { key: 'operations', label: 'admin.tests.operations.title' },
]
const activeTab = computed(() => tabs.some(tab => tab.key === route.query.tab) ? String(route.query.tab) : 'overview')
const accountId = computed(() => Number(route.query.account_id) || undefined)
const groups = ref<AdminGroup[]>([]), accounts = ref<AccountListItem[]>([])
const active = ref<InstanceType<typeof ActiveQualityPanel> | null>(null)
const passive = ref<InstanceType<typeof PassiveQualityPanel> | null>(null)
const overview = ref<InstanceType<typeof ChannelQualityOverview> | null>(null)
const operations = ref<InstanceType<typeof QualityOperationsPanel> | null>(null)
function setCatalog(g: AdminGroup[], a: AccountListItem[]) { groups.value = g; accounts.value = a }
function navigate(tab: string, policy?: number, account?: number) {
  void router.replace({ query: { tab, ...(account ? { account_id: account } : {}), ...(policy ? tab === 'plans' ? { plan_id: policy } : { policy_id: policy } : {}) } })
}
function refresh() {
  if (activeTab.value === 'overview') void overview.value?.refresh()
  else if (activeTab.value === 'plans' || activeTab.value === 'types') void active.value?.refresh()
  else if (activeTab.value === 'operations') void operations.value?.refresh()
  else void passive.value?.refresh()
}
</script>
