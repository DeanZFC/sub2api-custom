<template>
  <div class="channel-card-metrics my-3 min-w-0 flex-1" data-testid="card-metrics">
    <div class="metrics-layout" :class="{ 'metrics-layout--models': showModels }">
      <section class="min-w-0" :class="{ 'rounded-xl bg-gray-50 p-3 dark:bg-dark-900/40': showModels }" :aria-label="t('channelMonitorV2.cards.overall')">
        <div v-if="showModels" class="mb-3 flex items-center justify-between gap-2 text-xs">
          <button type="button" class="rounded font-semibold text-emerald-700 focus-visible:outline focus-visible:outline-2 focus-visible:outline-emerald-500 dark:text-emerald-400" :aria-pressed="!selectedModel" @click="$emit('select-model', undefined)">{{ t('channelMonitorV2.cards.overall') }}</button>
          <span class="truncate text-[10px] text-gray-400">{{ t('channelMonitorV2.cards.allModels') }}</span>
        </div>
        <dl class="grid grid-cols-2 gap-x-1 gap-y-4" :class="{ 'rounded-xl bg-gray-50 p-3 dark:bg-dark-900/40': !showModels }">
          <div class="min-w-0">
            <dt class="truncate text-xs text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.cacheHint')">{{ t('channelMonitorV2.metrics.cacheRate') }}</dt>
            <dd class="mt-1 font-mono text-lg font-bold tabular-nums text-gray-800 dark:text-gray-100">{{ formatMonitorCacheRate(row.metrics) }}</dd>
          </div>
          <div class="min-w-0">
            <dt class="truncate text-xs text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.metrics.successRate') }}</dt>
            <dd class="mt-1 font-mono text-lg font-bold tabular-nums" :class="successClass[row.health.error_rate]">{{ formatMonitorSuccessRateFromError(row.metrics.error_rate, row.health.error_rate) }}</dd>
          </div>
          <div class="min-w-0">
            <dt class="truncate text-xs text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.ttftP50')">{{ t('channelMonitorV2.metrics.ttft') }}</dt>
            <dd class="mt-1 font-mono text-lg font-bold tabular-nums text-gray-800 dark:text-gray-100" :title="latencyDetail(row)">{{ formatMonitorMs(row.metrics.ttft.p50_ms) }}</dd>
          </div>
          <div class="min-w-0" data-output-tps>
            <dt class="truncate text-xs text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.outputTpsDetail')">{{ t('channelMonitorV2.metrics.outputTps') }}</dt>
            <dd class="mt-1 font-mono text-lg font-bold tabular-nums text-gray-800 dark:text-gray-100" :title="formatMonitorOutputTps(row.metrics.output_tps)">{{ compactTps(row.metrics.output_tps) }}</dd>
          </div>
        </dl>
      </section>
      <section v-if="showModels" class="model-metrics min-w-0" :aria-label="t('channelMonitorV2.cards.models')">
        <div class="mb-2 flex h-5 items-center justify-between gap-2 text-xs">
          <h4 class="font-semibold text-gray-700 dark:text-gray-200">{{ t('channelMonitorV2.cards.models') }}</h4>
          <span class="text-[10px] text-gray-400">{{ t('channelMonitorV2.cards.modelCount', { count: models.length }) }}</span>
        </div>
        <div class="overflow-x-auto">
          <table class="model-metrics-table w-full table-fixed text-xs" :aria-label="t('channelMonitorV2.cards.models')">
            <colgroup><col class="model-name-column"><col><col><col><col></colgroup>
            <thead class="text-[10px] font-normal text-gray-500 dark:text-gray-400">
              <tr class="h-8 border-b border-gray-100 dark:border-dark-700">
                <th scope="col" class="text-left font-normal">{{ t('channelMonitorV2.filters.model') }}</th>
                <th scope="col" class="text-right font-normal" :title="t('channelMonitorV2.metrics.cacheHint')">{{ t('channelMonitorV2.metrics.cacheRate') }}</th>
                <th scope="col" class="text-right font-normal">{{ t('channelMonitorV2.metrics.successRate') }}</th>
                <th scope="col" class="text-right font-normal" :title="t('channelMonitorV2.metrics.ttftP50')">{{ t('channelMonitorV2.metrics.ttft') }}</th>
                <th scope="col" class="text-right font-normal" :title="t('channelMonitorV2.metrics.outputTpsDetail')">TPS</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="model in models" :key="model.model" class="h-12 border-b border-gray-100 last:border-0 dark:border-dark-700" :class="{ 'bg-emerald-50/70 dark:bg-emerald-900/20': selectedModel === model.model }" data-testid="model-metrics-row">
                <th scope="row" class="pr-2 text-left font-medium">
                  <button type="button" class="flex w-full min-w-0 items-center gap-1 rounded py-3 text-gray-700 hover:text-emerald-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-emerald-500 dark:text-gray-200" :title="model.model" :aria-pressed="selectedModel === model.model" @click="$emit('select-model', model.model)">
                    <span class="truncate">{{ model.model }}</span><span class="ml-auto shrink-0 text-gray-400" aria-hidden="true">›</span>
                  </button>
                </th>
                <td class="text-right tabular-nums text-gray-700 dark:text-gray-200">{{ formatMonitorCacheRate(model.metrics) }}</td>
                <td class="text-right tabular-nums" :class="successClass[model.health.error_rate]">{{ formatMonitorSuccessRateFromError(model.metrics.error_rate, model.health.error_rate) }}</td>
                <td class="text-right tabular-nums text-gray-700 dark:text-gray-200" :title="latencyDetail(model)">{{ formatMonitorMs(model.metrics.ttft.p50_ms) }}</td>
                <td class="text-right tabular-nums text-gray-700 dark:text-gray-200" :title="formatMonitorOutputTps(model.metrics.output_tps)">{{ compactTps(model.metrics.output_tps) }}</td>
              </tr>
              <tr v-if="!models.length"><td colspan="5" class="py-8 text-center text-gray-400">{{ t('channelMonitorV2.cards.noModels') }}</td></tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import { formatLatencyPrivacy, formatMonitorCacheRate, formatMonitorMs, formatMonitorOutputTps, formatMonitorRate, formatMonitorSuccessRateFromError } from './monitorFormat'

defineProps<{ row: MonitorMatrixRow; models: MonitorMatrixRow[]; showModels: boolean; selectedModel?: string }>()
defineEmits<{ 'select-model': [model: string | undefined] }>()
const { t } = useI18n()
const successClass = { healthy: 'text-emerald-600 dark:text-emerald-400', warning: 'text-amber-600 dark:text-amber-400', critical: 'text-rose-500 dark:text-rose-400', unknown: 'text-gray-400' }
function compactTps(value?: number | null) { return value == null || !Number.isFinite(value) || value < 0 ? '—' : formatMonitorRate(value) }
function latencyDetail(row: MonitorMatrixRow) { const m = row.metrics.ttft; return formatLatencyPrivacy(m.p50_ms, m.p90_ms, m.avg_ms, m.p95_ms) }
</script>

<style scoped>
.channel-card-metrics { container-type: inline-size; }
.metrics-layout { display: grid; gap: 1rem; }
.metrics-layout--models { min-height: 13rem; }
.model-metrics { min-height: 13rem; }
.model-metrics-table { min-width: 21rem; }
.model-name-column { width: 34%; }
@container (min-width: 540px) {
  .metrics-layout--models { height: 100%; grid-template-columns: clamp(11rem, 28%, 13rem) minmax(0, 1fr); }
}
</style>
