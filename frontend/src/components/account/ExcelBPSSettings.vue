<template>
  <section class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600" data-testid="excel-bps-settings">
    <label class="flex items-center justify-between gap-3 text-sm font-semibold">
      {{ t(`${prefix}.excelBPS`) }}
      <input type="checkbox" :checked="modelValue.enabled" :disabled="!modelValue.enabled && (!globallyEnabled || freePlan)" data-testid="excel-bps-toggle" @change="patch('enabled', checked($event))" />
    </label>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(`${prefix}.excelBPSDesc`) }}</p>
    <p v-if="!globallyEnabled" class="text-xs text-amber-700 dark:text-amber-400">{{ text('系统已关闭 Excel / BPS 协议。', 'Excel / BPS is disabled in system settings.') }}</p>
    <p v-if="freePlan" class="text-xs text-amber-700 dark:text-amber-400">{{ text('Free 套餐不能开启 Excel / BPS。', 'Free accounts cannot enable Excel / BPS.') }}</p>
    <template v-if="modelValue.enabled">
      <label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="modelValue.allModels" data-testid="excel-bps-all-models" @change="patch('allModels', checked($event))" />{{ t(`${prefix}.excelBPSAllModels`) }}</label>
      <div v-if="!modelValue.allModels" class="space-y-2" data-testid="excel-bps-model-selection">
        <label class="input-label">{{ t(`${prefix}.excelBPSModels`) }}</label>
        <ModelWhitelistSelector :model-value="modelValue.models" platform="openai" @update:model-value="patch('models', $event)" />
        <button type="button" class="btn btn-secondary btn-sm" @click="patch('models', ['gpt-6-astra'])">{{ t(`${prefix}.excelBPSAstraOnly`) }}</button>
        <p class="input-hint">{{ t(`${prefix}.excelBPSModelsHint`) }}</p>
      </div>
      <p class="text-xs text-amber-700 dark:text-amber-400">{{ t(`${prefix}.excelBPSNotice`) }}</p>
      <div v-for="item in flags" :key="item.key" class="space-y-1">
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="modelValue[item.key]" :data-testid="item.testId" @change="patch(item.key, checked($event))" />{{ t(`${prefix}.${item.label}`) }}</label>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(`${prefix}.${item.label}Desc`) }}</p>
      </div>
      <div v-if="modelValue.sessionProxy" class="space-y-2">
        <label class="input-label">{{ t(`${prefix}.excelBPSProxySource`) }}</label>
        <select :value="modelValue.proxySource" class="input" data-testid="excel-bps-proxy-source" @change="patch('proxySource', ($event.target as HTMLSelectElement).value as 'mihomo' | 'ip_pool')">
          <option value="mihomo">{{ t(`${prefix}.excelBPSProxySourceMihomo`) }}</option>
          <option value="ip_pool">{{ t(`${prefix}.excelBPSProxySourceIPPool`) }}</option>
        </select>
        <p v-if="modelValue.proxySource === 'ip_pool'" class="input-hint">{{ t(`${prefix}.excelBPSProxySourceIPPoolDesc`) }}</p>
      </div>
      <div v-if="modelValue.autoMoveOn403" class="space-y-2">
        <label class="input-label">{{ t(`${prefix}.excelBPS403TargetGroup`) }}</label>
        <select :value="modelValue.targetGroupId" class="input" data-testid="excel-bps-target-group" required @change="patch('targetGroupId', ($event.target as HTMLSelectElement).value === '' ? '' : Number(($event.target as HTMLSelectElement).value))">
          <option value="">{{ t(`${prefix}.excelBPS403SelectTarget`) }}</option>
          <option :value="0">{{ t(`${prefix}.excelBPS403LeaveAllGroups`) }}</option>
          <option v-for="group in groupChoices" :key="group.id" :value="group.id">{{ group.name }}</option>
        </select>
      </div>
    </template>
    <div v-if="modelValue.enabled || modelValue.recoveryPending" class="space-y-2">
      <label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="modelValue.autoRecoverOn403" :disabled="!modelValue.autoDisableOn403" data-testid="excel-bps-auto-recover-on-403" @change="patch('autoRecoverOn403', checked($event))" />{{ t(`${prefix}.excelBPSAutoRecoverOn403`) }}</label>
      <p class="input-hint">{{ t(`${prefix}.excelBPSAutoRecoverOn403Desc`) }}</p>
      <label v-if="modelValue.autoRecoverOn403 && modelValue.autoDisableOn403" class="block space-y-1">
        <span class="text-sm">{{ t(`${prefix}.excelBPS403RecoveryInterval`) }}</span>
        <input type="number" :value="modelValue.recoveryIntervalMinutes" min="1" max="10080" step="1" required class="input w-40" data-testid="excel-bps-recovery-interval" @input="patch('recoveryIntervalMinutes', Number(($event.target as HTMLInputElement).value))" />
        <p class="input-hint">{{ t(`${prefix}.excelBPS403RecoveryIntervalHint`) }}</p>
      </label>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import ModelWhitelistSelector from './ModelWhitelistSelector.vue'
import type { ExcelBPSConfig } from '@/utils/excelBps'

const props = withDefaults(defineProps<{ modelValue: ExcelBPSConfig; groups?: { id: number; name: string; platform?: string }[]; allowComposite?: boolean; planType?: string }>(), { allowComposite: true })
const emit = defineEmits<{ 'update:modelValue': [value: ExcelBPSConfig] }>()
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const app = useAppStore()
const globallyEnabled = computed(() => app.cachedPublicSettings?.excel_bps_enabled !== false)
const freePlan = computed(() => props.planType?.trim().toLowerCase() === 'free')
const prefix = 'admin.accounts.openai'
const groupChoices = computed(() => (props.groups || []).filter(group => group.platform === 'openai' || (props.allowComposite && group.platform === 'composite')))
const checked = (event: Event) => (event.target as HTMLInputElement).checked
function patch<K extends keyof ExcelBPSConfig>(key: K, value: ExcelBPSConfig[K]) {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}
const flags = [
  { key: 'omitUnsupportedTools', label: 'excelBPSOmitUnsupportedTools', testId: 'excel-bps-omit-unsupported-tools' },
  { key: 'ignoreImages', label: 'excelBPSIgnoreImages', testId: 'excel-bps-ignore-images' },
  { key: 'ignoreEncryptedContent', label: 'excelBPSIgnoreEncryptedContent', testId: 'excel-bps-ignore-encrypted-content' },
  { key: 'autoDisableOn403', label: 'excelBPSAutoDisableOn403', testId: 'excel-bps-auto-disable-on-403' },
  { key: 'autoMoveOn403', label: 'excelBPSAutoMoveOn403', testId: 'excel-bps-auto-move-on-403' },
  { key: 'sessionProxy', label: 'excelBPSMihomo', testId: 'excel-bps-mihomo' },
  { key: 'cacheCreationAsInput', label: 'excelBPSCacheCreationAsInput', testId: 'excel-bps-cache-creation-as-input' }
] as const
</script>
