<template>
  <BaseDialog
    :show="show"
    :title="t('keys.test.title')"
    width="normal"
    :close-on-escape="!running"
    :show-close-button="!running"
    @close="close"
  >
    <form id="test-key-form" class="space-y-4" @submit.prevent="run">
      <div
        v-if="apiKey"
        class="flex items-center justify-between gap-3 rounded-xl border border-gray-200 bg-gradient-to-r from-gray-50 to-gray-100 p-3 dark:border-dark-500 dark:from-dark-700 dark:to-dark-600"
        data-test="test-key-info"
      >
        <div class="flex min-w-0 items-center gap-3">
          <div class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-primary-500 to-primary-600">
            <Icon name="play" size="md" class="text-white" :stroke-width="2" />
          </div>
          <div class="min-w-0">
            <div class="truncate font-semibold text-gray-900 dark:text-gray-100" :title="apiKey.name">{{ apiKey.name }}</div>
            <div class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
              <span class="shrink-0 rounded bg-gray-200 px-1.5 py-0.5 text-[10px] font-medium dark:bg-dark-500">API KEY</span>
              <span class="truncate" :title="apiKey.group?.name">{{ apiKey.group?.name || t('keys.noGroup') }}</span>
            </div>
          </div>
        </div>
        <span
          :class="[
            'shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold',
            apiKey.status === 'active'
              ? 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400'
              : 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-400'
          ]"
        >{{ apiKey.status }}</span>
      </div>

      <div class="space-y-1.5">
        <label for="key-test-model" class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('keys.test.model') }}</label>
        <Select
          id="key-test-model"
          v-model="model"
          :options="modelOptions"
          :disabled="running || loadingModels"
          :searchable="true"
          creatable
          :aria-label="t('keys.test.model')"
          :placeholder="loadingModels ? t('common.loading') : t('keys.test.modelPlaceholder')"
          :search-placeholder="t('keys.test.modelPlaceholder')"
        />
        <p v-if="modelsError" class="text-xs text-amber-700 dark:text-amber-300" role="alert">{{ modelsError }}</p>
      </div>

      <div class="space-y-1.5">
        <label for="key-test-mode" class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('keys.test.mode') }}</label>
        <Select id="key-test-mode" :model-value="'regular'" :options="modeOptions" :disabled="running" :aria-label="t('keys.test.mode')" />
      </div>

      <div class="rounded-lg bg-amber-50 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:bg-amber-900/20 dark:text-amber-200" data-test="test-billing-notice">
        <p>{{ t('keys.test.billingHint') }}</p>
      </div>

      <div
        class="max-h-[240px] min-h-[120px] overflow-y-auto rounded-xl border border-gray-700 bg-gray-900 p-4 font-mono text-sm dark:border-gray-800 dark:bg-black"
        role="log"
        aria-live="polite"
        :aria-label="t('keys.test.output')"
        data-test="test-console"
      >
        <div v-if="!running && !result && !error" class="flex items-start gap-2 text-gray-400">
          <Icon name="play" size="sm" class="mt-0.5 shrink-0" :stroke-width="2" />
          <span>{{ t('keys.test.ready') }}</span>
        </div>
        <div v-if="running" role="status" class="flex items-center gap-2 text-yellow-400">
          <Icon name="refresh" size="sm" class="shrink-0 animate-spin" :stroke-width="2" />
          <span>{{ t('keys.test.connecting') }}</span>
        </div>
        <div v-if="result" class="space-y-3" data-test="test-result">
          <p class="break-words text-xs text-gray-400">
            {{ result.model || model }} · {{ elapsedMs }} ms
            <template v-if="result.usage">
              · {{ t('keys.test.inputTokens') }}: {{ result.usage.prompt_tokens ?? '—' }}
              · {{ t('keys.test.outputTokens') }}: {{ result.usage.completion_tokens ?? '—' }}
            </template>
          </p>
          <pre class="whitespace-pre-wrap break-words font-mono text-green-400">{{ responseText || t('keys.test.noText') }}</pre>
          <div class="flex items-center gap-2 border-t border-gray-700 pt-3 text-green-400">
            <Icon name="check" size="sm" :stroke-width="2" />
            <span>{{ t('keys.test.success') }}</span>
          </div>
        </div>
        <div v-if="error" role="alert" class="flex items-start gap-2 text-red-400" data-test="test-error">
          <Icon name="x" size="sm" class="mt-0.5 shrink-0" :stroke-width="2" />
          <span class="whitespace-pre-wrap break-words">{{ error }}</span>
        </div>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-2 px-1 text-xs text-gray-500 dark:text-gray-400">
        <span class="flex min-w-0 items-center gap-1" :title="model">
          <Icon name="grid" size="sm" class="shrink-0" :stroke-width="2" />
          <span class="max-w-[240px] truncate">{{ model || t('keys.test.model') }}</span>
        </span>
        <span class="flex min-w-0 items-center gap-1" :title="prompt">
          <Icon name="chat" size="sm" class="shrink-0" :stroke-width="2" />
          <span class="max-w-[200px] truncate">{{ t('keys.test.promptSummary', { prompt }) }}</span>
        </span>
      </div>
      <p v-if="result" class="text-xs text-gray-500 dark:text-gray-400">{{ t('keys.test.usageHint') }}</p>
    </form>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="rounded-lg bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500" :disabled="running" @click="close">{{ t('common.close') }}</button>
        <button
          :class="[
            'flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-white transition-all',
            !canStartTest
              ? 'cursor-not-allowed bg-primary-400'
              : result
                ? 'bg-green-500 hover:bg-green-600'
                : error
                  ? 'bg-orange-500 hover:bg-orange-600'
                  : 'bg-primary-500 hover:bg-primary-600'
          ]"
          type="submit"
          form="test-key-form"
          :disabled="!canStartTest"
          data-test="run-key-test"
        >
          <Icon :name="running || result || error ? 'refresh' : 'play'" size="sm" :class="running && 'animate-spin'" :stroke-width="2" />
          {{ running ? t('keys.test.running') : result || error ? t('keys.test.retry') : t('keys.test.start') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { Icon } from '@/components/icons'
import { getKeyTestModels, keyTestErrorMessage, runKeyTest, type KeyTestResult } from '@/api/keyTest'
import type { ApiKey } from '@/types'

const props = defineProps<{ show: boolean; apiKey: ApiKey | null }>()
const emit = defineEmits<{ close: []; tested: [] }>()
const { t } = useI18n()
const model = ref('')
const prompt = 'hi'
const models = ref<string[]>([])
const modelOptions = computed(() => models.value.map(id => ({ value: id, label: id })))
const modeOptions = computed(() => [
  { value: 'regular', label: t('keys.test.modeRegular') },
])
const loadingModels = ref(false)
const running = ref(false)
const canStartTest = computed(() => !running.value && !loadingModels.value && !!props.apiKey && !!model.value.trim())
const modelsError = ref('')
const error = ref('')
const result = ref<KeyTestResult | null>(null)
const elapsedMs = ref(0)
let modelsController: AbortController | null = null
let testController: AbortController | null = null
const responseText = computed(() => result.value?.choices?.[0]?.message?.content || result.value?.choices?.[0]?.message?.reasoning_content || '')

watch(() => [props.show, props.apiKey?.id] as const, async ([show, id]) => {
  modelsController?.abort()
  modelsController = null
  testController?.abort()
  testController = null
  running.value = false
  loadingModels.value = false
  if (!show || !id) return
  model.value = ''
  models.value = []
  modelsError.value = ''
  error.value = ''
  result.value = null
  elapsedMs.value = 0
  loadingModels.value = true
  const controller = new AbortController()
  modelsController = controller
  try {
    const available = await getKeyTestModels(id, controller.signal)
    if (controller.signal.aborted) return
    models.value = available
    model.value = available[0] || ''
  } catch (err) {
    if (!controller.signal.aborted) modelsError.value = keyTestErrorMessage(err, t('keys.test.modelsFailed'))
  } finally {
    if (modelsController === controller) loadingModels.value = false
  }
}, { immediate: true })

async function run() {
  if (!canStartTest.value || !props.apiKey) return
  running.value = true
  error.value = ''
  result.value = null
  const started = performance.now()
  const controller = new AbortController()
  testController = controller
  try {
    const response = await runKeyTest(props.apiKey.id, model.value.trim(), controller.signal)
    if (!controller.signal.aborted) {
      result.value = response
      elapsedMs.value = Math.round(performance.now() - started)
    }
  } catch (err) {
    if (!controller.signal.aborted) error.value = keyTestErrorMessage(err, t('keys.test.failed'))
  } finally {
    if (testController === controller) {
      running.value = false
      testController = null
      emit('tested')
    }
  }
}

function close() {
  if (!running.value) emit('close')
}

onUnmounted(() => {
  modelsController?.abort()
  testController?.abort()
  testController = null
})
</script>
