<template>
  <form id="intelligence-policy-form" class="space-y-4" @submit.prevent="save">
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <div class="grid items-start gap-5 md:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
      <div class="min-w-0 space-y-4">
        <label class="input-label block">
          {{ tr('policyName') }}
          <input
            v-model="policyName"
            class="input mt-1 w-full"
            type="text"
            data-policy-name
            maxlength="200"
            :placeholder="defaultName || tr('defaultPolicyName')"
            :disabled="busy"
          />
        </label>
        <fieldset>
          <legend class="input-label">{{ tr('group') }}</legend>
          <p class="input-hint mb-2">{{ tr('groupHint') }}</p>
          <div class="flex flex-wrap gap-2">
            <label v-for="g in choices" :key="g.id" class="chip"
              ><input v-model="groupIds" type="checkbox" :value="g.id" :disabled="busy" /> {{ g.name }}</label
            >
          </div>
          <label v-for="id in unavailableGroups" :key="id" class="chip text-amber-700 dark:text-amber-300">
            <input v-model="groupIds" type="checkbox" :value="id" :disabled="busy" />{{ groupName(id) }} ·
            {{ tr('groupUnavailable') }}
          </label>
          <p v-if="!choices.length" class="text-sm text-gray-500 dark:text-dark-400">
            {{ catalogReady ? tr('noActiveGroups') : t('common.loading') }}
          </p>
        </fieldset>
        <fieldset>
          <legend class="input-label">{{ tr('modelScope') }}</legend>
          <p class="input-hint mb-2">{{ tr('automaticScope') }}</p>
          <p v-if="!groupIds.length || catalogsLoading || !availableModels.length" class="input-hint">
            {{ !groupIds.length ? tr('chooseGroups') : catalogsLoading ? t('common.loading') : tr('noSharedModel') }}
          </p>
          <div class="flex flex-wrap gap-2">
            <label v-for="m in availableModels" :key="m" class="chip"
              ><input v-model="modelIds" type="checkbox" :value="m" :disabled="busy" /> {{ m }}</label
            >
          </div>
          <button v-if="catalogError" class="text-sm underline" type="button" @click="catalogRefresh++">
            {{ t('common.retry') }}
          </button>
        </fieldset>
        <div v-if="plan?.excluded_account_ids?.length" class="space-y-1 text-sm">
          <p class="input-hint">{{ tr('legacyExclusions', { count: plan.excluded_account_ids.length }) }}</p>
          <label class="flex items-center gap-2">
            <input v-model="clearLegacyExclusions" type="checkbox" data-clear-legacy-exclusions :disabled="busy" />
            {{ tr('includeLegacyAccounts') }}
          </label>
        </div>
        <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
          <div class="flex items-center justify-between gap-3">
            <label for="intelligence-auto-isolate" class="input-label mb-0">{{ tr('autoIsolate') }}</label>
            <Toggle id="intelligence-auto-isolate" v-model="settings.auto_isolate" :disabled="busy" />
          </div>
          <p class="input-hint">{{ tr(isolationDescription) }}</p>
        </div>
        <details class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
          <summary class="cursor-pointer text-sm font-medium">{{ tr('advanced') }}</summary>
          <p class="input-hint mt-2">{{ tr('rules') }}</p>
          <label v-if="settings.auto_isolate && modelIds.length > 1" class="mt-3 flex items-center gap-2 text-sm">
            <input v-model="settings.pause_on_any" type="checkbox" data-pause-on-any :disabled="busy" />{{ tr('pauseOnAny') }}
          </label>
          <div class="mt-2 grid gap-2 sm:grid-cols-2">
            <label class="input-label"
              >{{ t('admin.tests.reasoningEffort') }}
              <select v-model="effort" class="input w-full" data-intelligence-effort :disabled="busy">
                <option v-for="level in effortOptions" :key="level" :value="level">{{ effortLabel(level) }}</option>
              </select>
            </label>
            <label class="input-label"
              >{{ tr('interval')
              }}<input v-model.number="settings.interval_minutes" type="number" min="1" max="1440" class="input w-full" :disabled="busy"
            /></label>
            <label class="input-label"
              >{{ tr('cooldown')
              }}<input v-model.number="settings.cooldown_minutes" type="number" min="1" max="1440" class="input w-full" :disabled="busy"
            /></label>
            <label class="input-label"
              >{{ tr('passes')
              }}<input v-model.number="settings.recovery_passes" type="number" min="1" max="10" class="input w-full" :disabled="busy"
            /></label>
          </div>
        </details>
      </div>
      <aside class="min-w-0 space-y-2 rounded-xl bg-gray-50 p-4 text-sm text-gray-700 dark:bg-dark-900/50 dark:text-dark-200">
        <h3 class="font-semibold">{{ tr('scope') }}</h3>
        <p v-if="coverage">
          {{ tr('scopeSize', { accounts: coverage.account_ids.length, models: modelIds.length, total: coverage.targets }) }}
        </p>
        <p v-else-if="scopePending">{{ t('common.loading') }}</p>
        <p v-else-if="scopeError">
          {{ tr('scopeError') }}
          <button class="underline" type="button" @click="catalogRefresh++">{{ t('common.retry') }}</button>
        </p>
        <p v-else>{{ tr('chooseScope') }}</p>
        <dl v-if="coverage">
          <div v-for="g in selectedGroups" :key="g.id" class="flex justify-between gap-2 border-b py-1 dark:border-dark-700">
            <dt>{{ g.name }}</dt>
            <dd>{{ tr('nAccounts', { count: coverage.group_counts[String(g.id)] || 0 }) }}</dd>
          </div>
        </dl>
        <p v-if="shared.length" class="input-hint">
          {{ tr('shared', { count: shared.length }) }} {{ tr('groupStats') }}
        </p>
        <details v-if="eligible.length">
          <summary class="cursor-pointer">{{ tr('memberships') }}</summary>
          <ul class="max-h-48 overflow-y-auto text-xs">
            <li v-for="a in eligible" :key="a.id" class="flex flex-wrap justify-between gap-2 py-1">
              <span>{{ a.name }}</span
              ><span>{{ (a.group_ids || []).map(groupName).join(' / ') }}</span>
            </li>
          </ul>
        </details>
        <p v-if="settings.auto_isolate && otherGroups.length" class="text-xs text-amber-700 dark:text-amber-300">
          {{ tr('crossGroup', { groups: otherGroups.join(' / ') }) }}
        </p>
        <p v-if="overlap.length" class="text-xs text-amber-700 dark:text-amber-300">
          {{ tr('overlap', { plans: overlap.map((p) => p.name || '#' + p.id).join(' / ') }) }}
        </p>
        <p v-if="coverage" class="border-t pt-2 text-xs dark:border-dark-700">{{ tr('estimate', { count: dailyEstimate }) }}</p>
        <p v-if="budget" class="text-xs">
          {{
            tr('budget', {
              site: limit(budget.daily_requests),
              per: limit(budget.account_daily_requests),
              tokens: limit(budget.daily_tokens),
              perTokens: limit(budget.account_daily_tokens),
            })
          }}
        </p>
        <p v-else class="input-hint">{{ tr('budgetUnavailable') }}</p>
      </aside>
    </div>
  </form>
</template>
<script setup lang="ts">
import { computed, ref, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { intelligenceScope, type IntelligenceScope } from '@/api/admin/intelligence'
import { qualityOperationsAPI } from '@/api/admin/qualityOperations'
import Toggle from '@/components/common/Toggle.vue'
import type { AdminGroup, AccountListItem, IntelligencePolicy, TestPlan, CreateTestPlanRequest } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  groups: AdminGroup[]
  accounts: AccountListItem[]
  plans: TestPlan[]
  catalogReady: boolean
  plan?: TestPlan
}>()
const emit = defineEmits<{ saved: [plan: TestPlan] }>()
const { t, locale } = useI18n()
const tr = (s: string, params: Record<string, unknown> = {}) => t('channelMonitorV2.intelligence.' + s, params)
const settings = ref<IntelligencePolicy>({
  auto_isolate: false,
  pause_on_any: false,
  interval_minutes: 60,
  cooldown_minutes: 30,
  recovery_passes: 2,
  ...props.plan?.protection?.intelligence,
})
const groupIds = ref([...(props.plan?.group_ids || [])])
const modelIds = ref(props.plan?.model_ids?.length ? [...props.plan.model_ids] : [props.plan?.model_id || ''].filter(Boolean))
const policyName = ref(props.plan?.name || '')
const clearLegacyExclusions = ref(false)
const excludedIds = computed(() => clearLegacyExclusions.value ? [] : props.plan?.excluded_account_ids || [])
const isolationDescription = computed(() => {
  if (!settings.value.auto_isolate) return 'monitorOnly'
  if (modelIds.value.length === 1) return 'singleIsolation'
  return settings.value.pause_on_any ? 'pauseOnAnyHint' : 'modelIsolation'
})
const choices = computed(() => props.groups.filter((g) => g.status === 'active' && g.platform === 'openai'))
const unavailableGroups = computed(() => groupIds.value.filter((id) => !choices.value.some((g) => g.id === id)))
const selectedGroups = computed(() => choices.value.filter((g) => groupIds.value.includes(g.id)))
const groupName = (id: number) => props.groups.find((g) => g.id === id)?.name || '#' + id
const defaultName = computed(() => [...selectedGroups.value.map((g) => g.name).join(' + ')].slice(0, 200).join(''))
const availableModels = ref<string[]>([]),
  catalogsLoading = ref(false),
  catalogError = ref(false),
  catalogRefresh = ref(0)
const error = ref(''),
  busy = ref(false)
const effort = ref(props.plan?.protection?.intelligence?.model_default_effort ? 'default' : props.plan?.reasoning_effort || 'high')
const effortLabel = (level: string) => (level === 'default' ? tr('defaultEffort') : level)
const effortOptions = computed(() => [
  ...new Set(['default', 'low', 'medium', 'high', ...(props.plan?.reasoning_effort ? [props.plan.reasoning_effort] : [])]),
])
const coverage = ref<IntelligenceScope>(),
  scopePending = ref(false),
  scopeError = ref(false)
watch(
  [groupIds, modelIds, excludedIds, catalogRefresh],
  async ([groups, models, excluded], _, cleanup) => {
    const request = new AbortController()
    cleanup(() => request.abort())
    coverage.value = undefined
    scopeError.value = false
    scopePending.value = !!groups.length && !!models.length
    if (!scopePending.value) return
    try {
      const data = await intelligenceScope(groups, models, excluded, request.signal)
      if (!request.signal.aborted) coverage.value = data
    } catch {
      if (!request.signal.aborted) scopeError.value = true
    } finally {
      if (!request.signal.aborted) scopePending.value = false
    }
  },
  { immediate: true },
)
const eligible = computed(() => {
  const ids = new Set(coverage.value?.account_ids)
  return props.accounts.filter((a) => ids.has(a.id))
})
const shared = computed(() =>
  eligible.value.filter((a) => (a.group_ids || []).filter((id) => groupIds.value.includes(id)).length > 1),
)
const otherGroups = computed(() =>
  [...new Set(eligible.value.flatMap((a) => a.group_ids || []))].filter((id) => !groupIds.value.includes(id)).map(groupName),
)
const overlap = computed(() =>
  props.plans.filter(
    (p) =>
      p.id !== props.plan?.id &&
      p.enabled &&
      p.model_ids?.some((m) => modelIds.value.includes(m)) &&
      eligible.value.some((a) => !p.excluded_account_ids?.includes(a.id) && a.group_ids?.some((id) => p.group_ids.includes(id))),
  ),
)
const targets = computed(() => coverage.value?.targets || 0)
// One candy request also supplies the HTTP 429 signal. A passing round adds one artwork request.
const dailyEstimate = computed(() =>
  settings.value.interval_minutes > 0 ? Math.ceil(1440 / settings.value.interval_minutes) * targets.value * 2 : '—',
)
type Budget = { daily_requests: number; account_daily_requests: number; daily_tokens: number; account_daily_tokens: number }
const budget = ref<Budget>()
const limit = (n: number) => (n > 0 ? n.toLocaleString(locale.value) : tr('unlimited'))
const validInt = (n: number, max: number) => Number.isInteger(n) && n > 0 && n <= max
const canSave = computed(
  () =>
    props.catalogReady &&
    (!props.plan || props.plans.some((p) => p.id === props.plan?.id)) &&
    !catalogsLoading.value &&
    !catalogError.value &&
    selectedGroups.value.length === groupIds.value.length &&
    !scopePending.value &&
    !!coverage.value &&
    validInt(groupIds.value.length, 100) &&
    validInt(modelIds.value.length, 16) &&
    modelIds.value.every((m) => availableModels.value.includes(m)) &&
    validInt(settings.value.interval_minutes, 1440) &&
    validInt(settings.value.cooldown_minutes, 1440) &&
    validInt(settings.value.recovery_passes, 10),
)
function glob(pattern: string, value: string) {
  const parts = pattern.trim().toLowerCase().split('*')
  let rest = value.toLowerCase()
  if (parts.length === 1) return parts[0] === rest
  if (!rest.startsWith(parts[0])) return false
  rest = rest.slice(parts[0].length)
  for (const part of parts.slice(1, -1)) {
    const i = rest.indexOf(part)
    if (i < 0) return false
    rest = rest.slice(i + part.length)
  }
  return rest.endsWith(parts[parts.length - 1])
}
function groupModels(g: AdminGroup, candidates: string[]) {
  const allow = g.model_allowlist
  const names = [...new Set([...candidates, ...(allow?.models || [])].map((m) => m.trim()).filter((m) => m && !m.includes('*')))]
  return names.filter((m) => candidates.some((p) => glob(p, m)) && (!allow?.enabled || allow.models.some((p) => glob(p, m))))
}
watch(
  [selectedGroups, catalogRefresh],
  async ([groups], _, cleanup) => {
    let valid = true
    cleanup(() => {
      valid = false
    })
    availableModels.value = []
    catalogError.value = false
    catalogsLoading.value = groups.length > 0
    if (!groups.length) {
      modelIds.value = []
      return
    }
    try {
      const catalogs = await Promise.all(
        groups.map(async (g) => groupModels(g, await adminAPI.groups.getModelAllowlistCandidates(g.id, g.platform))),
      )
      if (!valid) return
      const common = catalogs[0].filter((m) => catalogs.every((c) => c.includes(m)))
      availableModels.value = common
      modelIds.value = common.length === 1 ? [common[0]] : modelIds.value.filter((m) => common.includes(m))
    } catch (e) {
      if (valid) {
        catalogError.value = true
        error.value = extractApiErrorMessage(e, tr('loadError'))
      }
    } finally {
      if (valid) catalogsLoading.value = false
    }
  },
  { immediate: true },
)
async function save() {
  if (!canSave.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const p = props.plan
    const config = {
      enabled: false,
      rules: [],
      intelligence: { ...settings.value, model_default_effort: effort.value === 'default' },
    }
    const payload: CreateTestPlanRequest = {
      name: policyName.value.trim() || defaultName.value,
      group_ids: [...groupIds.value],
      ...(clearLegacyExclusions.value ? { excluded_account_ids: [] } : {}),
      model_ids: [...modelIds.value],
      model_id: modelIds.value[0],
      reasoning_effort: effort.value,
      test_definition_ids: p?.test_definition_ids || [],
      protection: config,
      ...(p ? {} : { enabled: true }),
    }
    const saved = p ? await adminAPI.tests.updatePlan(p.id, payload) : await adminAPI.tests.createPlan(payload)
    emit('saved', saved)
  } catch (e) {
    error.value = extractApiErrorMessage(e, tr('saveError'))
  } finally {
    busy.value = false
  }
}
async function loadBudget() {
  try {
    const c = (await qualityOperationsAPI.get()).config
    budget.value = c.budget_enabled
      ? c
      : { daily_requests: 10000, account_daily_requests: 200, daily_tokens: 10000000, account_daily_tokens: 1000000 }
  } catch {
    budget.value = undefined
  }
}
function refresh() {
  catalogRefresh.value++
  void loadBudget()
}
onMounted(loadBudget)
defineExpose({ refresh, busy, canSave })
</script>
<style scoped>
.chip {
  @apply inline-flex max-w-full cursor-pointer items-center gap-2 break-all rounded-lg border border-gray-200 px-2 py-1.5 text-sm accent-primary-600 dark:border-dark-700;
}
.chip:has(:checked) {
  @apply border-primary-500 bg-primary-50 text-primary-800 dark:bg-primary-900/30 dark:text-primary-300;
}
</style>
