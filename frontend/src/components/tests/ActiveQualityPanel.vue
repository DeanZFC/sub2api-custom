<template>
  <div>
    <div class="space-y-6">
      <section v-show="activeTab === 'types'" class="border-b border-gray-200 pb-6 dark:border-dark-700">
        <div class="mb-4 flex items-center justify-between"><h3 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.types') }}</h3><button class="btn btn-primary btn-sm" @click="openType()"><Icon name="plus" size="sm" /> {{ t('common.create') }}</button></div>
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          <article v-for="type in orderedTypes" :key="type.id" class="rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900">
            <div class="flex items-start justify-between gap-2"><div class="min-w-0 break-words"><strong class="text-sm text-gray-900 dark:text-white">{{ type.name }}</strong><span class="ml-2 rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ typeKindLabel(type.output_kind) }}</span></div><div class="flex shrink-0 gap-1"><button class="btn btn-secondary h-8 w-8 p-0" :title="t('common.copy')" :aria-label="t('common.copy')" :disabled="saving" @click="copyType(type)"><Icon name="copy" size="sm" /></button><button class="btn btn-secondary h-8 w-8 p-0" :title="t('common.edit')" :aria-label="t('common.edit')" @click="openType(type)"><Icon name="edit" size="sm" /></button><button class="btn btn-secondary h-8 w-8 p-0 text-red-600" :title="t('common.delete')" :aria-label="t('common.delete')" @click="removeType(type)"><Icon name="trash" size="sm" /></button></div></div>
            <p class="mt-1 text-xs text-gray-500">{{ type.key }}</p><p class="mt-2 line-clamp-2 text-xs text-gray-600 dark:text-gray-300">{{ type.description || (type.output_kind === 'statistics' ? t('admin.tests.statisticsHint') : type.output_kind === 'model_check' ? t('admin.tests.modelCheckHint') : type.output_kind === 'capability' ? t('admin.tests.capability.hint') : type.prompt) }}</p>
            <label class="mt-3 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.sortOrder') }}<input v-model.number="type.sort_order" type="number" min="0" class="input h-8 w-20 text-xs" @change="updateTypeOrder(type)" /></label>
          </article>
          <p v-if="!types.length" class="text-sm text-gray-500">{{ t('common.noData') }}</p>
        </div>
      </section>

      <section v-show="activeTab === 'plans'">
        <div class="mb-4 flex items-center justify-between"><h3 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.plans') }}</h3><button data-testid="plan-create" class="btn btn-primary btn-sm" @click="openPlan()"><Icon name="plus" size="sm" /> {{ t('common.create') }}</button></div>
        <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700"><th class="px-2 py-2">{{ t('admin.tests.name') }}</th><th class="px-2 py-2">{{ t('admin.tests.type') }}</th><th class="px-2 py-2">{{ t('admin.tests.target') }}</th><th class="px-2 py-2">{{ t('admin.tests.model') }}</th><th class="px-2 py-2">{{ t('admin.tests.schedule') }}</th><th class="px-2 py-2">{{ t('common.status') }}</th><th class="px-2 py-2 text-right">{{ t('common.actions') }}</th></tr></thead><tbody>
          <tr v-for="plan in orderedPlans" :key="plan.id" class="border-b border-gray-100 dark:border-dark-800">
            <td class="min-w-32 px-2 py-3 font-medium text-gray-900 dark:text-white">{{ plan.name || `#${plan.id}` }}<div class="mt-1 text-xs font-normal text-gray-500">{{ t('admin.tests.sortOrder') }}: {{ plan.sort_order ?? 0 }}</div><p v-if="plan.migration_note" class="mt-2 text-xs font-normal text-amber-700 dark:text-amber-300" data-migration-note>{{ plan.migration_note }}</p></td>
            <td class="min-w-32 px-2 py-3"><div class="flex flex-wrap gap-1"><span v-for="type in planTypes(plan)" :key="type.id" class="badge badge-gray">{{ type.name }}</span><span v-if="!planTypes(plan).length">-</span></div></td>
            <td class="min-w-32 px-2 py-3">{{ groupName(plan) }}<div class="mt-1 text-xs text-gray-500">{{ targetName() }}</div></td>
            <td class="min-w-32 px-2 py-3 font-mono text-xs"><span v-if="modelIDs(plan).length">{{ modelIDs(plan).join(', ') }}</span><span v-else>-</span><span v-if="plan.reasoning_effort" class="ml-1 text-gray-500">({{ plan.reasoning_effort }})</span></td>
            <td class="min-w-52 px-2 py-3 text-xs"><template v-if="plan.schedule"><div v-for="(period, index) in plan.schedule.periods" :key="index">{{ period.start }}–{{ period.end }} · {{ period.interval_minutes % 60 === 0 ? t('admin.tests.timing.everyHours', { count: period.interval_minutes / 60 }) : t('admin.tests.timing.everyMinutes', { count: period.interval_minutes }) }}</div><div class="mt-1 text-gray-500">{{ plan.schedule.timezone }}</div></template><span v-else class="font-mono">{{ plan.cron_expression || '-' }}</span><div class="mt-1 text-gray-500">{{ t('admin.tests.nextRun') }}: {{ formatDate(plan.next_run_at || undefined) }}</div></td>
            <td class="px-2 py-3"><span :class="plan.enabled ? 'badge badge-success' : 'badge badge-gray'">{{ plan.enabled ? t('common.enabled') : t('common.disabled') }}</span><span v-if="plan.protection?.enabled" class="badge badge-primary mt-1">{{ t('admin.tests.protection.title') }}</span></td>
            <td class="px-2 py-3"><div class="flex justify-end gap-1">
              <button class="btn btn-secondary h-8 w-8 shrink-0 p-0" :title="t('admin.tests.run')" :aria-label="t('admin.tests.run')" :disabled="runningPlans.has(plan.id) || !!plan.migration_note" @click="run(plan)"><Icon name="play" size="sm" /></button>
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

    <BaseDialog :show="!!editingType" :title="editingType?.id ? t('common.edit') : t('common.create')" width="wide" @close="editingType = null"><div v-if="editingType" class="space-y-3"><label class="input-label">{{ t('admin.tests.name') }}<input v-model.trim="editingType.name" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.key') }}<input v-model.trim="editingType.key" class="input mt-1 w-full" /></label><label class="input-label">{{ t('admin.tests.sortOrder') }}<input v-model.number="editingType.sort_order" min="0" type="number" class="input mt-1 w-full" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.sortOrderHint') }}</span></label><label class="input-label">{{ t('admin.tests.kind') }}<select v-model="editingType.output_kind" class="input mt-1 w-full" data-type-kind><option value="html">HTML / SVG</option><option value="number">{{ t('admin.tests.number') }}</option><option value="text">{{ t('admin.tests.text') }}</option><option value="statistics">{{ t('admin.tests.statistics') }}</option><option value="model_check">{{ t('admin.tests.modelCheck') }}</option><option value="capability">{{ t('admin.tests.capability.title') }}</option><option v-if="!['html', 'number', 'text', 'statistics', 'model_check', 'capability'].includes(editingType.output_kind)" :value="editingType.output_kind">{{ editingType.output_kind }}</option></select></label><label class="input-label">{{ t('admin.tests.descriptionLabel') }}<input v-model.trim="editingType.description" class="input mt-1 w-full" /></label><p v-if="editingType.output_kind === 'statistics'" class="text-sm text-gray-500 dark:text-gray-400" data-statistics-hint>{{ t('admin.tests.statisticsHint') }}</p><p v-else-if="editingType.output_kind === 'model_check'" class="text-sm text-gray-500 dark:text-gray-400" data-model-check-hint>{{ t('admin.tests.modelCheckHint') }}</p><CapabilityBankEditor v-else-if="editingType.output_kind === 'capability'" :key="editingType.id || 0" v-model="editingType.prompt" /><label v-else class="input-label">{{ t('admin.tests.prompt') }}<textarea v-model="editingType.prompt" rows="6" class="input mt-1 w-full" /></label><label v-if="editingType.output_kind !== 'statistics'" class="input-label">{{ t('admin.tests.maxOutputTokens') }}<input v-model.number="editingType.max_output_tokens" type="number" min="0" max="131072" step="1" class="input mt-1 w-full" data-type-max-output-tokens /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.maxOutputTokensHint') }}</span></label><label class="flex items-center gap-2 text-sm"><input v-model="editingType.enabled" type="checkbox" /> {{ t('common.enabled') }}</label></div><template #footer><button class="btn btn-secondary" @click="editingType = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || !canSaveType" @click="saveType">{{ t('common.save') }}</button></template></BaseDialog>

    <BaseDialog :show="!!editingPlan" :title="editingPlan?.id ? t('common.edit') : t('common.create')" width="wide" @close="editingPlan = null">
      <div v-if="editingPlan" class="grid gap-4 sm:grid-cols-2">
        <p class="text-sm text-gray-500 sm:col-span-2">{{ t('admin.tests.experience.activeHint') }}</p>
        <label class="input-label">{{ t('admin.tests.name') }}<input v-model.trim="editingPlan.name" class="input mt-1 w-full" /></label>
        <p v-if="editingPlan.migration_note" class="text-sm text-amber-700 dark:text-amber-300 sm:col-span-2" data-plan-migration-note>{{ editingPlan.migration_note }}</p>
        <fieldset class="sm:col-span-2">
          <legend class="input-label">{{ t('admin.tests.types') }}</legend>
          <div class="mt-2 flex flex-wrap gap-x-6 gap-y-3">
            <label v-for="type in orderedTypes" :key="type.id" class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200" :class="{ 'opacity-50': !type.enabled }">
              <input v-model="editingPlan.test_definition_ids" :data-testid="`plan-type-${type.id}`" type="checkbox" :value="type.id" :disabled="!editingPlan.test_definition_ids.includes(type.id) && (!type.enabled || editingPlan.test_definition_ids.length >= 32)" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" />
              {{ type.name }}<span v-if="!type.enabled" class="text-xs text-gray-500">{{ t('common.disabled') }}</span>
            </label>
          </div>
          <p v-if="!orderedTypes.length" class="mt-2 text-sm text-gray-500">{{ t('common.noData') }}</p>
        </fieldset>
        <fieldset class="space-y-3 sm:col-span-2" data-plan-groups>
          <legend class="input-label">{{ t('admin.tests.strategyGroups') }}</legend>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.strategyGroupsHint') }}</p>
          <div class="flex flex-wrap gap-3">
            <label v-for="group in groups" :key="group.id" class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"><input type="checkbox" :checked="editingPlan.group_ids.includes(group.id)" :disabled="!editingPlan.group_ids.includes(group.id) && (!selectableGroup(group) || editingPlan.group_ids.length >= 100)" :data-plan-group="group.id" @change="selectGroup(group.id, ($event.target as HTMLInputElement).checked)" />{{ group.name }} (#{{ group.id }})</label>
          </div>
          <ol class="space-y-2" data-selected-plan-groups>
            <li v-for="(id, index) in editingPlan.group_ids" :key="id" class="flex items-center justify-between gap-2 rounded-md border border-gray-200 p-2 text-sm dark:border-dark-700" :data-selected-plan-group="id"><span>{{ index + 1 }}. {{ groups.find(group => group.id === id)?.name || `#${id}` }}</span><div class="flex gap-2"><button type="button" class="btn btn-secondary btn-sm" :disabled="index === 0" data-group-up :aria-label="t('admin.tests.moveGroupUp')" @click="moveGroup(index, -1)">↑</button><button type="button" class="btn btn-secondary btn-sm" :disabled="index === editingPlan.group_ids.length - 1" data-group-down :aria-label="t('admin.tests.moveGroupDown')" @click="moveGroup(index, 1)">↓</button><button type="button" class="btn btn-secondary btn-sm" :aria-label="t('common.remove')" @click="selectGroup(id, false)">×</button></div></li>
          </ol>
          <p v-if="editingPlan.group_ids.length && !validStrategyGroups(editingPlan.group_ids)" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ t('admin.tests.invalidStrategyGroups') }}</p>
        </fieldset>
        <div class="space-y-2 sm:col-span-2" data-plan-exclusions>
          <div class="flex items-center justify-between gap-2">
            <label for="plan-excluded-account-search" class="input-label">{{ t('admin.tests.excludedAccounts') }}</label>
            <span class="text-xs text-gray-500">{{ t('admin.tests.excludedAccountsCount', { count: editingPlan.excluded_account_ids.length }) }}</span>
          </div>
          <p class="text-xs text-gray-500">{{ t('admin.tests.excludedAccountsHint') }}</p>
          <input id="plan-excluded-account-search" v-model="excludedAccountSearch" type="search" class="input w-full" :placeholder="t('admin.tests.searchExcludedAccounts')" />
          <div v-if="excludedAccountChoices.length" class="max-h-48 space-y-2 overflow-y-auto rounded-md border border-gray-200 p-3 dark:border-dark-700">
            <label v-for="account in excludedAccountChoices" :key="account.id" class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input v-model="editingPlan.excluded_account_ids" type="checkbox" :value="account.id" :data-excluded-account="account.id" />
              <span>{{ account.name }} (#{{ account.id }})<span v-if="!account.inScope" class="ml-2 text-xs text-gray-500">{{ t('admin.tests.excludedAccountOutsideGroups') }}</span></span>
            </label>
          </div>
          <p v-else class="text-xs text-gray-500">{{ t(editingPlan.group_ids.length ? 'admin.tests.noExclusionAccounts' : 'admin.tests.selectGroupFirst') }}</p>
        </div>
        <fieldset class="input-label sm:col-span-2" data-plan-models>
          <legend>{{ t('admin.tests.model') }}</legend>
          <p class="mt-1 text-xs font-normal text-gray-500">{{ t('admin.tests.multiModelHint') }}</p>
          <p class="mt-1 text-xs font-normal text-gray-500">{{ t('admin.tests.multiModelProtectionHint') }}</p>
          <input v-if="modelOptions.length > 8" v-model="modelSearch" type="search" class="input mt-2 w-full text-sm font-normal" :placeholder="t('common.search')" :aria-label="t('admin.tests.model')" data-model-search />
          <div v-if="modelOptions.length" class="mt-2 grid max-h-48 gap-2 overflow-y-auto rounded-md border border-gray-200 p-3 sm:grid-cols-2 dark:border-dark-700" :aria-busy="modelOptionsLoading">
            <label v-for="option in visibleModelOptions" :key="String(option.value)" class="flex min-w-0 items-center gap-2 text-sm font-normal text-gray-700 dark:text-gray-200">
              <input v-model="editingPlan.model_ids" type="checkbox" :value="String(option.value)" :disabled="modelOptionsLoading || (!editingPlan.model_ids.includes(String(option.value)) && editingPlan.model_ids.length >= 16)" :data-plan-model="String(option.value)" />
              <span class="min-w-0 break-all">{{ option.label }}</span>
            </label>
            <p v-if="!visibleModelOptions.length" class="text-xs font-normal text-gray-500">{{ t('common.noData') }}</p>
          </div>
          <p v-else-if="modelOptionsLoading" class="mt-2 text-xs font-normal text-gray-500">{{ t('common.loading') }}</p>
          <p v-else class="mt-2 text-xs font-normal text-amber-700 dark:text-amber-300" data-no-common-model>{{ editingPlan.group_ids.length ? t('admin.tests.noCommonModels') : t('admin.tests.selectGroupFirst') }}</p>
          <p v-if="editingPlan.model_ids.length > 1" class="mt-1 text-xs font-normal text-primary-600 dark:text-primary-400">{{ t('admin.tests.multiModelRunHint', { count: editingPlan.model_ids.length }) }}</p>
        </fieldset>
        <TestScheduleEditor v-model:schedule="editingPlan.schedule" v-model:cron-expression="editingPlan.cron_expression" class="sm:col-span-2" />
        <TestProtectionEditor v-model="editingPlan.protection" :types="selectedProtectionTypes" :groups="protectionGroups" :schedule="editingPlan.schedule" class="sm:col-span-2" />
        <details class="rounded-lg border border-gray-200 p-4 dark:border-dark-700 sm:col-span-2" data-plan-advanced>
          <summary class="cursor-pointer text-sm font-medium">{{ t('admin.tests.experience.activeAdvanced') }}</summary>
          <div class="mt-4 grid gap-4 sm:grid-cols-2">
        <label class="input-label">{{ t('admin.tests.sortOrder') }}<input v-model.number="editingPlan.sort_order" min="0" type="number" class="input mt-1 w-full" /><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.planSortOrderHint') }}</span></label>
        <label class="input-label">{{ t('admin.tests.maxResults') }}<input v-model.number="editingPlan.max_results" min="1" type="number" class="input mt-1 w-full" /></label>
        <label v-if="reasoningEffortOptions.length" class="input-label">{{ t('admin.tests.reasoningEffort') }}<select v-model="editingPlan.reasoning_effort" class="input mt-1 w-full"><option :value="null">{{ t('admin.tests.reasoningEffortDefault') }}</option><option v-for="effort in reasoningEffortOptions" :key="effort" :value="effort">{{ effort }}</option></select><span class="mt-1 block text-xs font-normal text-gray-500">{{ t('admin.tests.reasoningEffortHint') }}</span></label>
          </div>
        </details>
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
        <AdminTestResultHistory :key="`${resultPlan.id}-${route?.query.account_id || 0}`" :initial-account-id="Number(route?.query.account_id) || undefined" :results="results" :accounts="accounts" :types="types" :now="resultsNow" :history="resultsHistory" :loading="resultsLoading" :retrying-result-id="retryingResultId" :deleting-result-id="deletingResultId" @retry="retryResult" @delete="removeResult" />
      </div>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import CapabilityBankEditor from '@/components/tests/CapabilityBankEditor.vue'
import { parseCapabilityConfig, validCapabilityConfig } from '@/utils/qualityCapability'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { reasoningEffortsForTestModel } from '@/utils/testReasoningEfforts'
import AdminTestResultHistory from '@/components/tests/AdminTestResultHistory.vue'
import TestProtectionEditor from '@/components/tests/TestProtectionEditor.vue'
import TestScheduleEditor from '@/components/tests/TestScheduleEditor.vue'
import { copyTestSchedule, defaultTestSchedule, validTestSchedule } from '@/utils/testSchedule'
import { defaultProtectionRule, normalizeAllPassProtection, copyTestProtection, validTestProtection } from '@/utils/testProtection'
import type { AccountListItem, AdminGroup, CreateTestPlanRequest, CreateTestTypeRequest, TestPlan, TestResult, TestType, TestProtectionConfig } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { SelectOption } from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const app = useAppStore()
const resultsLoading = ref(false)
const runningPlans = ref(new Set<number>())
const togglingPlanId = ref<number | null>(null)
const reportError = (error: unknown) => app.showError(extractApiErrorMessage(error, t('tests.loadFailed')))
const route = useRoute()
const props = withDefaults(defineProps<{ tab?: string }>(), { tab: 'plans' })
const emit = defineEmits<{ catalog: [groups: AdminGroup[], accounts: AccountListItem[]] }>()
const activeTab = computed(() => props.tab)
const loading = ref(false)
const saving = ref(false)
const types = ref<TestType[]>([])
const plans = ref<TestPlan[]>([])
const groups = ref<AdminGroup[]>([])
const accounts = ref<AccountListItem[]>([])
const excludedAccountSearch = ref('')
const modelOptions = ref<SelectOption[]>([])
const modelOptionsLoading = ref(false)
const modelSearch = ref('')
const visibleModelOptions = computed(() => modelOptions.value.filter(option => option.label.toLowerCase().includes(modelSearch.value.trim().toLowerCase())))
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
const editingPlan = ref<(CreateTestPlanRequest & { id?: number; model_ids: string[]; excluded_account_ids: number[]; protection: TestProtectionConfig; migration_note?: string }) | null>(null)
const resultPlan = ref<TestPlan | null>(null)
let modelOptionsRequest = 0

const canSaveType = computed(() => {
  const type = editingType.value
  if (!type?.name || !type.key || !type.output_kind) return false
  if (type.output_kind === 'statistics') return true
  if (type.output_kind === 'capability' && !validCapabilityConfig(parseCapabilityConfig(type.prompt))) return false
  return (type.output_kind === 'model_check' || Boolean(type.prompt.trim())) &&
    Number.isInteger(type.max_output_tokens) && type.max_output_tokens! >= 0 && type.max_output_tokens! <= 131072
})
const typeKindLabel = (kind: string) => kind === 'capability' ? t('admin.tests.capability.title') : kind === 'statistics' ? t('admin.tests.statistics') : kind === 'model_check' ? t('admin.tests.modelCheck') : kind
const orderedTypes = computed(() => [...types.value].sort((a, b) => {
  const orderA = a.sort_order == null ? Number.MAX_SAFE_INTEGER : a.sort_order
  const orderB = b.sort_order == null ? Number.MAX_SAFE_INTEGER : b.sort_order
  return orderA - orderB || a.id - b.id
}))
const orderedPlans = computed(() => [...plans.value].sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0) || a.id - b.id))
const modelIDs = (plan: Pick<TestPlan, 'model_id' | 'model_ids'>): string[] => {
  const values = plan.model_ids ?? (plan.model_id ? [plan.model_id] : [])
  return [...new Set(values.map(model => model.trim()).filter(Boolean))]
}
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
const selectedProtectionTypes = computed(() => orderedTypes.value.filter(type => editingPlan.value?.test_definition_ids.includes(type.id)))
const validStrategyTypes = (ids: number[]) => ids.length >= 1 && ids.length <= 32
  && new Set(ids).size === ids.length
  && ids.every(id => types.value.some(type => type.id === id && type.enabled))
const strategyGroups = (ids: number[]) => ids.map(id => groups.value.find(group => group.id === id)).filter((group): group is AdminGroup => Boolean(group))
const validStrategyGroups = (ids: number[]) => {
  const selected = strategyGroups(ids)
  return ids.length >= 1 && ids.length <= 100 && new Set(ids).size === ids.length && selected.length === ids.length
    && selected.every(group => group.status === 'active' && group.platform !== 'composite' && group.platform === selected[0].platform)
}
const protectionGroups = computed(() => strategyGroups(editingPlan.value?.group_ids || []).filter(group => group.status === 'active' && group.platform !== 'composite'))
const excludedAccountChoices = computed(() => {
  const plan = editingPlan.value
  if (!plan) return []
  const selected = new Set(plan.excluded_account_ids)
  const choices = new Map(accounts.value.map(account => [account.id, {
    id: account.id,
    name: account.name,
    inScope: account.group_ids?.some(id => plan.group_ids.includes(id)) ?? false,
  }]))
  // Preserve exclusions when an account moves groups or is temporarily missing.
  // Only an explicit uncheck removes a saved exclusion.
  for (const id of selected) {
    if (!choices.has(id)) choices.set(id, { id, name: t('admin.tests.unavailableExcludedAccount'), inScope: false })
  }
  const search = excludedAccountSearch.value.trim().toLowerCase()
  return [...choices.values()].filter(account => (account.inScope || selected.has(account.id))
    && (!search || (account.name + ' #' + account.id).toLowerCase().includes(search)))
})
const selectableGroup = (group: AdminGroup) => {
  const selected = strategyGroups(editingPlan.value?.group_ids || [])
  return group.status === 'active' && group.platform !== 'composite' && (!selected.length || group.platform === selected[0].platform)
}
const selectGroup = (id: number, selected: boolean) => {
  const plan = editingPlan.value
  if (!plan) return
  plan.group_ids = selected ? [...new Set([...plan.group_ids, id])] : plan.group_ids.filter(value => value !== id)
}
const moveGroup = (index: number, direction: number) => {
  const plan = editingPlan.value
  if (!plan || index + direction < 0 || index + direction >= plan.group_ids.length) return
  const ids = [...plan.group_ids]
  const selected = ids[index]
  ids[index] = ids[index + direction]
  ids[index + direction] = selected
  plan.group_ids = ids
}
const reasoningEffortOptions = computed(() => {
  const plan = editingPlan.value
  const models = plan ? modelIDs(plan) : []
  if (!plan || !models.length || !plan.group_ids.length) return []
  const eligibleAccounts = accounts.value.filter(account => account.schedulable !== false
    && (!account.status || ['active', 'quality_paused'].includes(account.status)) && !plan.excluded_account_ids.includes(account.id))
  const choices = models.flatMap(model => plan.group_ids.map(id => reasoningEffortsForTestModel(model, eligibleAccounts, id)))
  return choices.slice(1).reduce((common, options) => common.filter(effort => options.includes(effort)), choices[0])
})
const canSavePlan = computed(() => {
  const plan = editingPlan.value
  const selectedModels = plan ? modelIDs(plan) : []
  const allowedModels = new Set(modelOptions.value.map(option => String(option.value)))
  return Boolean(plan && validStrategyTypes(plan.test_definition_ids)
    && selectedModels.length > 0 && selectedModels.length <= 16 && selectedModels.every(model => allowedModels.has(model))
    && validTestSchedule(plan.schedule, plan.cron_expression) && validStrategyGroups(plan.group_ids)
    && !modelOptionsLoading.value
    && validTestProtection(plan.protection, selectedProtectionTypes.value, protectionGroups.value))
})
const loadModelOptions = async () => {
  const request = ++modelOptionsRequest
  const plan = editingPlan.value
  const selected = strategyGroups(plan?.group_ids || [])
  modelOptions.value = []
  if (!plan || !selected.length || !validStrategyGroups(plan.group_ids)) {
    modelOptionsLoading.value = false
    return
  }
  modelOptionsLoading.value = true
  try {
    const catalogs = await Promise.all(selected.map(group => adminAPI.groups.getModelAllowlistCandidates(group.id, group.platform)))
    if (request !== modelOptionsRequest || editingPlan.value !== plan) return
    const lists = catalogs.map(models => [...new Set(models.map(model => String(model).trim()).filter(Boolean))])
    const common = lists.slice(1).reduce((values, choices) => values.filter(value => choices.includes(value)), lists[0])
    modelOptions.value = common.map(value => ({ value, label: value }))
    plan.model_ids = modelIDs(plan).filter(model => common.includes(model))
    plan.model_id = plan.model_ids[0] || ''
  } catch (error) {
    if (request === modelOptionsRequest) reportError(error)
  } finally {
    if (request === modelOptionsRequest) modelOptionsLoading.value = false
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
    emit('catalog', groups.value, accounts.value)
  } catch (error) {
    reportError(error)
  } finally {
    loading.value = false
  }
}

// Include every status so exclusions can be configured before an account is
// re-enabled. Only schedulable accounts constrain strategy reasoning options.
const loadTestAccounts = async (): Promise<AccountListItem[]> => {
  const pageSize = 1000
  const first = await adminAPI.accounts.list(1, pageSize, { lite: '1' })
  const items = [...(first.items || [])]
  const pages = Math.max(first.pages || 1, Math.ceil((first.total || items.length) / pageSize))
  const rest = await Promise.all(Array.from({ length: pages - 1 }, (_, index) => adminAPI.accounts.list(index + 2, pageSize, { lite: '1' })))
  for (const page of rest) items.push(...(page.items || []))
  return [...new Map(items.map(account => [account.id, account])).values()]
}

const openType = (type?: TestType) => { editingType.value = type ? { id: type.id, name: type.name, key: type.key, output_kind: type.output_kind, prompt: type.prompt, max_output_tokens: type.max_output_tokens ?? 0, description: type.description || '', enabled: type.enabled, sort_order: type.sort_order ?? 0 } : { name: '', key: '', output_kind: 'html', prompt: '', max_output_tokens: 0, description: '', enabled: true, sort_order: orderedTypes.value.length ? Math.max(...orderedTypes.value.map(item => item.sort_order ?? 0)) + 1 : 0 } }
const saveType = async () => { if (!editingType.value || !canSaveType.value) return; saving.value = true; try { const { id, ...body } = editingType.value; if (['statistics', 'model_check'].includes(body.output_kind)) body.prompt = ''; if (body.output_kind === 'statistics') body.max_output_tokens = 0; if (id) await adminAPI.tests.updateType(id, body); else await adminAPI.tests.createType(body); editingType.value = null; await load() } catch (error) { reportError(error) } finally { saving.value = false } }
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
      max_output_tokens: type.max_output_tokens ?? 0,
      prompt: ['statistics', 'model_check'].includes(type.output_kind) ? '' : type.prompt,
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

const planPayload = (plan: TestPlan): CreateTestPlanRequest & { model_ids: string[] } => ({
  name: plan.name || '', sort_order: plan.sort_order ?? 0,
  group_ids: [...(plan.group_ids || [])], test_definition_ids: planTypeIDs(plan),
  excluded_account_ids: [...(plan.excluded_account_ids || [])],
  model_id: modelIDs(plan)[0] || '', model_ids: modelIDs(plan), reasoning_effort: plan.reasoning_effort ?? null,
  cron_expression: plan.cron_expression || '0 * * * *', enabled: plan.enabled,
  schedule: copyTestSchedule(plan.schedule),
  max_results: plan.max_results || 50, protection: copyTestProtection(plan.protection),
})
const openPlan = (plan?: TestPlan) => {
  excludedAccountSearch.value = ''
  modelSearch.value = ''
  editingPlan.value = plan
    ? { ...planPayload(plan), excluded_account_ids: [...(plan.excluded_account_ids || [])], id: plan.id, migration_note: plan.migration_note, protection: copyTestProtection(plan.protection) }
    : { name: '', sort_order: plans.value.length ? Math.max(...plans.value.map(item => item.sort_order ?? 0)) + 1 : 0, test_definition_ids: [], group_ids: [], excluded_account_ids: [], model_id: '', model_ids: [], reasoning_effort: null, cron_expression: '0 * * * *', schedule: defaultTestSchedule(), enabled: true, max_results: 50, protection: { enabled: true, mode: 'all_pass', rules: [] } }
}
const savePlan = async () => {
  if (!editingPlan.value || !canSavePlan.value) return
  saving.value = true
  try {
    const { id, migration_note: _migrationNote, ...body } = editingPlan.value
    if (id) await adminAPI.tests.updatePlan(id, body)
    else await adminAPI.tests.createPlan(body)
    editingPlan.value = null
    await load()
  } catch (error) { reportError(error) } finally { saving.value = false }
}
const copyPlan = async (plan: TestPlan) => {
  if (saving.value) return
  const payload = planPayload(plan)
  payload.name = `${plan.name || `#${plan.id}`}（Copy）`
  // Copies stay disabled until the administrator checks their shared scope.
  payload.enabled = false
  if (!validStrategyTypes(payload.test_definition_ids) || !validStrategyGroups(payload.group_ids) || !validTestSchedule(payload.schedule, payload.cron_expression) || !validTestProtection(payload.protection, types.value.filter(type => payload.test_definition_ids.includes(type.id)), strategyGroups(payload.group_ids))) {
    openPlan(plan)
    editingPlan.value!.id = undefined
    editingPlan.value!.name = payload.name
    editingPlan.value!.enabled = false
    return
  }
  saving.value = true
  try {
    await adminAPI.tests.createPlan(payload)
    await load()
  } catch (error) { reportError(error) } finally { saving.value = false }
}
const removePlan = async (plan: TestPlan) => { if (!window.confirm(`${t('common.delete')} ${plan.name || `#${plan.id}`}?`)) return; try { await adminAPI.tests.deletePlan(plan.id); await load() } catch (error) { reportError(error) } }
const togglePlanStatus = async (plan: TestPlan) => {
  if (plan.migration_note) {
    openPlan(plan)
    return
  }
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
  if (runningPlans.value.has(plan.id) || plan.migration_note) return
  runningPlans.value.add(plan.id)
  const startedAt = new Date().toISOString()
  const models = modelIDs(plan)
  pendingRuns.value.set(plan.id, planTypeIDs(plan).flatMap((typeID, typeIndex) => models.map((model, modelIndex) => ({
    id: -((typeIndex * models.length) + modelIndex + 1),
    plan_id: plan.id,
    plan_name: plan.name,
    test_definition_id: typeID,
    test_name: planTypes(plan).find(type => type.id === typeID)?.name,
    group_id: null,
    account_id: null,
    target_mode: 'all_accounts',
    model_id: model,
    reasoning_effort: plan.reasoning_effort ?? null,
    status: 'running',
    output_kind: types.value.find(type => type.id === typeID)?.output_kind || 'text',
    response_text: null,
    error_message: null,
    latency_ms: null,
    started_at: startedAt,
    created_at: startedAt,
  }))))
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
        return result.test_definition_id === pending.test_definition_id && result.model_id === pending.model_id
          && started >= new Date(pending.started_at || 0).getTime()
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
  () => editingPlan.value?.test_definition_ids.slice(),
  (ids, previous) => {
    const plan = editingPlan.value
    if (plan && !plan.id && plan.protection.enabled && plan.protection.mode === 'all_pass') {
      for (const id of ids || []) {
        const type = types.value.find(type => type.id === id && type.enabled)
        if (type && !previous?.includes(id) && !plan.protection.rules.some(rule => rule.test_definition_id === id)) plan.protection.rules.push(defaultProtectionRule(type))
      }
      normalizeAllPassProtection(plan.protection)
    }
    // Keep combined conditions intact so removed checks remain visible and must be repaired.
    if (plan) plan.protection.rules = plan.protection.rules.filter(rule => plan.test_definition_ids.includes(rule.test_definition_id))
  },
)
watch(reasoningEffortOptions, choices => {
  const plan = editingPlan.value
  if (plan?.reasoning_effort && !choices.includes(plan.reasoning_effort)) plan.reasoning_effort = null
})
watch(() => editingPlan.value?.model_ids?.slice(), ids => {
  const plan = editingPlan.value
  if (plan) plan.model_id = ids?.[0] || ''
})
watch(
  () => editingPlan.value ? JSON.stringify([...editingPlan.value.group_ids].sort((a, b) => a - b)) : undefined,
  () => { void loadModelOptions() },
)
watch(resultPlan, plan => {
  clearInterval(resultTimer)
  if (plan) resultTimer = setInterval(() => {
    resultsNow.value = Date.now()
    const hasRunning = results.value.some(result => result.status === 'running' || result.status === 'pending')
    if (props.tab === 'plans' && !document.hidden && (!resultsHistory.value || hasRunning)) void refreshResults()
  }, 5000)
})
onUnmounted(() => {
  clearInterval(resultTimer)
  resultsRequest++
  modelOptionsRequest++
})
const groupName = (plan: TestPlan) => (plan.group_ids || []).map(id => groups.value.find(group => group.id === id)?.name || `#${id}`).join(' / ') || '-'
const targetName = () => t('admin.tests.strategyTarget')
const formatDate = (value?: string) => value ? new Date(value).toLocaleString() : '-'
watch(() => props.tab, tab => {
  if (tab !== 'plans') { editingPlan.value = null; closeResults() }
  if (tab !== 'types') editingType.value = null
})
watch(() => [route?.query.plan_id, plans.value, props.tab], () => {
  const plan = plans.value.find(p => p.id === Number(route?.query.plan_id))
  if (props.tab === 'plans' && plan && resultPlan.value?.id !== plan.id) void showResults(plan)
})
onMounted(load)
defineExpose({ refresh: load })
</script>
