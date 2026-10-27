import type { ChannelQualityScore, QualityScoreConfig, QualityScoreDimensionKey } from '@/api/admin/channelQuality'

export const qualityScoreDimensions: QualityScoreDimensionKey[] = ['error_rate', 'cache_rate', 'ttft', 'duration']
export const qualityScoreWindowFields = [
  { key: 'window_seconds', min: 60, max: 86400 },
  { key: 'max_requests', min: 5, max: 1000 },
  { key: 'min_samples', min: 5, max: 1000 },
] as const

export function defaultQualityScoreConfig(version = 0): QualityScoreConfig {
  return {
    version, enabled: true, window_seconds: 900, max_requests: 100, min_samples: 20,
    error_rate: { weight: 40, good: 0, bad: 20 }, cache_rate: { weight: 25, good: 80, bad: 0 },
    ttft: { weight: 25, good: 2000, bad: 15000 }, duration: { weight: 10, good: 10000, bad: 120000 },
  }
}

export function qualityScoreConfigError(config: QualityScoreConfig): { key: string; dimension?: QualityScoreDimensionKey } | null {
  if (qualityScoreWindowFields.some(field => !Number.isInteger(config[field.key]) || config[field.key] < field.min || config[field.key] > field.max)) return { key: 'invalidWindow' }
  if (config.min_samples > config.max_requests) return { key: 'invalidSamples' }
  if (qualityScoreDimensions.some(key => !Number.isFinite(config[key].weight) || config[key].weight < 0 || config[key].weight > 100) || qualityScoreDimensions.every(key => config[key].weight === 0)) return { key: 'invalidWeights' }
  for (const key of qualityScoreDimensions) {
    const metric = config[key], maximum = ['error_rate', 'cache_rate'].includes(key) ? 100 : 3600000
    if (![metric.good, metric.bad].every(value => Number.isFinite(value) && value >= 0 && value <= maximum) || (key === 'cache_rate' ? metric.good <= metric.bad : metric.good >= metric.bad)) return { key: 'invalidStandard', dimension: key }
  }
  return null
}

export function qualityScoreValue(score?: ChannelQualityScore): number | null {
  return score && (score.status === 'ready' || score.status === 'partial' || !score.status) && score.value != null && Number.isFinite(score.value) ? score.value : null
}

export function qualityScoreClass(score: number | null | undefined): string {
  if (score == null || !Number.isFinite(score)) return 'text-gray-400 dark:text-dark-500'
  if (score >= 80) return 'text-green-700 dark:text-green-400'
  if (score >= 50) return 'text-amber-700 dark:text-amber-400'
  return 'text-red-700 dark:text-red-400'
}
