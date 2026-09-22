<template>
  <section class="space-y-4" data-combination-rules>
    <div>
      <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.protection.combined.rules') }}</h4>
      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.combined.logicHint') }}</p>
      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.combined.priorityHint') }}</p>
    </div>
    <section v-for="(rule, index) in modelValue" :key="rule.id" class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-700" :data-combination-rule="rule.id">
      <div class="flex flex-wrap items-end gap-3">
        <label class="input-label min-w-0 flex-1">{{ t('admin.tests.protection.combined.name') }}<input :value="rule.name" maxlength="100" class="input mt-1 w-full" data-combination-name @input="patchRule(index, { name: ($event.target as HTMLInputElement).value })" /></label>
        <label class="input-label">{{ t('admin.tests.protection.priority') }}<input :value="rule.priority" type="number" min="0" max="1000" step="1" class="input mt-1 w-28" data-combination-priority @input="patchRule(index, { priority: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
        <button type="button" class="btn btn-secondary btn-sm" :aria-label="t('admin.tests.protection.combined.removeRule')" data-remove-combination @click="emit('update:modelValue', modelValue.filter((_, i) => i !== index))">{{ t('common.delete') }}</button>
      </div>
      <TestCombinationConditionEditor :model-value="rule.condition" :types="types" :remaining-nodes="128 - combinationNodeCount(rule.condition)" @update:model-value="patchRule(index, { condition: $event })" />
      <TestOutcomeActionEditor :model-value="rule.action" outcome="pass" :groups="groups" :title="t('admin.tests.protection.combined.action')" :resume-label="t('admin.tests.protection.combined.resume')" @update:model-value="patchRule(index, { action: $event })" />
      <p v-if="combinationConflictsWithRecovery(rule, recoveryTestIds, modelValue)" class="text-xs text-red-600 dark:text-red-400" role="alert" data-combination-recovery-conflict>{{ t(rule.action.scheduling === 'resume' ? 'admin.tests.protection.combined.recoveryResumeConflict' : 'admin.tests.protection.combined.recoveryConflict') }}</p>
    </section>
    <p v-if="!modelValue.length" class="text-xs text-amber-700 dark:text-amber-300" data-no-combinations>{{ t('admin.tests.protection.combined.emptyRules') }}</p>
    <button type="button" class="btn btn-secondary btn-sm" :disabled="modelValue.length >= 32 || !types.length" data-add-combination @click="addRule">+ {{ t('admin.tests.protection.combined.addRule') }}</button>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.combined.actionHint') }}</p>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { AdminGroup, TestCombinationRule, TestType } from '@/types'
import { combinationConflictsWithRecovery, combinationNodeCount, newTestCombinationID } from '@/utils/testProtection'
import TestCombinationConditionEditor from './TestCombinationConditionEditor.vue'
import TestOutcomeActionEditor from './TestOutcomeActionEditor.vue'

const props = withDefaults(defineProps<{ modelValue: TestCombinationRule[]; types: TestType[]; groups: AdminGroup[]; recoveryTestIds?: number[] }>(), { recoveryTestIds: () => [] })
const emit = defineEmits<{ 'update:modelValue': [value: TestCombinationRule[]] }>()
const { t } = useI18n()
const patchRule = (index: number, patch: Partial<TestCombinationRule>) => emit('update:modelValue', props.modelValue.map((rule, i) => i === index ? { ...rule, ...patch } : rule))
const addRule = () => {
  if (props.modelValue.length >= 32 || !props.types.length) return
  const id = newTestCombinationID()
  emit('update:modelValue', [...props.modelValue, {
    id, name: t('admin.tests.protection.combined.defaultRuleName', { count: props.modelValue.length + 1 }), priority: 0,
    condition: { operator: 'all', conditions: [{ operator: 'test', test_definition_id: props.types[0].id, verdict: 'pass' }] },
    action: { scheduling: 'keep', group_mode: 'keep' },
  }])
}
</script>
