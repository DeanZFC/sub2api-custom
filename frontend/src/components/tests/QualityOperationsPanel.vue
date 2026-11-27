<template>
  <div class="space-y-5" data-quality-operations>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    <template v-if="snapshot">
      <div class="grid gap-4 sm:grid-cols-3">
        <div v-for="metric in ['requests', 'tokens', 'unknown_requests'] as const" :key="metric" class="card p-5">
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ tr(metric) }}</p>
          <strong class="mt-2 block text-2xl tabular-nums">{{ (total?.[metric] || 0).toLocaleString() }}</strong>
        </div>
      </div>
      <p class="input-hint">{{ tr('resetHint', { date: date(snapshot.resets_at) }) }}</p>
      <section class="card">
        <header class="card-header flex items-center justify-between gap-3">
          <h3 class="font-semibold">{{ tr('settings') }}</h3>
          <button type="button" class="btn btn-secondary btn-sm" data-edit-operations @click="openSettings">
            <Icon name="edit" size="sm" />{{ t('common.edit') }}
          </button>
        </header>
        <dl class="card-body grid gap-4 sm:grid-cols-3">
          <div v-for="setting in settingsSummary" :key="setting.label">
            <dt class="input-label">{{ setting.label }}</dt>
            <dd class="badge" :class="setting.enabled ? 'badge-success' : 'badge-gray'">
              {{ t(setting.enabled ? 'common.enabled' : 'common.disabled') }}
            </dd>
          </div>
        </dl>
      </section>
      <p v-if="saved" role="status" class="text-sm text-green-600 dark:text-green-400">{{ tr('saved') }}</p>
      <details class="card p-5">
        <summary class="cursor-pointer text-sm font-medium">{{ tr('accountUsage') }}</summary>
        <div class="mt-3 max-h-72 overflow-auto">
          <table class="table">
            <thead><tr><th>{{ tr('account') }}</th><th>{{ tr('requests') }}</th><th>{{ tr('tokens') }}</th><th>{{ tr('unknown_requests') }}</th></tr></thead>
            <tbody>
              <tr v-for="u in snapshot.usage.filter(u => u.account_id)" :key="u.account_id">
                <td>{{ accountName(u.account_id) }}</td><td>{{ u.requests }}</td><td>{{ u.tokens }}</td><td>{{ u.unknown_requests }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
      <BaseDialog
        :show="!!config"
        :title="tr('settings')"
        width="wide"
        :show-close-button="!busy"
        :close-on-escape="!busy"
        @close="config = null"
      >
        <form v-if="config" id="quality-operations-form" class="space-y-5" @submit.prevent="save">
          <fieldset class="min-w-0 space-y-5" :disabled="busy">
            <section class="space-y-3">
              <div class="flex items-center justify-between gap-3">
                <h3 class="font-semibold">{{ tr('budgetTitle') }}</h3>
                <Toggle v-model="config.budget_enabled" :aria-label="tr('enableBudget')" data-budget-toggle />
              </div>
              <p class="input-hint">{{ tr('budgetHint') }}</p>
              <fieldset v-show="config.budget_enabled || !!budgetError" :disabled="!config.budget_enabled && !budgetError" class="space-y-4" data-budget-fields>
                <div class="grid gap-4 sm:grid-cols-2">
                  <label v-for="f in budgetFields.slice(0, 2)" :key="f.key" class="input-label">
                    {{ tr(f.key) }}
                    <input v-model.number="config[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1" />
                  </label>
                </div>
                <details :open="!!budgetError" class="rounded-xl border border-gray-200 p-3 dark:border-dark-700">
                  <summary class="cursor-pointer text-sm">{{ t('admin.tests.experience.budgetAdvanced') }}</summary>
                  <div class="mt-3 grid gap-4 sm:grid-cols-3">
                    <label v-for="f in budgetFields.slice(2)" :key="f.key" class="input-label">
                      {{ tr(f.key) }}
                      <input v-model.number="config[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1" />
                    </label>
                  </div>
                </details>
              </fieldset>
              <p v-if="config.budget_enabled" class="text-xs text-amber-700 dark:text-amber-400">{{ tr('tokenHint') }}</p>
              <p v-else class="input-hint">{{ t('admin.tests.experience.budgetOff') }}</p>
            </section>
            <section class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700">
              <div class="flex items-center justify-between gap-3">
                <h3 class="font-semibold">{{ tr('frequencyTitle') }}</h3>
                <Toggle v-model="config.dynamic_enabled" :aria-label="tr('enableDynamic')" data-frequency-toggle />
              </div>
              <p class="input-hint">{{ tr('frequencyHint') }}</p>
              <fieldset v-show="config.dynamic_enabled || !!frequencyError" :disabled="!config.dynamic_enabled && !frequencyError" data-frequency-fields class="grid gap-4 sm:grid-cols-3">
                <label v-for="f in frequencyFields" :key="f.key" class="input-label">
                  {{ tr(f.key) }}
                  <input v-model.number="config[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1" />
                </label>
              </fieldset>
              <p v-if="!config.dynamic_enabled" class="input-hint">{{ t('admin.tests.experience.dynamicOff') }}</p>
            </section>
            <section v-if="config.pelican_review" class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700" data-pelican-review-config>
              <div class="flex items-center justify-between gap-3">
                <h3 class="font-semibold">{{ t('admin.tests.autoReview.title') }}</h3>
                <Toggle v-model="config.pelican_review.enabled" :aria-label="t('admin.tests.autoReview.enableGlobal')" data-pelican-review-global-enabled />
              </div>
              <p class="input-hint">{{ t('admin.tests.autoReview.globalHint') }}</p>
              <fieldset v-if="config.pelican_review.enabled" class="grid gap-4 sm:grid-cols-2" data-pelican-review-fields>
                <label class="input-label">
                  {{ t('admin.tests.autoReview.judgeAccount') }}
                  <select v-model.number="config.pelican_review.judge_account_id" required class="input mt-1" data-pelican-review-account>
                    <option :value="0" disabled>{{ t('admin.tests.autoReview.selectAccount') }}</option>
                    <option v-for="a in judgeAccounts" :key="a.id" :value="a.id">{{ a.name }} #{{ a.id }}</option>
                  </select>
                </label>
                <label class="input-label">
                  {{ t('admin.tests.autoReview.judgeModel') }}
                  <input v-model.trim="config.pelican_review.judge_model" required maxlength="200" class="input mt-1" data-pelican-review-model />
                </label>
                <label class="input-label">
                  {{ t('admin.tests.autoReview.dailyRequests') }}
                  <input v-model.number="config.pelican_review.daily_requests" required type="number" min="1" max="1000000" step="1" class="input mt-1" data-pelican-review-budget />
                </label>
                <p class="input-hint sm:col-span-2">{{ t('admin.tests.autoReview.budgetHint') }}</p>
              </fieldset>
              <p v-if="reviewError" role="alert" class="input-error-text">{{ reviewError }}</p>
            </section>
            <p v-if="budgetError || frequencyError" role="alert" class="input-error-text">{{ budgetError || frequencyError }}</p>
            <p v-if="saveError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ saveError }}</p>
          </fieldset>
        </form>
        <template #footer>
          <button type="button" class="btn btn-secondary" :disabled="busy" data-cancel-operations @click="config = null">{{ t('common.cancel') }}</button>
          <button type="submit" form="quality-operations-form" class="btn btn-primary" :disabled="busy || !!budgetError || !!frequencyError || !!reviewError" data-save-operations>
            {{ t(busy ? 'common.saving' : 'common.save') }}
          </button>
        </template>
      </BaseDialog>
    </template>
  </div>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { qualityOperationsAPI as api, copyQualityOperationsConfig, type QualityOperationsSnapshot, type QualityOperationsConfig } from '@/api/admin/qualityOperations'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import type { AccountListItem } from '@/types'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ visible: boolean; accounts: AccountListItem[] }>()
const { t } = useI18n()
const date = formatDateTime
const tr = (key: string, args: Record<string, unknown> = {}) => t(`admin.tests.operations.${key}`, args)
const snapshot = ref<QualityOperationsSnapshot | null>(null), config = ref<QualityOperationsConfig | null>(null)
const loading = ref(false), busy = ref(false), error = ref(''), saved = ref(false)
const saveError = ref('')
const settingsSummary = computed(() => [
  { label: tr('budgetTitle'), enabled: snapshot.value?.config.budget_enabled },
  { label: tr('frequencyTitle'), enabled: snapshot.value?.config.dynamic_enabled },
  { label: t('admin.tests.autoReview.title'), enabled: snapshot.value?.config.pelican_review?.enabled },
])
const total = computed(() => snapshot.value?.usage.find(u => u.account_id === 0))
const budgetFields = [{ key: 'daily_requests', min: 0, max: 10000000 }, { key: 'daily_tokens', min: 0, max: 1000000000000 }, { key: 'account_daily_requests', min: 0, max: 10000000 }, { key: 'account_daily_tokens', min: 0, max: 1000000000000 }, { key: 'token_reservation', min: 1024, max: 10000000 }] as const
const frequencyFields = [{ key: 'healthy_rounds', min: 1, max: 100 }, { key: 'healthy_base_seconds', min: 60, max: 86400 }, { key: 'healthy_max_seconds', min: 60, max: 86400 }] as const
function invalidField(fields: readonly { key: keyof QualityOperationsConfig; min: number; max: number }[]) {
  const c = config.value
  if (!c) return ''
  const field = fields.find(f => !Number.isInteger(c[f.key]) || Number(c[f.key]) < f.min || Number(c[f.key]) > f.max)
  return field ? t('admin.tests.experience.invalidSetting', { field: tr(field.key), min: field.min, max: field.max }) : ''
}
const budgetError = computed(() => invalidField(budgetFields))
const frequencyError = computed(() => invalidField(frequencyFields) || (config.value && config.value.healthy_max_seconds < config.value.healthy_base_seconds ? t('admin.tests.experience.frequencyOrder') : ''))
const judgeAccounts = computed(() => props.accounts.filter(a => a.platform === 'openai'))
const reviewError = computed(() => {
  const review = config.value?.pelican_review
  if (!review?.enabled) return ''
  return Number.isSafeInteger(review.judge_account_id) && review.judge_account_id > 0
    && judgeAccounts.value.some(a => a.id === review.judge_account_id)
    && !!review.judge_model.trim() && [...review.judge_model].length <= 200
    && Number.isInteger(review.daily_requests) && review.daily_requests >= 1 && review.daily_requests <= 1000000
    ? '' : t('admin.tests.autoReview.invalidConfig')
})
function accountName(id: number) { return `${props.accounts.find(a => a.id === id)?.name || ''} #${id}` }
function openSettings() {
  if (!snapshot.value) return
  config.value = copyQualityOperationsConfig(snapshot.value.config)
  saved.value = false
  saveError.value = ''
}

let refreshRequest = 0
async function refresh() {
  const request = ++refreshRequest
  loading.value = true
  error.value = ''
  try {
    const latest = await api.get()
    if (request !== refreshRequest) return
    if (config.value && snapshot.value && !busy.value) {
      const previous = copyQualityOperationsConfig(snapshot.value.config)
      if (JSON.stringify(config.value) === JSON.stringify(previous)) {
        config.value = copyQualityOperationsConfig(latest.config)
      }
    }
    snapshot.value = latest
  } catch (e) {
    if (request === refreshRequest) error.value = extractApiErrorMessage(e, tr('failed'))
  } finally {
    if (request === refreshRequest) loading.value = false
  }
}
async function save() {
  if (!config.value || !snapshot.value || busy.value || budgetError.value || frequencyError.value || reviewError.value) return
  busy.value = true
  saveError.value = ''
  try {
    const updated = await api.save(copyQualityOperationsConfig(config.value))
    snapshot.value = { ...snapshot.value, config: updated }
    config.value = null
    saved.value = true
    void refresh()
  } catch (e) {
    saveError.value = extractApiErrorMessage(e, tr('failed'))
  } finally {
    busy.value = false
  }
}
watch(() => props.visible, visible => { if (visible) void refresh() }, { immediate: true })
defineExpose({ refresh })
</script>
