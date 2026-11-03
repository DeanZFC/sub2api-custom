<template>
  <section class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-600" data-testid="excel-bps-settings">
    <label class="flex items-center justify-between gap-3 text-sm font-semibold">
      {{ t(`${prefix}.excelBPS`) }}
      <input type="checkbox" :checked="modelValue.enabled" data-testid="excel-bps-toggle" @change="patch('enabled', checked($event))" />
    </label>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(`${prefix}.excelBPSDesc`) }}</p>
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
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import ModelWhitelistSelector from './ModelWhitelistSelector.vue'
import type { ExcelBPSConfig } from '@/utils/excelBps'

const props = withDefaults(defineProps<{ modelValue: ExcelBPSConfig; groups?: { id: number; name: string; platform?: string }[]; allowComposite?: boolean }>(), { allowComposite: true })
const emit = defineEmits<{ 'update:modelValue': [value: ExcelBPSConfig] }>()
const { t } = useI18n()
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
