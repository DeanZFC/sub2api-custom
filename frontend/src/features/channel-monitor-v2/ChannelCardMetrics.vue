<template>
  <div class="channel-card-metrics my-5 min-w-0 flex-1" data-testid="card-metrics">
    <div class="metrics-layout">
      <section class="min-w-0" :aria-label="t('channelMonitorV2.cards.overall')">
        <div v-if="showModels" class="mb-2 flex items-center justify-between gap-2 text-xs">
          <button type="button" class="rounded font-semibold text-emerald-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-emerald-500 dark:text-emerald-400" :aria-pressed="!selectedModel" @click="$emit('select-model', undefined)">{{ t('channelMonitorV2.cards.overall') }}</button>
          <span class="truncate text-[10px] text-gray-400">{{ t('channelMonitorV2.cards.allModels') }}</span>
        </div>
        <dl class="overall-grid grid grid-cols-2 gap-2">
          <div class="min-w-0">
            <dt class="truncate text-xs text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.cacheHint')">{{ t('channelMonitorV2.metrics.cacheRate') }}</dt>
            <dd class="text-slate-900 dark:text-slate-100">{{ formatMonitorCacheRate(row.metrics) }}</dd>
          </div>
          <div class="min-w-0">
            <dt class="truncate text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.metrics.successRate') }}</dt>
            <dd :class="successClass[row.health.error_rate]">{{ formatMonitorSuccessRateFromError(row.metrics.error_rate, row.health.error_rate) }}</dd>
          </div>
          <div class="min-w-0">
            <dt class="truncate text-xs text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.ttftP50')">{{ t('channelMonitorV2.metrics.ttft') }}</dt>
            <dd class="text-slate-900 dark:text-slate-100" :title="latencyDetail(row)">{{ formatMonitorMs(row.metrics.ttft.p50_ms) }}</dd>
          </div>
          <div class="min-w-0" data-output-tps>
            <dt class="truncate text-xs text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.outputTpsDetail')">{{ t('channelMonitorV2.metrics.outputTps') }}</dt>
            <dd class="text-slate-900 dark:text-slate-100" :title="formatMonitorOutputTps(row.metrics.output_tps)">{{ compactTps(row.metrics.output_tps) }}</dd>
          </div>
        </dl>
      </section>
      <section v-if="showModels" class="model-metrics min-w-0 rounded-2xl border border-slate-200/90 bg-white/60 p-1.5 dark:border-dark-700 dark:bg-dark-900/30" :aria-label="t('channelMonitorV2.cards.models')">
        <div class="model-grid items-center px-2 py-1 text-xs">
          <div class="model-name flex flex-wrap items-baseline gap-x-2">
            <h4 class="font-semibold text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.cards.models') }}</h4>
            <span class="text-[10px] text-gray-400">{{ t('channelMonitorV2.cards.modelCount', { count: models.length }) }}</span>
          </div>
          <template v-if="models.length">
            <span v-for="key in modelMetricKeys" :key="key" class="model-column-title text-right text-[10px] text-gray-500 dark:text-gray-400" aria-hidden="true">{{ t(`channelMonitorV2.metrics.${key}`) }}</span>
          </template>
        </div>
        <div :id="modelsListId" class="divide-y divide-gray-100 dark:divide-dark-700">
          <button
            v-for="model in visibleModels"
            :key="model.model"
            type="button"
            class="model-grid w-full items-center rounded-md px-2 py-2 text-left transition-colors hover:bg-emerald-50/50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-emerald-500 dark:hover:bg-emerald-900/20"
            :class="{ 'bg-emerald-50 dark:bg-emerald-900/30': selectedModel === model.model }"
            :title="model.model"
            :aria-pressed="selectedModel === model.model"
            data-testid="model-metrics-card"
            @click="$emit('select-model', model.model)"
          >
            <span class="model-name flex min-w-0 items-center gap-1 text-xs font-semibold text-gray-800 dark:text-gray-100">
              <span class="min-w-0 break-words">{{ model.model }}</span>
              <span class="shrink-0 text-gray-400" aria-hidden="true">›</span>
            </span>
            <span class="model-value" :title="t('channelMonitorV2.metrics.cacheHint')">
              <span class="model-label">{{ t('channelMonitorV2.metrics.cacheRate') }}</span>
              <span data-testid="model-cache-rate">{{ formatMonitorCacheRate(model.metrics) }}</span>
            </span>
            <span class="model-value">
              <span class="model-label">{{ t('channelMonitorV2.metrics.successRate') }}</span>
              <span :class="successClass[model.health.error_rate]">{{ formatMonitorSuccessRateFromError(model.metrics.error_rate, model.health.error_rate) }}</span>
            </span>
            <span class="model-value" :title="latencyDetail(model)">
              <span class="model-label">{{ t('channelMonitorV2.metrics.ttft') }}</span>
              {{ formatMonitorMs(model.metrics.ttft.p50_ms) }}
            </span>
            <span class="model-value" :title="formatMonitorOutputTps(model.metrics.output_tps)">
              <span class="model-label">{{ t('channelMonitorV2.metrics.outputTps') }}</span>
              {{ compactTps(model.metrics.output_tps) }}
            </span>
          </button>
        </div>
        <p v-if="!models.length" class="py-2 text-center text-xs text-gray-400">{{ t('channelMonitorV2.cards.noModels') }}</p>
        <button v-if="models.length > collapsedModelCount" type="button" class="mt-2 flex w-full items-center justify-center gap-1 rounded-lg py-2 text-xs font-medium text-emerald-600 hover:bg-emerald-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-emerald-500 dark:text-emerald-400 dark:hover:bg-emerald-900/20" :aria-expanded="modelsExpanded" :aria-controls="modelsListId" data-testid="model-metrics-toggle" @click="modelsExpanded = !modelsExpanded">
          {{ modelsExpanded ? t('channelMonitorV2.cards.collapseModels') : t('channelMonitorV2.cards.expandModels', { count: models.length - collapsedModelCount }) }}
          <span class="inline-block transition-transform" :class="modelsExpanded ? '-rotate-90' : 'rotate-90'" aria-hidden="true">›</span>
        </button>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import { formatLatencyPrivacy, formatMonitorCacheRate, formatMonitorMs, formatMonitorOutputTps, formatMonitorRate, formatMonitorSuccessRateFromError } from './monitorFormat'

const props = defineProps<{ row: MonitorMatrixRow; models: MonitorMatrixRow[]; showModels: boolean; selectedModel?: string }>()
defineEmits<{ 'select-model': [model: string | undefined] }>()
const { t } = useI18n()
const modelMetricKeys = ['cacheRate', 'successRate', 'ttft', 'outputTps'] as const
const collapsedModelCount = 3
const modelsExpanded = ref(false)
const modelsListId = `channel-models-${useId()}`
const visibleModels = computed(() => modelsExpanded.value ? props.models : props.models.slice(0, collapsedModelCount))
watch(() => props.models.length, count => { if (count <= collapsedModelCount) modelsExpanded.value = false })
const successClass = { healthy: 'text-emerald-700 dark:text-emerald-400', warning: 'text-amber-700 dark:text-amber-400', critical: 'text-red-600 dark:text-red-400', unknown: 'text-gray-400' }
function compactTps(value?: number | null) { return value == null || !Number.isFinite(value) || value < 0 ? '—' : formatMonitorRate(value) }
function latencyDetail(row: MonitorMatrixRow) { const m = row.metrics.ttft; return formatLatencyPrivacy(m.p50_ms, m.p90_ms, m.avg_ms, m.p95_ms) }
</script>

<style scoped>
.channel-card-metrics { container-type: inline-size; }
.metrics-layout { display: grid; gap: 1rem; }
.overall-grid > div {
  @apply rounded-2xl border border-slate-200/90 bg-slate-50/80 px-2.5 py-3 dark:border-dark-700 dark:bg-dark-900/40;
}
.overall-grid dd {
  @apply mt-1.5 font-mono text-lg font-bold leading-tight tabular-nums;
}
.model-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0.25rem 0.5rem;
}
.model-name { grid-column: 1 / -1; }
.model-column-title { display: none; }
.model-value { @apply min-w-0 font-mono text-xs font-semibold tabular-nums text-gray-700 dark:text-gray-200; }
.model-label { @apply block font-sans text-[10px] font-normal text-gray-500 dark:text-gray-400; }
@container (max-width: 18rem) {
  .model-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@container (min-width: 24rem) {
  .overall-grid { grid-template-columns: repeat(4, minmax(0, 1fr)); }
}
@container (min-width: 26rem) {
  .model-grid { grid-template-columns: minmax(0, 2.4fr) repeat(4, minmax(0, 1fr)); }
  .model-name { grid-column: auto; }
  .model-column-title { display: block; }
  .model-value { text-align: right; }
  .model-label { @apply sr-only; }
}
</style>
