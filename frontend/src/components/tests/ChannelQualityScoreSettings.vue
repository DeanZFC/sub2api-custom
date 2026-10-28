<template>
  <section class="space-y-3 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900" data-score-settings>
    <h3 class="font-semibold">{{ tr('title') }}</h3>
    <p class="text-sm text-gray-500">{{ tr('intro') }}</p>
    <p v-if="loading && !config" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }} <button v-if="!conflict" type="button" class="underline" @click="refresh()">{{ t('common.retry') }}</button></p>
    <button v-if="conflict" type="button" class="btn btn-secondary btn-sm" data-score-reload :disabled="busy || loading" @click="refresh(true)">{{ tr('reload') }}</button>
    <form v-if="config" class="space-y-4" @submit.prevent="save">
      <label class="flex items-center gap-2 text-sm"><input v-model="config.enabled" type="checkbox" :disabled="busy" />{{ tr('enabled') }}</label>
      <p class="text-sm text-gray-600 dark:text-gray-300" data-score-window>{{ tr('window', { minutes: Number((config.window_seconds / 60).toFixed(2)), max: config.max_requests, min: config.min_samples }) }}</p>
      <p class="text-xs text-gray-500">{{ tr(config.enabled ? 'fallback' : 'disabledHint') }}</p>
      <details :open="!!validationError" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700" data-score-advanced>
        <summary class="cursor-pointer text-sm font-medium">{{ tr('advanced') }}</summary>
        <fieldset :disabled="busy" class="mt-4 space-y-4">
          <p class="text-xs text-gray-500">{{ tr('independent') }}</p>
          <div class="grid gap-3 sm:grid-cols-3"><label v-for="field in qualityScoreWindowFields" :key="field.key" class="input-label">{{ tr(field.key) }}<input v-model.number="config[field.key]" :data-score-field="field.key" type="number" :min="field.min" :max="field.max" step="1" required class="input mt-1 w-full" /></label></div>
          <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr><th class="p-2">{{ tr('dimension') }}</th><th class="p-2">{{ tr('weight') }}</th><th class="p-2">{{ tr('good') }}</th><th class="p-2">{{ tr('bad') }}</th></tr></thead><tbody>
            <tr v-for="key in qualityScoreDimensions" :key="key"><th scope="row" class="p-2 font-normal">{{ tr('dimensions.' + key) }}</th><td v-for="field in ['weight', 'good', 'bad'] as const" :key="field" class="p-2"><input v-model.number="config[key][field]" :aria-label="tr('dimensions.' + key) + ' · ' + tr(field)" :data-score-field="`${key}.${field}`" type="number" min="0" :max="field === 'weight' || key === 'error_rate' || key === 'cache_rate' ? 100 : key === 'tps' ? 1000000 : 3600000" step="any" required class="input w-full min-w-24" /></td></tr>
          </tbody></table></div>
          <p class="text-xs leading-5 text-gray-500">{{ tr('formula') }}</p>
          <button type="button" class="btn btn-secondary btn-sm" data-score-defaults @click="config = defaultQualityScoreConfig(config.version)">{{ tr('defaults') }}</button>
        </fieldset>
      </details>
      <p v-if="validationError" role="alert" class="text-sm text-red-600">{{ validationError }}</p>
      <div class="flex items-center gap-3"><button type="submit" class="btn btn-primary btn-sm" data-save-score :disabled="busy || loading || !!validationError || conflict">{{ t('common.save') }}</button><span v-if="saved" role="status" class="text-sm text-green-600">{{ tr('saved') }}</span></div>
    </form>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { channelQualityAPI, type QualityScoreConfig } from '@/api/admin/channelQuality'
import { defaultQualityScoreConfig, qualityScoreConfigError, qualityScoreDimensions, qualityScoreWindowFields } from '@/utils/channelQualityScore'
import { extractApiErrorMessage } from '@/utils/apiError'
const { t } = useI18n()
const tr = (key: string, values: Record<string, unknown> = {}) => t(`admin.tests.scoring.${key}`, values)
const config = ref<QualityScoreConfig | null>(null), loading = ref(false), busy = ref(false), error = ref(''), conflict = ref(false), saved = ref(false)
let baseline = '', request = 0
const validationError = computed(() => {
  const issue = config.value && qualityScoreConfigError(config.value)
  return issue ? tr(issue.key, { dimension: issue.dimension ? tr('dimensions.' + issue.dimension) : '' }) : ''
})
async function refresh(discard = false) {
  if (busy.value) return
  const token = ++request
  loading.value = true
  try {
    const latest = { ...defaultQualityScoreConfig(), ...await channelQualityAPI.scoreConfig() }
    if (token !== request) return
    if (!config.value || discard || JSON.stringify(config.value) === baseline) {
      config.value = latest; baseline = JSON.stringify(latest); conflict.value = false; error.value = ''
    }
  } catch (e) { if (token === request) error.value = extractApiErrorMessage(e, tr('failed')) }
  finally { if (token === request) loading.value = false }
}
async function save() {
  if (!config.value || busy.value || loading.value || validationError.value || conflict.value) return
  busy.value = true; saved.value = false; error.value = ''
  try {
    config.value = { ...defaultQualityScoreConfig(), ...await channelQualityAPI.saveScoreConfig(JSON.parse(JSON.stringify(config.value))) }
    baseline = JSON.stringify(config.value); saved.value = true
  } catch (e) {
    const failure = e as { status?: number; response?: { status?: number } }
    conflict.value = (failure.response?.status || failure.status) === 409
    error.value = conflict.value ? tr('conflict') : extractApiErrorMessage(e, tr('failed'))
  } finally { busy.value = false }
}
watch(config, () => { saved.value = false }, { deep: true, flush: 'sync' })
onMounted(() => { void refresh() })
onUnmounted(() => { request++ })
defineExpose({ refresh })
</script>
