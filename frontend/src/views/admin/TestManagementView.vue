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
          <article v-for="type in types" :key="type.id" class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
            <div class="flex items-start justify-between gap-2"><div><strong class="text-sm text-gray-900 dark:text-white">{{ type.name }}</strong><span class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ type.output_kind }}</span></div><div class="flex gap-2"><button class="text-xs text-primary-600" @click="openType(type)">{{ t('common.edit') }}</button><button class="text-xs text-red-600" @click="removeType(type)">{{ t('common.delete') }}</button></div></div>
            <p class="mt-1 text-xs text-gray-500">{{ type.key }}</p><p class="mt-2 line-clamp-2 text-xs text-gray-600 dark:text-gray-300">{{ type.description || type.prompt }}</p>
          </article>
          <p v-if="!types.length" class="text-sm text-gray-500">{{ t('common.noData') }}</p>
        </div>
      </section>

      <section class="card p-4">
        <div class="mb-4 flex items-center justify-between"><h3 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.plans') }}</h3><button class="btn btn-primary btn-sm" @click="openPlan()"><Icon name="plus" size="sm" /> {{ t('common.create') }}</button></div>
        <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700"><th class="px-2 py-2">{{ t('admin.tests.name') }}</th><th class="px-2 py-2">{{ t('admin.tests.type') }}</th><th class="px-2 py-2">{{ t('admin.tests.target') }}</th><th class="px-2 py-2">{{ t('admin.tests.model') }}</th><th class="px-2 py-2">{{ t('admin.tests.schedule') }}</th><th class="px-2 py-2">{{ t('common.status') }}</th><th class="px-2 py-2 text-right">{{ t('common.actions') }}</th></tr></thead><tbody>
          <tr v-for="plan in plans" :key="plan.id" class="border-b border-gray-100 dark:border-dark-800"><td class="px-2 py-3 font-medium text-gray-900 dark:text-white">{{ plan.name || `#${plan.id}` }}</td><td class="px-2 py-3">{{ typeName(plan) }}</td><td class="px-2 py-3">{{ targetName(plan) }}</td><td class="px-2 py-3 font-mono text-xs">{{ plan.model_id || '-' }}</td><td class="px-2 py-3 font-mono text-xs">{{ plan.cron_expression || '-' }}<div class="mt-1 font-sans text-gray-500">{{ t('admin.tests.nextRun') }}: {{ formatDate(plan.next_run_at || undefined) }}</div></td><td class="px-2 py-3"><span :class="plan.enabled ? 'badge badge-success' : 'badge badge-gray'">{{ plan.enabled ? t('common.enabled') : t('common.disabled') }}</span></td><td class="px-2 py-3"><div class="flex justify-end gap-1"><button class="btn btn-secondary btn-sm" :disabled="runningPlans.has(plan.id)" @click="run(plan)">{{ t('admin.tests.run') }}</button><button class="btn btn-secondary btn-sm" @click="showResults(plan)">{{ t('admin.tests.results') }}</button><button class="btn btn-secondary btn-sm" @click="openPlan(plan)">{{ t('common.edit') }}</button><button class="btn btn-secondary btn-sm text-red-600" @click="removePlan(plan)">{{ t('common.delete') }}</button></div></td></tr>
          <tr v-if="!plans.length"><td colspan="7" class="px-2 py-8 text-center text-sm text-gray-500">{{ t('common.noData') }}</td></tr>
        </tbody></table></div>
      </section>
    </div>

    <BaseDialog :show="!!editingType" :title="editingType?.id ? t('common.edit') : t('common.create')" width="wide" @close="editingType = null"><div v-if="editingType" class="space-y-3"><label class="input-label">{{ t('admin.tests.name') }}<input v-model.trim="editingType.name" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.key') }}<input v-model.trim="editingType.key" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.kind') }}<input v-model.trim="editingType.output_kind" list="test-output-kinds" class="input mt-1 w-full" /><datalist id="test-output-kinds"><option value="html">HTML / SVG</option><option value="number">{{ t('admin.tests.number') }}</option><option value="text">{{ t('admin.tests.text') }}</option></datalist></label><label class="input-label">{{ t('admin.tests.descriptionLabel') }}<input v-model.trim="editingType.description" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.prompt') }}<textarea v-model="editingType.prompt" rows="6" class="input mt-1 w-full" /></label><label class="flex items-center gap-2 text-sm"><input v-model="editingType.enabled" type="checkbox" /> {{ t('common.enabled') }}</label></div><template #footer><button class="btn btn-secondary" @click="editingType = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || !canSaveType" @click="saveType">{{ t('common.save') }}</button></template></BaseDialog>

    <BaseDialog :show="!!editingPlan" :title="editingPlan?.id ? t('common.edit') : t('common.create')" width="wide" @close="editingPlan = null"><div v-if="editingPlan" class="grid gap-3 sm:grid-cols-2"><label class="input-label">{{ t('admin.tests.name') }}<input v-model.trim="editingPlan.name" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.type') }}<select v-model.number="editingPlan.test_definition_id" class="input mt-1 w-full"><option :value="0" disabled>{{ t('admin.tests.selectType') }}</option><option v-for="type in types" :key="type.id" :value="type.id" :disabled="!type.enabled">{{ type.name }}</option></select></label><label class="input-label">{{ t('admin.tests.target') }}<select v-model="targetKind" class="input mt-1 w-full"><option value="group">{{ t('admin.tests.group') }}</option><option value="account">{{ t('admin.tests.account') }}</option></select></label><label v-if="targetKind === 'group'" class="input-label">{{ t('admin.tests.group') }}<select v-model="editingPlan.group_id" class="input mt-1 w-full"><option :value="null">{{ t('admin.tests.selectGroup') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} (#{{ group.id }})</option></select></label><label v-else class="input-label">{{ t('admin.tests.account') }}<select v-model="editingPlan.account_id" class="input mt-1 w-full"><option :value="null">{{ t('admin.tests.selectAccount') }}</option><option v-for="account in accounts" :key="account.id" :value="account.id">{{ account.name || `#${account.id}` }} (#{{ account.id }})</option></select></label><p v-if="targetKind === 'group'" class="text-xs text-gray-500 sm:col-span-2">{{ t('admin.tests.groupHint') }}</p><label class="input-label">{{ t('admin.tests.model') }}<input v-model.trim="editingPlan.model_id" class="input mt-1 w-full" placeholder="gpt-4o-mini" /></label><label class="input-label sm:col-span-2">{{ t('admin.tests.cron') }}<input v-model.trim="editingPlan.cron_expression" class="input mt-1 w-full" placeholder="*/30 * * * *" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.cronHint') }}</span></label><label class="input-label">{{ t('admin.tests.maxResults') }}<input v-model.number="editingPlan.max_results" min="1" type="number" class="input mt-1 w-full" /></label><label class="flex items-center gap-2 pt-5 text-sm"><input v-model="editingPlan.enabled" type="checkbox" /> {{ t('common.enabled') }}</label></div><template #footer><button class="btn btn-secondary" @click="editingPlan = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || !canSavePlan" @click="savePlan">{{ t('common.save') }}</button></template></BaseDialog>

    <BaseDialog :show="!!resultPlan" :title="`${t('admin.tests.results')} · ${resultPlan?.name || ''}`" width="wide" @close="resultPlan = null">
      <div v-if="resultPlan" class="space-y-3">
        <div class="flex items-center justify-between gap-3">
          <span class="text-xs text-gray-500">{{ t('admin.tests.resultsHint') }}</span>
          <button class="btn btn-secondary btn-sm" :disabled="resultsLoading" @click="refreshResults">{{ t('common.refresh') }}</button>
        </div>
        <p v-if="!results.length" class="text-sm text-gray-500">{{ resultsLoading ? t('common.loading') : t('common.noData') }}</p>
        <article v-for="result in results" :key="result.id" class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
          <div v-if="result.error_message" class="mb-2 rounded bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ result.error_message }}</div>
          <div class="flex flex-wrap justify-between gap-2 text-xs text-gray-500">
            <span>{{ result.test_name || typeName(resultPlan) }} · {{ result.model_id }} · {{ t('admin.tests.account') }} {{ result.account_id || '-' }}</span>
            <span>{{ result.status }} · {{ result.latency_ms ?? '-' }}ms · {{ formatDate(result.started_at) }}</span>
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
import TestResultOutput from '@/components/tests/TestResultOutput.vue'
import type { AccountListItem, AdminGroup, CreateTestPlanRequest, CreateTestTypeRequest, TestPlan, TestResult, TestType } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const app = useAppStore()
const resultsLoading = ref(false)
const runningPlans = ref(new Set<number>())
const reportError = (error: unknown) => app.showError(extractApiErrorMessage(error, t('tests.loadFailed')))
const loading = ref(false)
const saving = ref(false)
const types = ref<TestType[]>([])
const plans = ref<TestPlan[]>([])
const groups = ref<AdminGroup[]>([])
const accounts = ref<AccountListItem[]>([])
const results = ref<TestResult[]>([])
const editingType = ref<(CreateTestTypeRequest & { id?: number }) | null>(null)
const editingPlan = ref<(CreateTestPlanRequest & { id?: number }) | null>(null)
const targetKind = ref<'group' | 'account'>('group')
const resultPlan = ref<TestPlan | null>(null)

const canSaveType = computed(() => Boolean(editingType.value?.name && editingType.value?.key && editingType.value?.prompt && editingType.value?.output_kind))
const canSavePlan = computed(() => {
  const plan = editingPlan.value
  return Boolean(plan?.test_definition_id && plan.model_id && plan.cron_expression && (targetKind.value === 'group' ? plan.group_id : plan.account_id))
})

const load = async () => {
  loading.value = true
  try {
    const [loadedTypes, loadedPlans, loadedGroups, loadedAccounts] = await Promise.all([adminAPI.tests.listTypes(false), adminAPI.tests.listPlans(), adminAPI.groups.getAll(), loadAllActiveAccounts()])
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

const openType = (type?: TestType) => { editingType.value = type ? { id: type.id, name: type.name, key: type.key, output_kind: type.output_kind, prompt: type.prompt, description: type.description || '', enabled: type.enabled } : { name: '', key: '', output_kind: 'html', prompt: '', description: '', enabled: true } }
const saveType = async () => { if (!editingType.value) return; saving.value = true; try { const { id, ...body } = editingType.value; if (id) await adminAPI.tests.updateType(id, body); else await adminAPI.tests.createType(body); editingType.value = null; await load() } catch (error) { reportError(error) } finally { saving.value = false } }
const removeType = async (type: TestType) => { if (!window.confirm(`${t('common.delete')} ${type.name}?`)) return; try { await adminAPI.tests.deleteType(type.id); await load() } catch (error) { reportError(error) } }

const openPlan = (plan?: TestPlan) => {
  if (plan) {
    targetKind.value = plan.account_id ? 'account' : 'group'
    editingPlan.value = { id: plan.id, name: plan.name, test_definition_id: plan.test_definition_id || 0, group_id: targetKind.value === 'group' ? (plan.group_id ?? null) : null, account_id: targetKind.value === 'account' ? (plan.account_id ?? null) : null, model_id: plan.model_id || '', cron_expression: plan.cron_expression || '', enabled: plan.enabled, max_results: plan.max_results || 50 }
  } else {
    targetKind.value = 'group'
    editingPlan.value = { name: '', test_definition_id: types.value.find(type => type.enabled)?.id || 0, group_id: null, account_id: null, model_id: '', cron_expression: '*/30 * * * *', enabled: true, max_results: 50 }
  }
}
const savePlan = async () => { if (!editingPlan.value) return; saving.value = true; try { const { id, ...body } = editingPlan.value; const payload = { ...body, group_id: targetKind.value === 'group' ? body.group_id : null, account_id: targetKind.value === 'account' ? body.account_id : null }; if (id) await adminAPI.tests.updatePlan(id, payload); else await adminAPI.tests.createPlan(payload); editingPlan.value = null; await load() } catch (error) { reportError(error) } finally { saving.value = false } }
const removePlan = async (plan: TestPlan) => { if (!window.confirm(`${t('common.delete')} ${plan.name || `#${plan.id}`}?`)) return; try { await adminAPI.tests.deletePlan(plan.id); await load() } catch (error) { reportError(error) } }
const run = async (plan: TestPlan) => {
  if (runningPlans.value.has(plan.id)) return
  runningPlans.value.add(plan.id)
  try {
    await adminAPI.tests.runPlan(plan.id)
    app.showSuccess(t('admin.tests.runStarted'))
    await showResults(plan)
  } catch (error) { reportError(error) }
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
    if (resultPlan.value?.id === id) results.value = next
  } catch (error) { reportError(error) }
  finally { resultsLoading.value = false }
}
let resultTimer: ReturnType<typeof setInterval> | undefined
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
