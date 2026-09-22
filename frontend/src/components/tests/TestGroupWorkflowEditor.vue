<template>
  <fieldset class="space-y-3 rounded-lg border border-primary-200 bg-primary-50/30 p-4 dark:border-primary-800 dark:bg-primary-950/20" data-group-workflow-editor>
    <legend class="px-1 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.tests.groupWorkflow.title') }}</legend>
    <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.tests.groupWorkflow.description') }}</p>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="input-label">{{ t('admin.tests.groupWorkflow.automaticTest') }}<select :value="modelValue.automatic_test_id" class="input mt-1 w-full" data-workflow-automatic @change="patch({ automatic_test_id: Number(($event.target as HTMLSelectElement).value) })"><option :value="0">{{ t('admin.tests.groupWorkflow.selectTest') }}</option><option v-for="type in numericTypes" :key="type.id" :value="type.id">{{ type.name }}</option></select></label>
      <label class="input-label">{{ t('admin.tests.groupWorkflow.reviewTest') }}<select :value="modelValue.review_test_id" class="input mt-1 w-full" data-workflow-review @change="patch({ review_test_id: Number(($event.target as HTMLSelectElement).value) })"><option :value="0">{{ t('admin.tests.groupWorkflow.selectTest') }}</option><option v-for="type in htmlTypes" :key="type.id" :value="type.id">{{ type.name }}</option></select></label>
      <label class="input-label">{{ t('admin.tests.groupWorkflow.passGroup') }}<select :value="modelValue.pass_group_id" class="input mt-1 w-full" data-workflow-pass-group @change="patch({ pass_group_id: Number(($event.target as HTMLSelectElement).value) })"><option :value="0">{{ t('admin.tests.selectGroup') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} (#{{ group.id }})</option></select></label>
      <label class="input-label">{{ t('admin.tests.groupWorkflow.failGroup') }}<select :value="modelValue.fail_group_id" class="input mt-1 w-full" data-workflow-fail-group @change="patch({ fail_group_id: Number(($event.target as HTMLSelectElement).value) })"><option :value="0">{{ t('admin.tests.selectGroup') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} (#{{ group.id }})</option></select></label>
    </div>
    <div class="space-y-2 rounded-md border border-gray-200 bg-white/60 p-3 dark:border-dark-700 dark:bg-dark-900/60">
      <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"><input :checked="reviewVote.public_enabled === true" type="checkbox" data-workflow-public-vote @change="patchReviewVote({ public_enabled: ($event.target as HTMLInputElement).checked })" />{{ t('admin.tests.groupWorkflow.publicVote') }}</label>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(reviewVote.public_enabled ? 'admin.tests.groupWorkflow.publicVoteHint' : 'admin.tests.groupWorkflow.privateReviewHint') }}</p>
      <div v-if="reviewVote.public_enabled" class="grid gap-3 sm:grid-cols-2">
        <label class="input-label">{{ t('admin.tests.protection.rejectAbove') }}<input :value="reviewVote.reject_above" type="number" min="0" max="1000000" step="1" class="input mt-1 w-full" data-workflow-vote-reject @input="patchReviewVote({ reject_above: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
        <label class="input-label">{{ t('admin.tests.protection.passAtLeast') }}<input :value="reviewVote.pass_at_least" type="number" min="1" max="1000000" step="1" class="input mt-1 w-full" data-workflow-vote-pass @input="patchReviewVote({ pass_at_least: ($event.target as HTMLInputElement).valueAsNumber })" /></label>
      </div>
    </div>
    <p class="text-sm font-medium text-amber-700 dark:text-amber-300" data-workflow-replace-hint>{{ t('admin.tests.groupWorkflow.replaceHint') }}</p>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.tests.groupWorkflow.roundHint') }}</p>
    <p class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.tests.groupWorkflow.conflictHint') }}</p>
    <p v-if="!validTestGroupWorkflow(modelValue, types, groups)" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ t('admin.tests.groupWorkflow.invalid') }}</p>
  </fieldset>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminGroup, TestGroupWorkflow, TestProtectionVote, TestType } from '@/types'
import { validTestGroupWorkflow } from '@/utils/testProtection'

const props = defineProps<{ modelValue: TestGroupWorkflow; types: TestType[]; groups: AdminGroup[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: TestGroupWorkflow] }>()
const { t } = useI18n()
const numericTypes = computed(() => props.types.filter(type => type.enabled && type.output_kind === 'number'))
const htmlTypes = computed(() => props.types.filter(type => type.enabled && type.output_kind === 'html'))
const reviewVote = computed<TestProtectionVote>(() => ({ enabled: true, public_enabled: false, reject_above: 0, pass_at_least: 1, ...props.modelValue.review_vote }))
const patch = (change: Partial<TestGroupWorkflow>) => emit('update:modelValue', { ...props.modelValue, ...change })
const patchReviewVote = (change: Partial<TestProtectionVote>) => patch({ review_vote: { ...reviewVote.value, ...change, enabled: true } })
</script>
