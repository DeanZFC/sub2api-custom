<template>
  <div class="space-y-5" data-quality-operations>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    <template v-if="snapshot && config">
      <div class="grid gap-3 sm:grid-cols-3">
        <div v-for="metric in ['requests', 'tokens', 'unknown_requests'] as const" :key="metric" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><p class="text-sm text-gray-500">{{ tr(metric) }}</p><strong class="mt-2 block text-2xl">{{ (total?.[metric] || 0).toLocaleString() }}</strong></div>
      </div>
      <p class="text-xs text-gray-500">{{ tr('resetHint', { date: date(snapshot.resets_at) }) }}</p>
      <form class="space-y-5 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900" @submit.prevent="save">
        <h3 class="font-semibold">{{ tr('budgetTitle') }}</h3><p class="text-sm text-gray-500">{{ tr('budgetHint') }}</p>
        <label class="flex items-center gap-2 text-sm"><input v-model="config.budget_enabled" type="checkbox" />{{ tr('enableBudget') }}</label>
        <fieldset :disabled="!config.budget_enabled && !budgetError" v-show="config.budget_enabled || !!budgetError" class="space-y-4" data-budget-fields>
          <div class="grid gap-4 sm:grid-cols-2"><label v-for="f in budgetFields.slice(0, 2)" :key="f.key" class="input-label">{{ tr(f.key) }}<input v-model.number="config[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1 w-full" /></label></div>
          <details :open="!!budgetError" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"><summary class="cursor-pointer text-sm">{{ t('admin.tests.experience.budgetAdvanced') }}</summary><div class="mt-3 grid gap-4 sm:grid-cols-3"><label v-for="f in budgetFields.slice(2)" :key="f.key" class="input-label">{{ tr(f.key) }}<input v-model.number="config[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1 w-full" /></label></div></details>
        </fieldset>
        <p v-if="!config.budget_enabled" class="text-sm text-gray-500">{{ t('admin.tests.experience.budgetOff') }}</p>
        <p v-if="config.budget_enabled" class="text-xs text-amber-700 dark:text-amber-400">{{ tr('tokenHint') }}</p>
        <hr class="dark:border-dark-700" /><h3 class="font-semibold">{{ tr('frequencyTitle') }}</h3>
        <label class="flex items-center gap-2 text-sm"><input v-model="config.dynamic_enabled" type="checkbox" />{{ tr('enableDynamic') }}</label><p class="text-sm text-gray-500">{{ tr('frequencyHint') }}</p>
        <fieldset :disabled="!config.dynamic_enabled && !frequencyError" v-show="config.dynamic_enabled || !!frequencyError" data-frequency-fields class="grid gap-4 sm:grid-cols-3"><label v-for="f in frequencyFields" :key="f.key" class="input-label">{{ tr(f.key) }}<input v-model.number="config[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1 w-full" /></label></fieldset>
        <p v-if="!config.dynamic_enabled" class="text-sm text-gray-500">{{ t('admin.tests.experience.dynamicOff') }}</p>
        <hr class="dark:border-dark-700" />
        <section v-if="config.pelican_review" class="space-y-3" data-pelican-review-config>
          <h3 class="font-semibold">{{ t('admin.tests.autoReview.title') }}</h3>
          <label class="flex items-center gap-2 text-sm"><input v-model="config.pelican_review.enabled" type="checkbox" data-pelican-review-global-enabled />{{ t('admin.tests.autoReview.enableGlobal') }}</label>
          <p class="text-sm text-gray-500">{{ t('admin.tests.autoReview.globalHint') }}</p>
          <fieldset v-if="config.pelican_review.enabled" class="grid gap-4 sm:grid-cols-2" data-pelican-review-fields>
            <label class="input-label">{{ t('admin.tests.autoReview.judgeAccount') }}<select v-model.number="config.pelican_review.judge_account_id" required class="input mt-1 w-full" data-pelican-review-account><option :value="0" disabled>{{ t('admin.tests.autoReview.selectAccount') }}</option><option v-for="a in judgeAccounts" :key="a.id" :value="a.id">{{ a.name }} #{{ a.id }}</option></select></label>
            <label class="input-label">{{ t('admin.tests.autoReview.judgeModel') }}<input v-model.trim="config.pelican_review.judge_model" required maxlength="200" class="input mt-1 w-full" data-pelican-review-model /></label>
            <label class="input-label">{{ t('admin.tests.autoReview.dailyRequests') }}<input v-model.number="config.pelican_review.daily_requests" required type="number" min="1" max="1000000" step="1" class="input mt-1 w-full" data-pelican-review-budget /></label>
            <p class="text-xs text-gray-500 sm:col-span-2">{{ t('admin.tests.autoReview.budgetHint') }}</p>
          </fieldset>
          <p v-if="reviewError" role="alert" class="text-sm text-red-600">{{ reviewError }}</p>
        </section>
        <p v-if="budgetError || frequencyError" role="alert" class="text-sm text-red-600">{{ budgetError || frequencyError }}</p>
        <div class="flex items-center gap-3"><button class="btn btn-primary" type="submit" :disabled="busy || !!budgetError || !!frequencyError || !!reviewError" data-save-operations>{{ t('common.save') }}</button><span v-if="saved" role="status" class="text-sm text-green-600">{{ tr('saved') }}</span></div>
      </form>
      <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><summary class="cursor-pointer text-sm font-medium">{{ tr('accountUsage') }}</summary><div class="mt-3 max-h-72 overflow-auto"><table class="w-full text-left text-sm"><thead><tr><th class="p-2">{{ tr('account') }}</th><th class="p-2">{{ tr('requests') }}</th><th class="p-2">{{ tr('tokens') }}</th><th class="p-2">{{ tr('unknown_requests') }}</th></tr></thead><tbody><tr v-for="u in snapshot.usage.filter(u => u.account_id)" :key="u.account_id"><td class="p-2">{{ accountName(u.account_id) }}</td><td class="p-2">{{ u.requests }}</td><td class="p-2">{{ u.tokens }}</td><td class="p-2">{{ u.unknown_requests }}</td></tr></tbody></table></div></details>
    </template>
  </div>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { qualityOperationsAPI as api, copyQualityOperationsConfig, type QualityOperationsSnapshot, type QualityOperationsConfig } from '@/api/admin/qualityOperations'
import type { AccountListItem } from '@/types'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ visible: boolean; accounts: AccountListItem[] }>()
const { t } = useI18n()
const date = formatDateTime
const tr = (key: string, args: Record<string, unknown> = {}) => t(`admin.tests.operations.${key}`, args)
const snapshot = ref<QualityOperationsSnapshot | null>(null), config = ref<QualityOperationsConfig | null>(null)
const loading = ref(false), busy = ref(false), error = ref(''), saved = ref(false)
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
function report(e: unknown) { error.value = extractApiErrorMessage(e, tr('failed')) }
async function refresh() {
  if (loading.value) return
  loading.value = true; error.value = ''
  try { const previous = snapshot.value ? copyQualityOperationsConfig(snapshot.value.config) : undefined; snapshot.value = await api.get(); if (!config.value || JSON.stringify(config.value) === JSON.stringify(previous)) config.value = copyQualityOperationsConfig(snapshot.value.config) } catch (e) { report(e) } finally { loading.value = false }
}
async function save() {
  if (!config.value || busy.value || budgetError.value || frequencyError.value || reviewError.value) return
  busy.value = true; error.value = ''; saved.value = false
  try { config.value = copyQualityOperationsConfig(await api.save(copyQualityOperationsConfig(config.value))); if (snapshot.value) snapshot.value.config = copyQualityOperationsConfig(config.value); saved.value = true; await refresh() } catch (e) { report(e) } finally { busy.value = false }
}
watch(() => props.visible, visible => { if (visible) void refresh() }, { immediate: true })
watch(config, () => { saved.value = false }, { deep: true, flush: 'sync' })
defineExpose({ refresh })
</script>
