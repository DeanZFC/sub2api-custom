<template>
  <select :value="modelValue" class="input mb-2" :disabled="disabled" data-testid="account-proxy-mode" :aria-label="t('admin.accounts.proxy')" @change="change">
    <option value="">{{ t('ovhIPv6.manualPool') }}</option>
    <option v-if="allowManaged !== false" value="ovh_ipv6">{{ t('ovhIPv6.mode.ovh_ipv6') }}</option>
    <option value="random">{{ t('ovhIPv6.mode.random') }}</option>
    <option value="least_used">{{ t('ovhIPv6.mode.least_used') }}</option>
    <option value="direct">{{ t('admin.accounts.noProxy') }}</option>
  </select>
  <p v-if="modelValue === 'ovh_ipv6'" class="input-hint mb-2">{{ t('ovhIPv6.allocateOnSave') }} {{ t('ovhIPv6.oauthTiming') }}</p>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { AccountProxyMode } from '@/types'
withDefaults(defineProps<{ modelValue: AccountProxyMode; disabled?: boolean; allowManaged?: boolean }>(), { allowManaged: true })
const emit = defineEmits<{ 'update:modelValue': [mode: AccountProxyMode] }>()
const { t } = useI18n()
function change(event: Event) { emit('update:modelValue', (event.target as HTMLSelectElement).value as AccountProxyMode) }
</script>
