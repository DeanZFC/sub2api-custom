<template>
  <AppLayout>
    <div class="space-y-6">
      <header class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 pb-5 dark:border-dark-700">
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.title') }}</h2>
        </div>
        <button class="btn btn-secondary" :disabled="loading" @click="load"><Icon name="refresh" size="sm" /> {{ t('common.refresh') }}</button>
      </header>

      <section class="border-b border-gray-200 pb-6 dark:border-dark-700">
        <div class="mb-4 flex items-center justify-between"><h3 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.types') }}</h3><button class="btn btn-primary btn-sm" @click="openType()"><Icon name="plus" size="sm" /> {{ t('common.create') }}</button></div>
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          <article v-for="type in orderedTypes" :key="type.id" class="rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900">
            <div class="flex items-start justify-between gap-2"><div class="min-w-0 break-words"><strong class="text-sm text-gray-900 dark:text-white">{{ type.name }}</strong><span class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ typeKindLabel(type.output_kind) }}</span></div><div class="flex shrink-0 gap-1"><button class="btn btn-secondary h-8 w-8 p-0" :title="t('common.copy')" :aria-label="t('common.copy')" :disabled="saving" @click="copyType(type)"><Icon name="copy" size="sm" /></button><button class="btn btn-secondary h-8 w-8 p-0" :title="t('common.edit')" :aria-label="t('common.edit')" @click="openType(type)"><Icon name="edit" size="sm" /></button><button class="btn btn-secondary h-8 w-8 p-0 text-red-600" :title="t('common.delete')" :aria-label="t('common.delete')" @click="removeType(type)"><Icon name="trash" size="sm" /></button></div></div>
            <p class="mt-1 text-xs text-gray-500">{{ type.key }}</p><p class="mt-2 line-clamp-2 text-xs text-gray-600 dark:text-gray-300">{{ type.description || (type.output_kind === 'statistics' ? t('admin.tests.statisticsHint') : type.prompt) }}</p>
            <label class="mt-3 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.sortOrder') }}<input v-model.number="type.sort_order" type="number" min="0" class="input h-8 w-20 text-xs" @change="updateTypeOrder(type)" /></label>
          </article>
          <p v-if="!types.length" class="text-sm text-gray-500">{{ t('common.noData') }}</p>
        </div>
      </section>

      <section>
        <div class="mb-4 flex items-center justify-between"><h3 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.plans') }}</h3><button data-testid="plan-create" class="btn btn-primary btn-sm" @click="openPlan()"><Icon name="plus" size="sm" /> {{ t('common.create') }}</button></div>
        <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700"><th class="px-2 py-2">{{ t('admin.tests.name') }}</th><th class="px-2 py-2">{{ t('admin.tests.type') }}</th><th class="px-2 py-2">{{ t('admin.tests.target') }}</th><th class="px-2 py-2">{{ t('admin.tests.model') }}</th><th class="px-2 py-2">{{ t('admin.tests.schedule') }}</th><th class="px-2 py-2">{{ t('common.status') }}</th><th class="px-2 py-2 text-right">{{ t('common.actions') }}</th></tr></thead><tbody>
          <tr v-for="plan in orderedPlans" :key="plan.id" class="border-b border-gray-100 dark:border-dark-800">
            <td class="min-w-32 px-2 py-3 font-medium text-gray-900 dark:text-white">{{ plan.name || `#${plan.id}` }}<div class="mt-1 text-xs font-normal text-gray-500">{{ t('admin.tests.sortOrder') }}: {{ plan.sort_order ?? 0 }}</div></td>
            <td class="min-w-32 px-2 py-3"><div class="flex flex-wrap gap-1"><span v-for="type in planTypes(plan)" :key="type.id" class="badge badge-gray">{{ type.name }}</span><span v-if="!planTypes(plan).length">-</span></div></td>
            <td class="min-w-32 px-2 py-3">{{ groupName(plan) }}<div class="mt-1 text-xs text-gray-500">{{ targetName(plan) }}</div></td>
            <td class="min-w-32 px-2 py-3 font-mono text-xs">{{ plan.model_id || '-' }}<span v-if="plan.reasoning_effort" class="ml-1 text-gray-500">({{ plan.reasoning_effort }})</span></td>
            <td class="min-w-40 px-2 py-3 font-mono text-xs">{{ plan.cron_expression || '-' }}<div class="mt-1 font-sans text-gray-500">{{ t('admin.tests.nextRun') }}: {{ formatDate(plan.next_run_at || undefined) }}</div></td>
            <td class="px-2 py-3"><span :class="plan.enabled ? 'badge badge-success' : 'badge badge-gray'">{{ plan.enabled ? t('common.enabled') : t('common.disabled') }}</span><span v-if="plan.protection?.enabled" class="badge badge-primary mt-1">{{ t('admin.tests.protection.title') }}</span></td>
            <td class="px-2 py-3"><div class="flex justify-end gap-1">
              <button class="btn btn-secondary h-8 w-8 shrink-0 p-0" :title="t('admin.tests.run')" :aria-label="t('admin.tests.run')" :disabled="runningPlans.has(plan.id) || manualSchedulingStopped(plan)" @click="run(plan)"><Icon name="play" size="sm" /></button>
              <button class="btn btn-secondary h-8 w-8 shrink-0 p-0" :title="t('admin.tests.results')" :aria-label="t('admin.tests.results')" @click="showResults(plan)"><Icon name="eye" size="sm" /></button>
              <button role="switch" :aria-checked="plan.enabled" class="btn btn-secondary h-8 w-8 shrink-0 p-0" :title="plan.enabled ? t('admin.tests.disablePlan') : t('admin.tests.enablePlan')" :aria-label="plan.enabled ? t('admin.tests.disablePlan') : t('admin.tests.enablePlan')" :disabled="togglingPlanId === plan.id" @click="togglePlanStatus(plan)"><Icon :name="plan.enabled ? 'ban' : 'checkCircle'" size="sm" /></button>
              <button class="btn btn-secondary h-8 w-8 shrink-0 p-0" :title="t('common.copy')" :aria-label="t('common.copy')" :disabled="saving" @click="copyPlan(plan)"><Icon name="copy" size="sm" /></button>
              <button class="btn btn-secondary h-8 w-8 shrink-0 p-0" :title="t('common.edit')" :aria-label="t('common.edit')" @click="openPlan(plan)"><Icon name="edit" size="sm" /></button>
              <button class="btn btn-secondary h-8 w-8 shrink-0 p-0 text-red-600" :title="t('common.delete')" :aria-label="t('common.delete')" @click="removePlan(plan)"><Icon name="trash" size="sm" /></button>
            </div></td>
          </tr>
          <tr v-if="!orderedPlans.length"><td colspan="7" class="px-2 py-8 text-center text-sm text-gray-500">{{ t('common.noData') }}</td></tr>
        </tbody></table></div>
      </section>
    </div>

    <BaseDialog :show="!!editingType" :title="editingType?.id ? t('common.edit') : t('common.create')" width="wide" @close="editingType = null"><div v-if="editingType" class="space-y-3"><label class="input-label">{{ t('admin.tests.name') }}<input v-model.trim="editingType.name" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.key') }}<input v-model.trim="editingType.key" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.sortOrder') }}<input v-model.number="editingType.sort_order" min="0" type="number" class="input mt-1 w-full" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.sortOrderHint') }}</span></label><label class="input-label">{{ t('admin.tests.kind') }}<select v-model="editingType.output_kind" class="input mt-1 w-full" data-type-kind><option value="html">HTML / SVG</option><option value="number">{{ t('admin.tests.number') }}</option><option value="text">{{ t('admin.tests.text') }}</option><option value="statistics">{{ t('admin.tests.statistics') }}</option><option v-if="!['html', 'number', 'text', 'statistics'].includes(editingType.output_kind)" :value="editingType.output_kind">{{ editingType.output_kind }}</option></select></label><label class="input-label">{{ t('admin.tests.descriptionLabel') }}<input v-model.trim="editingType.description" class="input mt-1 w-full" /></label><p v-if="editingType.output_kind === 'statistics'" class="text-sm text-gray-500 dark:text-gray-400" data-statistics-hint>{{ t('admin.tests.statisticsHint') }}</p><label v-else class="input-label">{{ t('admin.tests.prompt') }}<textarea v-model="editingType.prompt" rows="6" class="input mt-1 w-full" /></label><label class="flex items-center gap-2 text-sm"><input v-model="editingType.enabled" type="checkbox" /> {{ t('common.enabled') }}</label></div><template #footer><button class="btn btn-secondary" @click="editingType = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || !canSaveType" @click="saveType">{{ t('common.save') }}</button></template></BaseDialog>

    <BaseDialog :show="!!editingPlan" :title="editingPlan?.id ? t('common.edit') : t('common.create')" width="wide" @close="editingPlan = null">
      <div v-if="editingPlan" class="grid gap-4 sm:grid-cols-2">
        <label class="input-label">{{ t('admin.tests.name') }}<input v-model.trim="editingPlan.name" class="input mt-1 w-full" /></label>
        <label class="input-label">{{ t('admin.tests.sortOrder') }}<input v-model.number="editingPlan.sort_order" min="0" type="number" class="input mt-1 w-full" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.planSortOrderHint') }}</span></label>
        <fieldset class="sm:col-span-2">
          <legend class="input-label">{{ t('admin.tests.types') }}</legend>
          <div class="mt-2 flex flex-wrap gap-x-6 gap-y-3">
            <label v-for="type in orderedTypes" :key="type.id" class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200" :class="{ 'opacity-50': !type.enabled }">
              <input v-model="editingPlan.test_definition_ids" :data-testid="`plan-type-${type.id}`" type="checkbox" :value="type.id" :disabled="!type.enabled && !editingPlan.test_definition_ids.includes(type.id)" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
              {{ type.name }}<span v-if="!type.enabled" class="text-xs text-gray-500">{{ t('common.disabled') }}</span>
            </label>
          </div>
          <p v-if="!orderedTypes.length" class="mt-2 text-sm text-gray-500">{{ t('common.noData') }}</p>
        </fieldset>
        <label class="input-label">{{ t('admin.tests.group') }}<select v-model.number="editingPlan.group_id" class="input mt-1 w-full"><option :value="null">{{ t('admin.tests.selectGroup') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} (#{{ group.id }})</option></select></label>
        <label class="input-label">{{ t('admin.tests.accountOptional') }}<select v-model="accountSelection" class="input mt-1 w-full" :disabled="!editingPlan.group_id"><option :value="null">{{ editingPlan.group_id ? t('admin.tests.groupTest') : t('admin.tests.selectGroupFirst') }}</option><option value="all" :disabled="!editingPlan.group_id">{{ t('admin.tests.allAccountsInGroup') }}</option><option v-for="account in filteredAccounts" :key="account.id" :value="account.id">{{ account.name || t('admin.tests.account') }} (#{{ account.id }})</option></select></label>
        <p class="text-xs text-gray-500 sm:col-span-2">{{ targetModeHint }}</p>
        <p v-if="invalidAccountTarget" class="text-xs text-amber-700 dark:text-amber-300 sm:col-span-2" data-invalid-account-target>{{ t('admin.tests.protection.accountMovedHint') }}</p>
        <label class="input-label">{{ t('admin.tests.model') }}<Select v-model="editingPlan.model_id" :options="modelOptions" :loading="modelOptionsLoading" searchable :disabled="!editingPlan.group_id || modelOptionsLoading" :placeholder="editingPlan.group_id ? t('admin.tests.model') : t('admin.tests.selectGroupFirst')" class="mt-1" /></label>
        <label v-if="reasoningEffortOptions.length" class="input-label">{{ t('admin.tests.reasoningEffort') }}<select v-model="editingPlan.reasoning_effort" class="input mt-1 w-full"><option :value="null">{{ t('admin.tests.reasoningEffortDefault') }}</option><option v-for="effort in reasoningEffortOptions" :key="effort" :value="effort">{{ effort }}</option></select><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.reasoningEffortHint') }}</span></label>
        <label class="input-label sm:col-span-2">{{ t('admin.tests.cron') }}<input v-model.trim="editingPlan.cron_expression" class="input mt-1 w-full" placeholder="*/30 * * * *" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.cronHint') }}</span></label>
        <TestProtectionEditor v-model="editingPlan.protection" :types="selectedProtectionTypes" :target-mode="editingPlan.target_mode" :groups="protectionGroups" class="sm:col-span-2" />
        <label class="input-label">{{ t('admin.tests.maxResults') }}<input v-model.number="editingPlan.max_results" min="1" type="number" class="input mt-1 w-full" /></label>
        <label class="flex items-center gap-2 pt-5 text-sm"><input v-model="editingPlan.enabled" type="checkbox" /> {{ t('common.enabled') }}</label>
      </div>
      <template #footer><button class="btn btn-secondary" @click="editingPlan = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || !canSavePlan" @click="savePlan">{{ t('common.save') }}</button></template>
    </BaseDialog>

    <BaseDialog :show="!!resultPlan" :title="`${t('admin.tests.results')} · ${resultPlan?.name || ''}`" width="extra-wide" @close="closeResults">
      <div v-if="resultPlan" class="min-w-0 space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 pb-3 dark:border-dark-700">
          <div class="flex gap-1" role="tablist" :aria-label="t('admin.tests.results')">
            <button type="button" role="tab" :aria-selected="!resultsHistory" class="rounded-md px-3 py-2 text-sm font-medium" :class="!resultsHistory ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-50 dark:hover:bg-dark-800'" :disabled="resultsLoading" @click="setResultHistory(false)">{{ t('admin.tests.latestResults') }}</button>
            <button type="button" role="tab" :aria-selected="resultsHistory" class="rounded-md px-3 py-2 text-sm font-medium" :class="resultsHistory ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-50 dark:hover:bg-dark-800'" :disabled="resultsLoading" @click="setResultHistory(true)">{{ t('admin.tests.showHistory') }}</button>
          </div>
          <button class="btn btn-secondary btn-sm" :disabled="resultsLoading" @click="refreshResults"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
        </div>
        <AdminTestResultHistory :key="resultPlan.id" :results="results" :accounts="accounts" :types="types" :now="resultsNow" :history="resultsHistory" :loading="resultsLoading" :retrying-result-id="retryingResultId" :deleting-result-id="deletingResultId" @retry="retryResult" @delete="removeResult" />
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { reasoningEffortsForTestModel } from '@/utils/testReasoningEfforts'
import AdminTestResultHistory from '@/components/tests/AdminTestResultHistory.vue'
import TestProtectionEditor from '@/components/tests/TestProtectionEditor.vue'
import { copyTestProtection, validTestProtection } from '@/utils/testProtection'
import type { AccountListItem, AdminGroup, CreateTestPlanRequest, CreateTestTypeRequest, TestPlan, TestResult, TestType, TestProtectionConfig } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const app = useAppStore()
const resultsLoading = ref(false)
const runningPlans = ref(new Set<number>())
const togglingPlanId = ref<number | null>(null)
const reportError = (error: unknown) => app.showError(extractApiErrorMessage(error, t('tests.loadFailed')))
const loading = ref(false)
const saving = ref(false)
const types = ref<TestType[]>([])
const plans = ref<TestPlan[]>([])
const groups = ref<AdminGroup[]>([])
const accounts = ref<AccountListItem[]>([])
const modelOptions = ref<SelectOption[]>([])
const modelOptionsLoading = ref(false)
const results = ref<TestResult[]>([])
const resultsNow = ref(Date.now())
const resultsHistory = ref(false)
let resultsRequest = 0
// A manual run is asynchronous. Keep a local placeholder visible immediately
// after the request is accepted until the first persisted result arrives.
const pendingRuns = ref(new Map<number, TestResult[]>())
const deletingResultId = ref<number | null>(null)
const retryingResultId = ref<number | null>(null)
const editingType = ref<(CreateTestTypeRequest & { id?: number }) | null>(null)
const editingPlan = ref<(CreateTestPlanRequest & { id?: number; test_definition_ids: number[]; protection: TestProtectionConfig }) | null>(null)
const resultPlan = ref<TestPlan | null>(null)
type AccountSelection = number | 'all' | null

const canSaveType = computed(() => Boolean(editingType.value?.name && editingType.value?.key && editingType.value?.output_kind && (editingType.value.output_kind === 'statistics' || editingType.value.prompt.trim())))
const typeKindLabel = (kind: string) => kind === 'statistics' ? t('admin.tests.statistics') : kind
const orderedTypes = computed(() => [...types.value].sort((a, b) => {
  const orderA = a.sort_order == null ? Number.MAX_SAFE_INTEGER : a.sort_order
  const orderB = b.sort_order == null ? Number.MAX_SAFE_INTEGER : b.sort_order
  return orderA - orderB || a.id - b.id
}))
const orderedPlans = computed(() => [...plans.value].sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0) || a.id - b.id))
const planTypeIDs = (plan: TestPlan): number[] => {
  if (plan.test_definition_ids?.length) return [...new Set(plan.test_definition_ids)]
  if (plan.test_definitions?.length) return plan.test_definitions.map(type => type.id)
  const id = plan.test_definition_id || plan.test_definition?.id
  return id ? [id] : []
}
const planTypes = (plan: TestPlan) => planTypeIDs(plan).map(id => ({
  id,
  name: types.value.find(type => type.id === id)?.name || plan.test_definitions?.find(type => type.id === id)?.name || (plan.test_definition?.id === id ? plan.test_definition.name : `#${id}`),
}))
const canSavePlan = computed(() => {
  const plan = editingPlan.value
  return Boolean(plan?.test_definition_ids.length && plan.model_id && plan.cron_expression && plan.group_id
    && !invalidAccountTarget.value
    && validTestProtection(plan.protection, plan.target_mode, selectedProtectionTypes.value, protectionGroups.value))
})

const selectedProtectionTypes = computed(() => orderedTypes.value.filter(type => editingPlan.value?.test_definition_ids.includes(type.id)))
const eligibleProtectionGroups = (groupID?: number | null) => {
  const source = groups.value.find(group => group.id === groupID)
  return source ? groups.value.filter(group => group.platform === source.platform && group.platform !== 'composite' && group.status === 'active') : []
}
const protectionGroups = computed(() => eligibleProtectionGroups(editingPlan.value?.group_id))

const filteredAccounts = computed(() => {
  const groupID = editingPlan.value?.group_id
  if (!groupID) return []
  const savedPlan = plans.value.find(plan => plan.id === editingPlan.value?.id)
  // Keep an existing target editable after its quality rules moved it to a new group.
  return accounts.value.filter(account => account.group_ids?.includes(Number(groupID))
    || (savedPlan?.group_id === groupID && savedPlan.account_id === account.id))
})
const invalidAccountTarget = computed(() => editingPlan.value?.target_mode === 'account'
  && !filteredAccounts.value.some(account => account.id === editingPlan.value?.account_id))
const accountSelection = computed<AccountSelection>({
  get: () => {
    const plan = editingPlan.value
    if (!plan || plan.target_mode === 'group') return null
    if (plan.target_mode === 'all_accounts' || !plan.account_id) return 'all'
    return plan.account_id
  },
  set: value => {
    const plan = editingPlan.value
    if (!plan) return
    if (value === null) {
      plan.target_mode = 'group'
      plan.account_id = null
    } else if (value === 'all') {
      plan.target_mode = 'all_accounts'
      plan.account_id = null
    } else {
      plan.target_mode = 'account'
      plan.account_id = Number(value)
    }
  },
})
const targetModeHint = computed(() => {
  const mode = editingPlan.value?.target_mode
  if (mode === 'group') return t('admin.tests.groupHint')
  if (mode === 'all_accounts') return t('admin.tests.allAccountsHint')
  return t('admin.tests.accountHint')
})
const reasoningEffortOptions = computed(() => {
  const plan = editingPlan.value
  if (!plan?.model_id) return []
  return reasoningEffortsForTestModel(plan.model_id, accounts.value, plan.group_id, plan.account_id)
})
const loadModelOptions = async (groupID: number | null) => {
  if (!groupID) {
    modelOptions.value = []
    return
  }
  const group = groups.value.find(item => item.id === groupID)
  if (!group) return
  modelOptionsLoading.value = true
  try {
    const models = await adminAPI.groups.getModelAllowlistCandidates(groupID, group.platform)
    const unique = Array.from(new Set(models.map(model => String(model).trim()).filter(Boolean)))
    // Keep an existing plan's model selectable even if the upstream catalog
    // has since changed or the model was removed from the current allowlist.
    const current = editingPlan.value?.model_id?.trim()
    if (current && !unique.includes(current)) unique.unshift(current)
    modelOptions.value = unique.map(value => ({ value, label: value }))
  } catch (error) {
    modelOptions.value = editingPlan.value?.model_id ? [{ value: editingPlan.value.model_id, label: editingPlan.value.model_id }] : []
    reportError(error)
  } finally {
    modelOptionsLoading.value = false
  }
}
const load = async () => {
  loading.value = true
  try {
    const [loadedTypes, loadedPlans, loadedGroups, loadedAccounts] = await Promise.all([adminAPI.tests.listTypes(false), adminAPI.tests.listPlans(), adminAPI.groups.getAll(), loadTestAccounts()])
    types.value = loadedTypes
    plans.value = loadedPlans
    groups.value = loadedGroups
    accounts.value = loadedAccounts
  } catch (error) {
    reportError(error)
  } finally {
    loading.value = false
  }
}

// Include quality-paused accounts so administrators can edit their existing
// protection or schedule follow-up checks. A manual scheduling stop is retained
// in the selector; the runner, and the single-account Run button, respect it.
const loadTestAccounts = async (): Promise<AccountListItem[]> => {
  const pageSize = 1000
  const loadStatus = async (status: 'active' | 'quality_paused') => {
    const first = await adminAPI.accounts.list(1, pageSize, { lite: '1', status })
    const items = [...(first.items || [])]
    const pages = Math.max(first.pages || 1, Math.ceil((first.total || items.length) / pageSize))
    const rest = await Promise.all(Array.from({ length: pages - 1 }, (_, index) => adminAPI.accounts.list(index + 2, pageSize, { lite: '1', status })))
    for (const page of rest) items.push(...(page.items || []))
    return items
  }
  const batches = await Promise.all([loadStatus('active'), loadStatus('quality_paused')])
  return [...new Map(batches.flat().map(account => [account.id, account])).values()]
}

const openType = (type?: TestType) => { editingType.value = type ? { id: type.id, name: type.name, key: type.key, output_kind: type.output_kind, prompt: type.prompt, description: type.description || '', enabled: type.enabled, sort_order: type.sort_order ?? 0 } : { name: '', key: '', output_kind: 'html', prompt: '', description: '', enabled: true, sort_order: orderedTypes.value.length ? Math.max(...orderedTypes.value.map(item => item.sort_order ?? 0)) + 1 : 0 } }
const saveType = async () => { if (!editingType.value) return; saving.value = true; try { const { id, ...body } = editingType.value; if (body.output_kind === 'statistics') body.prompt = ''; if (id) await adminAPI.tests.updateType(id, body); else await adminAPI.tests.createType(body); editingType.value = null; await load() } catch (error) { reportError(error) } finally { saving.value = false } }
const copyType = async (type: TestType) => {
  if (saving.value) return
  saving.value = true
  try {
    const usedKeys = new Set(types.value.map(item => item.key))
    // Definition keys are limited to 100 characters by the API. Preserve the
    // copy suffix even when an existing definition already uses the full limit.
    const baseKey = `${type.key.slice(0, 95)}-copy`
    let key = baseKey
    let suffix = 2
    while (usedKeys.has(key)) {
      const suffixText = `-${suffix++}`
      key = `${baseKey.slice(0, 100 - suffixText.length)}${suffixText}`
    }
    await adminAPI.tests.createType({
      name: `${type.name}（Copy）`,
      key,
      description: type.description || '',
      output_kind: type.output_kind,
      prompt: type.output_kind === 'statistics' ? '' : type.prompt,
      enabled: type.enabled,
      sort_order: type.sort_order,
    })
    await load()
  } catch (error) {
    reportError(error)
  } finally {
    saving.value = false
  }
}
const updateTypeOrder = async (type: TestType) => {
  if (saving.value) return
  saving.value = true
  try {
    await adminAPI.tests.updateType(type.id, { sort_order: Math.max(0, Number(type.sort_order) || 0) })
    await load()
  } catch (error) {
    reportError(error)
  } finally {
    saving.value = false
  }
}
const removeType = async (type: TestType) => { if (!window.confirm(`${t('common.delete')} ${type.name}?`)) return; try { await adminAPI.tests.deleteType(type.id); await load() } catch (error) { reportError(error) } }

const openPlan = (plan?: TestPlan) => {
  if (plan) {
    const account = plan.account_id ? accounts.value.find(item => item.id === plan.account_id) : undefined
    const targetMode = plan.target_mode || (plan.account_id ? 'account' : 'all_accounts')
    editingPlan.value = { id: plan.id, name: plan.name, sort_order: plan.sort_order ?? 0, test_definition_ids: planTypeIDs(plan), group_id: plan.group_id ?? account?.group_ids?.[0] ?? null, account_id: plan.account_id ?? null, target_mode: targetMode, model_id: plan.model_id || '', reasoning_effort: plan.reasoning_effort ?? null, cron_expression: plan.cron_expression || '', enabled: plan.enabled, max_results: plan.max_results || 50, protection: copyTestProtection(plan.protection) }
  } else {
    editingPlan.value = { name: '', sort_order: plans.value.length ? Math.max(...plans.value.map(item => item.sort_order ?? 0)) + 1 : 0, test_definition_ids: [], group_id: null, account_id: null, target_mode: 'group', model_id: '', reasoning_effort: null, cron_expression: '*/30 * * * *', enabled: true, max_results: 50, protection: copyTestProtection() }
  }
  void loadModelOptions(editingPlan.value.group_id ?? null)
}
const savePlan = async () => { if (!editingPlan.value || !canSavePlan.value) return; saving.value = true; try { const { id, ...body } = editingPlan.value; const payload = { ...body, test_definition_id: body.test_definition_ids[0], group_id: body.group_id || null, account_id: body.account_id || null }; if (id) await adminAPI.tests.updatePlan(id, payload); else await adminAPI.tests.createPlan(payload); editingPlan.value = null; await load() } catch (error) { reportError(error) } finally { saving.value = false } }
const copyPlan = async (plan: TestPlan) => {
  if (saving.value) return
  const selectedAccount = plan.account_id ? accounts.value.find(account => account.id === plan.account_id) : undefined
  const targetInvalid = Boolean(plan.account_id && (!plan.group_id || !selectedAccount?.group_ids?.includes(plan.group_id)))
  const protectionInvalid = !validTestProtection(plan.protection, plan.target_mode, types.value.filter(type => planTypeIDs(plan).includes(type.id)), eligibleProtectionGroups(plan.group_id))
  if (targetInvalid || protectionInvalid) {
    // An existing rule can continue checking a moved account. Its copy has no
    // tracked membership yet, so let the administrator choose a valid target.
    openPlan(plan)
    editingPlan.value!.id = undefined
    editingPlan.value!.name = `${plan.name || `#${plan.id}`}（Copy）`
    return
  }
  saving.value = true
  try {
    await adminAPI.tests.createPlan({
      name: `${plan.name || `#${plan.id}`}（Copy）`,
      sort_order: plan.sort_order ?? 0,
      test_definition_id: planTypeIDs(plan)[0],
      test_definition_ids: planTypeIDs(plan),
      group_id: plan.group_id ?? null,
      account_id: plan.account_id ?? null,
      target_mode: plan.target_mode,
      model_id: plan.model_id || '',
      reasoning_effort: plan.reasoning_effort ?? null,
      cron_expression: plan.cron_expression || '*/30 * * * *',
      enabled: plan.enabled,
      max_results: plan.max_results || 50,
      protection: copyTestProtection(plan.protection),
    })
    await load()
  } catch (error) {
    reportError(error)
  } finally {
    saving.value = false
  }
}
const removePlan = async (plan: TestPlan) => { if (!window.confirm(`${t('common.delete')} ${plan.name || `#${plan.id}`}?`)) return; try { await adminAPI.tests.deletePlan(plan.id); await load() } catch (error) { reportError(error) } }
const togglePlanStatus = async (plan: TestPlan) => {
  if (togglingPlanId.value === plan.id) return
  togglingPlanId.value = plan.id
  try {
    await adminAPI.tests.updatePlan(plan.id, { enabled: !plan.enabled })
    plan.enabled = !plan.enabled
  } catch (error) {
    reportError(error)
  } finally {
    togglingPlanId.value = null
  }
}
const manualSchedulingStopped = (plan: TestPlan) => Boolean(plan.account_id && accounts.value.find(account => account.id === plan.account_id)?.schedulable === false)
const run = async (plan: TestPlan) => {
  if (runningPlans.value.has(plan.id) || manualSchedulingStopped(plan)) return
  runningPlans.value.add(plan.id)
  const startedAt = new Date().toISOString()
  pendingRuns.value.set(plan.id, planTypeIDs(plan).map((typeID, index) => ({
    id: -(index + 1),
    plan_id: plan.id,
    plan_name: plan.name,
    test_definition_id: typeID,
    test_name: planTypes(plan).find(type => type.id === typeID)?.name,
    group_id: plan.group_id ?? null,
    account_id: plan.account_id ?? null,
    target_mode: plan.target_mode,
    model_id: plan.model_id,
    reasoning_effort: plan.reasoning_effort ?? null,
    status: 'running',
    output_kind: types.value.find(type => type.id === typeID)?.output_kind || 'text',
    response_text: null,
    error_message: null,
    latency_ms: null,
    started_at: startedAt,
    created_at: startedAt,
  })))
  resultsHistory.value = false
  resultsRequest++
  resultsLoading.value = false
  resultPlan.value = plan
  results.value = pendingRuns.value.get(plan.id)!
  try {
    await adminAPI.tests.runPlan(plan.id)
    app.showSuccess(t('admin.tests.runStarted'))
    await refreshResults()
  } catch (error) {
    pendingRuns.value.delete(plan.id)
    results.value = results.value.filter(result => result.id > 0 || result.plan_id !== plan.id)
    reportError(error)
  }
  finally { runningPlans.value.delete(plan.id) }
}
const showResults = async (plan: TestPlan) => {
  resultsRequest++
  resultsLoading.value = false
  results.value = []
  resultsHistory.value = false
  resultPlan.value = plan
  await refreshResults()
}
const closeResults = () => {
  resultsRequest++
  resultsLoading.value = false
  resultPlan.value = null
}
const setResultHistory = async (history: boolean) => {
  if (resultsHistory.value === history) return
  resultsHistory.value = history
  await refreshResults()
}
const refreshResults = async () => {
  resultsNow.value = Date.now()
  const id = resultPlan.value?.id
  if (!id || resultsLoading.value) return
  const request = ++resultsRequest
  resultsLoading.value = true
  try {
    const next = await adminAPI.tests.listResults(id, resultsHistory.value ? resultPlan.value?.max_results || 50 : 1)
    if (request === resultsRequest && resultPlan.value?.id === id) {
      const placeholders = (pendingRuns.value.get(id) || []).filter(pending => !next.some(result => {
        const started = result.started_at ? new Date(result.started_at).getTime() : 0
        return result.test_definition_id === pending.test_definition_id && started >= new Date(pending.started_at || 0).getTime()
      }))
      if (placeholders.length) pendingRuns.value.set(id, placeholders)
      else pendingRuns.value.delete(id)
      results.value = [...placeholders, ...next]
    }
  } catch (error) { if (request === resultsRequest) reportError(error) }
  finally { if (request === resultsRequest) resultsLoading.value = false }
}
const removeResult = async (result: TestResult) => {
  if (!window.confirm(`${t('common.delete')} #${result.id}?`)) return
  if (deletingResultId.value !== null) return
  const planID = resultPlan.value?.id
  deletingResultId.value = result.id
  try {
    await adminAPI.tests.deleteResult(result.id)
    app.showSuccess(t('common.deleted'))
    if (resultPlan.value?.id === planID) {
      resultsRequest++
      resultsLoading.value = false
      results.value = results.value.filter(item => item.id !== result.id)
      await refreshResults()
    }
  } catch (error) {
    reportError(error)
  } finally {
    deletingResultId.value = null
  }
}
const retryResult = async (result: TestResult) => {
  if (!result.id || !result.account_id || retryingResultId.value !== null) return
  const planID = resultPlan.value?.id
  retryingResultId.value = result.id
  try {
    const updated = await adminAPI.tests.retryResult(result.id)
    // The retry endpoint updates the existing execution row and returns that
    // row in its new running state. Replace it in place so the administrator
    // sees the cleared output/error immediately without adding another card.
    if (resultPlan.value?.id === planID && (updated.plan_id == null || updated.plan_id === planID)) {
      resultsRequest++
      resultsLoading.value = false
      const index = results.value.findIndex(item => item.id === result.id)
      if (index >= 0 && updated.id === result.id) results.value.splice(index, 1, { ...updated, account_name: updated.account_name || results.value[index].account_name })
    }
    app.showSuccess(t('admin.tests.retryStarted'))
  } catch (error) {
    reportError(error)
  } finally {
    retryingResultId.value = null
  }
}
let resultTimer: ReturnType<typeof setInterval> | undefined
watch(
  () => editingPlan.value ? [editingPlan.value.target_mode, ...editingPlan.value.test_definition_ids] : [],
  () => {
    const plan = editingPlan.value
    if (!plan) return
    if (plan.target_mode === 'group') plan.protection.enabled = false
    plan.protection.rules = plan.protection.rules.filter(rule => plan.test_definition_ids.includes(rule.test_definition_id))
  },
)
watch(
  () => [editingPlan.value?.model_id, editingPlan.value?.group_id, editingPlan.value?.account_id] as const,
  () => {
    const plan = editingPlan.value
    if (plan?.reasoning_effort && !reasoningEffortOptions.value.includes(plan.reasoning_effort)) {
      plan.reasoning_effort = null
    }
  },
  { deep: true },
)
watch(
  () => editingPlan.value?.group_id,
  (groupID, previous) => {
    if (groupID !== previous) {
      // Opening an existing plan initializes the watcher from undefined;
      // preserve its current model until the catalog has loaded. Clear only
      // when the administrator actively switches to another group.
      if (previous !== undefined && editingPlan.value) {
        editingPlan.value.model_id = ''
        const selected = editingPlan.value.account_id
        if (selected && !filteredAccounts.value.some(account => account.id === selected)) accountSelection.value = null
      }
      void loadModelOptions(groupID ?? null)
    }
  },
)
watch(resultPlan, plan => {
  clearInterval(resultTimer)
  if (plan) resultTimer = setInterval(() => {
    resultsNow.value = Date.now()
    const hasRunning = results.value.some(result => result.status === 'running' || result.status === 'pending')
    if (!document.hidden && (!resultsHistory.value || hasRunning)) void refreshResults()
  }, 5000)
})
onUnmounted(() => {
  clearInterval(resultTimer)
  resultsRequest++
})
const groupName = (plan: TestPlan) => plan.group_id ? groups.value.find(group => group.id === plan.group_id)?.name || `#${plan.group_id}` : '-'
const targetName = (plan: TestPlan) => plan.account_id
  ? `${accounts.value.find(account => account.id === plan.account_id)?.name || t('admin.tests.account')} (#${plan.account_id})`
  : t(plan.target_mode === 'group' ? 'admin.tests.groupTest' : 'admin.tests.allAccountsInGroup')
const formatDate = (value?: string) => value ? new Date(value).toLocaleString() : '-'
onMounted(load)
</script>
