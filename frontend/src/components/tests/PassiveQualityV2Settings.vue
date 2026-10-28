<template>
  <section class="space-y-4 rounded-xl border border-primary-200 p-4 dark:border-primary-900" data-passive-v2-settings>
    <h3 class="font-semibold">{{ v('title') }}</h3>
    <p class="text-xs leading-5 text-gray-500">{{ v('independentHint') }}</p>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="input-label">{{ p('mode') }}<select v-model="policy.mode" class="input mt-1 w-full" data-v2-mode><option value="observe">{{ p('observe') }}</option><option value="enforce">{{ p('enforce') }}</option></select></label>
      <label class="input-label">{{ v('availabilityAction') }}<select v-model="policy.action" class="input mt-1 w-full" data-v2-availability-action><option value="pause">{{ p('actionPause') }}</option><option value="limit">{{ p('actionLimit') }}</option></select></label>
      <label v-if="policy.ttft.enabled" class="input-label">{{ v('latencyAction') }}<select v-model="policy.ttft_action" class="input mt-1 w-full"><option value="observe">{{ p('observe') }}</option><option value="limit">{{ p('actionLimit') }}</option></select></label>
      <label v-if="policy.cache_rate.enabled && policy.cache_protection" class="input-label">{{ v('cacheAction') }}<select v-model="policy.cache_protection.action" class="input mt-1 w-full" data-cache-action><option value="observe">{{ p('observe') }}</option><option value="reduce">{{ v('reduce') }}</option></select></label>
    </div>
    <div v-if="policy.ttft.enabled && policy.ttft_action === 'limit'" class="grid gap-3 sm:grid-cols-2">
      <label class="input-label">{{ v('latencyConcurrency') }}<input v-model.number="policy.ttft_concurrency_limit" type="number" min="1" max="1000" class="input mt-1 w-full" /></label>
      <label class="input-label">{{ v('latencyPenalty') }}<input v-model.number="policy.ttft_priority_penalty" type="number" min="0" max="1000" class="input mt-1 w-full" /></label>
    </div>
    <div v-if="policy.tps.enabled" class="grid gap-3 sm:grid-cols-3" data-speed-settings>
      <label class="input-label">{{ v('speedAction') }}<select v-model="policy.tps_action" class="input mt-1 w-full"><option value="observe">{{ p('observe') }}</option><option value="limit">{{ p('actionLimit') }}</option></select></label>
      <label v-if="policy.tps_action === 'limit'" class="input-label">{{ v('speedConcurrency') }}<input v-model.number="policy.tps_concurrency_limit" type="number" min="1" max="1000" class="input mt-1 w-full" /></label>
      <label v-if="policy.tps_action === 'limit'" class="input-label">{{ v('speedPenalty') }}<input v-model.number="policy.tps_priority_penalty" type="number" min="0" max="1000" class="input mt-1 w-full" /></label>
    </div>
    <template v-if="policy.cache_rate.enabled && policy.cache_protection">
      <p class="rounded-lg bg-primary-50 p-3 text-xs leading-5 dark:bg-primary-950/30">{{ v('cacheHint') }}</p>
      <fieldset v-for="kind in windowKinds" :key="kind" class="space-y-3" :data-cache-window="kind">
        <legend class="mb-2 text-sm font-semibold">{{ v(kind) }}</legend>
        <p class="text-xs text-gray-500">{{ v(kind + 'Hint') }}</p>
        <div class="grid gap-3 sm:grid-cols-3">
          <label v-for="field in windowFields" :key="field.key" class="input-label">{{ field.common ? p(field.key) : v(field.key) }}<input v-model.number="policy.cache_protection[kind][field.key]" type="number" :min="field.min" :max="field.max" step="1" class="input mt-1 w-full" :data-cache-field="kind + '.' + field.key" /></label>
        </div>
      </fieldset>
      <fieldset class="space-y-3">
        <legend class="mb-2 text-sm font-semibold">{{ v('recoveryAndGuard') }}</legend>
        <p class="text-xs text-gray-500">{{ v('recoveryHint') }}</p>
        <div class="grid gap-3 sm:grid-cols-3"><label v-for="field in recoveryFields" :key="field.key" class="input-label">{{ v(field.key) }}<input v-model.number="policy.cache_protection[field.key]" type="number" :min="field.min" :max="field.max" step="1" class="input mt-1 w-full" :data-cache-field="field.key" /></label></div>
        <p class="text-xs text-gray-500">{{ v('guardHint') }}</p>
      </fieldset>
    </template>
  </section>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PassivePolicy } from '@/api/admin/passiveQuality'
const policy = defineModel<PassivePolicy>({ required: true })
const { t } = useI18n()
const v = (key: string) => t('admin.tests.passive.v2.' + key)
const p = (key: string) => t('admin.tests.passive.' + key)
const windowKinds = ['quick', 'action_window'] as const
const windowFields = [
  { key: 'window_seconds', min: 60, max: 86400, common: true }, { key: 'max_samples', min: 5, max: 2000, common: true },
  { key: 'min_samples', min: 5, max: 2000, common: true }, { key: 'confirmations', min: 1, max: 10, common: true },
  { key: 'new_samples', min: 1, max: 2000, common: true }, { key: 'min_tokens', min: 1, max: 1000000000, common: false },
  { key: 'min_sessions', min: 2, max: 2000, common: false }, { key: 'max_session_share_percent', min: 10, max: 90, common: false },
] as const
const recoveryFields = [
  { key: 'allocation_percent', min: 1, max: 99 }, { key: 'recovery_step_percent', min: 1, max: 99 }, { key: 'recovery_confirmations', min: 1, max: 10 },
  { key: 'min_hold_seconds', min: 30, max: 86400 }, { key: 'recovery_hold_seconds', min: 30, max: 86400 },
  { key: 'max_reduced_percent', min: 1, max: 90 }, { key: 'min_serving_accounts', min: 1, max: 10000 },
  { key: 'reuse_window_seconds', min: 30, max: 3600 }, { key: 'warmup_requests', min: 1, max: 20 },
] as const
</script>
