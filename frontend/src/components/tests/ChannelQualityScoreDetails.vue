<template>
  <div v-if="score" data-score-details class="space-y-2">
    <p>{{ score.models > 0 ? t('admin.tests.quality.qualityScoreSamples', { samples: score.samples, models: score.models }) : t('admin.tests.quality.qualityScoreAccountSamples', { samples: score.samples }) }}</p>
    <p>{{ tr('status.' + score.status) }}</p>
    <p>{{ tr('window', { minutes: Number((score.window_seconds / 60).toFixed(2)), max: score.max_requests, min: score.min_samples }) }}</p>
    <p>{{ score.model ? tr('model', { model: score.model }) : tr('aggregate') }}</p>
    <p :title="tr('confidenceHint')">{{ tr('coverage', { value: Math.round(score.coverage_percent) }) }} · {{ tr('confidence', { value: Math.round(score.confidence * 100) }) }}</p>
    <p>{{ tr('updated', { date: date(score.latest_at) }) }}</p>
    <p v-if="score.status === 'partial'" class="text-amber-700 dark:text-amber-300">{{ tr('partialHint') }}</p>
    <p v-if="score.status === 'incomplete'" class="text-amber-700 dark:text-amber-300">{{ tr('incompleteHint') }}</p>
    <div v-if="score.dimensions?.length" class="overflow-x-auto">
      <table class="w-full min-w-[30rem] text-left">
        <thead>
          <tr>
            <th class="p-2">{{ tr('dimension') }}</th>
            <th class="p-2">{{ tr('observed') }}</th>
            <th class="p-2">{{ tr('dimensionScore') }}</th>
            <th class="p-2">{{ tr('samples') }}</th>
            <th class="p-2">{{ tr('weight') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="dimension in score.dimensions" :key="dimension.key" :data-score-dimension="dimension.key" class="border-t border-gray-200 dark:border-dark-700">
            <th scope="row" class="p-2 font-normal">
              {{ tr('dimensions.' + dimension.key) }}
              <span v-if="dimension.status !== 'ready'" class="mt-1 block text-amber-700 dark:text-amber-300">
                {{ dimension.reason && te('admin.tests.scoring.reasons.' + dimension.reason) ? tr('reasons.' + dimension.reason) : tr('status.' + dimension.status) }}
              </span>
            </th>
            <td class="p-2">{{ metric(dimension.value, dimension.unit === 'percent' ? '%' : dimension.unit === 'token/s' ? ' token/s' : ' ms') }}</td>
            <td class="p-2">{{ dimension.status === 'ready' ? metric(dimension.score) : '—' }}</td>
            <td class="p-2">{{ dimension.samples }}</td>
            <td class="p-2">{{ dimension.weight }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
  <p v-else data-score-details>{{ tr('noScore') }}</p>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ChannelQualityScore } from '@/api/admin/channelQuality'
import { useQualityLabels } from '@/composables/useQualityLabels'

defineProps<{ score?: ChannelQualityScore }>()

const { t, te } = useI18n()
const { date } = useQualityLabels()
const tr = (key: string, values: Record<string, unknown> = {}) => t(`admin.tests.scoring.${key}`, values)
const metric = (number: number | null | undefined, unit = '') => number == null || !Number.isFinite(number) ? '—' : `${Math.round(number * 100) / 100}${unit}`
</script>
