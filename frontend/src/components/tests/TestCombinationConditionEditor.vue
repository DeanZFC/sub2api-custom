<template>
  <div class="min-w-0 space-y-3 rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900" data-combination-condition :data-condition-depth="depth">
    <div v-if="modelValue.operator === 'test'" class="flex flex-wrap items-center gap-2">
      <select :value="modelValue.test_definition_id ?? ''" class="input min-w-0 flex-1" :aria-label="t('admin.tests.protection.combined.test')" data-condition-test @change="patch({ test_definition_id: Number(($event.target as HTMLSelectElement).value) })">
        <option value="" disabled>{{ t('admin.tests.protection.combined.chooseTest') }}</option>
        <option v-if="missingReference" :value="modelValue.test_definition_id" disabled>{{ t('admin.tests.protection.combined.unavailableTest', { id: modelValue.test_definition_id }) }}</option>
        <option v-for="type in types" :key="type.id" :value="type.id">{{ type.name }}</option>
      </select>
      <select :value="modelValue.verdict || 'pass'" class="input w-32" :aria-label="t('admin.tests.protection.combined.verdict')" data-condition-verdict @change="patch({ verdict: ($event.target as HTMLSelectElement).value as 'pass' | 'fail' })">
        <option value="pass">{{ t('admin.tests.protection.combined.pass') }}</option>
        <option value="fail">{{ t('admin.tests.protection.combined.fail') }}</option>
      </select>
      <button v-if="removable" type="button" class="btn btn-secondary btn-sm" :aria-label="t('admin.tests.protection.combined.removeCondition')" data-remove-condition @click="emit('remove')">{{ t('common.delete') }}</button>
    </div>
    <template v-else>
      <div class="flex flex-wrap items-center gap-2">
        <select :value="modelValue.operator" class="input w-full sm:w-auto" :aria-label="t('admin.tests.protection.combined.operator')" data-condition-operator @change="patch({ operator: ($event.target as HTMLSelectElement).value as 'all' | 'any' })">
          <option value="all">{{ t('admin.tests.protection.combined.all') }}</option>
          <option value="any">{{ t('admin.tests.protection.combined.any') }}</option>
        </select>
        <button v-if="removable" type="button" class="btn btn-secondary btn-sm sm:ml-auto" :aria-label="t('admin.tests.protection.combined.removeGroup')" data-remove-condition @click="emit('remove')">{{ t('common.delete') }}</button>
      </div>
      <div class="space-y-2 border-l-2 border-primary-100 pl-2 dark:border-primary-900 sm:pl-3">
        <TestCombinationConditionEditor v-for="(child, index) in modelValue.conditions || []" :key="index" :model-value="child" :types="types" :depth="depth + 1" :remaining-nodes="remainingNodes" removable @update:model-value="updateChild(index, $event)" @remove="removeChild(index)" />
      </div>
      <p v-if="!modelValue.conditions?.length" class="text-xs text-red-600 dark:text-red-400" role="alert">{{ t('admin.tests.protection.combined.emptyGroup') }}</p>
      <div class="flex flex-wrap gap-3 text-xs font-medium text-primary-600 dark:text-primary-400">
        <button type="button" :disabled="!types.length || depth >= 6 || remainingNodes < 1" class="disabled:opacity-40" data-add-condition @click="addCondition(false)">+ {{ t('admin.tests.protection.combined.addCondition') }}</button>
        <button type="button" :disabled="!types.length || depth >= 5 || remainingNodes < 2" class="disabled:opacity-40" data-add-condition-group @click="addCondition(true)">+ {{ t('admin.tests.protection.combined.addGroup') }}</button>
      </div>
    </template>
    <p v-if="missingReference" class="text-xs text-red-600 dark:text-red-400" role="alert" data-missing-condition-test>{{ t('admin.tests.protection.combined.missingReference') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TestCombinationCondition, TestType } from '@/types'

const props = withDefaults(defineProps<{ modelValue: TestCombinationCondition; types: TestType[]; depth?: number; remainingNodes: number; removable?: boolean }>(), { depth: 1, removable: false })
const emit = defineEmits<{ 'update:modelValue': [value: TestCombinationCondition]; remove: [] }>()
const { t } = useI18n()
const missingReference = computed(() => props.modelValue.operator === 'test' && props.modelValue.test_definition_id != null && !props.types.some(type => type.id === props.modelValue.test_definition_id))
const patch = (patch: Partial<TestCombinationCondition>) => emit('update:modelValue', { ...props.modelValue, ...patch })
const updateChild = (index: number, condition: TestCombinationCondition) => patch({ conditions: props.modelValue.conditions?.map((child, i) => i === index ? condition : child) })
const removeChild = (index: number) => patch({ conditions: props.modelValue.conditions?.filter((_, i) => i !== index) })
const addCondition = (group: boolean) => {
  if (!props.types.length || props.remainingNodes < (group ? 2 : 1) || props.depth >= (group ? 5 : 6)) return
  const leaf: TestCombinationCondition = { operator: 'test', test_definition_id: props.types[0].id, verdict: 'pass' }
  patch({ conditions: [...(props.modelValue.conditions || []), group ? { operator: 'all', conditions: [leaf] } : leaf] })
}
</script>
