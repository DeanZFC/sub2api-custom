<template>
  <div class="channel-card-metrics my-3 min-w-0 flex-1" data-testid="card-metrics">
    <div class="metrics-layout">
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
        <div :id="modelsListId" class="model-metrics-grid">
          <button
            v-for="model in visibleModels"
            :key="model.model"
            type="button"
            class="min-w-0 rounded-xl border p-2.5 text-left transition-colors hover:border-emerald-300 hover:bg-emerald-50/50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-emerald-500 dark:hover:border-emerald-700 dark:hover:bg-emerald-900/20"
            :class="selectedModel === model.model ? 'border-emerald-400 bg-emerald-50/70 dark:border-emerald-600 dark:bg-emerald-900/20' : 'border-gray-100 bg-gray-50/70 dark:border-dark-700 dark:bg-dark-900/30'"
            :title="model.model"
            :aria-pressed="selectedModel === model.model"
            data-testid="model-metrics-card"
            @click="$emit('select-model', model.model)"
          >
            <span class="mb-3 flex min-w-0 items-center gap-1 text-xs font-semibold text-gray-800 dark:text-gray-100"><span class="truncate">{{ model.model }}</span><span class="ml-auto shrink-0 text-gray-400" aria-hidden="true">›</span></span>
            <span class="grid grid-cols-2 gap-x-2 gap-y-2.5">
              <span class="min-w-0">
                <span class="block truncate text-[10px] text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.cacheHint')">{{ t('channelMonitorV2.metrics.cacheRate') }}</span>
                <span class="block mt-0.5 font-mono text-xs font-semibold tabular-nums text-gray-700 dark:text-gray-200" data-testid="model-cache-rate">{{ formatMonitorCacheRate(model.metrics) }}</span>
              </span>
              <span class="min-w-0">
                <span class="block truncate text-[10px] text-gray-500 dark:text-gray-400">{{ t('channelMonitorV2.metrics.successRate') }}</span>
                <span class="block mt-0.5 font-mono text-xs font-semibold tabular-nums" :class="successClass[model.health.error_rate]">{{ formatMonitorSuccessRateFromError(model.metrics.error_rate, model.health.error_rate) }}</span>
              </span>
              <span class="min-w-0">
                <span class="block truncate text-[10px] text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.ttftP50')">{{ t('channelMonitorV2.metrics.ttft') }}</span>
                <span class="block mt-0.5 font-mono text-xs font-semibold tabular-nums text-gray-700 dark:text-gray-200" :title="latencyDetail(model)">{{ formatMonitorMs(model.metrics.ttft.p50_ms) }}</span>
              </span>
              <span class="min-w-0">
                <span class="block truncate text-[10px] text-gray-500 dark:text-gray-400" :title="t('channelMonitorV2.metrics.outputTpsDetail')">TPS</span>
                <span class="block mt-0.5 font-mono text-xs font-semibold tabular-nums text-gray-700 dark:text-gray-200" :title="formatMonitorOutputTps(model.metrics.output_tps)">{{ compactTps(model.metrics.output_tps) }}</span>
              </span>
            </span>
          </button>
        </div>
        <p v-if="!models.length" class="py-6 text-center text-xs text-gray-400">{{ t('channelMonitorV2.cards.noModels') }}</p>
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
const collapsedModelCount = 3
const modelsExpanded = ref(false)
const modelsListId = `channel-models-${useId()}`
const visibleModels = computed(() => modelsExpanded.value ? props.models : props.models.slice(0, collapsedModelCount))
watch(() => props.models.length, count => { if (count <= collapsedModelCount) modelsExpanded.value = false })
const successClass = { healthy: 'text-emerald-600 dark:text-emerald-400', warning: 'text-amber-600 dark:text-amber-400', critical: 'text-rose-500 dark:text-rose-400', unknown: 'text-gray-400' }
function compactTps(value?: number | null) { return value == null || !Number.isFinite(value) || value < 0 ? '—' : formatMonitorRate(value) }
function latencyDetail(row: MonitorMatrixRow) { const m = row.metrics.ttft; return formatLatencyPrivacy(m.p50_ms, m.p90_ms, m.avg_ms, m.p95_ms) }
</script>

<style scoped>
.channel-card-metrics { container-type: inline-size; }
.metrics-layout { display: grid; gap: 1rem; }
.model-metrics-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.5rem; }
@container (max-width: 360px) {
  .model-metrics-grid { grid-template-columns: minmax(0, 1fr); }
}
</style>
