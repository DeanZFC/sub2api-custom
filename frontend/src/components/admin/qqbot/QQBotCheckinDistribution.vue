<template>
  <fieldset class="min-w-0 space-y-3 rounded-lg border p-4 dark:border-dark-700">
    <legend class="font-medium">{{ t('qqbot.checkinDistributionTitle') }}</legend>
    <p class="text-sm text-gray-500">{{ t('qqbot.checkinDistributionHelp') }}</p>
    <div v-for="(band, index) in distribution.bands" :key="index" class="grid items-end gap-3 border-b pb-3 dark:border-dark-700 sm:grid-cols-2 lg:grid-cols-[1fr_1fr_auto]">
      <label class="min-w-0 space-y-1 text-sm">
        <span>{{ t('qqbot.checkinBandRange') }}</span>
        <span class="flex items-center gap-2">
          <span class="whitespace-nowrap tabular-nums">{{ percentText(startBPS(index)) }}% –</span>
          <input
            :value="percentText(band.end_bps)" :data-testid="'band-end-' + index"
            :readonly="index === distribution.bands.length - 1"
            type="number" min="0.01" max="100" step="0.01" required
            class="input min-w-0 read-only:bg-gray-100 dark:read-only:bg-dark-700"
            @input="setPercent(band, 'end_bps', $event)"
          />
          <span>%</span>
        </span>
      </label>
      <label class="min-w-0 space-y-1 text-sm">
        <span>{{ t('qqbot.checkinBandProbability') }}</span>
        <input
          :value="percentText(band.probability_bps)" :data-testid="'band-probability-' + index"
          type="number" min="0" max="100" step="0.01" required class="input"
          @input="setPercent(band, 'probability_bps', $event)"
        />
      </label>
      <div class="flex items-center gap-3 sm:col-span-2 lg:col-span-1 lg:self-end">
        <button
          type="button" class="btn btn-secondary" :data-testid="'split-band-' + index"
          :disabled="!canSplit(band, index)" @click="splitBand(index)"
        >{{ t('qqbot.splitCheckinBand') }}</button>
        <button
          type="button" class="text-sm text-red-600 disabled:opacity-50" :data-testid="'remove-band-' + index"
          :disabled="distribution.bands.length === 1" @click="removeBand(index)"
        >{{ t('common.delete') }}</button>
      </div>
    </div>
    <p class="text-sm" :class="total === 10000 ? 'text-gray-500' : 'text-red-600'" data-testid="probability-total">{{ t('qqbot.checkinDistributionTotal', { total: Number.isFinite(total) ? (total / 100).toFixed(2) : '—' }) }}</p>
  </fieldset>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { QQCheckinRewardBand, QQCheckinRewardDistribution } from '@/api/qqbot'

const distribution = defineModel<QQCheckinRewardDistribution>({ required: true })
const { t } = useI18n()
const total = computed(() => distribution.value.bands.reduce((sum, band) => sum + band.probability_bps, 0))

function startBPS(index: number) {
  return index === 0 ? 0 : distribution.value.bands[index - 1]!.end_bps
}
function percentText(bps: number) {
  return Number.isFinite(bps) ? String(bps / 100) : ''
}
function setPercent(band: QQCheckinRewardBand, field: keyof QQCheckinRewardBand, event: Event) {
  const percent = (event.target as HTMLInputElement).valueAsNumber
  const bps = Math.round(percent * 100)
  // Reject extra decimals on save while keeping them visible for correction.
  band[field] = bps / 100 === percent ? bps : percent * 100
}
function canSplit(band: QQCheckinRewardBand, index: number) {
  const width = band.end_bps - startBPS(index)
  return Number.isInteger(width) && width >= 2 && Number.isInteger(band.probability_bps)
}
function splitBand(index: number) {
  const bands = distribution.value.bands
  const band = bands[index]!
  const start = startBPS(index)
  const width = band.end_bps - start
  const half = Math.floor(width / 2)
  const weight = Math.round(band.probability_bps * half / width)
  bands.splice(index, 0, { end_bps: start + half, probability_bps: weight })
  band.probability_bps -= weight
}
function removeBand(index: number) {
  const bands = distribution.value.bands
  const band = bands[index]!
  const neighbor = bands[index + 1] ?? bands[index - 1]!
  if (index === bands.length - 1) neighbor.end_bps = band.end_bps
  if (Number.isFinite(band.probability_bps)) neighbor.probability_bps += band.probability_bps
  bands.splice(index, 1)
}
</script>
