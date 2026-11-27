<template>
  <AppLayout>
    <div class="min-w-0 space-y-6">
      <header class="card p-4 sm:p-6">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h1 class="page-title flex items-center gap-2">
            <Icon name="beaker" size="md" class="text-primary-600 dark:text-primary-400" />
            {{ t('admin.tests.title') }}
          </h1>
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="refresh">
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />{{ t('common.refresh') }}
          </button>
        </div>
        <nav class="tabs mt-4 inline-flex" role="tablist" :aria-label="t('admin.tests.title')">
          <button
            v-for="tab in tabs"
            :key="tab.key"
            type="button"
            role="tab"
            class="tab"
            :class="{ 'tab-active': activeTab === tab.key }"
            :data-quality-section="tab.key"
            :aria-selected="activeTab === tab.key"
            @click="navigate(tab.key)"
          >{{ t(tab.label) }}</button>
        </nav>
      </header>
      <p v-if="catalogError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ catalogError }}</p>
      <QualityOperationsPanel v-if="activeTab === 'operations'" ref="operations" :visible="true" :accounts="accounts" />
      <IntelligencePolicyPanel
        v-else-if="catalogReady"
        ref="intelligencePanel"
        :groups="groups"
        :accounts="accounts"
        :plans="plans"
        :catalog-ready="catalogReady"
        :initial-plan-id="Number(route.query.plan_id) || undefined"
        @changed="reloadCatalog"
      />
      <p v-else-if="loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import IntelligencePolicyPanel from '@/components/tests/IntelligencePolicyPanel.vue'
import QualityOperationsPanel from '@/components/tests/QualityOperationsPanel.vue'
import { extractApiErrorMessage } from '@/utils/apiError'
import { loadTestAccounts } from '@/utils/testAccounts'
import type { AccountListItem, AdminGroup, TestPlan } from '@/types'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const tabs = [
  { key: 'intelligence', label: 'channelMonitorV2.intelligence.monitor' },
  { key: 'operations', label: 'admin.tests.operations.title' },
] as const
const activeTab = computed(() => route.query.tab === 'operations' && !route.query.plan_id ? 'operations' : 'intelligence')
const plans = ref<TestPlan[]>([])
const groups = ref<AdminGroup[]>([])
const accounts = ref<AccountListItem[]>([])
const catalogReady = ref(false)
const loading = ref(false)
const catalogError = ref('')
const intelligencePanel = ref<InstanceType<typeof IntelligencePolicyPanel> | null>(null)
const operations = ref<InstanceType<typeof QualityOperationsPanel> | null>(null)
let catalogRequest = 0

async function reloadCatalog() {
  const request = ++catalogRequest
  loading.value = true
  catalogError.value = ''
  try {
    const [loadedPlans, loadedGroups, loadedAccounts] = await Promise.all([
      adminAPI.tests.listPlans(),
      adminAPI.groups.getAll(),
      loadTestAccounts(),
    ])
    if (request !== catalogRequest) return
    plans.value = loadedPlans
    groups.value = loadedGroups
    accounts.value = loadedAccounts
    catalogReady.value = true
  } catch (error) {
    if (request === catalogRequest) catalogError.value = extractApiErrorMessage(error, t('tests.loadFailed'))
  } finally {
    if (request === catalogRequest) loading.value = false
  }
}

function navigate(tab: typeof tabs[number]['key']) {
  void router.replace({ query: { tab } })
}

function refresh() {
  void reloadCatalog()
  if (activeTab.value === 'operations') void operations.value?.refresh()
  else void intelligencePanel.value?.refresh()
}

onMounted(reloadCatalog)
</script>
