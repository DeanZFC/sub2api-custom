<template>
  <fieldset class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700" data-protection-editor>
    <label class="flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
      <input :checked="modelValue.enabled" type="checkbox" data-protection-enabled @change="setEnabled(($event.target as HTMLInputElement).checked)" />
      {{ t('admin.tests.protection.title') }}
    </label>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(combined ? 'admin.tests.protection.combined.description' : 'admin.tests.protection.description') }}</p>
    <div v-if="modelValue.enabled" class="space-y-4">
      <label class="input-label block">{{ t('admin.tests.protection.combined.mode') }}
        <select :value="modelValue.mode || 'per_test'" class="input mt-1 w-full" data-protection-mode @change="setMode(($event.target as HTMLSelectElement).value as 'per_test' | 'combined')">
          <option value="per_test">{{ t('admin.tests.protection.combined.perTest') }}</option>
          <option value="combined">{{ t('admin.tests.protection.combined.title') }}</option>
        </select>
      </label>
      <p v-if="combined" class="rounded-lg bg-blue-50 p-3 text-xs text-blue-800 dark:bg-blue-950/30 dark:text-blue-200" data-combined-mode-hint>{{ t('admin.tests.protection.combined.modeHint') }}</p>
      <section v-for="type in types" :key="type.id" class="space-y-3 rounded-md bg-gray-50 p-3 dark:bg-dark-800" :data-protection-type="type.id">
        <label class="flex items-center gap-2 text-sm font-medium text-gray-900 dark:text-white">
          <input :checked="!!ruleFor(type.id)" type="checkbox" :disabled="!type.enabled && !ruleFor(type.id)" data-rule-enabled @change="toggleRule(type, ($event.target as HTMLInputElement).checked)" />{{ type.name }}
        </label>
        <p v-if="!type.enabled" class="text-xs text-amber-700 dark:text-amber-300" data-disabled-type-hint>{{ t('admin.tests.protection.disabledType') }}</p>
        <fieldset v-if="ruleFor(type.id)" :disabled="!type.enabled" class="space-y-3">
          <template v-if="!combined">
            <label class="input-label block">{{ t('admin.tests.protection.priority') }}<input :value="ruleFor(type.id)?.priority ?? 0" type="number" min="0" max="1000" step="1" class="input mt-1 w-32" data-rule-priority @input="patchRule(type.id, { priority: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.priorityHint') }}</p>
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"><input :checked="ruleFor(type.id)?.required_pass || false" :disabled="ruleFor(type.id)?.vote?.enabled" type="checkbox" data-rule-required-pass @change="patchRule(type.id, { required_pass: ($event.target as HTMLInputElement).checked })" />{{ t('admin.tests.protection.requiredPass') }}</label>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.requiredPassHint') }}</p>
          </template>
          <h4 class="text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.conditions') }}</h4>
          <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"><input :checked="ruleFor(type.id)?.pause_on_failure || !!ruleFor(type.id)?.on_fail" :disabled="!!ruleFor(type.id)?.on_fail" type="checkbox" data-rule-failure @change="patchRule(type.id, { pause_on_failure: ($event.target as HTMLInputElement).checked })" />{{ t('admin.tests.protection.pauseOnFailure') }}</label>
          <p v-if="ruleFor(type.id)?.on_fail" class="text-xs text-gray-500 dark:text-gray-400" data-rule-failure-action-hint>{{ t(combined ? 'admin.tests.protection.combined.failureActionHint' : 'admin.tests.protection.failureActionHint') }}</p>
          <label v-if="type.output_kind === 'statistics'" class="input-label block">{{ t('admin.tests.protection.minSamples') }}<input :value="ruleFor(type.id)?.min_samples ?? 1" type="number" min="0" max="1000000000" step="1" class="input mt-1 w-32" data-rule-samples @input="patchRule(type.id, { min_samples: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
          <template v-if="type.output_kind === 'model_check'">
            <label class="input-label block">{{ t('tests.modelCheck.matchMode') }}<select :value="ruleFor(type.id)?.model_match || 'exact'" class="input mt-1 w-full" data-rule-model-match @change="patchRule(type.id, { model_match: ($event.target as HTMLSelectElement).value as TestProtectionRule['model_match'] })"><option value="exact">{{ t('tests.modelCheck.exact') }}</option><option value="snapshot">{{ t('tests.modelCheck.snapshot') }}</option></select></label>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.modelMatchHint') }}</p>
          </template>
          <div v-for="(threshold, index) in ruleFor(type.id)?.thresholds || []" :key="index" class="flex flex-wrap items-center gap-2" data-threshold>
            <select :value="threshold.metric" class="input flex-1" :aria-label="t('admin.tests.protection.metric')" @change="patchThreshold(type.id, index, { metric: ($event.target as HTMLSelectElement).value as TestProtectionMetric })"><option v-for="metric in protectionMetrics(type.output_kind)" :key="metric" :value="metric">{{ t(`admin.tests.protection.metrics.${metric}`) }}</option></select>
            <select :value="threshold.operator" class="input w-28" :aria-label="t('admin.tests.protection.operator')" @change="patchThreshold(type.id, index, { operator: ($event.target as HTMLSelectElement).value as 'lt' | 'gt' })"><option value="lt">{{ t('admin.tests.protection.below') }}</option><option value="gt">{{ t('admin.tests.protection.above') }}</option></select>
            <input :value="threshold.value" type="number" step="any" :min="threshold.metric === 'output_numeric' ? undefined : 0" :max="threshold.metric.endsWith('_rate') ? 100 : undefined" class="input w-28" :aria-label="t('admin.tests.protection.threshold')" @input="patchThreshold(type.id, index, { value: ($event.target as HTMLInputElement).valueAsNumber })" />
            <button type="button" class="btn btn-secondary btn-sm" :aria-label="t('admin.tests.protection.removeThreshold')" @click="removeThreshold(type.id, index)">{{ t('common.delete') }}</button>
          </div>
          <button type="button" class="text-xs font-medium text-primary-600 dark:text-primary-400" :disabled="(ruleFor(type.id)?.thresholds?.length || 0) >= 20" data-add-threshold @click="addThreshold(type)">+ {{ t('admin.tests.protection.addThreshold') }}</button>
          <template v-if="!['statistics', 'model_check'].includes(type.output_kind)">
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"><input :checked="ruleFor(type.id)?.vote?.enabled || false" type="checkbox" data-rule-vote @change="toggleVote(type.id, ($event.target as HTMLInputElement).checked)" />{{ t('admin.tests.protection.vote') }}</label>
            <template v-if="ruleFor(type.id)?.vote?.enabled">
              <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"><input :checked="ruleFor(type.id)?.vote?.public_enabled === true" type="checkbox" data-rule-public-vote @change="patchVote(type.id, { public_enabled: ($event.target as HTMLInputElement).checked })" />{{ t('admin.tests.protection.publicVote') }}</label>
              <p class="text-xs text-gray-500 dark:text-gray-400" data-public-vote-hint>{{ t(ruleFor(type.id)?.vote?.public_enabled ? 'admin.tests.protection.publicVoteHint' : 'admin.tests.protection.adminOnlyHint') }}</p>
            </template>
            <div v-if="ruleFor(type.id)?.vote?.enabled && ruleFor(type.id)?.vote?.public_enabled" class="grid gap-3 sm:grid-cols-2">
              <label class="input-label">{{ t('admin.tests.protection.rejectAbove') }}<input :value="ruleFor(type.id)?.vote?.reject_above" type="number" min="0" max="1000000" step="1" class="input mt-1 w-full" data-vote-reject @input="patchVote(type.id, { reject_above: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
              <label class="input-label">{{ t('admin.tests.protection.passAtLeast') }}<input :value="ruleFor(type.id)?.vote?.pass_at_least" type="number" min="1" max="1000000" step="1" class="input mt-1 w-full" data-vote-pass @input="patchVote(type.id, { pass_at_least: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
              <p class="text-xs text-gray-500 sm:col-span-2">{{ t('admin.tests.protection.voteHint') }}</p>
            </div>
            <label class="input-label block">{{ t(ruleFor(type.id)?.vote?.enabled ? 'admin.tests.protection.referenceAnswer' : 'admin.tests.protection.expectedAnswer') }}<textarea :value="ruleFor(type.id)?.expected_answer || ''" rows="2" maxlength="10000" class="input mt-1 w-full" data-rule-answer @input="patchRule(type.id, { expected_answer: ($event.target as HTMLTextAreaElement).value })" /></label>
            <label v-if="!ruleFor(type.id)?.vote?.enabled" class="input-label block">{{ t('admin.tests.protection.answerMatch') }}<select :value="ruleFor(type.id)?.answer_match || 'exact'" class="input ml-2" data-rule-answer-match @change="patchRule(type.id, { answer_match: ($event.target as HTMLSelectElement).value as TestProtectionRule['answer_match'] })"><option value="exact">{{ t('admin.tests.protection.exact') }}</option><option value="contains">{{ t('admin.tests.protection.contains') }}</option><option value="numeric">{{ t('admin.tests.protection.numeric') }}</option></select></label>
          </template>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.protection.conditionsHint') }}</p>
          <div v-if="!combined" class="grid gap-3 lg:grid-cols-2">
            <TestOutcomeActionEditor :model-value="ruleFor(type.id)?.on_pass" outcome="pass" :groups="groups" @update:model-value="patchRule(type.id, { on_pass: $event })" />
            <TestOutcomeActionEditor :model-value="ruleFor(type.id)?.on_fail" outcome="fail" :groups="groups" @update:model-value="patchRule(type.id, { on_fail: $event })" />
          </div>
          <p v-if="!combined && (ruleFor(type.id)?.on_pass?.group_mode === 'assign' || ruleFor(type.id)?.on_fail?.group_mode === 'assign')" class="text-xs text-gray-500 dark:text-gray-400" data-routing-hint>{{ t('admin.tests.protection.actions.routingHint') }}</p>
          <div v-if="combined && type.output_kind === 'statistics'" class="space-y-2" data-independent-recovery>
            <p class="text-xs text-blue-700 dark:text-blue-300">{{ t('admin.tests.protection.combined.recoveryHint') }}</p>
            <button v-if="!canEnableCacheRecovery(ruleFor(type.id)!, type.output_kind) && hasCacheThreshold(type.id)" type="button" class="btn btn-secondary btn-sm" data-prepare-recovery @click="prepareRecovery(type.id)">{{ t('admin.tests.protection.combined.prepareRecovery') }}</button>
          </div>
          <TestCacheRecoveryEditor v-if="type.output_kind === 'statistics' || ruleFor(type.id)?.recovery" :rule="ruleFor(type.id)!" :output-kind="type.output_kind" @update:model-value="patchRule(type.id, { recovery: $event })" />
        </fieldset>
      </section>
      <TestCombinationRulesEditor v-if="combined" :model-value="modelValue.combinations || []" :types="combinationTypes" :groups="groups" :recovery-test-ids="modelValue.rules.filter(rule => rule.recovery?.enabled).map(rule => rule.test_definition_id)" @update:model-value="update(next => { next.combinations = $event })" />
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(combined ? 'admin.tests.protection.combined.samplesHint' : 'admin.tests.protection.rulesHint') }}</p>
      <p v-if="!validTestProtection(modelValue, types, groups)" class="text-xs text-red-600 dark:text-red-400" role="alert">{{ t(combined ? 'admin.tests.protection.combined.invalid' : 'admin.tests.protection.invalid') }}</p>
    </div>
  </fieldset>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminGroup, TestCombinationRule, TestProtectionConfig, TestProtectionMetric, TestProtectionRule, TestProtectionThreshold, TestType } from '@/types'
import { canEnableCacheRecovery, copyTestCombination, copyTestProtection, defaultProtectionRule, newTestCombinationID, protectionMetrics, validTestProtection } from '@/utils/testProtection'
import TestOutcomeActionEditor from './TestOutcomeActionEditor.vue'
import TestCacheRecoveryEditor from './TestCacheRecoveryEditor.vue'
import TestCombinationRulesEditor from './TestCombinationRulesEditor.vue'

const props = withDefaults(defineProps<{ modelValue: TestProtectionConfig; types: TestType[]; groups?: AdminGroup[] }>(), { groups: () => [] })
const emit = defineEmits<{ 'update:modelValue': [value: TestProtectionConfig] }>()
const { t } = useI18n()
const combined = computed(() => props.modelValue.mode === 'combined')
const combinationTypes = computed(() => props.types.filter(type => type.enabled && ruleFor(type.id)))
const combinationDraft = ref<TestCombinationRule[]>()
const ruleFor = (id: number) => props.modelValue.rules.find(rule => rule.test_definition_id === id)
const update = (change: (value: TestProtectionConfig) => void) => {
  const next = copyTestProtection(props.modelValue)
  change(next)
  emit('update:modelValue', next)
}
const setEnabled = (enabled: boolean) => update(next => {
  next.enabled = enabled
  if (next.enabled && !next.rules.length) next.rules = props.types.filter(type => type.enabled).map(defaultProtectionRule)
})
const setMode = (mode: 'per_test' | 'combined') => update(next => {
  if (mode === 'combined') {
    next.combinations = combinationDraft.value?.map(copyTestCombination) || next.combinations || (['pass', 'fail'] as const).map(verdict => ({
      id: newTestCombinationID(),
      name: t(`admin.tests.protection.combined.${verdict === 'pass' ? 'allPassName' : 'anyFailName'}`),
      priority: 0,
      condition: { operator: verdict === 'pass' ? 'all' : 'any', conditions: combinationTypes.value.map(type => ({ operator: 'test', test_definition_id: type.id, verdict })) },
      action: { scheduling: 'keep', group_mode: 'keep' },
    }))
  } else {
    combinationDraft.value = next.combinations?.map(copyTestCombination)
    delete next.combinations
  }
  next.mode = mode
})
const hasCacheThreshold = (id: number) => !!ruleFor(id)?.thresholds?.some(item => item.metric === 'cache_rate')
const prepareRecovery = (id: number) => update(next => {
  const rule = next.rules.find(item => item.test_definition_id === id)
  if (!rule) return
  rule.on_pass = { group_mode: 'keep', ...rule.on_pass, scheduling: 'resume' }
  rule.on_fail = { group_mode: 'keep', ...rule.on_fail, scheduling: 'pause' }
})
const toggleRule = (type: TestType, enabled: boolean) => update(next => {
  if (enabled && !type.enabled) return
  next.rules = next.rules.filter(rule => rule.test_definition_id !== type.id)
  if (enabled) next.rules.push(defaultProtectionRule(type))
})
const patchRule = (id: number, patch: Partial<TestProtectionRule>) => update(next => {
  const rule = next.rules.find(item => item.test_definition_id === id)
  if (rule) Object.assign(rule, patch)
})
const addThreshold = (type: TestType) => {
  const metric = protectionMetrics(type.output_kind)[0]
  patchRule(type.id, { thresholds: [...(ruleFor(type.id)?.thresholds || []), { metric, operator: metric.endsWith('_ms') ? 'gt' : 'lt', value: metric.endsWith('_rate') ? 90 : 0 }] })
}
const patchThreshold = (id: number, index: number, patch: Partial<TestProtectionThreshold>) => patchRule(id, { thresholds: ruleFor(id)?.thresholds?.map((value, i) => i === index ? { ...value, ...patch } : { ...value }) })
const removeThreshold = (id: number, index: number) => patchRule(id, { thresholds: ruleFor(id)?.thresholds?.filter((_, i) => i !== index) })
const toggleVote = (id: number, enabled: boolean) => patchRule(id, { ...(enabled ? { required_pass: false } : {}), vote: { reject_above: 0, pass_at_least: 3, public_enabled: false, ...ruleFor(id)?.vote, enabled, ...(!enabled ? { public_enabled: false } : {}) } })
const patchVote = (id: number, patch: Partial<NonNullable<TestProtectionRule['vote']>>) => patchRule(id, { vote: { enabled: true, public_enabled: false, reject_above: 0, pass_at_least: 3, ...ruleFor(id)?.vote, ...patch } })
</script>
