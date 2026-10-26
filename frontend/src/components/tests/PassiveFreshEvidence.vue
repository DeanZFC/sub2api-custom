<template>
  <p v-if="hasFreshMetrics" class="text-gray-500" data-fresh-evidence>{{ t('admin.tests.experience.freshMetrics', { errors: metric(evidence.fresh_error_rate, '%'), errorSamples: evidence.fresh, ttft: metric(evidence.fresh_ttft, ' ms'), ttftSamples: evidence.ttft_fresh ?? '—', cache: metric(evidence.fresh_cache_rate, '%'), cacheSamples: evidence.cache_fresh ?? '—' }) }}</p>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PassiveEvidence } from '@/api/admin/passiveQuality'
const props = defineProps<{ evidence: PassiveEvidence }>()
const { t } = useI18n()
const hasFreshMetrics = computed(() => ['fresh_error_rate', 'fresh_ttft', 'fresh_cache_rate'].some(key => key in props.evidence))
const metric = (value: number | null | undefined, unit: string) => value == null ? '—' : `${Math.round(value * 100) / 100}${unit}`
</script>
