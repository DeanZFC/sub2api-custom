<template>
  <div class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600">
    <div class="flex items-center justify-between gap-4">
      <div>
        <label class="input-label mb-0">{{ t('admin.accounts.upstreamBilling.autoProbe') }}</label>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.upstreamBilling.autoProbeHint') }}
        </p>
      </div>
      <Toggle
        :model-value="autoProbeEnabled || hasLimit"
        :disabled="hasLimit"
        :data-testid="toggleTestId"
        :aria-label="t('admin.accounts.upstreamBilling.autoProbe')"
        :title="hasLimit ? t('admin.accounts.upstreamBilling.rateLimitLocksProbe') : undefined"
        class="disabled:cursor-not-allowed disabled:opacity-60"
        @update:model-value="updateProbe"
      />
    </div>
    <label class="block">
      <span class="input-label">{{ t('admin.accounts.upstreamBilling.rateLimit') }}</span>
      <input
        :value="rateLimit ?? ''"
        type="number"
        min="0"
        step="any"
        class="input"
        data-testid="upstream-billing-rate-limit"
        :placeholder="t('admin.accounts.upstreamBilling.rateLimitPlaceholder')"
        @input="updateLimit"
      />
      <span class="input-hint block">{{ t('admin.accounts.upstreamBilling.rateLimitHint') }}</span>
    </label>
  </div>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'

const props = withDefaults(defineProps<{
  autoProbeEnabled: boolean
  rateLimit: number | null
  toggleTestId?: string
}>(), {
  toggleTestId: 'upstream-billing-auto-probe'
})

const emit = defineEmits<{
  (event: 'update:autoProbeEnabled', value: boolean): void
  (event: 'update:rateLimit', value: number | null): void
}>()
const { t } = useI18n()
const hasLimit = computed(() => props.rateLimit != null)

watch(() => props.rateLimit, (value) => {
  if (value != null && !props.autoProbeEnabled) emit('update:autoProbeEnabled', true)
}, { immediate: true })

const updateProbe = (value: boolean) => {
  emit('update:autoProbeEnabled', hasLimit.value || value)
}

const updateLimit = (event: Event) => {
  const input = event.target as HTMLInputElement
  emit('update:rateLimit', input.value === '' ? null : input.valueAsNumber)
}
</script>
