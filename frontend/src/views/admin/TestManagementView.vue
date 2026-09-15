<template>
  <AppLayout>
    <div class="space-y-6">
      <header class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 pb-5 dark:border-dark-700">
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.title') }}</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.tests.description') }}</p>
        </div>
        <button class="btn btn-secondary" :disabled="loading" @click="load"><Icon name="refresh" size="sm" /> {{ t('common.refresh') }}</button>
      </header>

      <section class="card p-4">
        <div class="mb-4 flex items-center justify-between"><h3 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.types') }}</h3><button class="btn btn-primary btn-sm" @click="openType()"><Icon name="plus" size="sm" /> {{ t('common.create') }}</button></div>
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          <article v-for="type in orderedTypes" :key="type.id" class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
            <div class="flex items-start justify-between gap-2"><div><strong class="text-sm text-gray-900 dark:text-white">{{ type.name }}</strong><span class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ type.output_kind }}</span></div><div class="flex gap-2"><button class="text-xs text-primary-600" :disabled="saving" @click="copyType(type)">{{ t('common.copy') }}</button><button class="text-xs text-primary-600" @click="openType(type)">{{ t('common.edit') }}</button><button class="text-xs text-red-600" @click="removeType(type)">{{ t('common.delete') }}</button></div></div>
            <p class="mt-1 text-xs text-gray-500">{{ type.key }}</p><p class="mt-2 line-clamp-2 text-xs text-gray-600 dark:text-gray-300">{{ type.description || type.prompt }}</p>
            <label class="mt-3 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.sortOrder') }}<input v-model.number="type.sort_order" type="number" min="0" class="input h-8 w-20 text-xs" @change="updateTypeOrder(type)" /></label>
          </article>
          <p v-if="!types.length" class="text-sm text-gray-500">{{ t('common.noData') }}</p>
        </div>
      </section>

      <section class="card p-4">
        <div class="mb-4 flex items-center justify-between"><h3 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.plans') }}</h3><button data-testid="plan-create" class="btn btn-primary btn-sm" @click="openPlan()"><Icon name="plus" size="sm" /> {{ t('common.create') }}</button></div>
        <div v-if="planTypeTabs.length" class="mb-4 flex gap-2 overflow-x-auto border-b border-gray-200 dark:border-dark-700" role="tablist" :aria-label="t('admin.tests.type')">
          <button v-for="type in planTypeTabs" :key="type.id" :data-testid="`plan-tab-${type.id}`" role="tab" :aria-selected="activePlanTypeId === type.id" class="shrink-0 border-b-2 px-3 py-2 text-sm font-medium transition-colors" :class="activePlanTypeId === type.id ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'" @click="activePlanTypeId = type.id">{{ type.name }}</button>
        </div>
        <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700"><th class="px-2 py-2">{{ t('admin.tests.name') }}</th><th class="px-2 py-2">{{ t('admin.tests.type') }}</th><th class="px-2 py-2">{{ t('admin.tests.target') }}</th><th class="px-2 py-2">{{ t('admin.tests.model') }}</th><th class="px-2 py-2">{{ t('admin.tests.schedule') }}</th><th class="px-2 py-2">{{ t('common.status') }}</th><th class="px-2 py-2 text-right">{{ t('common.actions') }}</th></tr></thead><tbody>
          <tr v-for="plan in visiblePlans" :key="plan.id" class="border-b border-gray-100 dark:border-dark-800"><td class="px-2 py-3 font-medium text-gray-900 dark:text-white">{{ plan.name || `#${plan.id}` }}</td><td class="px-2 py-3">{{ typeName(plan) }}</td><td class="px-2 py-3">{{ targetName(plan) }}</td><td class="px-2 py-3 font-mono text-xs">{{ plan.model_id || '-' }}<span v-if="plan.reasoning_effort" class="ml-1 text-gray-500">({{ plan.reasoning_effort }})</span></td><td class="px-2 py-3 font-mono text-xs">{{ plan.cron_expression || '-' }}<div class="mt-1 font-sans text-gray-500">{{ t('admin.tests.nextRun') }}: {{ formatDate(plan.next_run_at || undefined) }}</div></td><td class="px-2 py-3"><span :class="plan.enabled ? 'badge badge-success' : 'badge badge-gray'">{{ plan.enabled ? t('common.enabled') : t('common.disabled') }}</span></td><td class="px-2 py-3"><div class="flex justify-end gap-1"><button class="btn btn-secondary btn-sm" :disabled="runningPlans.has(plan.id)" @click="run(plan)">{{ t('admin.tests.run') }}</button><button class="btn btn-secondary btn-sm" @click="showResults(plan)">{{ t('admin.tests.results') }}</button><button class="btn btn-secondary btn-sm" :disabled="togglingPlanId === plan.id" @click="togglePlanStatus(plan)">{{ togglingPlanId === plan.id ? t('common.loading') : plan.enabled ? t('admin.tests.disablePlan') : t('admin.tests.enablePlan') }}</button><button class="btn btn-secondary btn-sm" :disabled="saving" @click="copyPlan(plan)">{{ t('common.copy') }}</button><button class="btn btn-secondary btn-sm" @click="openPlan(plan)">{{ t('common.edit') }}</button><button class="btn btn-secondary btn-sm text-red-600" @click="removePlan(plan)">{{ t('common.delete') }}</button></div></td></tr>
          <tr v-if="!visiblePlans.length"><td colspan="7" class="px-2 py-8 text-center text-sm text-gray-500">{{ t('common.noData') }}</td></tr>
        </tbody></table></div>
      </section>
    </div>

    <BaseDialog :show="!!editingType" :title="editingType?.id ? t('common.edit') : t('common.create')" width="wide" @close="editingType = null"><div v-if="editingType" class="space-y-3"><label class="input-label">{{ t('admin.tests.name') }}<input v-model.trim="editingType.name" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.key') }}<input v-model.trim="editingType.key" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.sortOrder') }}<input v-model.number="editingType.sort_order" min="0" type="number" class="input mt-1 w-full" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.sortOrderHint') }}</span></label><label class="input-label">{{ t('admin.tests.kind') }}<input v-model.trim="editingType.output_kind" list="test-output-kinds" class="input mt-1 w-full" /><datalist id="test-output-kinds"><option value="html">HTML / SVG</option><option value="number">{{ t('admin.tests.number') }}</option><option value="text">{{ t('admin.tests.text') }}</option></datalist></label><label class="input-label">{{ t('admin.tests.descriptionLabel') }}<input v-model.trim="editingType.description" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.prompt') }}<textarea v-model="editingType.prompt" rows="6" class="input mt-1 w-full" /></label><label class="flex items-center gap-2 text-sm"><input v-model="editingType.enabled" type="checkbox" /> {{ t('common.enabled') }}</label></div><template #footer><button class="btn btn-secondary" @click="editingType = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || !canSaveType" @click="saveType">{{ t('common.save') }}</button></template></BaseDialog>

    <BaseDialog :show="!!editingPlan" :title="editingPlan?.id ? t('common.edit') : t('common.create')" width="wide" @close="editingPlan = null"><div v-if="editingPlan" class="grid gap-3 sm:grid-cols-2"><label class="input-label">{{ t('admin.tests.name') }}<input v-model.trim="editingPlan.name" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.sortOrder') }}<input v-model.number="editingPlan.sort_order" min="0" type="number" class="input mt-1 w-full" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.planSortOrderHint') }}</span></label><label class="input-label">{{ t('admin.tests.type') }}<select v-model.number="editingPlan.test_definition_id" class="input mt-1 w-full"><option :value="0" disabled>{{ t('admin.tests.selectType') }}</option><option v-for="type in orderedTypes" :key="type.id" :value="type.id" :disabled="!type.enabled">{{ type.name }}</option></select></label><label class="input-label">{{ t('admin.tests.group') }}<select v-model.number="editingPlan.group_id" class="input mt-1 w-full"><option :value="null">{{ t('admin.tests.selectGroup') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} (#{{ group.id }})</option></select></label><label class="input-label">{{ t('admin.tests.accountOptional') }}<select v-model="accountSelection" class="input mt-1 w-full" :disabled="!editingPlan.group_id"><option :value="null">{{ editingPlan.group_id ? t('admin.tests.groupTest') : t('admin.tests.selectGroupFirst') }}</option><option value="all" :disabled="!editingPlan.group_id">{{ t('admin.tests.allAccountsInGroup') }}</option><option v-for="account in filteredAccounts" :key="account.id" :value="account.id">{{ account.name || t('admin.tests.account') }} (#{{ account.id }})</option></select></label><p class="text-xs text-gray-500 sm:col-span-2">{{ targetModeHint }}</p><label class="input-label">{{ t('admin.tests.model') }}<Select v-model="editingPlan.model_id" :options="modelOptions" :loading="modelOptionsLoading" searchable :disabled="!editingPlan.group_id || modelOptionsLoading" :placeholder="editingPlan.group_id ? t('admin.tests.model') : t('admin.tests.selectGroupFirst')" class="mt-1" /></label><label v-if="reasoningEffortOptions.length" class="input-label">{{ t('admin.tests.reasoningEffort') }}<select v-model="editingPlan.reasoning_effort" class="input mt-1 w-full"><option :value="null">{{ t('admin.tests.reasoningEffortDefault') }}</option><option v-for="effort in reasoningEffortOptions" :key="effort" :value="effort">{{ effort }}</option></select><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.reasoningEffortHint') }}</span></label><label class="input-label sm:col-span-2">{{ t('admin.tests.cron') }}<input v-model.trim="editingPlan.cron_expression" class="input mt-1 w-full" placeholder="*/30 * * * *" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.cronHint') }}</span></label><label class="input-label">{{ t('admin.tests.maxResults') }}<input v-model.number="editingPlan.max_results" min="1" type="number" class="input mt-1 w-full" /></label><label class="flex items-center gap-2 pt-5 text-sm"><input v-model="editingPlan.enabled" type="checkbox" /> {{ t('common.enabled') }}</label></div><template #footer><button class="btn btn-secondary" @click="editingPlan = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || !canSavePlan" @click="savePlan">{{ t('common.save') }}</button></template></BaseDialog>

    <BaseDialog :show="!!resultPlan" :title="`${t('admin.tests.results')} · ${resultPlan?.name || ''}`" width="wide" @close="resultPlan = null">
      <div v-if="resultPlan" class="space-y-3">
        <div class="flex items-center justify-between gap-3">
          <span class="text-xs text-gray-500">{{ t('admin.tests.resultsHint') }}</span>
          <button class="btn btn-secondary btn-sm" :disabled="resultsLoading" @click="refreshResults">{{ t('common.refresh') }}</button>
        </div>
        <p v-if="!results.length" class="text-sm text-gray-500">{{ resultsLoading ? t('common.loading') : t('common.noData') }}</p>
        <article v-for="result in results" :key="result.id" class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
          <div v-if="result.error_message" class="mb-2 rounded bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ result.error_message }}</div>
          <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500">
            <span>{{ result.test_name || typeName(resultPlan) }} · {{ result.model_id || '-' }}<template v-if="result.reasoning_effort"> · {{ t('admin.tests.reasoningEffort') }}: {{ result.reasoning_effort }}</template> · {{ t('admin.tests.account') }} {{ result.account_id || '-' }}</span>
            <span class="flex items-center gap-2">
              <span>{{ result.status }} · {{ result.latency_ms ?? '-' }}ms · {{ formatDate(result.started_at) }}</span>
              <button
                v-if="result.status === 'failed' && result.account_id"
                type="button"
                class="text-primary-600 hover:text-primary-700 disabled:cursor-not-allowed disabled:opacity-50"
                :disabled="retryingResultId === result.id"
                @click="retryResult(result)"
              >
                {{ retryingResultId === result.id ? t('common.loading') : t('admin.tests.retry') }}
              </button>
              <button
                v-if="result.id > 0"
                type="button"
                class="text-red-600 hover:text-red-700 disabled:cursor-not-allowed disabled:opacity-50"
                :disabled="deletingResultId === result.id"
                @click="removeResult(result)"
              >
                {{ deletingResultId === result.id ? t('common.loading') : t('common.delete') }}
              </button>
            </span>
          </div>
          <TestResultOutput class="mt-3" :result="result" />
        </article>
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
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import type { AccountListItem, AdminGroup, CreateTestPlanRequest, CreateTestTypeRequest, TestPlan, TestResult, TestType } from '@/types'
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
const activePlanTypeId = ref<number | null>(null)
const groups = ref<AdminGroup[]>([])
const accounts = ref<AccountListItem[]>([])
const modelOptions = ref<SelectOption[]>([])
const modelOptionsLoading = ref(false)
const results = ref<TestResult[]>([])
// A manual run is asynchronous. Keep a local placeholder visible immediately
// after the request is accepted until the first persisted result arrives.
const pendingRuns = ref(new Map<number, TestResult>())
const deletingResultId = ref<number | null>(null)
const retryingResultId = ref<number | null>(null)
const editingType = ref<(CreateTestTypeRequest & { id?: number }) | null>(null)
const editingPlan = ref<(CreateTestPlanRequest & { id?: number }) | null>(null)
const resultPlan = ref<TestPlan | null>(null)
type AccountSelection = number | 'all' | null

const canSaveType = computed(() => Boolean(editingType.value?.name && editingType.value?.key && editingType.value?.prompt && editingType.value?.output_kind))
const orderedTypes = computed(() => [...types.value].sort((a, b) => {
  const orderA = a.sort_order == null ? Number.MAX_SAFE_INTEGER : a.sort_order
  const orderB = b.sort_order == null ? Number.MAX_SAFE_INTEGER : b.sort_order
  return orderA - orderB || a.id - b.id
}))
const planTypeTabs = computed(() => {
  const tabs = orderedTypes.value.map(type => ({ id: type.id, name: type.name }))
  if (plans.value.some(plan => !plan.test_definition_id)) tabs.push({ id: 0, name: t('admin.tests.uncategorized') })
  return tabs
})
const visiblePlans = computed(() => activePlanTypeId.value === null
  ? plans.value
  : activePlanTypeId.value === 0
    ? plans.value.filter(plan => !plan.test_definition_id)
    : plans.value.filter(plan => plan.test_definition_id === activePlanTypeId.value))
const canSavePlan = computed(() => {
  const plan = editingPlan.value
  return Boolean(plan?.test_definition_id && plan.model_id && plan.cron_expression && plan.group_id)
})

const filteredAccounts = computed(() => {
  const groupID = editingPlan.value?.group_id
  if (!groupID) return []
  return accounts.value.filter(account => account.group_ids?.includes(Number(groupID)))
})
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
    const [loadedTypes, loadedPlans, loadedGroups, loadedAccounts] = await Promise.all([adminAPI.tests.listTypes(false), adminAPI.tests.listPlans(), adminAPI.groups.getAll(), loadAllActiveAccounts()])
    types.value = loadedTypes
    plans.value = loadedPlans
    groups.value = loadedGroups
    accounts.value = loadedAccounts
    if (!planTypeTabs.value.some(type => type.id === activePlanTypeId.value)) activePlanTypeId.value = planTypeTabs.value[0]?.id ?? null
  } catch (error) {
    reportError(error)
  } finally {
    loading.value = false
  }
}

// The account endpoint is paginated. A group plan can target any schedulable
// account, so loading only the first page would make older accounts impossible
// to select and would render their names as bare IDs in the plan table.
const loadAllActiveAccounts = async (): Promise<AccountListItem[]> => {
  const pageSize = 1000
  const first = await adminAPI.accounts.list(1, pageSize, { lite: '1', status: 'active' })
  const items = [...(first.items || [])]
  const pages = Math.max(first.pages || 1, Math.ceil((first.total || items.length) / pageSize))
  if (pages <= 1) return items
  const rest = await Promise.all(Array.from({ length: pages - 1 }, (_, index) => adminAPI.accounts.list(index + 2, pageSize, { lite: '1', status: 'active' })))
  for (const page of rest) items.push(...(page.items || []))
  return items
}

const openType = (type?: TestType) => { editingType.value = type ? { id: type.id, name: type.name, key: type.key, output_kind: type.output_kind, prompt: type.prompt, description: type.description || '', enabled: type.enabled, sort_order: type.sort_order ?? 0 } : { name: '', key: '', output_kind: 'html', prompt: '', description: '', enabled: true, sort_order: orderedTypes.value.length ? Math.max(...orderedTypes.value.map(item => item.sort_order ?? 0)) + 1 : 0 } }
const saveType = async () => { if (!editingType.value) return; saving.value = true; try { const { id, ...body } = editingType.value; if (id) await adminAPI.tests.updateType(id, body); else await adminAPI.tests.createType(body); editingType.value = null; await load() } catch (error) { reportError(error) } finally { saving.value = false } }
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
      prompt: type.prompt,
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
    editingPlan.value = { id: plan.id, name: plan.name, sort_order: plan.sort_order ?? 0, test_definition_id: plan.test_definition_id || 0, group_id: plan.group_id ?? account?.group_ids?.[0] ?? null, account_id: plan.account_id ?? null, target_mode: targetMode, model_id: plan.model_id || '', reasoning_effort: plan.reasoning_effort ?? null, cron_expression: plan.cron_expression || '', enabled: plan.enabled, max_results: plan.max_results || 50 }
  } else {
    editingPlan.value = { name: '', sort_order: visiblePlans.value.length ? Math.max(...visiblePlans.value.map(item => item.sort_order ?? 0)) + 1 : 0, test_definition_id: activePlanTypeId.value && activePlanTypeId.value > 0 ? activePlanTypeId.value : (orderedTypes.value.find(type => type.enabled)?.id || 0), group_id: null, account_id: null, target_mode: 'group', model_id: '', reasoning_effort: null, cron_expression: '*/30 * * * *', enabled: true, max_results: 50 }
  }
  void loadModelOptions(editingPlan.value.group_id ?? null)
}
const savePlan = async () => { if (!editingPlan.value) return; saving.value = true; try { const { id, ...body } = editingPlan.value; const payload = { ...body, group_id: body.group_id || null, account_id: body.account_id || null }; if (id) await adminAPI.tests.updatePlan(id, payload); else await adminAPI.tests.createPlan(payload); editingPlan.value = null; await load() } catch (error) { reportError(error) } finally { saving.value = false } }
const copyPlan = async (plan: TestPlan) => {
  if (saving.value) return
  saving.value = true
  try {
    await adminAPI.tests.createPlan({
      name: `${plan.name || `#${plan.id}`}（Copy）`,
      sort_order: plan.sort_order ?? 0,
      test_definition_id: plan.test_definition_id || plan.test_definition?.id || 0,
      group_id: plan.group_id ?? null,
      account_id: plan.account_id ?? null,
      target_mode: plan.target_mode,
      model_id: plan.model_id || '',
      reasoning_effort: plan.reasoning_effort ?? null,
      cron_expression: plan.cron_expression || '*/30 * * * *',
      enabled: plan.enabled,
      max_results: plan.max_results || 50,
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
const run = async (plan: TestPlan) => {
  if (runningPlans.value.has(plan.id)) return
  runningPlans.value.add(plan.id)
  const startedAt = new Date().toISOString()
  pendingRuns.value.set(plan.id, {
    id: -plan.id,
    plan_id: plan.id,
    plan_name: plan.name,
    test_name: typeName(plan),
    group_id: plan.group_id ?? null,
    account_id: plan.account_id ?? null,
    model_id: plan.model_id,
    reasoning_effort: plan.reasoning_effort ?? null,
    status: 'running',
    output_kind: types.value.find(type => type.id === plan.test_definition_id)?.output_kind || 'text',
    response_text: null,
    error_message: null,
    latency_ms: null,
    started_at: startedAt,
    created_at: startedAt,
  })
  resultPlan.value = plan
  results.value = [pendingRuns.value.get(plan.id)!]
  try {
    await adminAPI.tests.runPlan(plan.id)
    app.showSuccess(t('admin.tests.runStarted'))
    await refreshResults()
  } catch (error) {
    pendingRuns.value.delete(plan.id)
    results.value = results.value.filter(result => result.id !== -plan.id)
    reportError(error)
  }
  finally { runningPlans.value.delete(plan.id) }
}
const showResults = async (plan: TestPlan) => {
  results.value = []
  resultPlan.value = plan
  await refreshResults()
}
const refreshResults = async () => {
  const id = resultPlan.value?.id
  if (!id || resultsLoading.value) return
  resultsLoading.value = true
  try {
    const next = await adminAPI.tests.listResults(id, resultPlan.value?.max_results || 50)
    if (resultPlan.value?.id === id) {
      const pending = pendingRuns.value.get(id)
      const hasFreshResult = pending && next.some(result => {
        const started = result.started_at ? new Date(result.started_at).getTime() : 0
        return started >= new Date(pending.started_at || 0).getTime()
      })
      if (hasFreshResult) pendingRuns.value.delete(id)
      const placeholder = pendingRuns.value.get(id)
      results.value = placeholder ? [placeholder, ...next] : next
    }
  } catch (error) { reportError(error) }
  finally { resultsLoading.value = false }
}
const removeResult = async (result: TestResult) => {
  if (!window.confirm(`${t('common.delete')} #${result.id}?`)) return
  if (deletingResultId.value !== null) return
  deletingResultId.value = result.id
  try {
    await adminAPI.tests.deleteResult(result.id)
    app.showSuccess(t('common.deleted'))
    await refreshResults()
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
      const index = results.value.findIndex(item => item.id === result.id)
      if (index >= 0 && updated.id === result.id) results.value.splice(index, 1, updated)
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
  if (plan) resultTimer = setInterval(() => { if (!document.hidden) void refreshResults() }, 5000)
})
onUnmounted(() => clearInterval(resultTimer))
const typeName = (plan: TestPlan) => types.value.find(type => type.id === plan.test_definition_id)?.name || plan.test_definition?.name || '-'
const targetName = (plan: TestPlan) => plan.account_id ? accounts.value.find(account => account.id === plan.account_id)?.name || `#${plan.account_id}` : plan.group_id ? groups.value.find(group => group.id === plan.group_id)?.name || `#${plan.group_id}` : '-'
const formatDate = (value?: string) => value ? new Date(value).toLocaleString() : '-'
onMounted(load)
</script>
