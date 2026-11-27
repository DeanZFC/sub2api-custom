<template>
  <section class="space-y-4">
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="notice" role="status" class="text-sm text-gray-600 dark:text-dark-300">{{ notice }}</p>

    <IntelligencePolicyEditor
      v-if="view === 'editor'"
      :key="editingPlan?.id ?? 'new'"
      ref="editor"
      :plan="editingPlan"
      :groups="groups"
      :accounts="accounts"
      :plans="policies"
      :catalog-ready="catalogReady"
      @saved="finishEditing"
      @cancel="showList"
    />
    <template v-else-if="view === 'results' && resultPlan">
      <header class="flex flex-wrap items-center gap-3">
        <button type="button" class="btn btn-secondary btn-sm" data-back-to-policies @click="showList">
          <Icon name="arrowLeft" size="sm" />{{ tr('backToPolicies') }}
        </button>
        <h3 class="font-semibold">{{ resultPlan.name || '#' + resultPlan.id }}</h3>
      </header>
      <IntelligencePolicyResults :key="resultPlan.id" :plan="resultPlan" :refresh-key="overviewRefresh" />
    </template>

    <div v-else class="card overflow-hidden">
      <header class="flex flex-wrap items-center justify-between gap-3 px-4 py-4">
        <h3 class="font-semibold">{{ tr('policy') }}</h3>
        <button type="button" class="btn btn-primary btn-sm" data-new-policy :disabled="busy || !catalogReady" @click="edit()">
          <Icon name="plus" size="sm" />{{ tr('newPolicy') }}
        </button>
      </header>
      <p v-if="!policies.length" class="border-t px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-700">
        {{ catalogReady ? tr('emptyPolicies') : t('common.loading') }}
      </p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-left text-sm" :aria-label="tr('policy')">
          <thead class="border-y bg-gray-50 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-dark-300">
            <tr>
              <th scope="col">{{ t('common.name') }}</th>
              <th scope="col">{{ tr('group') }}</th>
              <th scope="col">{{ tr('modelScope') }}</th>
              <th scope="col">{{ t('common.status') }}</th>
              <th scope="col">{{ t('common.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="p in policies" :key="p.id" :data-policy-id="p.id">
              <th scope="row" class="min-w-36 align-top font-medium text-gray-900 dark:text-white">
                {{ p.name || '#' + p.id }}
                <span v-if="p.name" class="ml-1 text-xs font-normal text-gray-500">#{{ p.id }}</span>
                <span v-if="p.protection?.intelligence?.interval_minutes" class="mt-1 block text-xs font-normal text-gray-500">
                  {{ t('admin.tests.timing.everyMinutes', { count: p.protection.intelligence.interval_minutes }) }}
                </span>
              </th>
              <td>{{ p.group_ids?.map(groupName).join(' / ') || '—' }}</td>
              <td>{{ p.model_ids?.join(' / ') || p.model_id || '—' }}</td>
              <td>
                <span :class="p.enabled ? 'badge-success' : 'badge-gray'" class="badge whitespace-nowrap">
                  {{ t(p.enabled ? 'common.enabled' : 'common.disabled') }}
                </span>
              </td>
              <td>
                <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
                  <button type="button" class="row-action" data-edit-policy :disabled="busy" @click="edit(p)">
                    {{ t('common.edit') }}
                  </button>
                  <button type="button" class="row-action" data-run-policy :disabled="busy" @click="run(p)">
                    {{ t('admin.tests.run') }}
                  </button>
                  <button type="button" class="row-action" data-policy-results :disabled="busy" @click="showResults(p.id)">
                    {{ tr('viewPolicyResults') }}
                  </button>
                  <button type="button" class="row-action" data-toggle-policy :disabled="busy" @click="toggle(p)">
                    {{ t(p.enabled ? 'admin.tests.disablePlan' : 'admin.tests.enablePlan') }}
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import Icon from '@/components/icons/Icon.vue'
import type { AccountListItem, AdminGroup, TestPlan } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import IntelligencePolicyEditor from './IntelligencePolicyEditor.vue'
import IntelligencePolicyResults from './IntelligencePolicyResults.vue'

const props = defineProps<{
  groups: AdminGroup[]
  accounts: AccountListItem[]
  plans: TestPlan[]
  catalogReady: boolean
  initialPlanId?: number
}>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const tr = (key: string, params: Record<string, unknown> = {}) => t('channelMonitorV2.intelligence.' + key, params)
const policies = ref<TestPlan[]>([])
watch(() => props.plans, (plans) => {
  policies.value = plans.filter((p) => p.protection?.intelligence)
}, { immediate: true })

const view = ref<'list' | 'editor' | 'results'>('list')
const editingPlan = ref<TestPlan>()
const resultPlanId = ref<number>()
const resultPlan = computed(() => policies.value.find((p) => p.id === resultPlanId.value))
const editor = ref<InstanceType<typeof IntelligencePolicyEditor> | null>(null)
const overviewRefresh = ref(0)
const error = ref('')
const notice = ref('')
const busy = ref(false)
const groupName = (id: number) => props.groups.find((group) => group.id === id)?.name || '#' + id

function showList() {
  view.value = 'list'
  editingPlan.value = undefined
  resultPlanId.value = undefined
  error.value = ''
  notice.value = ''
}
function edit(plan?: TestPlan) {
  showList()
  editingPlan.value = plan
  view.value = 'editor'
}
function showResults(id: number) {
  showList()
  resultPlanId.value = id
  view.value = 'results'
}
watch(
  () => props.catalogReady && policies.value.some((p) => p.id === props.initialPlanId) ? props.initialPlanId : undefined,
  (id) => { if (id) showResults(id) },
  { immediate: true },
)

function updateRow(saved: TestPlan) {
  const index = policies.value.findIndex((p) => p.id === saved.id)
  if (index < 0) policies.value.push(saved)
  else policies.value[index] = saved
  emit('changed')
}
function finishEditing(saved: TestPlan) {
  showList()
  updateRow(saved)
  notice.value = tr('saved')
}
async function toggle(plan: TestPlan) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    updateRow(await adminAPI.tests.updatePlan(plan.id, { enabled: !plan.enabled }))
  } catch (e) {
    error.value = extractApiErrorMessage(e, tr('saveError'))
  } finally {
    busy.value = false
  }
}
async function run(plan: TestPlan) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await adminAPI.tests.runPlan(plan.id)
    notice.value = t('admin.tests.runStarted')
  } catch (e) {
    error.value = extractApiErrorMessage(e, tr('saveError'))
  } finally {
    busy.value = false
  }
}
function refresh() {
  editor.value?.refresh()
  overviewRefresh.value++
}
defineExpose({ refresh })
</script>

<style scoped>
th, td {
  @apply px-4 py-3;
}
td {
  @apply align-top;
}
.row-action {
  @apply whitespace-nowrap text-sm font-medium text-primary-700 hover:underline disabled:opacity-50 dark:text-primary-300;
}
</style>
