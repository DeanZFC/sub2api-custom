<template>
  <fieldset class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-dark-700" data-capability-editor>
    <legend class="px-1 font-semibold">{{ tr('title') }}</legend>
    <p class="text-sm leading-6 text-gray-500">{{ tr('hint') }}</p>
    <div class="flex flex-wrap gap-4"><label v-for="family in ['candy', 'state', 'instructions']" :key="family" class="flex items-center gap-2 text-sm"><input v-model="config.families" type="checkbox" :value="family" />{{ tr(family) }}</label></div>
    <div class="grid gap-3 sm:grid-cols-2"><label class="input-label">{{ tr('samples') }}<input v-model.number="config.samples" type="number" min="1" max="5" class="input mt-1 w-full" /></label><label class="input-label">{{ tr('required') }}<input v-model.number="config.required_passes" type="number" min="1" :max="config.samples" class="input mt-1 w-full" /></label></div>
    <div v-for="(q, index) in config.questions" :key="index" class="space-y-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
      <div class="flex gap-2"><input v-model.trim="q.key" class="input min-w-0 flex-1" :placeholder="tr('questionKey')" maxlength="100" /><button type="button" class="btn btn-secondary btn-sm" @click="config.questions.splice(index, 1)">{{ t('common.delete') }}</button></div>
      <textarea v-model="q.prompt" class="input w-full" rows="3" :placeholder="tr('questionPrompt')" maxlength="8000" />
      <div class="grid gap-2 sm:grid-cols-2"><input v-if="q.match !== 'not_date'" v-model="q.expected" class="input" :placeholder="tr('expected')" maxlength="2000" data-capability-expected /><select v-model="q.match" class="input" data-capability-match><option value="exact">{{ tr('exact') }}</option><option value="number">{{ tr('number') }}</option><option value="not_date">{{ tr('notDate') }}</option></select></div>
      <p v-if="q.match === 'not_date'" class="text-sm leading-6 text-gray-500" data-not-date-rule>{{ tr('notDateRule') }}</p>
    </div>
    <button type="button" class="btn btn-secondary btn-sm" :disabled="config.questions.length >= 50" @click="config.questions.push({ key: `question-${Date.now()}`, prompt: '', expected: '', match: 'exact' })">{{ tr('add') }}</button>
    <p v-if="!validCapabilityConfig(config)" role="alert" class="text-sm text-amber-600">{{ tr('invalid') }}</p>
  </fieldset>
</template>
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { defaultCapabilityConfig, parseCapabilityConfig, validCapabilityConfig } from '@/utils/qualityCapability'
const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const { t } = useI18n()
const tr = (key: string) => t(`admin.tests.capability.${key}`)
const config = ref(parseCapabilityConfig(props.modelValue) || defaultCapabilityConfig())
watch(config, value => emit('update:modelValue', JSON.stringify(value)), { deep: true, immediate: true })
watch(() => props.modelValue, raw => { if (raw !== JSON.stringify(config.value)) { const parsed = parseCapabilityConfig(raw); if (parsed) config.value = parsed } })
</script>
