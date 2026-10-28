<template>
  <div data-quality-score :title="t('admin.tests.quality.qualityScoreHint')">
    <div class="flex flex-wrap items-center gap-2">
      <span class="text-xs text-gray-500">{{ t('admin.tests.quality.qualityScore') }}</span>
      <strong class="font-mono text-lg" :class="qualityScoreClass(value)">{{ value == null ? '—' : Math.round(value) }}</strong>
      <span v-if="!compact" class="text-xs text-gray-500">{{ score ? t('admin.tests.quality.' + (score.models > 0 ? 'qualityScoreSamples' : 'qualityScoreAccountSamples'), { samples: score.samples, models: score.models }) : tr('noScore') }}</span>
      <span v-if="score?.status && score.status !== 'ready'" class="text-xs text-amber-700 dark:text-amber-300" data-score-status>{{ tr('status.' + score.status) }}</span>
    </div>
    <details v-if="!compact && score?.dimensions?.length" class="mt-2 text-xs" data-score-evidence>
      <summary class="cursor-pointer text-gray-500">{{ tr('details') }} · {{ tr('coverage', { value: Math.round(score.coverage_percent) }) }}</summary>
      <div class="mt-3 space-y-2">
        <p>{{ tr('window', { minutes: Number((score.window_seconds / 60).toFixed(2)), max: score.max_requests, min: score.min_samples }) }}</p>
        <p>{{ score.model ? tr('model', { model: score.model }) : tr('aggregate') }}</p>
        <p :title="tr('confidenceHint')">{{ tr('confidence', { value: Math.round(score.confidence * 100) }) }} · {{ tr('updated', { date: date(score.latest_at) }) }}</p>
        <p v-if="score.status === 'partial'" class="text-amber-700 dark:text-amber-300">{{ tr('partialHint') }}</p>
        <p v-if="score.status === 'incomplete'" class="text-amber-700 dark:text-amber-300">{{ tr('incompleteHint') }}</p>
        <div class="overflow-x-auto"><table class="w-full text-left"><thead><tr><th class="p-2">{{ tr('dimension') }}</th><th class="p-2">{{ tr('observed') }}</th><th class="p-2">{{ tr('dimensionScore') }}</th><th class="p-2">{{ tr('samples') }}</th><th class="p-2">{{ tr('weight') }}</th></tr></thead><tbody>
          <tr v-for="dimension in score.dimensions" :key="dimension.key" :data-score-dimension="dimension.key" class="border-t dark:border-dark-700"><th scope="row" class="p-2 font-normal">{{ tr('dimensions.' + dimension.key) }}<span v-if="dimension.status !== 'ready'" class="mt-1 block text-amber-700 dark:text-amber-300">{{ dimension.reason && te('admin.tests.scoring.reasons.' + dimension.reason) ? tr('reasons.' + dimension.reason) : tr('status.' + dimension.status) }}</span></th><td class="p-2">{{ metric(dimension.value, dimension.unit === 'percent' ? '%' : dimension.unit === 'token/s' ? ' token/s' : ' ms') }}</td><td class="p-2">{{ dimension.status === 'ready' ? metric(dimension.score) : '—' }}</td><td class="p-2">{{ dimension.samples }}</td><td class="p-2">{{ dimension.weight }}</td></tr>
        </tbody></table></div>
      </div>
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ChannelQualityScore } from '@/api/admin/channelQuality'
import { qualityScoreClass, qualityScoreValue } from '@/utils/channelQualityScore'
import { useQualityLabels } from '@/composables/useQualityLabels'
const props = defineProps<{ score?: ChannelQualityScore; compact?: boolean }>()
const { t, te } = useI18n(), { date } = useQualityLabels()
const tr = (key: string, values: Record<string, unknown> = {}) => t(`admin.tests.scoring.${key}`, values)
const value = computed(() => qualityScoreValue(props.score))
const metric = (number: number | null | undefined, unit = '') => number == null || !Number.isFinite(number) ? '—' : `${Math.round(number * 100) / 100}${unit}`
</script>
