<template>
  <details class="rounded-lg border border-gray-200 p-4 dark:border-dark-700" data-quality-replay>
    <summary class="cursor-pointer font-semibold">{{ tr('title') }}</summary>
    <div class="mt-4 space-y-4">
      <p class="text-sm text-gray-500">{{ tr('intro') }}</p>
      <p class="text-xs text-gray-500">{{ t('admin.tests.passive.modelFilter') }}: {{ policy.model || t('admin.tests.passive.allModels') }} · {{ t(`admin.tests.passive.${policy.scope === 'account' ? 'accountScope' : 'modelScope'}`) }}</p>
      <div class="grid gap-3 sm:grid-cols-3"><label class="input-label">{{ tr('from') }}<input v-model="from" type="datetime-local" class="input mt-1 w-full" /></label><label class="input-label">{{ tr('to') }}<input v-model="to" type="datetime-local" class="input mt-1 w-full" /></label><label class="input-label">{{ tr('step') }}<input v-model.number="step" type="number" min="15" max="3600" step="1" class="input mt-1 w-full" /></label></div>
      <p class="text-xs text-gray-500">{{ tr('limits') }}</p>
      <div class="max-h-36 space-y-2 overflow-auto"><label v-for="a in choices" :key="a.id" class="flex items-center gap-2 text-sm"><input v-model="selected" type="checkbox" :value="a.id" :disabled="selected.length >= 10 && !selected.includes(a.id)" />{{ a.name }} #{{ a.id }}</label></div>
      <p v-if="!choices.length" class="text-sm text-amber-600">{{ tr('noAccounts') }}</p>
      <button type="button" class="btn btn-secondary" :disabled="disabled || running || !!validation" data-run-replay @click="run">{{ tr(running ? 'running' : 'run') }}</button><p v-if="validation" class="text-xs text-gray-500">{{ validation }}</p><p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <template v-if="result">
        <p class="text-sm font-medium" role="status">{{ tr('summary', { evaluations: result.evaluations, triggers: result.triggers, pending: result.pending }) }}</p>
        <ul class="list-inside list-disc space-y-1 text-xs text-amber-700 dark:text-amber-400"><li v-for="warning in result.warnings" :key="warning">{{ tr(`warnings.${warning}`) }}</li></ul>
        <label class="flex items-center gap-2 text-sm"><input v-model="changesOnly" type="checkbox" />{{ tr('changesOnly') }}</label>
        <div class="max-h-96 overflow-auto"><table class="w-full text-left text-xs"><thead><tr><th class="p-2">{{ tr('timeAccount') }}</th><th class="p-2">{{ tr('verdict') }}</th><th class="p-2">{{ tr('metrics') }}</th><th class="p-2">{{ tr('action') }}</th></tr></thead><tbody><tr v-for="p in points" :key="`${p.account_id}-${p.at}`" class="border-t border-gray-100 dark:border-dark-800"><td class="p-2">{{ date(p.at) }}<br />#{{ p.account_id }}</td><td class="p-2">{{ tr(`verdicts.${p.verdict}`) }}<br />{{ reason(p.reason) }}</td><td class="p-2">{{ p.evidence.samples }} · {{ metric(p.evidence.error_rate, '%') }} · {{ metric(p.evidence.cache_rate, '%') }} · {{ metric(p.evidence.ttft, ' ms') }}</td><td class="p-2">{{ p.action ? tr(`actions.${p.action}`) : '—' }}<br />{{ tr('hits', { count: p.hits }) }}</td></tr><tr v-if="!points.length"><td colspan="4" class="p-4 text-center text-gray-500">{{ tr('noChanges') }}</td></tr></tbody></table></div>
      </template>
    </div>
  </details>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { qualityOperationsAPI as api, type QualityReplayResult } from '@/api/admin/qualityOperations'
import type { PassivePolicy } from '@/api/admin/passiveQuality'
import type { AccountListItem } from '@/types'
import { useQualityLabels } from '@/composables/useQualityLabels'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ policy: PassivePolicy; accounts: AccountListItem[]; disabled?: boolean }>()
const { t } = useI18n(), { date, reason } = useQualityLabels()
const tr = (key: string, args: Record<string, unknown> = {}) => t(`admin.tests.replay.${key}`, args)
function localTime(ms: number) { const d = new Date(ms); return new Date(ms - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
const from = ref(localTime(Date.now() - 3600000)), to = ref(localTime(Date.now())), step = ref(60), selected = ref<number[]>([])
const result = ref<QualityReplayResult | null>(null), running = ref(false), error = ref(''), changesOnly = ref(true)
let controller: AbortController | null = null, generation = 0
const choices = computed(() => props.accounts.filter(a => !props.policy.excluded_account_ids.includes(a.id) && a.group_ids?.some(id => props.policy.group_ids.includes(id))))
const validation = computed(() => {
  if (!selected.value.length) return tr('selectAccounts')
  const start = new Date(from.value).getTime(), end = new Date(to.value).getTime()
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start || end > Date.now() + 60000 || start < Date.now() - 48 * 3600000 || end - start > 24 * 3600000 || !Number.isInteger(step.value) || step.value < 15 || step.value > 3600 || (Math.floor((end - start) / (step.value * 1000)) + 1) * selected.value.length > 2000) return tr('limits')
  return ''
})
const points = computed(() => result.value?.points.filter(p => !changesOnly.value || (p.action && p.action !== 'recovery_requires_probe')) || [])
function metric(n: number | null, unit: string) { return n == null ? '—' : `${Math.round(n * 100) / 100}${unit}` }
function invalidate() { generation++; controller?.abort(); controller = null; running.value = false; result.value = null; error.value = '' }
async function run() {
  if (validation.value || props.disabled || running.value) return
  invalidate(); const id = generation; controller = new AbortController(); running.value = true
  try {
    const output = await api.replay({ config: JSON.parse(JSON.stringify(props.policy)), account_ids: [...selected.value], from: new Date(from.value).toISOString(), to: new Date(to.value).toISOString(), step_seconds: step.value }, controller.signal)
    if (id === generation) result.value = output
  } catch (e) { if (id === generation) error.value = extractApiErrorMessage(e, tr('failed')) } finally { if (id === generation) running.value = false }
}
watch([() => props.policy, from, to, step, selected], invalidate, { deep: true })
watch(choices, list => { selected.value = selected.value.filter(id => list.some(a => a.id === id)) })
onBeforeUnmount(invalidate)
</script>
