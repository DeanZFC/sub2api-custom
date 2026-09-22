<template>
  <section class="space-y-3 rounded-lg border border-blue-200 bg-blue-50/40 p-3 dark:border-blue-900 dark:bg-blue-950/10" data-cache-recovery>
    <label class="flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
      <input :checked="recovery.enabled" type="checkbox" :disabled="!eligible && !recovery.enabled" data-recovery-enabled @change="patch({ enabled: ($event.target as HTMLInputElement).checked })" />
      {{ t('admin.tests.protection.recovery.title') }}
    </label>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.recovery.description') }}</p>
    <p v-if="!eligible" class="text-xs text-amber-700 dark:text-amber-300" data-recovery-eligibility>{{ t('admin.tests.protection.recovery.eligibility') }}</p>
    <fieldset v-if="recovery.enabled" :disabled="!eligible" class="space-y-3">
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="input-label block">{{ t('admin.tests.protection.recovery.cooldown') }}<input :value="recovery.cooldown_seconds" type="number" min="60" max="86400" step="1" class="input mt-1 w-full" data-recovery-cooldown @input="patch({ cooldown_seconds: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
        <label class="input-label block">{{ t('admin.tests.protection.recovery.trial') }}<input :value="recovery.trial_seconds" type="number" min="60" max="3600" step="1" class="input mt-1 w-full" data-recovery-trial @input="patch({ trial_seconds: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
        <label class="input-label block">{{ t('admin.tests.protection.recovery.maxRequests') }}<input :value="recovery.max_requests" type="number" min="1" max="1000" step="1" class="input mt-1 w-full" data-recovery-max-requests @input="patch({ max_requests: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
        <label class="input-label block">{{ t('admin.tests.protection.recovery.minSamples') }}<input :value="recovery.min_samples" type="number" min="1" :max="recovery.max_requests" step="1" class="input mt-1 w-full" data-recovery-min-samples @input="patch({ min_samples: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
        <label class="input-label block">{{ t('admin.tests.protection.recovery.recoverRate') }}<input :value="recovery.recover_rate" type="number" :min="cacheRecoveryThreshold(rule)" max="100" step="any" class="input mt-1 w-full" data-recovery-rate @input="patch({ recover_rate: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
      </div>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.recovery.cooldownHint') }}</p>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.recovery.trialHint', { count: recovery.max_requests }) }}</p>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.recovery.samplesHint') }}</p>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.recovery.retryHint') }}</p>
    </fieldset>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestProtectionRecovery, TestProtectionRule } from '@/types'
import { cacheRecoveryThreshold, canEnableCacheRecovery, defaultTestProtectionRecovery } from '@/utils/testProtection'

const props = defineProps<{ rule: TestProtectionRule; outputKind: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: TestProtectionRecovery] }>()
const { t } = useI18n()
const recovery = computed(() => props.rule.recovery || defaultTestProtectionRecovery(props.rule))
const eligible = computed(() => canEnableCacheRecovery(props.rule, props.outputKind))
const patch = (value: Partial<TestProtectionRecovery>) => emit('update:modelValue', { ...recovery.value, ...value })
</script>
